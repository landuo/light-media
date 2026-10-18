package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"light-media/internal/plugins"
	sr "light-media/internal/plugins/seedrescue"
	"light-media/internal/plugins/uninstall"
)

func rescueDefault(t *testing.T) (*App, PluginInstallation) {
	t.Helper()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	in, run := rescueInstallation(t, a, sr.ParseConfig(sr.Object{}).Current())
	rescueFinish(t, a, run)
	return a, in
}
func TestSeedRescueDeletedOrChangedEvidenceNeverAuthorizesReset(t *testing.T) {
	for _, damage := range []string{"state", "root", "deferred", "run", "data", "data_revision"} {
		t.Run(damage, func(t *testing.T) {
			a, in := rescueDefault(t)
			ctx := context.Background()
			queued, e := a.pluginStore.EnqueueRun(ctx, in.InstanceID, "run", nil)
			if e != nil {
				t.Fatal(e)
			}
			run, e := a.pluginStore.ClaimRun(ctx)
			if e != nil || run.ID != queued.ID {
				t.Fatal(e)
			}
			rescueFinish(t, a, run)
			var query string
			args := []any{in.InstanceID}
			switch damage {
			case "state":
				query = `DELETE FROM plugin_seed_rescue_state WHERE instance_id=?`
			case "root":
				query = `DELETE FROM plugin_seed_rescue_roots WHERE instance_id=?`
			case "deferred":
				query = `DELETE FROM plugin_deferred_operations WHERE run_id=?`
				args = []any{run.ID}
			case "run":
				if _, e = a.Store.DB.Exec(`DELETE FROM plugin_deferred_operations WHERE run_id=?`, run.ID); e != nil {
					t.Fatal(e)
				}
				query = `DELETE FROM plugin_runs WHERE id=?`
				args = []any{run.ID}
			case "data":
				query = `DELETE FROM plugin_values WHERE instance_id=? AND kind='data'`
			case "data_revision":
				query = `UPDATE plugin_values SET revision=revision+1 WHERE instance_id=? AND kind='data'`
			}
			if _, e = a.Store.DB.Exec(query, args...); e != nil {
				t.Fatal(e)
			}
			if _, _, e = a.pluginStore.seedRescueRead(ctx, a.Store.DB, in.InstanceID, in.Generation); e == nil {
				t.Fatal("deleted evidence accepted", damage)
			}
			if e = a.pluginStore.Reset(ctx, in.InstanceID); e == nil {
				t.Fatal("missing proof created a new root", damage)
			}
			current, _ := a.pluginStore.Installation(ctx, in.InstanceID)
			if current.Generation != in.Generation {
				t.Fatal("failed reset advanced generation")
			}
		})
	}
}
func TestSeedRescueStartedAndOwnerFactAreAtomicAndColdNotReplayed(t *testing.T) {
	a, in := rescueDefault(t)
	ctx := context.Background()
	_, e := a.pluginStore.EnqueueRun(ctx, in.InstanceID, "run", nil)
	if e != nil {
		t.Fatal(e)
	}
	run, e := a.pluginStore.ClaimRun(ctx)
	if e != nil {
		t.Fatal(e)
	}
	d, e := a.preparePluginDeferred(ctx, in, run)
	if e != nil {
		t.Fatal(e)
	}
	held, release, e := d.lockSeedRescue(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`CREATE TRIGGER fail_rescue_root BEFORE UPDATE ON plugin_seed_rescue_roots BEGIN SELECT RAISE(ABORT,'owned root failure'); END`); e != nil {
		t.Fatal(e)
	}
	if e = d.changePhase(held, "prepared", "started", nil); e == nil {
		t.Fatal("root failure committed started")
	}
	state, _, e := a.pluginStore.seedRescueRead(ctx, a.Store.DB, in.InstanceID, in.Generation)
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := state.Runs[run.ID]; ok {
		t.Fatal("rolled back owner survived")
	}
	receipt, _, _ := a.pluginStore.readDeferred(ctx, a.Store.DB, run)
	if receipt.Phase != "prepared" {
		t.Fatal(receipt.Phase)
	}
	a.Store.DB.Exec(`DROP TRIGGER fail_rescue_root`)
	if e = d.changePhase(held, "prepared", "started", nil); e != nil {
		t.Fatal(e)
	}
	release()
	state, _, e = a.pluginStore.seedRescueRead(ctx, a.Store.DB, in.InstanceID, in.Generation)
	if e != nil || state.Runs[run.ID].Entered || state.Runs[run.ID].Phase != "started" {
		t.Fatal("accepted fact missing before Host", e)
	}
	dir := a.Dir
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	useBuiltInPluginProcess(t, cold)
	actual, _ := cold.pluginStore.Run(ctx, run.ID)
	if actual.State != "reconcile_pending" {
		t.Fatal("cold replayed accepted run", actual.State)
	}
	_, e = cold.pluginStore.EnqueueRun(ctx, in.InstanceID, "run", nil)
	if e != nil {
		t.Fatal(e)
	}
	second, e := cold.pluginStore.ClaimRun(ctx)
	if e != nil {
		t.Fatal(e)
	}
	cold.executePluginRun(ctx, second)
	second, _ = cold.pluginStore.Run(ctx, second.ID)
	if second.State != "failed" || second.ErrorCode != seedRescueBlockedByUnknown {
		t.Fatal("new run polluted unknown", second.State, second.ErrorCode)
	}
	state, _, e = cold.pluginStore.seedRescueRead(ctx, cold.Store.DB, in.InstanceID, in.Generation)
	if e != nil || state.Runs[second.ID].Entered || len(state.Runs[second.ID].Effects) != 0 || seedRescueNoUnknown(state) == nil {
		t.Fatal("zero-effect barrier invalid", e)
	}
	if e = cold.pluginStore.Reset(ctx, in.InstanceID); e == nil {
		t.Fatal("reset erased unknown")
	}
}
func TestSeedRescueOriginalResetAndBulkUninstallRetirement(t *testing.T) {
	for _, operation := range []string{"reset", "bulk"} {
		t.Run(operation, func(t *testing.T) {
			a, in := rescueDefault(t)
			ctx := context.Background()
			_, e := a.pluginStore.EnqueueRun(ctx, in.InstanceID, "run", nil)
			if e != nil {
				t.Fatal(e)
			}
			run, _ := a.pluginStore.ClaimRun(ctx)
			rescueFinish(t, a, run)
			if operation == "reset" {
				cookie := setupTestAdmin(t, a)
				response := callTest(t, a.Handler(), "POST", "/api/plugins/SeedRescue/reset", "{}", cookie, "")
				if response.Code != 200 {
					t.Fatal(response.Code, response.Body.String())
				}
			} else {
				bulk := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{in.InstanceID}}, true)
				rescueFinish(t, a, bulk)
			}
			current, e := a.pluginStore.Installation(ctx, in.InstanceID)
			if e != nil || current.Generation != in.Generation+1 {
				t.Fatal(current, e)
			}
			state, _, e := a.pluginStore.seedRescueRead(ctx, a.Store.DB, in.InstanceID, current.Generation)
			if e != nil || len(state.Runs) != 0 || len(state.Sources) != 0 {
				t.Fatal("new generation revived old run", e)
			}
			if operation == "reset" {
				if len(state.Values) != 0 || state.AppliedRevision != 0 {
					t.Fatal("reset revived old KV")
				}
				var config int
				a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_values WHERE instance_id=?`, in.InstanceID).Scan(&config)
				if config != 0 {
					t.Fatal(config)
				}
			} else if current.Installed {
				t.Fatal("bulk did not uninstall")
			}
		})
	}
}
func TestSeedRescueReadSDKCannotRequestMutationOrSecretInput(t *testing.T) {
	a, in := rescueDefault(t)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	if response := callTest(t, a.Handler(), "GET", "/api/plugins/SeedRescue/api/status", "", nil, ""); response.Code != 401 {
		t.Fatal("read became public", response.Code)
	}
	req := plugins.HTTPRequest{Key: "status", Method: "GET", Path: "/status", RequestID: "owned-request", Query: map[string][]string{}, Params: map[string]string{}, Headers: map[string]string{}}
	run, e := a.enqueuePluginHTTP(ctx, in, req, pluginAPIScope{Key: req.Key, Method: req.Method, RequestID: req.RequestID, Authority: pluginAPIAuthority{Kind: "session", Proof: tokenHash(cookie.Value)}})
	if e != nil {
		t.Fatal(e)
	}
	worker, e := seedRescueWorkerInput(run)
	if e != nil || strings.Contains(string(worker), "rss_urls") || strings.Contains(string(worker), "token") {
		t.Fatal("worker received private config", e)
	}
	var inv plugins.Invocation
	json.Unmarshal(worker, &inv)
	if string(inv.Config) != "{}" {
		t.Fatal(string(inv.Config))
	}
	forged := plugins.Identity{PluginID: sr.PluginID, Version: sr.Version, InstanceID: "other"}
	if _, e = a.pluginHostCall(ctx, in, &run, forged, sr.ReadCapability, json.RawMessage(`{"request_id":"owned-request"}`)); e == nil {
		t.Fatal("cross instance allowed")
	}
	if _, e = a.pluginSeedRescueHost(ctx, in, &run, sr.APICapability, json.RawMessage(`{"request_id":"owned-request"}`)); e == nil {
		t.Fatal("readonly SDK forged mutation")
	}
}

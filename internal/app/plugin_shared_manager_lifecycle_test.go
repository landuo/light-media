package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins"
)

// These finite host tests use real owned SQLite and the real resource
// dispatcher, but explicitly supplied call authority. They are not a real SDK
// process/installation proof; that separate gate follows the other S10 blocks.
func managerMutationFixture(t *testing.T) (*App, originalBaseCallScope, plugins.Identity, *atomic.Bool) {
	t.Helper()
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	active := new(atomic.Bool)
	active.Store(true)
	in := PluginInstallation{PluginID: "OwnedCaller", InstanceID: "OwnedCaller", Version: "1"}
	scope := originalBaseCallScope{installation: in, phase: "query", validate: func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !active.Load() {
			return errors.New("owned callback revoked")
		}
		return nil
	}}
	return a, scope, plugins.Identity{PluginID: in.PluginID, InstanceID: in.InstanceID, Version: in.Version}, active
}

func managerMutationCall(a *App, scope originalBaseCallScope, identity plugins.Identity, method, target string, force bool) (json.RawMessage, error) {
	raw, _ := json.Marshal(plugins.PluginManagerDeleteRequest{PluginID: target, Force: force})
	return a.pluginOriginalResourceHost(context.Background(), scope, identity, method, raw)
}

func managerMutationSeed(t *testing.T, a *App, target string) {
	t.Helper()
	a.pluginMu.Lock()
	a.pluginOriginalBase.mu.Lock()
	_, err := a.originalBaseUpdateConfigLocked(context.Background(), target, json.RawMessage(`{"persisted":"before-delete"}`))
	a.pluginOriginalBase.mu.Unlock()
	a.pluginMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.pluginStore.originalBaseSaveData(context.Background(), target, "owned", json.RawMessage(`{"data":"retained"}`)); err != nil {
		t.Fatal(err)
	}
}

func managerMutationConfig(t *testing.T, a *App, target, want string) {
	t.Helper()
	raw, _, err := a.readOriginalBaseConfigForInit(context.Background(), target)
	if err != nil || !originalBaseJSONEqual(raw, json.RawMessage(want)) {
		t.Fatalf("config %s got=%s want=%s err=%v", target, raw, want, err)
	}
}

func managerMutationData(t *testing.T, a *App, target, want string) {
	t.Helper()
	key := "owned"
	raw, err := a.pluginStore.originalBaseGetData(context.Background(), target, &key)
	if err != nil || string(raw) != want {
		t.Fatalf("data %s got=%s want=%s err=%v", target, raw, want, err)
	}
}

func TestSharedManagerMutationsClassForceAndIsolation(t *testing.T) {
	a, scope, identity, active := managerMutationFixture(t)
	ctx := context.Background()
	for _, target := range []string{"OwnedA", "OwnedB", "OwnedStopped"} {
		managerMutationSeed(t, a, target)
	}
	wantConfig := `{"persisted":"before-delete"}`
	wantData := `{"data":"retained"}`
	for _, method := range []string{plugins.SharedPluginConfigDelete, plugins.SharedPluginDataDelete} {
		out, err := managerMutationCall(a, scope, identity, method, "OwnedA", false)
		if err != nil || string(out) != "false" {
			t.Fatalf("absent class gate: %s %v", out, err)
		}
	}
	managerMutationConfig(t, a, "OwnedA", wantConfig)
	managerMutationData(t, a, "OwnedA", wantData)
	// Class-only (failed initialization) is enough; neither installed nor
	// running membership is a substitute for the source class gate.
	a.storeOriginalPluginClass("OwnedA", originalPluginClass{})
	if _, err := a.pluginStore.Installation(ctx, "OwnedA"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("fixture installed a target: %v", err)
	}
	if _, ok := a.pluginRunningInstances.Load("OwnedA"); ok {
		t.Fatal("fixture has a running target")
	}
	path := filepath.Join(a.Dir, "owned-data-file")
	if err := os.WriteFile(path, []byte("not namespace KV"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		out, err := managerMutationCall(a, scope, identity, plugins.SharedPluginConfigDelete, "OwnedA", false)
		if err != nil || string(out) != "true" {
			t.Fatalf("class config delete/empty success: %s %v", out, err)
		}
	}
	managerMutationConfig(t, a, "OwnedA", "null")
	managerMutationData(t, a, "OwnedA", wantData)
	if _, err := a.pluginStore.Config(ctx, "OwnedA"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("config row remains: %v", err)
	}
	if out, err := managerMutationCall(a, scope, identity, plugins.SharedPluginDataDelete, "OwnedA", false); err != nil || string(out) != "true" {
		t.Fatalf("class data delete: %s %v", out, err)
	}
	managerMutationData(t, a, "OwnedA", "null")
	for _, method := range []string{plugins.SharedPluginConfigDelete, plugins.SharedPluginDataDelete} {
		out, err := managerMutationCall(a, scope, identity, method, "OwnedStopped", true)
		if err != nil || string(out) != "true" {
			t.Fatalf("force unknown namespace: %s %v", out, err)
		}
	}
	managerMutationConfig(t, a, "OwnedStopped", "null")
	managerMutationData(t, a, "OwnedStopped", "null")
	managerMutationConfig(t, a, "OwnedB", wantConfig)
	managerMutationData(t, a, "OwnedB", wantData)
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "not namespace KV" {
		t.Fatal("KV deletion touched files", err)
	}
	if _, ok := a.pluginOriginalClasses.Load("OwnedA"); !ok {
		t.Fatal("delete removed the class")
	}
	if _, err := a.pluginStore.Installation(ctx, "OwnedStopped"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("force fabricated an installation", err)
	}
	// Force does not waive the actual dispatcher identity or active scope.
	wrong := identity
	wrong.InstanceID = "OtherCaller"
	if _, err := managerMutationCall(a, scope, wrong, plugins.SharedPluginDataDelete, "OwnedB", true); err == nil {
		t.Fatal("wrong caller identity accepted")
	}
	active.Store(false)
	if _, err := managerMutationCall(a, scope, identity, plugins.SharedPluginConfigDelete, "OwnedB", true); err == nil {
		t.Fatal("revoked force accepted")
	}
	active.Store(true)
	for _, raw := range []string{`{}`, `{"plugin_id":null}`, `{"plugin_id":"OwnedB","force":"yes"}`, `{"plugin_id":"OwnedB","unknown":1}`} {
		if _, err := a.pluginOriginalResourceHost(ctx, scope, identity, plugins.SharedPluginDataDelete, json.RawMessage(raw)); err == nil {
			t.Fatalf("malformed request accepted: %s", raw)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.pluginOriginalResourceHost(canceled, scope, identity, plugins.SharedPluginDataDelete, json.RawMessage(`{"plugin_id":"OwnedB","force":true}`)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel lost: %v", err)
	}
	managerMutationConfig(t, a, "OwnedB", wantConfig)
	managerMutationData(t, a, "OwnedB", wantData)
	// All eight implemented source methods are now resource capabilities;
	// declaration alone never grants a base-only contract or call authority.
	for _, method := range []string{plugins.SharedPluginInit, plugins.SharedPluginRemove, plugins.SharedPluginConfigDelete, plugins.SharedPluginDataDelete, plugins.SharedPluginState, plugins.SharedLocalPlugins, plugins.SharedSchedulerRemovePluginJob, plugins.SharedSchedulerUpdatePluginJob} {
		if !originalStandardCapabilityAllowed(pluginResourcesSDKContract, method) || originalStandardCapabilityAllowed(pluginBaseSDKContract, method) {
			t.Fatalf("S10 resource admission boundary mismatch: %s", method)
		}
	}
}

func TestSharedManagerMutationsConfigFailureHotCold(t *testing.T) {
	for _, outage := range []bool{false, true} {
		name := "delete_failure"
		if outage {
			name = "read_failure"
		}
		t.Run(name, func(t *testing.T) {
			a, scope, identity, _ := managerMutationFixture(t)
			ctx := context.Background()
			target := "OwnedFailure"
			a.storeOriginalPluginClass(target, originalPluginClass{})
			managerMutationSeed(t, a, target)
			if outage {
				if _, err := a.Store.DB.Exec(`ALTER TABLE plugin_values RENAME TO owned_s10_unavailable_values`); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_s10_reject_delete BEFORE DELETE ON plugin_values WHEN OLD.instance_id='OwnedFailure' AND OLD.kind='config' BEGIN SELECT RAISE(ABORT,'owned S10 config failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := managerMutationCall(a, scope, identity, plugins.SharedPluginConfigDelete, target, false); err == nil {
				t.Fatal("real SQLite config failure hidden")
			}
			managerMutationConfig(t, a, target, "null")
			raw, err := a.originalManagerConfiguration(ctx, target)
			if err != nil || string(raw) != "{}" {
				t.Fatalf("manager failed-delete hot cache: %s %v", raw, err)
			}
			if outage {
				// The class gate precedes even the first unavailable-table access.
				if raw, err := managerMutationCall(a, scope, identity, plugins.SharedPluginConfigDelete, "NotAClass", false); err != nil || string(raw) != "false" {
					t.Fatalf("class gate read SQL: %s %v", raw, err)
				}
				if _, err := managerMutationCall(a, scope, identity, plugins.SharedPluginDataDelete, target, false); err == nil {
					t.Fatal("real SQLite data failure hidden")
				}
				if _, err := a.Store.DB.Exec(`ALTER TABLE owned_s10_unavailable_values RENAME TO plugin_values`); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_s10_reject_delete`); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				managerMutationConfig(t, a, target, "null")
			}
			disk, err := a.pluginStore.Config(ctx, target)
			if err != nil || string(disk.Value) != `{"persisted":"before-delete"}` {
				t.Fatalf("failure deleted cold row: %s %v", disk.Value, err)
			}
			managerMutationData(t, a, target, `{"data":"retained"}`)
			dir := a.Dir
			if err = a.Close(); err != nil {
				t.Fatal(err)
			}
			cold, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			defer cold.Close()
			settings := cold.Settings()
			if settings.PluginStatisticShare == nil || *settings.PluginStatisticShare || settings.PluginServerURL == nil || *settings.PluginServerURL != "" {
				t.Fatal("cold owned settings lost external-service isolation")
			}
			managerMutationConfig(t, cold, target, `{"persisted":"before-delete"}`)
			managerMutationData(t, cold, target, `{"data":"retained"}`)
		})
	}
}

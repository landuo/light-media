package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

func originHTTPRun(t *testing.T, manifest plugins.Manifest) (*App, PluginRun) {
	t.Helper()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	installCloneSource(t, a.pluginStore, manifest)
	body := `{"revision":0,"config":{"enable":false}}`
	if manifest.PluginID == "CategoryEditor" {
		body = `{"revision":0,"config":{"enabled":false,"content":""}}`
	}
	w := callTest(t, a.Handler(), "PUT", "/api/plugins/"+manifest.PluginID+"/config", body, cookie, "")
	if w.Code != http.StatusOK {
		t.Fatalf("save %d %s", w.Code, w.Body)
	}
	run, err := a.pluginStore.ClaimRun(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return a, run
}

func TestPluginConfigSaveOriginAuthenticatedHTTP(t *testing.T) {
	for _, m := range []plugins.Manifest{builtin.SystemProcessManifest(), builtin.CategoryEditorManifest()} {
		t.Run(m.PluginID, func(t *testing.T) {
			a, run := originHTTPRun(t, m)
			yes, err := a.pluginStore.ConfigSaveOrigin(context.Background(), run)
			if err != nil || !yes {
				t.Fatal(yes, err)
			}
			reopened, err := NewPluginStore(context.Background(), a.Store)
			if err != nil {
				t.Fatal(err)
			}
			yes, err = reopened.ConfigSaveOrigin(context.Background(), run)
			if err != nil || !yes {
				t.Fatal("origin lost reopening store", yes, err)
			}
		})
	}
}

func TestPluginConfigSaveOriginRejectsUnauthorizedHTTP(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	installCloneSource(t, a.pluginStore, builtin.SystemProcessManifest())
	if w := callTest(t, a.Handler(), "PUT", "/api/plugins/SystemProcess/config", `{"revision":0,"config":{}}`, nil, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := callTest(t, a.Handler(), "PUT", "/api/plugins/SystemProcess/config", `{"revision":0,"config":{}}`, cookie, "https://cross-site.invalid"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	var n int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_origins`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

func TestPluginConfigSaveOriginIgnoresPayloadAndProgrammaticInit(t *testing.T) {
	for _, mode := range []string{"payload", "reload", "wrong-instance", "direct-save"} {
		t.Run(mode, func(t *testing.T) {
			_, p := pluginStoreFixture(t)
			ctx := context.Background()
			m := builtin.SystemProcessManifest()
			installCloneSource(t, p, m)
			if _, err := p.SaveConfig(ctx, m.PluginID, 0, map[string]any{"enable": false}); err != nil {
				t.Fatal(err)
			}
			var err error
			if mode == "direct-save" {
				_, _, err = p.SaveConfigAndQueue(ctx, m.PluginID, 1, map[string]any{"enable": false})
			} else {
				action := "init"
				if mode == "reload" {
					action = "reload"
					ctx = pluginConfigSaveRequest(httptest.NewRequest("PUT", "/api/plugins/SystemProcess/config", nil)).Context()
				}
				if mode == "wrong-instance" {
					ctx = pluginConfigSaveRequest(httptest.NewRequest("PUT", "/api/plugins/SystemProcessother/config", nil)).Context()
				}
				_, err = p.EnqueueRun(ctx, m.PluginID, action, json.RawMessage(`{"config_saved":true,"origin":"config-save"}`))
			}
			if err != nil {
				t.Fatal(err)
			}
			run, err := p.ClaimRun(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			yes, err := p.ConfigSaveOrigin(context.Background(), run)
			if err != nil || yes {
				t.Fatal("untrusted origin accepted", yes, err)
			}
		})
	}
}

func TestPluginConfigSaveOriginRuntimeChainImmutable(t *testing.T) {
	a, run := originHTTPRun(t, builtin.SystemProcessManifest())
	p := a.pluginStore
	ctx := context.Background()
	var original []byte
	if err := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_run_origins WHERE run_id=?`, run.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	originalInput := append([]byte(nil), run.Input...)
	for _, patch := range []map[string]json.RawMessage{{"test_once": json.RawMessage(`false`)}, {"dashboard_widget_size": json.RawMessage(`8`)}} {
		if err := p.patchRunConfig(ctx, &run, patch); err != nil {
			t.Fatal(err)
		}
		yes, err := p.ConfigSaveOrigin(ctx, run)
		if err != nil || !yes {
			t.Fatal(yes, err)
		}
	}
	var after []byte
	if err := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_run_origins WHERE run_id=?`, run.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, after) || !bytes.Equal(originalInput, run.Input) {
		t.Fatal("immutable original authority changed")
	}
	var n int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_config_transitions WHERE run_id=?`, run.ID).Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
}

func TestPluginConfigSaveOriginPendingDefaultsChain(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	p := a.pluginStore
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	w := callTest(t, a.Handler(), "PUT", "/api/plugins/SystemProcess/config", `{"revision":0,"config":{"enable":false}}`, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	in, err := p.Installation(ctx, m.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	revision := int64(1)
	for _, key := range []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine"} {
		value, err := p.NormalizeInstanceDefaults(ctx, in, revision, map[string]json.RawMessage{key: json.RawMessage(`false`)})
		if err != nil {
			t.Fatal(err)
		}
		revision = value.Revision
	}
	run, err := p.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	yes, err := p.ConfigSaveOrigin(ctx, run)
	if err != nil || !yes {
		t.Fatal(yes, err)
	}
	var n int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_config_transitions WHERE run_id=?`, run.ID).Scan(&n); err != nil || n != 9 {
		t.Fatal(n, err)
	}
}

func TestPluginConfigSaveOriginProofFailuresRollbackAndReplay(t *testing.T) {
	for _, mode := range []string{"missing-edge", "corrupt-edge", "old-config-replay", "revision-and-config-replay", "stale-run", "insert-failure"} {
		t.Run(mode, func(t *testing.T) {
			a, run := originHTTPRun(t, builtin.SystemProcessManifest())
			p := a.pluginStore
			ctx := context.Background()
			old := run
			var oldConfig []byte
			if err := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, run.InstanceID).Scan(&oldConfig); err != nil {
				t.Fatal(err)
			}
			if mode == "insert-failure" {
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER origin_proof_fail BEFORE INSERT ON plugin_run_config_transitions BEGIN SELECT RAISE(ABORT,'synthetic proof failure'); END`); err != nil {
					t.Fatal(err)
				}
				if err := p.patchRunConfig(ctx, &run, map[string]json.RawMessage{"test_once": json.RawMessage(`false`)}); err == nil {
					t.Fatal("proof failure ignored")
				}
				current, err := p.Config(ctx, run.InstanceID)
				if err != nil || current.Revision != old.ConfigRevision || run.ConfigRevision != old.ConfigRevision {
					t.Fatal("non-atomic failure", current.Revision, run.ConfigRevision, err)
				}
				yes, err := p.ConfigSaveOrigin(ctx, run)
				if err != nil || !yes {
					t.Fatal(yes, err)
				}
				return
			}
			if err := p.patchRunConfig(ctx, &run, map[string]json.RawMessage{"test_once": json.RawMessage(`false`)}); err != nil {
				t.Fatal(err)
			}
			var err error
			switch mode {
			case "missing-edge":
				_, err = a.Store.DB.Exec(`DELETE FROM plugin_run_config_transitions WHERE run_id=?`, run.ID)
			case "corrupt-edge":
				_, err = a.Store.DB.Exec(`UPDATE plugin_run_config_transitions SET sealed=x'00' WHERE run_id=?`, run.ID)
			case "old-config-replay":
				_, err = a.Store.DB.Exec(`UPDATE plugin_values SET sealed=? WHERE instance_id=? AND kind='config' AND item_key='main'`, oldConfig, run.InstanceID)
			case "revision-and-config-replay":
				_, err = a.Store.DB.Exec(`UPDATE plugin_values SET sealed=?,revision=? WHERE instance_id=? AND kind='config' AND item_key='main'`, oldConfig, old.ConfigRevision, run.InstanceID)
				if err != nil {
					t.Fatal(err)
				}
				_, err = a.Store.DB.Exec(`UPDATE plugin_runs SET config_revision=? WHERE id=?`, old.ConfigRevision, run.ID)
				run = old
			case "stale-run":
				run = old
			}
			if err != nil {
				t.Fatal(err)
			}
			yes, err := p.ConfigSaveOrigin(ctx, run)
			if err == nil || yes {
				t.Fatal("invalid proof accepted", yes, err)
			}
		})
	}
}

func TestPluginConfigSaveOriginCloneIsolation(t *testing.T) {
	a, source := originHTTPRun(t, builtin.SystemProcessManifest())
	p := a.pluginStore
	ctx := context.Background()
	if err := p.FinishRun(ctx, source.ID, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	clone, err := p.Clone(ctx, source.InstanceID, PluginCloneRequest{Suffix: "copy"})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?`, clone.InstanceID).Scan(&n); err != nil || n != 0 {
		t.Fatal("clone created an invocation", n, err)
	}
	// Authenticate a real synthetic HTTP session, not a forged context marker.
	w := callTest(t, a.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "lm_session" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no synthetic login session")
	}
	w = callTest(t, a.Handler(), "PUT", "/api/plugins/"+clone.InstanceID+"/config", `{"revision":1,"config":{"enable":false}}`, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	run, err := p.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	yes, err := p.ConfigSaveOrigin(ctx, run)
	if err != nil || !yes || run.InstanceID != clone.InstanceID {
		t.Fatal(yes, err, run.InstanceID)
	}
	forged := run
	forged.InstanceID = source.InstanceID
	if yes, err = p.ConfigSaveOrigin(ctx, forged); err == nil || yes {
		t.Fatal("cross-instance run accepted", yes, err)
	}
	var original []byte
	if err = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_run_origins WHERE run_id=?`, source.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_run_origins SET sealed=? WHERE run_id=?`, original, run.ID); err != nil {
		t.Fatal(err)
	}
	if yes, err = p.ConfigSaveOrigin(ctx, run); err == nil || yes {
		t.Fatal("cross-instance encrypted origin accepted", yes, err)
	}
}

func TestPluginConfigSaveOriginRejectsWrongRuntimePreimage(t *testing.T) {
	for _, mode := range []string{"id-cross-instance", "instance", "version", "action", "input"} {
		t.Run(mode, func(t *testing.T) {
			a, run := originHTTPRun(t, builtin.SystemProcessManifest())
			p := a.pluginStore
			ctx := context.Background()
			clone, err := p.Clone(ctx, run.InstanceID, PluginCloneRequest{Suffix: "other"})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = p.SaveConfigAndQueue(ctx, clone.InstanceID, 1, map[string]any{"enable": false})
			if err != nil {
				t.Fatal(err)
			}
			other, err := p.ClaimRun(ctx)
			if err != nil {
				t.Fatal(err)
			}
			sourceBefore, err := p.Config(ctx, run.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			cloneBefore, err := p.Config(ctx, other.InstanceID)
			if err != nil {
				t.Fatal(err)
			}
			forged := run
			switch mode {
			case "id-cross-instance":
				forged.ID = other.ID
			case "instance":
				forged.InstanceID = other.InstanceID
			case "version":
				forged.Version = "wrong"
			case "action":
				forged.Action = "reload"
			case "input":
				forged.Input = json.RawMessage(`{"config":{}}`)
			}
			if err = p.patchRunConfig(ctx, &forged, map[string]json.RawMessage{"test_once": json.RawMessage(`false`)}); err == nil {
				t.Fatal("wrong runtime preimage accepted")
			}
			for _, expected := range []struct {
				run   PluginRun
				value PluginStoredValue
			}{{run, sourceBefore}, {other, cloneBefore}} {
				value, e := p.Config(ctx, expected.run.InstanceID)
				if e != nil || value.Revision != expected.value.Revision || !bytes.Equal(value.Value, expected.value.Value) {
					t.Fatal("config changed", e)
				}
				current, e := p.Run(ctx, expected.run.ID)
				if e != nil || current.ConfigRevision != expected.run.ConfigRevision || !bytes.Equal(current.Input, expected.run.Input) {
					t.Fatal("run changed", e)
				}
			}
			var n int
			if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_config_transitions`).Scan(&n); err != nil || n != 0 {
				t.Fatal("proof changed", n, err)
			}
		})
	}
}

func TestPluginConfigSaveOriginInsertFailureRollsBackHTTP(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	p := a.pluginStore
	ctx := context.Background()
	installCloneSource(t, p, builtin.SystemProcessManifest())
	before, err := p.Installation(ctx, "SystemProcess")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER origin_insert_fail BEFORE INSERT ON plugin_run_origins BEGIN SELECT RAISE(ABORT,'synthetic origin failure'); END`); err != nil {
		t.Fatal(err)
	}
	w := callTest(t, a.Handler(), "PUT", "/api/plugins/SystemProcess/config", `{"revision":0,"config":{"enable":false}}`, cookie, "")
	if w.Code == 200 {
		t.Fatal("origin insert failure reported success")
	}
	for _, table := range []string{"plugin_runs", "plugin_run_origins", "plugin_values"} {
		var n int
		if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("partial HTTP config transaction", table, n, err)
		}
	}
	after, err := p.Installation(ctx, "SystemProcess")
	if err != nil || before.Generation != after.Generation || before.DesiredEnabled != after.DesiredEnabled {
		t.Fatal("installation changed", err)
	}
}

func TestPluginConfigSaveOriginPendingProofFailureRollsBackAll(t *testing.T) {
	a, run := originHTTPRun(t, builtin.SystemProcessManifest())
	p := a.pluginStore
	ctx := context.Background()
	pending, err := p.EnqueueRun(ctx, run.InstanceID, "reload", nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := p.Config(ctx, run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	// Scope the injected failure to the second propagation edge, after the
	// current run/config update and its proof have already been attempted.
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER pending_proof_fail BEFORE INSERT ON plugin_run_config_transitions WHEN NEW.run_id='` + pending.ID + `' BEGIN SELECT RAISE(ABORT,'synthetic pending proof failure'); END`); err != nil {
		t.Fatal(err)
	}
	original := run
	if err = p.patchRunConfig(ctx, &run, map[string]json.RawMessage{"test_once": json.RawMessage(`false`)}); err == nil {
		t.Fatal("pending proof failure ignored")
	}
	current, err := p.Config(ctx, run.InstanceID)
	if err != nil || current.Revision != value.Revision || !bytes.Equal(current.Value, value.Value) || run.ConfigRevision != original.ConfigRevision {
		t.Fatal("config/run leaked partial change", err)
	}
	for _, before := range []PluginRun{original, pending} {
		after, e := p.Run(ctx, before.ID)
		if e != nil || after.ConfigRevision != before.ConfigRevision || !bytes.Equal(after.Input, before.Input) || after.State != before.State {
			t.Fatal("partial run propagation", e)
		}
	}
	var n int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_run_config_transitions`).Scan(&n); err != nil || n != 0 {
		t.Fatal("partial proof propagation", n, err)
	}
}

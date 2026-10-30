package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// Fixed-source synchronous set_plugin_config publishes SystemConfigOper's cache
// before a real SQL update failure, without reaching init_plugin afterward.
func TestSharedManagerQueriesHTTPFailedSavePublishesConfiguration(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	managerInstall(t, a, cookie, "ALPHA")
	p := managerWorker(t, a, "OwnedAlpha")
	before := json.RawMessage(`{"ordinary":"persisted-before-http-failure"}`)
	if err := managerBaseSet(t, p, "OwnedAlpha", before); err != nil {
		t.Fatal(err)
	}
	initial := managerSnapshot(t, p)
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_http_write_failure BEFORE UPDATE ON plugin_values WHEN OLD.instance_id='OwnedAlpha' AND OLD.kind='config' BEGIN SELECT RAISE(ABORT,'owned HTTP save failure'); END`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_http_write_failure`); err != nil {
			t.Error(err)
		}
	}()
	res := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedAlpha", `{"ordinary":"published-before-http-failure","":"base-retains"}`, cookie, "")
	if res.Code != http.StatusInternalServerError {
		t.Fatal("actual SQL failure did not reach HTTP", res.Code)
	}
	disk, err := a.pluginStore.Config(context.Background(), "OwnedAlpha")
	if err != nil {
		t.Fatal(err)
	}
	managerEqual(t, disk.Value, before)
	if after := managerSnapshot(t, p); after.PID != initial.PID || after.InitCount != initial.InitCount {
		t.Fatal("failed save initialized or replaced worker")
	}
	managerEqual(t, managerQuery(t, p, "config", "OwnedAlpha"), json.RawMessage(`{"ordinary":"published-before-http-failure"}`))
	managerEqual(t, managerBaseGet(t, p, "OwnedAlpha", nil), json.RawMessage(`{"ordinary":"published-before-http-failure","":"base-retains"}`))
}

func TestSharedManagerQueriesHTTPReadFailurePublishesConfiguration(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	managerInstall(t, a, cookie, "ALPHA")
	p := managerWorker(t, a, "OwnedAlpha")
	if err := managerBaseSet(t, p, "OwnedAlpha", json.RawMessage(`{"ordinary":"persisted-before-http-failure"}`)); err != nil {
		t.Fatal(err)
	}
	initial := managerSnapshot(t, p)
	if _, err := a.Store.DB.Exec(`ALTER TABLE plugin_values RENAME TO owned_unavailable_values`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := a.Store.DB.Exec(`ALTER TABLE owned_unavailable_values RENAME TO plugin_values`); err != nil {
			t.Error(err)
		}
	}()
	res := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedAlpha", `{"ordinary":"published-before-http-read-failure"}`, cookie, "")
	if res.Code != http.StatusInternalServerError {
		t.Fatal("actual read failure did not reach HTTP", res.Code)
	}
	managerEqual(t, managerQuery(t, p, "config", "OwnedAlpha"), json.RawMessage(`{"ordinary":"published-before-http-read-failure"}`))
	managerEqual(t, managerBaseGet(t, p, "OwnedAlpha", nil), json.RawMessage(`{"ordinary":"published-before-http-read-failure"}`))
	if after := managerSnapshot(t, p); after.PID != initial.PID || after.InitCount != initial.InitCount {
		t.Fatal("failed read initialized or replaced worker")
	}
}

func TestSharedManagerQueriesHTTPSameFalseyAndCold(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	managerInstall(t, a, cookie, "ALPHA")
	p := managerWorker(t, a, "OwnedAlpha")
	initial := managerSnapshot(t, p)
	value := `{"ordinary":"successful-save"}`
	put := func(raw string) {
		t.Helper()
		res := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedAlpha", raw, cookie, "")
		assertOriginalRegistrySuccess(t, res.Code, res.Body.Bytes())
	}
	put(value)
	first, err := a.pluginStore.Config(context.Background(), "OwnedAlpha")
	if err != nil {
		t.Fatal(err)
	}
	put(value)
	second, err := a.pluginStore.Config(context.Background(), "OwnedAlpha")
	if err != nil || first.Revision != second.Revision {
		t.Fatal("same value rewrote config", first.Revision, second.Revision, err)
	}
	managerEqual(t, managerQuery(t, p, "config", "OwnedAlpha"), json.RawMessage(value))
	put(`{}`)
	managerEqual(t, managerQuery(t, p, "config", "OwnedAlpha"), map[string]any{})
	managerEqual(t, managerBaseGet(t, p, "OwnedAlpha", nil), map[string]any{})
	if _, err := a.pluginStore.Config(context.Background(), "OwnedAlpha"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("falsey source set did not delete row", err)
	}
	warm := managerSnapshot(t, p)
	if warm.InitCount != initial.InitCount+3 || warm.PID != initial.PID {
		t.Fatal("successful saves did not reinitialize same object", initial, warm)
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	markets := []string{}
	cold, err := New(Options{DataDir: dir, PluginMarketsOverride: &markets})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	fresh := cold.startOriginalPlugin(context.Background(), "OwnedAlpha")
	if fresh == nil {
		t.Fatal("cold worker did not start")
	}
	managerEqual(t, managerQuery(t, fresh, "config", "OwnedAlpha"), map[string]any{})
	managerEqual(t, managerBaseGet(t, fresh, "OwnedAlpha", nil), nil)
	if snap := managerSnapshot(t, fresh); snap.InitCount != 1 || snap.PID == warm.PID {
		t.Fatal("not cold independent SDK", snap)
	}
}

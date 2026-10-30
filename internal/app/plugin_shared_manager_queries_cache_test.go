package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// The oracle executes unchanged Manager/Base/SystemConfigOper and real SQLite.
// Failed writes publish even falsey cache entries before the following read outage.
func TestSharedManagerQueriesWarmCacheOutageValues(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/shared-plugin-manager/cache-outage-source.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Rows []struct {
			Input, Manager, Base json.RawMessage
			WriteError           string `json:"write_error"`
			ReadError            string `json:"read_error"`
		} `json:"failed_write_then_read_outage"`
	}
	if err = json.Unmarshal(raw, &oracle); err != nil || len(oracle.Rows) != 7 {
		t.Fatal("missing real source outage witness", err)
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	managerInstall(t, a, cookie, "ALPHA")
	p := managerWorker(t, a, "OwnedAlpha")
	managerEqual(t, managerBaseGet(t, p, "OwnedCachedMissing", nil), nil)
	outage := false
	defer func() {
		if outage {
			if _, e := a.Store.DB.Exec(`ALTER TABLE owned_unavailable_values RENAME TO plugin_values`); e != nil {
				t.Error(e)
			}
		}
	}()
	for _, row := range oracle.Rows {
		if row.WriteError != "IntegrityError" || row.ReadError != "OperationalError" {
			t.Fatal("source did not reach real SQL failures")
		}
		if err = managerBaseSet(t, p, "OwnedAlpha", map[string]any{"ordinary": "persisted before rejected write"}); err != nil {
			t.Fatal(err)
		}
		for _, stmt := range []string{
			`CREATE TRIGGER owned_reject_delete BEFORE DELETE ON plugin_values WHEN OLD.instance_id='OwnedAlpha' BEGIN SELECT RAISE(ABORT,'owned mutation failure'); END`,
			`CREATE TRIGGER owned_reject_update BEFORE UPDATE ON plugin_values WHEN OLD.instance_id='OwnedAlpha' BEGIN SELECT RAISE(ABORT,'owned mutation failure'); END`,
		} {
			if _, err = a.Store.DB.Exec(stmt); err != nil {
				t.Fatal(err)
			}
		}
		if err = managerBaseSet(t, p, "OwnedAlpha", row.Input); err == nil {
			t.Fatal("actual write did not fail", string(row.Input))
		}
		for _, stmt := range []string{`DROP TRIGGER owned_reject_delete`, `DROP TRIGGER owned_reject_update`, `ALTER TABLE plugin_values RENAME TO owned_unavailable_values`} {
			if _, err = a.Store.DB.Exec(stmt); err != nil {
				t.Fatal(err)
			}
		}
		outage = true
		if _, err = a.Store.DB.Exec(`SELECT * FROM plugin_values`); err == nil {
			t.Fatal("read outage not reached")
		}
		managerEqual(t, managerQuery(t, p, "config", "OwnedAlpha"), row.Manager)
		managerEqual(t, managerBaseGet(t, p, "OwnedAlpha", nil), row.Base)
		managerEqual(t, managerBaseGet(t, p, "OwnedCachedMissing", nil), nil)
		// The completed constructor snapshot also establishes missing namespaces,
		// matching source even when this specific namespace has never been read.
		managerEqual(t, managerBaseGet(t, p, "OwnedUncachedMissing", nil), nil)
		a.pluginOriginalBase.mu.Lock()
		copy, copyErr := a.originalBaseConfigLocked(context.Background(), "OwnedAlpha")
		if len(copy) > 0 {
			copy[0] = '!'
		}
		again, againErr := a.originalBaseConfigLocked(context.Background(), "OwnedAlpha")
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		_, canceledErr := a.originalBaseConfigLocked(canceled, "OwnedAlpha")
		a.pluginOriginalBase.mu.Unlock()
		if copyErr != nil || againErr != nil || !errors.Is(canceledErr, context.Canceled) {
			t.Fatal(copyErr, againErr, canceledErr)
		}
		managerEqual(t, again, row.Base)
		if _, err = a.Store.DB.Exec(`ALTER TABLE owned_unavailable_values RENAME TO plugin_values`); err != nil {
			t.Fatal(err)
		}
		outage = false
	}
}

func TestSharedManagerQueriesCacheNeverHidesInvalidSeal(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	managerInstall(t, a, cookie, "ALPHA")
	p := managerWorker(t, a, "OwnedAlpha")
	value := json.RawMessage(`{"ordinary":"known-valid"}`)
	if err := managerBaseSet(t, p, "OwnedAlpha", value); err != nil {
		t.Fatal(err)
	}
	managerEqual(t, managerQuery(t, p, "config", "OwnedAlpha"), value)
	var sealed []byte
	if err := a.Store.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id='OwnedAlpha' AND kind='config' AND item_key='main'`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := a.Store.DB.Exec(`UPDATE plugin_values SET sealed=? WHERE instance_id='OwnedAlpha' AND kind='config' AND item_key='main'`, sealed); err != nil {
			t.Error(err)
		}
	}()
	if _, err := a.Store.DB.Exec(`UPDATE plugin_values SET sealed=x'00' WHERE instance_id='OwnedAlpha' AND kind='config' AND item_key='main'`); err != nil {
		t.Fatal(err)
	}
	for _, call := range []struct {
		method string
		input  any
	}{
		{"query", map[string]any{"method": "config", "target": "OwnedAlpha"}},
		{"base", map[string]any{"target": "OwnedAlpha"}},
	} {
		if _, err := managerCall(t, p, call.method, call.input); err == nil {
			t.Fatal("warm cache hid invalid seal", call.method)
		}
	}
	if err := managerBaseSet(t, p, "OwnedAlpha", map[string]any{"ordinary": "must-not-publish"}); err == nil {
		t.Fatal("Base writer hid invalid seal")
	}
	res := callTest(t, a.Handler(), "PUT", "/api/plugins/OwnedAlpha", `{"ordinary":"must-not-publish"}`, cookie, "")
	if res.Code != 400 || !strings.Contains(res.Body.String(), `"PLUGIN_DATA_CORRUPT"`) {
		t.Fatal("HTTP writer hid invalid seal", res.Code)
	}
	a.pluginOriginalBase.mu.Lock()
	retained := copyOriginalBaseJSON(a.pluginOriginalBase.config["OwnedAlpha"].value)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, canceledErr := a.originalBaseUpdateConfigLocked(canceled, "OwnedAlpha", json.RawMessage(`{"ordinary":"canceled"}`))
	_, malformedErr := a.originalBaseUpdateConfigLocked(context.Background(), "OwnedAlpha", json.RawMessage(`not-json`))
	afterRejected := copyOriginalBaseJSON(a.pluginOriginalBase.config["OwnedAlpha"].value)
	a.pluginOriginalBase.mu.Unlock()
	if !errors.Is(canceledErr, context.Canceled) || malformedErr == nil {
		t.Fatal(canceledErr, malformedErr)
	}
	managerEqual(t, retained, value)
	managerEqual(t, afterRejected, value)
}

func TestSharedManagerQueriesPrefetchAtomicAndScoped(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	seal, err := a.pluginStore.seal("config", "OwnedPrefetched", "main", json.RawMessage(`{"ordinary":false}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []struct {
		id, kind, key string
		sealed        []byte
	}{
		{"OwnedPrefetched", "config", "main", seal},
		{"OwnedBad", "config", "main", []byte{0}},
		{"OwnedData", "data", "main", []byte{0}},
	} {
		if _, err := a.Store.DB.Exec(`INSERT INTO plugin_values(instance_id,kind,item_key,revision,sealed,updated_at) VALUES(?,?,?,1,?,?)`, stmt.id, stmt.kind, stmt.key, stmt.sealed, now()); err != nil {
			t.Fatal(err)
		}
	}
	// This uninitialized cache uses the real database/sealer but has no completed
	// constructor snapshot. No partial successful row may escape a later error.
	pending := &App{Store: a.Store, pluginStore: a.pluginStore}
	assertUnpublished := func() {
		t.Helper()
		if pending.pluginOriginalBase.snapshotReady || len(pending.pluginOriginalBase.config) != 0 {
			t.Fatal("partial constructor cache published")
		}
	}
	if err := pending.prefetchOriginalPluginConfig(ctx); err == nil {
		t.Fatal("constructor hid invalid config seal")
	}
	assertUnpublished()
	if _, err := a.Store.DB.Exec(`DELETE FROM plugin_values WHERE instance_id='OwnedBad'`); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := pending.prefetchOriginalPluginConfig(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertUnpublished()
	if _, err := a.Store.DB.Exec(`ALTER TABLE plugin_values RENAME TO owned_unavailable_values`); err != nil {
		t.Fatal(err)
	}
	err = pending.prefetchOriginalPluginConfig(ctx)
	_, readErr := pending.originalBaseConfigLocked(ctx, "OwnedPrefetched")
	if _, restoreErr := a.Store.DB.Exec(`ALTER TABLE owned_unavailable_values RENAME TO plugin_values`); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	if err == nil || readErr == nil {
		t.Fatal("incomplete constructor invented a cache", err, readErr)
	}
	assertUnpublished()
	if err := pending.prefetchOriginalPluginConfig(ctx); err != nil {
		t.Fatal(err)
	}
	if !pending.pluginOriginalBase.snapshotReady || len(pending.pluginOriginalBase.config) != 1 {
		t.Fatal("prefetch included non-config namespaces")
	}
	managerEqual(t, pending.pluginOriginalBase.config["OwnedPrefetched"].value, json.RawMessage(`{"ordinary":false}`))
}

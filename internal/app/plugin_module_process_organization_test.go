package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

// Only the owning disposable process fixture calls this. It rejects real
// SQLite writes, rather than returning a mocked success/failure HTTP response.
func setOwnedOrganizationFailure(t *testing.T, dir string, fail bool) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "light-media.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, operation := range []string{"INSERT", "UPDATE"} {
		name := "owned_organization_failure_" + operation
		query := "DROP TRIGGER IF EXISTS " + name
		if fail {
			query = "CREATE TRIGGER IF NOT EXISTS " + name + " BEFORE " + operation + " ON plugin_organization BEGIN SELECT RAISE(ABORT,'owned organization write rejected'); END"
		}
		if _, err = db.Exec(query); err != nil {
			t.Fatal("owned organization failure control", err)
		}
	}
}

func verifyOwnedProductionOrganization(t *testing.T, dir, repository string,
	request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) {
	t.Helper()
	const source = ownedStandardFixtureID
	const clone = source + "grouped"
	read := func() PluginOrganization {
		var value struct{ Data PluginOrganization }
		if json.Unmarshal(request("GET", "/api/plugins/organization", nil, 200), &value) != nil {
			t.Fatal("actual organization read")
		}
		return value.Data
	}
	echo := func(id string) map[string]json.RawMessage {
		var value map[string]json.RawMessage
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/echo?key=shared", nil, 200), &value) != nil || len(value["nonce"]) == 0 {
			t.Fatal("actual SDK read", id)
		}
		return value
	}
	before := echo(source)
	folders := json.RawMessage(`{"Z-first":{"plugins":["` + source + `","` + source + `"],"showIcon":false},"A-second":["` + source + `"],"ignored":23}`)
	request("POST", "/api/v1/plugin/folders", folders, 200)
	order := json.RawMessage(`[{"id":"A-second","type":"folder","order":0},{"id":"Z-first","type":"folder","order":1}]`)
	request("POST", "/api/v1/user/config/PluginOrder", order, 200)
	saved := read()
	success("POST", "/api/plugins/"+source+"/clone", map[string]any{"suffix": "grouped", "name": "Owned grouped clone"})
	grouped := read()
	var first struct{ Plugins []string }
	if json.Unmarshal(grouped.Folders["Z-first"], &first) != nil || len(first.Plugins) != 3 || first.Plugins[2] != clone || !bytes.Equal(grouped.Folders["A-second"], json.RawMessage(`["`+source+`"]`)) {
		t.Fatal("clone did not use first source dictionary membership", string(grouped.Folders["Z-first"]))
	}
	after := echo(source)
	for _, key := range []string{"nonce", "init_count", "config", "value"} {
		if !bytes.Equal(before[key], after[key]) {
			t.Fatal("grouping reinitialized or modified source SDK", key)
		}
	}
	stop()
	start()
	cold := read()
	if !originalBaseJSONEqual(cold.Order, grouped.Order) || !originalBaseJSONEqual(cold.Folders["Z-first"], grouped.Folders["Z-first"]) {
		t.Fatal("organization did not recover on actual cold startup")
	}
	restarted := echo(source)
	if bytes.Equal(restarted["nonce"], before["nonce"]) || !bytes.Equal(restarted["config"], before["config"]) || !bytes.Equal(restarted["value"], before["value"]) {
		t.Fatal("actual restart SDK/config/KV recovery")
	}
	setOwnedOrganizationFailure(t, dir, true)
	request("PUT", "/api/plugins/organization", map[string]any{"folders": map[string]any{}}, 500)
	failed := read()
	if !originalBaseJSONEqual(failed.Folders["Z-first"], cold.Folders["Z-first"]) || failed.Revision != cold.Revision {
		t.Fatal("real SQLite failure changed durable organization")
	}
	setOwnedOrganizationFailure(t, dir, false)
	success("DELETE", "/api/plugins/"+clone, nil)
	remaining := read()
	if !originalBaseJSONEqual(remaining.Folders["Z-first"], saved.Folders["Z-first"]) {
		t.Fatal("clone uninstall changed source membership")
	}
	// Fixed _remove_plugin_from_folders uses list.remove once per dictionary.
	success("DELETE", "/api/plugins/"+source, nil)
	removed := read()
	if json.Unmarshal(removed.Folders["Z-first"], &first) != nil || len(first.Plugins) != 1 || first.Plugins[0] != source {
		t.Fatal("uninstall did not preserve source first-only duplicate behavior")
	}
	// The surrounding lifecycle browser gate expects the source to be installed.
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": source, "repo_url": repository})
	request("PUT", "/api/plugins/organization", map[string]any{"folders": map[string]any{}, "order": []any{}}, 200)
	t.Log("actual organization: opaque JSON, first dictionary clone grouping, real independent SDK/config/KV, cold recovery, SQLite failed-write rollback, duplicate first-only uninstall; no physical grouping directories")
}

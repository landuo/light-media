package app

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
)

// Fixed plugins/__init__.py:239-334 delegates to the namespace-aware config,
// PluginData and local data-path helpers. Expected upsert identity, missing-key
// null, omitted-key rows and empty-key deletion come from plugindata_oper.py;
// config replacement/no-change/null comes from systemconfig_oper.py:30.
// Every observation here crosses a real cmd/server HTTP listener and SDK PID.
func verifyOwnedBaseProductionProcess(t *testing.T, dir, id, repository string,
	request func(string, string, any, int) []byte,
	success func(string, string, any), stop, start func()) {
	t.Helper()
	success("POST", "/api/plugins/install", map[string]any{"plugin_id": id, "repo_url": repository})
	type observed struct {
		originalBaseSDKSnapshot
		RepeatResult json.RawMessage        `json:"repeat_result"`
		Events       []plugins.MutableEvent `json:"events"`
	}
	read := func(instance, target string) observed {
		t.Helper()
		path := "/api/v1/plugin/" + instance + "/snapshot"
		if target != "" {
			path += "?target=" + url.QueryEscape(target)
		}
		var got observed
		if json.Unmarshal(request("GET", path, nil, 200), &got) != nil || !got.StaleDenied {
			t.Fatal("actual Base API observation failed or expired callback remained usable")
		}
		for _, row := range got.Rows {
			if row.Key == "owned_stale_must_not_exist" {
				t.Fatal("expired SDK callback wrote persisted data")
			}
		}
		return got
	}
	plan := func(instance string, value map[string]any) {
		t.Helper()
		success("PUT", "/api/plugins/"+instance, map[string]any{"enabled": false, "owned_plan": value})
	}
	keyID := func(got observed, key string) int64 {
		t.Helper()
		for _, row := range got.Rows {
			if row.Key == key {
				return row.ID
			}
		}
		t.Fatal("missing actual database row", key)
		return 0
	}
	initial := read(id, "")
	if initial.InitCount != 1 || string(initial.Config) != "null" || string(initial.Value) != "null" || len(initial.Rows) != 0 || initial.Path != filepath.Join(dir, "plugin-data", id) {
		t.Fatal("Base initial namespace/path did not match source")
	}
	request("GET", "/api/v1/plugin/"+id+"/snapshot?target="+url.QueryEscape("../escape"), nil, 502)
	if _, err := os.Stat(filepath.Join(dir, "escape")); !os.IsNotExist(err) {
		t.Fatal("rejected Base path escaped its data directory", err)
	}
	plan(id, map[string]any{"config": map[string]any{"marker": "base-source"}, "key": "shared", "value": json.RawMessage(`{"large":900719925474099312345,"nested":[false,0,null]}`), "repeat_config": true})
	written := read(id, "")
	if written.InitCount != 2 || written.UpdateResult == nil || !*written.UpdateResult || string(written.RepeatResult) != "null" {
		t.Fatal("Base update_config reinitialized itself or changed True/None return behavior")
	}
	assertOriginalBaseJSON(t, written.InitAfter, `{"marker":"base-source"}`)
	assertOriginalBaseJSON(t, written.Config, `{"marker":"base-source"}`)
	assertOriginalBaseJSON(t, written.Value, `{"large":900719925474099312345,"nested":[false,0,null]}`)
	firstID := keyID(written, "shared")
	plan(id, map[string]any{"config": map[string]any{"marker": "base-source"}, "key": "shared", "value": false})
	if got := read(id, ""); string(got.Value) != "false" || keyID(got, "shared") != firstID {
		t.Fatal("same-key upsert changed row identity or collapsed false")
	}
	// An explicit target is the original Base namespace argument, not a new
	// installation or a clone-management operation.
	unknown := "OwnedBaseUninstalled"
	plan(id, map[string]any{"target": unknown, "config": map[string]any{"marker": "external"}, "key": "shared", "value": 0})
	other := read(id, unknown)
	assertOriginalBaseJSON(t, other.Config, `{"marker":"external"}`)
	if string(other.Value) != "0" || other.Path != filepath.Join(dir, "plugin-data", unknown) || string(read(id, "").Value) != "false" {
		t.Fatal("explicit namespace changed the caller's data or lost numeric zero")
	}
	var installed struct{ Data []string }
	if json.Unmarshal(request("GET", "/api/plugins/installed", nil, 200), &installed) != nil {
		t.Fatal("missing actual installed list")
	}
	for _, item := range installed.Data {
		if item == unknown {
			t.Fatal("Base namespace write fabricated installation membership")
		}
	}
	clone := id + "base"
	success("POST", "/api/plugins/"+id+"/clone", map[string]string{"suffix": "Base"})
	cloned := read(clone, "")
	if string(cloned.Value) != "null" || cloned.Path == initial.Path {
		t.Fatal("clone copied source KV or shared its data directory")
	}
	plan(clone, map[string]any{"config": map[string]any{"marker": "base-clone"}, "key": "shared", "value": "clone"})
	if string(read(id, "").Value) != "false" || string(read(clone, "").Value) != `"clone"` {
		t.Fatal("default clone namespace leaked into source")
	}
	file := filepath.Join(cloned.Path, "owned-base-file.txt")
	if err := os.WriteFile(file, []byte("base file remains"), 0600); err != nil {
		t.Fatal(err)
	}
	// Fixed system.py:907 sends the committed value to enabled plugin
	// subscribers. Exercise the actual settings HTTP producer, not an injected
	// event or a direct call to the dispatch registry.
	counts := map[string]int{id: len(read(id, "").Events), clone: len(read(clone, "").Events)}
	sources := []string{repository, repository + "?event-probe=1"}
	success("PUT", "/api/plugins/markets", map[string]any{"plugin_markets": sources})
	for _, instance := range []string{id, clone} {
		deadline := time.Now().Add(3 * time.Second)
		var got observed
		for {
			got = read(instance, "")
			if len(got.Events) > counts[instance] {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("real settings commit did not reach enabled plugin subscriber", instance)
			}
			time.Sleep(25 * time.Millisecond)
		}
		var payload struct {
			Key        []string `json:"key"`
			Value      string   `json:"value"`
			ChangeType string   `json:"change_type"`
		}
		event := got.Events[counts[instance]]
		if event.Type != "config.updated" || json.Unmarshal(event.Data, &payload) != nil || len(payload.Key) != 1 || payload.Key[0] != "PLUGIN_MARKET" || payload.Value != strings.Join(sources, ",") || payload.ChangeType != "update" {
			t.Fatal("real settings event lost the original committed value", instance)
		}
	}
	success("PUT", "/api/plugins/markets", map[string]any{"plugin_markets": []string{repository}})
	// Delete one key, then an empty key deletes all database rows. A missing
	// keyed lookup stays JSON null; enumeration stays an empty list.
	beforeDelete := read(clone, "")
	plan(clone, map[string]any{"delete": "shared"})
	deleted := read(clone, "")
	if string(deleted.Value) != "null" || len(deleted.Rows) != len(beforeDelete.Rows)-1 {
		t.Fatal("key deletion changed more than its own database record")
	}
	// Clone creation includes real stop/reload calls. This fixture's stop
	// callback persists owned_stop; keyed deletion must retain that other row.
	for _, previous := range beforeDelete.Rows {
		if previous.Key == "shared" {
			continue
		}
		found := false
		for _, current := range deleted.Rows {
			if current.ID == previous.ID && current.Key == previous.Key && originalBaseJSONEqual(current.Value, previous.Value) {
				found = true
			}
		}
		if !found {
			t.Fatal("key deletion removed or mutated an unrelated row", previous.Key)
		}
	}
	plan(id, map[string]any{"key": "one", "value": 1})
	plan(id, map[string]any{"delete": ""})
	if got := read(id, ""); len(got.Rows) != 0 || string(got.Value) != "null" {
		t.Fatal("empty-key delete did not clear namespace rows")
	}
	plan(id, map[string]any{"config": map[string]any{}, "key": "shared", "value": "restart-kept"})
	assertOriginalBaseJSON(t, read(id, "").Config, `{}`)
	stop()
	start()
	restarted := read(id, "")
	if restarted.InitCount != 1 || string(restarted.Config) != "null" || string(restarted.Value) != `"restart-kept"` {
		t.Fatal("restart did not distinguish empty-config memory cache from deleted DB row and persisted KV")
	}
	if string(read(id, unknown).Value) != "0" {
		t.Fatal("restart lost explicit namespace data")
	}
	success("POST", "/api/plugins/"+clone+"/reset", nil)
	if got := read(clone, ""); string(got.Config) != "null" || string(got.Value) != "null" {
		t.Fatal("reset did not clear only clone DB configuration/data")
	}
	if raw, err := os.ReadFile(file); err != nil || string(raw) != "base file remains" {
		t.Fatal("reset removed Base data-path files", err)
	}
	success("DELETE", "/api/plugins/"+clone, nil)
	success("DELETE", "/api/plugins/"+id, nil)
	t.Log("Base production HTTP/SDK verified: config replacement/None/cache, KV upsert/rows/delete, explicit namespaces, clone and file isolation, restart/reset/uninstall; actual settings producer delivers committed config.updated values to source/clone")
}

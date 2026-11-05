package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

func hostDownloaderFixture(t *testing.T) (*App, *http.Cookie, *originalPluginInstance) {
	t.Helper()
	binary := os.Getenv("PLUGIN_HOST_DOWNLOADER_FIXTURE_BINARY")
	if binary == "" {
		t.Fatal("PLUGIN_HOST_DOWNLOADER_FIXTURE_BINARY must name the separately built standard SDK worker")
	}
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	manifest := installOwnedResourceProcessWorker(t, a, cookie, binary)
	value, ok := a.pluginRunningInstances.Load(manifest.PluginID)
	if !ok {
		t.Fatal("installed standard SDK worker not running")
	}
	return a, cookie, value.(*originalPluginInstance)
}

func hostDownloaderCall(t *testing.T, object *originalPluginInstance, method string, value any, out any) error {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	result, err := object.callSession(context.Background(), plugins.Runner{Timeout: 10 * time.Second}, method, raw, nil)
	if err != nil {
		return err
	}
	if out != nil {
		return json.Unmarshal(result.Result, out)
	}
	return nil
}

func TestHostDownloaderCompletedActualSDKFixedSourceOracle(t *testing.T) {
	var oracle struct {
		SourceCommit string `json:"source_commit"`
		Cases        []struct {
			Kind     string            `json:"kind"`
			Tags     []string          `json:"tags"`
			Rows     []json.RawMessage `json:"rows"`
			Expected []string          `json:"expected_hashes"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-downloader-20261031/source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Cases) != 8 {
		t.Fatal("fixed source oracle unavailable", err)
	}
	for i, scenario := range oracle.Cases {
		t.Run(fmt.Sprintf("%d-%s", i, scenario.Kind), func(t *testing.T) {
			a, _, object := hostDownloaderFixture(t)
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario.Kind == "qbittorrent" {
					if r.URL.Path != "/api/v2/torrents/info" || r.Method != "POST" {
						t.Error("unexpected qB endpoint")
						http.Error(w, "unexpected", 400)
						return
					}
					_ = r.ParseForm()
					if r.Form.Get("filter") != "completed" || r.Form.Has("hashes") {
						t.Error("source completed selector changed")
					}
					reads.Add(1)
					_ = json.NewEncoder(w).Encode(scenario.Rows)
					return
				}
				var envelope map[string]json.RawMessage
				if r.URL.Path != "/transmission/rpc" || json.NewDecoder(r.Body).Decode(&envelope) != nil {
					http.Error(w, "unexpected", 400)
					return
				}
				var method string
				_ = json.Unmarshal(envelope["method"], &method)
				if method == "session_get" {
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"version":"4.1"}}`, envelope["id"])
					return
				}
				if method != "torrent_get" {
					t.Error("unexpected Transmission method", method)
					return
				}
				var args map[string]json.RawMessage
				_ = json.Unmarshal(envelope["params"], &args)
				if _, all := args["ids"]; all {
					t.Error("enumeration unexpectedly selected IDs")
				}
				reads.Add(1)
				body, _ := json.Marshal(scenario.Rows)
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"torrents":%s}}`, envelope["id"], body)
			}))
			defer server.Close()
			settings := a.Settings()
			settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: scenario.Kind, BaseURL: server.URL}}}
			ownedDownloaderSave(t, a, settings)
			var out plugins.DownloaderTasksV1
			if err := hostDownloaderCall(t, object, "completed", plugins.DownloaderCompletedRequestV1{DownloaderID: "owned", Tags: scenario.Tags}, &out); err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, task := range out.Tasks {
				got = append(got, task.Hash)
			}
			if !reflect.DeepEqual(got, scenario.Expected) || reads.Load() != 1 {
				t.Fatalf("fixed source completion mismatch: got %v want %v, reads %d", got, scenario.Expected, reads.Load())
			}
			if err := hostDownloaderCall(t, object, "completed", plugins.DownloaderCompletedRequestV1{DownloaderID: "owned", Hashes: []string{}}, &out); err != nil || len(out.Tasks) != 0 || reads.Load() != 1 {
				t.Fatal("explicit empty selector broadened", err)
			}
		})
	}
}

func TestHostDownloaderQBMutationActualSDKAndFailureNoReplay(t *testing.T) {
	a, cookie, object := hostDownloaderFixture(t)
	var mu sync.Mutex
	calls := []string{}
	var reject atomic.Bool
	var addPaused atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer OWNED_API_KEY" {
			t.Error("host credential missing")
		}
		endpoint := strings.TrimPrefix(r.URL.Path, "/api/v2/")
		if endpoint == "app/webapiVersion" {
			_, _ = io.WriteString(w, "2.11.0")
			return
		}
		if endpoint == "torrents/tags" {
			_, _ = io.WriteString(w, `["owned","two"]`)
			return
		}
		if endpoint == "torrents/add" {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			wantPaused := "False"
			if addPaused.Load() {
				wantPaused = "True"
			}
			if r.FormValue("paused") != wantPaused || r.FormValue("stopped") != wantPaused || r.FormValue("savepath") != "/owned/downloads" || r.FormValue("tags") != "owned" || r.FormValue("autoTMM") != "False" || r.FormValue("category") != "" || r.FormValue("upLimit") != "1024" || r.FormValue("dlLimit") != "2048" {
				t.Error("source add parameters changed")
			}
			file, _, err := r.FormFile("torrent__0")
			if err != nil {
				t.Error("torrent material missing", err)
			} else {
				defer file.Close()
				content, _ := io.ReadAll(file)
				if string(content) != "owned material" {
					t.Error("material altered")
				}
			}
			mu.Lock()
			calls = append(calls, endpoint)
			mu.Unlock()
			_, _ = io.WriteString(w, `{"added_torrent_ids":{"`+ownedDownloaderHash+`":{}},"errors":{}}`)
			return
		}
		_ = r.ParseForm()
		if endpoint != "torrents/deleteTags" && r.Form.Get("hashes") != ownedDownloaderHash {
			t.Error("mutation selector changed", endpoint)
		}
		if endpoint == "torrents/delete" && r.Form.Get("deleteFiles") != "false" {
			t.Fatal("task removal selected deletion of data")
		}
		mu.Lock()
		calls = append(calls, endpoint)
		mu.Unlock()
		if reject.Load() {
			http.Error(w, "OWNED_PRIVATE_ERROR", 403)
		}
	}))
	defer server.Close()
	settings := a.Settings()
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL, APIKey: "OWNED_API_KEY", AutoCategory: true}}}
	ownedDownloaderSave(t, a, settings)
	for _, paused := range []bool{false, true} {
		addPaused.Store(paused)
		path, no := "/owned/downloads", false
		up, down := int64(1024), int64(2048)
		var out plugins.DownloaderMutationV1
		if err := hostDownloaderCall(t, object, "add", plugins.DownloaderAddRequestV1{DownloaderID: "owned", Torrent: []byte("owned material"), SavePath: &path, Paused: paused, Tags: []string{"owned"}, IgnoreCategoryCheck: &no, UploadLimitBytes: &up, DownloadLimitBytes: &down}, &out); err != nil || !out.Accepted {
			t.Fatal("actual add failed", err, out)
		}
	}
	for _, action := range []string{"start", "pause", "recheck", "remove_task", "reannounce", "set_tags", "remove_tags", "delete_tags", "set_category"} {
		request := plugins.DownloaderControlRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash}, Action: action}
		if strings.Contains(action, "tags") {
			request.Tags = []string{"owned"}
		}
		if action == "set_category" {
			category := "new-category"
			request.Category = &category
		}
		var out plugins.DownloaderMutationV1
		if err := hostDownloaderCall(t, object, "control", request, &out); err != nil || !out.Accepted || out.Outcome != "confirmed" {
			t.Fatal("actual control failed", action, err, out)
		}
	}
	var tags plugins.DownloaderTagsV1
	if err := hostDownloaderCall(t, object, "tags", plugins.DownloaderTagsRequestV1{DownloaderID: "owned"}, &tags); err != nil || !reflect.DeepEqual(tags.Tags, []string{"owned", "two"}) {
		t.Fatal("actual tags failed", err, tags)
	}
	var deletedDefinitions plugins.DownloaderMutationV1
	if err := hostDownloaderCall(t, object, "delete_tags", plugins.DownloaderDeleteTagsRequestV1{DownloaderID: "owned", Tags: []string{"global-only"}}, &deletedDefinitions); err != nil || !deletedDefinitions.Accepted {
		t.Fatal("direct vendor tag-definition deletion unavailable", err, deletedDefinitions)
	}
	want := []string{"torrents/add", "torrents/add", "torrents/start", "torrents/stop", "torrents/recheck", "torrents/delete", "torrents/reannounce", "torrents/addTags", "torrents/removeTags", "torrents/removeTags", "torrents/deleteTags", "torrents/setCategory", "torrents/deleteTags"}
	mu.Lock()
	got := append([]string{}, calls...)
	mu.Unlock()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("source control delegation differs: %v", got)
	}
	reject.Store(true)
	err := hostDownloaderCall(t, object, "control", plugins.DownloaderControlRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash}, Action: "start"}, nil)
	if err == nil || strings.Contains(err.Error(), "OWNED_PRIVATE_ERROR") || strings.Contains(err.Error(), "OWNED_API_KEY") {
		t.Fatal("ambiguous rejected mutation succeeded or leaked remote data", err)
	}
	mu.Lock()
	count := len(calls)
	mu.Unlock()
	if count != len(want)+1 {
		t.Fatal("dispatched mutation retried")
	}
	reject.Store(false)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+object.installation.PluginID+"/clone", `{"suffix":"Twin"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	clone, ok := a.pluginRunningInstances.Load(object.installation.PluginID + "twin")
	if !ok {
		t.Fatal("clone not running")
	}
	var sourcePID, clonePID int
	if hostDownloaderCall(t, object, "worker_pid", nil, &sourcePID) != nil || hostDownloaderCall(t, clone.(*originalPluginInstance), "worker_pid", nil, &clonePID) != nil || sourcePID == clonePID || sourcePID == os.Getpid() {
		t.Fatal("source/clone worker identity not isolated")
	}
	if err := hostDownloaderCall(t, clone.(*originalPluginInstance), "tags", plugins.DownloaderTagsRequestV1{DownloaderID: "owned"}, &tags); err != nil {
		t.Fatal("clone current host scope failed", err)
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err := cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	cold.wg.Wait()
	value, ok := cold.pluginRunningInstances.Load(object.installation.PluginID)
	if !ok {
		t.Fatal("installed source did not recover on cold start")
	}
	var coldPID int
	if hostDownloaderCall(t, value.(*originalPluginInstance), "worker_pid", nil, &coldPID) != nil || coldPID == sourcePID {
		t.Fatal("cold source process not restarted")
	}
	if err := hostDownloaderCall(t, value.(*originalPluginInstance), "tags", plugins.DownloaderTagsRequestV1{DownloaderID: "owned"}, &tags); err != nil {
		t.Fatal("cold resource settings/credentials not restored", err)
	}
	// A cold restart only reinitializes; it must never replay a prior mutation.
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != count {
		t.Fatal("cold restart replayed downloader mutation")
	}
}

func TestHostDownloaderTransmissionMutationsKeepActualOwnedFile(t *testing.T) {
	for _, modern := range []bool{false, true} {
		t.Run(fmt.Sprint(modern), func(t *testing.T) {
			a, _, object := hostDownloaderFixture(t)
			file := filepath.Join(t.TempDir(), "owned-seeding-file")
			if os.WriteFile(file, []byte("owned payload to retain"), 0600) != nil {
				t.Fatal("fixture file create failed")
			}
			var removals, controls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var envelope map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&envelope)
				var method string
				_ = json.Unmarshal(envelope["method"], &method)
				if !modern && method == "session_get" {
					_, _ = io.WriteString(w, `{"result":"method name not recognized","arguments":{}}`)
					return
				}
				key, id := "arguments", envelope["tag"]
				if modern {
					key, id = "params", envelope["id"]
				}
				var args map[string]json.RawMessage
				_ = json.Unmarshal(envelope[key], &args)
				if method != "session-get" && method != "session_get" {
					var hashes []string
					_ = json.Unmarshal(args["ids"], &hashes)
					if !reflect.DeepEqual(hashes, []string{ownedDownloaderHash}) {
						t.Error("Transmission selected wrong task")
					}
					controls.Add(1)
				}
				if method == "torrent_remove" || method == "torrent-remove" {
					removals.Add(1)
					var deleteData bool
					parameter := "delete-local-data"
					if modern {
						parameter = "delete_local_data"
					}
					value, exists := args[parameter]
					if !exists || json.Unmarshal(value, &deleteData) != nil || deleteData {
						_ = os.Remove(file)
						t.Error("Transmission delete selected files or omitted explicit false")
					}
				}
				if method == "torrent_set" || method == "torrent-set" {
					var labels []string
					if json.Unmarshal(args["labels"], &labels) != nil || len(labels) != 2 || labels[0] != "existing" || labels[1] != "owned" {
						t.Error("source org_tags set union not preserved", labels)
					}
				}
				if modern {
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"version":"4.1"}}`, id)
				} else {
					_, _ = fmt.Fprintf(w, `{"tag":%s,"result":"success","arguments":{"version":"4.0"}}`, id)
				}
			}))
			defer server.Close()
			settings := a.Settings()
			settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "transmission", BaseURL: server.URL}}}
			ownedDownloaderSave(t, a, settings)
			for _, action := range []string{"start", "pause", "recheck", "remove_task", "set_tags"} {
				request := plugins.DownloaderControlRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash}, Action: action}
				if action == "set_tags" {
					request.Tags = []string{"owned", "owned"}
					request.OriginalTags = []string{"existing", "owned"}
				}
				var out plugins.DownloaderMutationV1
				if err := hostDownloaderCall(t, object, "control", request, &out); err != nil || !out.Accepted {
					t.Fatal(action, err, out)
				}
			}
			var rejected plugins.DownloaderMutationV1
			if err := hostDownloaderCall(t, object, "control", plugins.DownloaderControlRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash}, Action: "set_tags", Tags: []string{}}, &rejected); err != nil || rejected.Accepted || rejected.Outcome != "rejected" || controls.Load() != 5 {
				t.Fatal("empty TR tag array did not return source false without RPC", err, rejected, controls.Load())
			}
			content, err := os.ReadFile(file)
			if err != nil || string(content) != "owned payload to retain" || removals.Load() != 1 || controls.Load() != 5 {
				t.Fatal("task-only removal lost data or wrong operations", err, removals.Load(), controls.Load())
			}
		})
	}
}

func TestHostDownloaderCurrentScopeAndStrictParameters(t *testing.T) {
	a, _, object := hostDownloaderFixture(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, `[]`)
	}))
	defer server.Close()
	settings := a.Settings()
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}}}
	ownedDownloaderSave(t, a, settings)
	identity := plugins.Identity{PluginID: object.installation.PluginID, InstanceID: object.installation.InstanceID, Version: object.installation.Version}
	callback, revoke := object.originalBaseSessionHost(context.Background(), "completed", nil)
	for _, scenario := range []struct{ method, raw string }{
		{plugins.DownloaderCompletedCapability, `null`},
		{plugins.DownloaderCompletedCapability, `{"downloader_id":"owned","url":"http://foreign.invalid"}`},
		{plugins.DownloaderCompletedCapability, `{"downloader_id":"owned","hashes":["all"]}`},
		{plugins.DownloaderControlCapability, `{"downloader_id":"owned","hashes":[],"action":"start"}`},
		{plugins.DownloaderControlCapability, `{"downloader_id":"owned","hashes":["` + ownedDownloaderHash + `"],"action":"remove_task","delete_files":true}`},
		{plugins.DownloaderAddCapability, `{"downloader_id":"owned","magnet":"https://foreign.invalid"}`},
		{plugins.DownloaderAddCapability, `{"downloader_id":"owned","torrent":"YQ==","password":"foreign"}`},
	} {
		if result, err := callback(context.Background(), identity, scenario.method, json.RawMessage(scenario.raw)); err == nil || len(result) != 0 {
			t.Fatal("invalid resource accepted", scenario.method)
		}
	}
	wrong := identity
	wrong.InstanceID += "foreign"
	if _, err := callback(context.Background(), wrong, plugins.DownloaderCompletedCapability, json.RawMessage(`{"downloader_id":"owned","hashes":[]}`)); err == nil {
		t.Fatal("foreign SDK instance accepted")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := callback(cancelled, identity, plugins.DownloaderCompletedCapability, json.RawMessage(`{"downloader_id":"owned"}`)); err == nil {
		t.Fatal("cancelled call dispatched")
	}
	revoke()
	if _, err := callback(context.Background(), identity, plugins.DownloaderCompletedCapability, json.RawMessage(`{"downloader_id":"owned"}`)); err == nil {
		t.Fatal("expired callback dispatched")
	}
	if hits.Load() != 0 {
		t.Fatal("invalid identity/parameters/scope reached downloader")
	}
}

func TestHostDownloaderFixedSourceMalformedFilterOrder(t *testing.T) {
	var oracle struct {
		Faults []struct {
			Kind          string            `json:"kind"`
			Name          string            `json:"name"`
			Rows          []json.RawMessage `json:"rows"`
			ExpectedError bool              `json:"expected_error"`
			ExpectedCount int               `json:"expected_count"`
		} `json:"faults"`
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-downloader-20261031/source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Faults) != 2 {
		t.Fatal("fixed source fault oracle unavailable", err)
	}
	for _, scenario := range oracle.Faults {
		t.Run(scenario.Name, func(t *testing.T) {
			a, _, object := hostDownloaderFixture(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario.Kind == "qbittorrent" {
					_ = json.NewEncoder(w).Encode(scenario.Rows)
					return
				}
				var envelope map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&envelope)
				var method string
				_ = json.Unmarshal(envelope["method"], &method)
				if method == "session_get" {
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"version":"4.1"}}`, envelope["id"])
					return
				}
				body, _ := json.Marshal(scenario.Rows)
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"torrents":%s}}`, envelope["id"], body)
			}))
			defer server.Close()
			settings := a.Settings()
			settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: scenario.Kind, BaseURL: server.URL}}}
			ownedDownloaderSave(t, a, settings)
			var out plugins.DownloaderTasksV1
			err := hostDownloaderCall(t, object, "completed", plugins.DownloaderCompletedRequestV1{DownloaderID: "owned", Tags: []string{"required"}}, &out)
			if (err != nil) != scenario.ExpectedError || err == nil && len(out.Tasks) != scenario.ExpectedCount {
				t.Fatal("source filter/error order changed", err, out)
			}
		})
	}
}

func TestHostDownloaderAllTaskListActualSDKSourceRows(t *testing.T) {
	var oracle struct {
		Cases []struct {
			Kind string            `json:"kind"`
			Tags []string          `json:"tags"`
			Rows []json.RawMessage `json:"rows"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-downloader-20261031/source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil {
		t.Fatal(err)
	}
	for _, scenario := range oracle.Cases {
		if scenario.Tags != nil {
			continue
		}
		t.Run(scenario.Kind, func(t *testing.T) {
			a, _, object := hostDownloaderFixture(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario.Kind == "qbittorrent" {
					_ = r.ParseForm()
					if len(r.Form) != 0 {
						t.Error("all-task query invented a native filter")
					}
					_ = json.NewEncoder(w).Encode(scenario.Rows)
					return
				}
				var env map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&env)
				var method string
				_ = json.Unmarshal(env["method"], &method)
				if method == "session_get" {
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"version":"4.1"}}`, env["id"])
					return
				}
				var args map[string]json.RawMessage
				_ = json.Unmarshal(env["params"], &args)
				if _, ok := args["ids"]; ok {
					t.Error("all-task enumeration unexpectedly restricted IDs")
				}
				body, _ := json.Marshal(scenario.Rows)
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"torrents":%s}}`, env["id"], body)
			}))
			defer server.Close()
			settings := a.Settings()
			settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: scenario.Kind, BaseURL: server.URL}}}
			ownedDownloaderSave(t, a, settings)
			var out plugins.DownloaderTasksV1
			if err := hostDownloaderCall(t, object, "list", plugins.DownloaderListRequestV1{DownloaderID: "owned"}, &out); err != nil || len(out.Tasks) != len(scenario.Rows) {
				t.Fatal("all-task source rows filtered or unavailable", err, len(out.Tasks), len(scenario.Rows))
			}
		})
	}
}

func TestHostDownloaderTransmissionLimitsActualSDKFixedSource(t *testing.T) {
	var oracle struct {
		Cases []struct {
			Up    *int64 `json:"upload_limit_kb"`
			Down  *int64 `json:"download_limit_kb"`
			Calls []struct {
				Arguments map[string]any `json:"arguments"`
			} `json:"calls"`
		} `json:"tr_limit_cases"`
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-downloader-20261031/source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 3 {
		t.Fatal("source limits oracle unavailable", err)
	}
	for _, modern := range []bool{false, true} {
		t.Run(fmt.Sprint(modern), func(t *testing.T) {
			a, _, object := hostDownloaderFixture(t)
			var selected map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var env map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&env)
				var method string
				_ = json.Unmarshal(env["method"], &method)
				if !modern && method == "session_get" {
					_, _ = io.WriteString(w, `{"result":"method name not recognized","arguments":{}}`)
					return
				}
				key, id := "arguments", env["tag"]
				if modern {
					key, id = "params", env["id"]
				}
				if method == "torrent_set" || method == "torrent-set" {
					selected = nil
					_ = json.Unmarshal(env[key], &selected)
				}
				if modern {
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"version":"4.1"}}`, id)
				} else {
					_, _ = fmt.Fprintf(w, `{"tag":%s,"result":"success","arguments":{"version":"4.0"}}`, id)
				}
			}))
			defer server.Close()
			settings := a.Settings()
			settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: "transmission", BaseURL: server.URL}}}
			ownedDownloaderSave(t, a, settings)
			for _, scenario := range oracle.Cases {
				var out plugins.DownloaderMutationV1
				if err := hostDownloaderCall(t, object, "control", plugins.DownloaderControlRequestV1{DownloaderID: "owned", Hashes: []string{ownedDownloaderHash}, Action: "set_limits", UploadLimitKB: scenario.Up, DownloadLimitKB: scenario.Down}, &out); err != nil || !out.Accepted {
					t.Fatal("source speed mutation unavailable", err, out)
				}
				want := scenario.Calls[0].Arguments
				for sourceKey, wireKey := range map[string]string{"uploadLimit": "upload_limit", "uploadLimited": "upload_limited", "downloadLimit": "download_limit", "downloadLimited": "download_limited"} {
					if !modern {
						wireKey = sourceKey
					}
					if !reflect.DeepEqual(selected[wireKey], want[sourceKey]) {
						t.Fatal("source KB/s or Python truthiness changed", sourceKey, selected, want)
					}
				}
			}
		})
	}
}

func TestHostDownloaderDownloadingActualSDKFixedSourceOracle(t *testing.T) {
	var oracle struct {
		SourceCommit string `json:"source_commit"`
		Cases        []struct {
			Kind     string            `json:"kind"`
			Tags     []string          `json:"tags"`
			Rows     []json.RawMessage `json:"rows"`
			Expected []string          `json:"expected_hashes"`
		} `json:"downloading_cases"`
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-downloader-20261031/source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || oracle.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Cases) != 8 {
		t.Fatal("fixed source oracle unavailable", err)
	}
	for i, scenario := range oracle.Cases {
		t.Run(fmt.Sprintf("%d-%s", i, scenario.Kind), func(t *testing.T) {
			a, _, object := hostDownloaderFixture(t)
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario.Kind == "qbittorrent" {
					if r.URL.Path != "/api/v2/torrents/info" || r.Method != "POST" {
						t.Error("unexpected qB endpoint")
						http.Error(w, "unexpected", 400)
						return
					}
					_ = r.ParseForm()
					if r.Form.Get("filter") != "downloading" || r.Form.Has("hashes") {
						t.Error("source downloading selector changed")
					}
					reads.Add(1)
					_ = json.NewEncoder(w).Encode(scenario.Rows)
					return
				}
				var envelope map[string]json.RawMessage
				if r.URL.Path != "/transmission/rpc" || json.NewDecoder(r.Body).Decode(&envelope) != nil {
					http.Error(w, "unexpected", 400)
					return
				}
				var method string
				_ = json.Unmarshal(envelope["method"], &method)
				if method == "session_get" {
					_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"version":"4.1"}}`, envelope["id"])
					return
				}
				if method != "torrent_get" {
					t.Error("unexpected Transmission method", method)
					return
				}
				var args map[string]json.RawMessage
				_ = json.Unmarshal(envelope["params"], &args)
				if _, all := args["ids"]; all {
					t.Error("enumeration unexpectedly selected IDs")
				}
				reads.Add(1)
				body, _ := json.Marshal(scenario.Rows)
				_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"torrents":%s}}`, envelope["id"], body)
			}))
			defer server.Close()
			settings := a.Settings()
			settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned", Kind: scenario.Kind, BaseURL: server.URL}}}
			ownedDownloaderSave(t, a, settings)
			var out plugins.DownloaderTasksV1
			if err := hostDownloaderCall(t, object, "downloading", plugins.DownloaderCompletedRequestV1{DownloaderID: "owned", Tags: scenario.Tags}, &out); err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, task := range out.Tasks {
				got = append(got, task.Hash)
			}
			if !reflect.DeepEqual(got, scenario.Expected) || reads.Load() != 1 {
				t.Fatalf("fixed source downloading mismatch: got %v want %v, reads %d", got, scenario.Expected, reads.Load())
			}
			if err := hostDownloaderCall(t, object, "downloading", plugins.DownloaderCompletedRequestV1{DownloaderID: "owned", Hashes: []string{}}, &out); err != nil || len(out.Tasks) != 0 || reads.Load() != 1 {
				t.Fatal("explicit empty selector broadened", err)
			}
		})
	}
}

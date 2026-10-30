package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func originalCenterSetList(t *testing.T, a *App, ids []string) {
	t.Helper()
	raw, _ := json.Marshal(ids)
	sealed, e := a.pluginStore.seal(originalInstalledOrderKind, "admin", "main", raw)
	if e != nil {
		t.Fatal(e)
	}
	_, e = a.Store.DB.Exec(`INSERT INTO records(kind,id,payload,updated_at) VALUES(?,'main',?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, originalInstalledOrderKind, sealed, now())
	if e != nil {
		t.Fatal(e)
	}
}
func TestOriginalPluginCenterInventoryAuthorityAndBudgets(t *testing.T) {
	originalCenterTestEnvironment(t)
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"plugin_id":"Ghost","average_rating":4.5}`))
	}))
	defer peer.Close()
	settings := a.Settings()
	settings.PluginServerURL = originalServerURLPointer(peer.URL)
	if e := a.saveSettings(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	index := guideMarketIndex("Owned", "https://owned.invalid/not-downloaded")
	manifest, _ := json.Marshal(index.Releases[0].Manifest)
	if e := a.pluginStore.Install(context.Background(), PluginInstallation{InstanceID: "Owned", PluginID: "Owned", Version: "2.0", ManifestSchemaVersion: 1, Manifest: manifest}); e != nil {
		t.Fatal(e)
	}
	originalCenterSetList(t, a, []string{"Ghost"})
	for _, version := range []string{"v1", "v2"} {
		path := "/api/" + version + "/plugin/rating/"
		result := callTest(t, a.Handler(), "POST", path+"Ghost", `{"rating":4.5}`, cookie, "")
		if result.Code != 200 {
			t.Fatal("list-only identity denied", result.Code, result.Body.String())
		}
		before := calls.Load()
		result = callTest(t, a.Handler(), "POST", path+"Owned", `{"rating":4.5}`, cookie, "")
		if result.Code != 400 || calls.Load() != before || !strings.Contains(result.Body.String(), "插件 Owned 未安装，无法评分") {
			t.Fatal("installation row bypassed source list", result.Code, result.Body.String())
		}
		result = callTest(t, a.Handler(), "POST", path+"Ghost", `{"rating":4.5}`, nil, "")
		if result.Code != 401 {
			t.Fatal("missing host session accepted")
		}
		result = callTest(t, a.Handler(), "POST", path+"Ghost", `{"rating":4.5}`, cookie, "https://cross.invalid")
		if result.Code != 403 {
			t.Fatal("cross origin management write accepted")
		}
		result = callTest(t, a.Handler(), "POST", path+"Ghost", `{"rating":4.5,"padding":"`+strings.Repeat("x", 2<<20)+`"}`, cookie, "")
		if result.Code != 413 || calls.Load() != before {
			t.Fatal("body budget changed or invalid write reached peer", result.Code)
		}
	}
	for _, raw := range []string{`"NaN"`, `"Infinity"`, `1e999`} {
		if _, e := originalRatingFloat(json.RawMessage(raw)); e == nil {
			t.Fatal("shared finite helper was relaxed", raw)
		}
	}
	for _, raw := range []string{`1e999`, `"Infinity"`, `2`} {
		if _, ok := originalServiceProgressBoolean(json.RawMessage(raw)); ok {
			t.Fatal("shared service boolean boundary relaxed", raw)
		}
	}
	value, e := originalRatingFloat(json.RawMessage(`true`))
	if e != nil || value != 1 {
		t.Fatal("shared original float coercion changed")
	}
	err := originalRatingValidation("float_parsing", "Input should be a valid number", []any{"body", "rating"}, "synthetic-password=NOT_REAL", nil)
	if strings.Contains(err.Error(), "NOT_REAL") {
		t.Fatal("diagnostic Error leaks request value")
	}
}

// Independent process calls the actual startup sync/report function with an
// empty repository set. It does not substitute a report implementation, launch
// unrelated daemon timers, or connect to a production center.
func TestOriginalPluginCenterRestartReportProcess(t *testing.T) {
	originalCenterTestEnvironment(t)
	if dir := os.Getenv("OWNED_CENTER_REPORT_DATA"); dir != "" {
		a, e := New(Options{DataDir: dir})
		if e != nil {
			t.Fatal(e)
		}
		a.synchronizeOriginalPluginsAtStartup(context.Background())
		uid, e := a.originalPluginServerUID(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		var marker string
		e = a.Store.DB.QueryRow(`SELECT value FROM plugin_market_server_state WHERE key='install_report'`).Scan(&marker)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(map[string]any{"uid": uid, "marker": marker, "pid": os.Getpid()})
		if e = os.WriteFile(os.Getenv("OWNED_CENTER_REPORT_RESULT"), raw, 0600); e != nil {
			t.Fatal(e)
		}
		if e = a.Close(); e != nil {
			t.Fatal(e)
		}
		return
	}
	dir := t.TempDir()
	var status atomic.Int32
	status.Store(503)
	var mu sync.Mutex
	calls := []map[string]any{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/plugin/install" {
			t.Errorf("unexpected startup request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		mu.Lock()
		calls = append(calls, body)
		mu.Unlock()
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"success":false}`))
	}))
	defer peer.Close()
	seed := func(ids []string, shared bool) {
		a, e := New(Options{DataDir: dir})
		if e != nil {
			t.Fatal(e)
		}
		s := a.Settings()
		s.PluginServerURL = originalServerURLPointer(peer.URL)
		s.PluginStatisticShare = &shared
		s.PluginMarkets = []string{}
		if e = a.saveSettings(context.Background(), s); e != nil {
			t.Fatal(e)
		}
		originalCenterSetList(t, a, ids)
		if e = a.Close(); e != nil {
			t.Fatal(e)
		}
	}
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	uid := ""
	run := func(phase string, wantCalls int, wantMark string) {
		t.Helper()
		out := filepath.Join(t.TempDir(), "result.json")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "-test.run=^TestOriginalPluginCenterRestartReportProcess$", "-test.v")
		command.Env = append(os.Environ(), "OWNED_CENTER_REPORT_DATA="+dir, "OWNED_CENTER_REPORT_RESULT="+out)
		logs, e := command.CombinedOutput()
		if e != nil {
			t.Fatalf("%s child: %v %s", phase, e, logs)
		}
		var result struct {
			UID, Marker string
			PID         int
		}
		raw, e := os.ReadFile(out)
		if e != nil || json.Unmarshal(raw, &result) != nil {
			t.Fatal(e)
		}
		if result.PID == os.Getpid() || len(result.UID) != 64 || result.Marker != wantMark {
			t.Fatal("cold process marker/identity mismatch", phase, result.Marker)
		}
		if uid != "" && uid != result.UID {
			t.Fatal("UID changed on process restart")
		}
		uid = result.UID
		mu.Lock()
		got := len(calls)
		mu.Unlock()
		if got != wantCalls {
			t.Fatalf("%s actual reports=%d want%d", phase, got, wantCalls)
		}
	}
	seed([]string{}, true)
	run("empty does not mark", 0, "")
	seed([]string{"Owned"}, false)
	run("sharing off does not mark", 0, "")
	seed([]string{"Owned"}, true)
	run("HTTP503 remains retryable", 1, "")
	status.Store(200)
	run("HTTP200 marks even success false body", 2, "1")
	run("cold marked startup does not report twice", 2, "1")
	mu.Lock()
	defer mu.Unlock()
	for _, call := range calls {
		want := map[string]any{"plugins": []any{map[string]any{"plugin_id": "Owned", "repo_url": nil}}}
		raw, _ := json.Marshal(call)
		expected, _ := json.Marshal(want)
		if string(raw) != string(expected) {
			t.Fatal("source initial report payload differs", string(raw))
		}
	}
}

func TestOriginalPluginCenterActualInstallReportsOnce(t *testing.T) {
	originalCenterTestEnvironment(t)
	index, payload := originalMarketOwnedIndex(t, "OWNED_STANDARD_FIXTURE_BINARY")
	var reports atomic.Int32
	var invalidArtifact atomic.Bool
	var peer *httptest.Server
	peer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			_ = json.NewEncoder(w).Encode(index)
		case "/artifact", "/artifact-fail":
			if invalidArtifact.Load() {
				_, _ = w.Write([]byte("owned malformed artifact"))
			} else {
				_, _ = w.Write(payload)
			}
		case "/plugin/install/" + index.PluginID:
			if r.Method != "POST" {
				t.Error("wrong installation report method")
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["plugin_id"] != index.PluginID || body["repo_url"] != peer.URL+"/index" || len(r.Header.Get("X-MoviePilot-User-Uid")) != 64 {
				t.Error("actual install report protocol mismatch")
			}
			reports.Add(1)
			_, _ = w.Write([]byte(`{"success":false}`))
		default:
			t.Errorf("unexpected owned repository request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	index.Releases[0].Artifacts[0].URL = peer.URL + "/artifact"
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	settings := a.Settings()
	settings.PluginServerURL = originalServerURLPointer(peer.URL)
	shared := true
	settings.PluginStatisticShare = &shared
	if e := a.saveSettings(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	request := func(force bool) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"plugin_id": index.PluginID, "repo_url": peer.URL + "/index", "force": force})
		return callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	}
	response := request(false)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	if reports.Load() != 1 {
		t.Fatal("real successful installation did not report exactly once")
	}
	// Source existing-code/nonforce branch still reports this installation action.
	response = request(false)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	if reports.Load() != 2 {
		t.Fatal("existing-code source installation report missing")
	}
	shared = false
	settings.PluginStatisticShare = &shared
	if e := a.saveSettings(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	response = request(true)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	if reports.Load() != 2 {
		t.Fatal("disabled installation statistics still reported")
	}
	shared = true
	settings.PluginStatisticShare = &shared
	if e := a.saveSettings(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	index.CurrentVersion = "9.0"
	index.Releases[0].Version = "9.0"
	index.Releases[0].Manifest.Version = "9.0"
	index.Releases[0].Artifacts[0].URL = peer.URL + "/artifact-fail"
	index.Releases[0].Artifacts[0].SHA256 = strings.Repeat("a", 64)
	index.Releases[0].Artifacts[0].ExecutableSHA256 = strings.Repeat("a", 64)
	invalidArtifact.Store(true)
	response = request(true)
	if response.Code == 200 && strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatal("malformed owned artifact installed")
	}
	if reports.Load() != 2 {
		t.Fatal("failed installation reported success")
	}
}

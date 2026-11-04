package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
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
		if result.Code != 200 || calls.Load() != before+1 {
			t.Fatal("complete valid rating body did not reach peer once", result.Code)
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

// Independent processes call the actual startup synchronizer with an empty
// repository set. Installation reports stay disabled regardless of server
// response or sharing settings; old receipts remain untouched.
func TestOriginalPluginCenterRestartNeverReportsOrChangesLegacyReceipt(t *testing.T) {
	originalCenterTestEnvironment(t)
	if dir := os.Getenv("OWNED_CENTER_REPORT_DATA"); dir != "" {
		a, e := New(Options{DataDir: dir})
		if e != nil {
			t.Fatal(e)
		}
		a.synchronizeOriginalPluginsAtStartup(context.Background())
		state := originalPluginReportState(t, a)
		identityCollected := a.pluginOriginalUserUID.Load() != nil
		uid, e := a.originalPluginServerUID(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(map[string]any{"uid": uid, "state": state, "identity_collected": identityCollected, "pid": os.Getpid()})
		if e = os.WriteFile(os.Getenv("OWNED_CENTER_REPORT_RESULT"), raw, 0600); e != nil {
			t.Fatal(e)
		}
		if e = a.Close(); e != nil {
			t.Fatal(e)
		}
		return
	}
	dir := t.TempDir()
	var status, requests atomic.Int32
	status.Store(http.StatusServiceUnavailable)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"success":false}`))
	}))
	defer peer.Close()
	seed := func(ids []string, shared bool, prefix string, legacy bool) {
		a, e := New(Options{DataDir: dir})
		if e != nil {
			t.Fatal(e)
		}
		s := a.Settings()
		s.PluginServerURL = originalServerURLPointer(peer.URL + prefix)
		s.PluginStatisticShare = &shared
		s.PluginMarkets = []string{}
		if e = a.saveSettings(context.Background(), s); e != nil {
			t.Fatal(e)
		}
		originalCenterSetList(t, a, ids)
		if legacy {
			if e = a.originalPluginServerState(context.Background()); e != nil {
				t.Fatal(e)
			}
			if _, e = a.Store.DB.Exec(`INSERT INTO plugin_market_server_state(key,value) VALUES('install_report','1'),('owned_legacy_state','preserve-me')`); e != nil {
				t.Fatal(e)
			}
		}
		if e = a.Close(); e != nil {
			t.Fatal(e)
		}
	}
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	uid := ""
	run := func(phase string, wantState map[string]string) {
		t.Helper()
		out := filepath.Join(t.TempDir(), "result.json")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "-test.run=^TestOriginalPluginCenterRestartNeverReportsOrChangesLegacyReceipt$", "-test.v")
		command.Env = append(os.Environ(), "OWNED_CENTER_REPORT_DATA="+dir, "OWNED_CENTER_REPORT_RESULT="+out)
		logs, e := command.CombinedOutput()
		if e != nil {
			t.Fatalf("%s child: %v %s", phase, e, logs)
		}
		var result struct {
			UID               string
			State             map[string]string
			IdentityCollected bool `json:"identity_collected"`
			PID               int
		}
		raw, e := os.ReadFile(out)
		if e != nil || json.Unmarshal(raw, &result) != nil {
			t.Fatal(e)
		}
		if result.PID == os.Getpid() || len(result.UID) != 64 || !reflect.DeepEqual(result.State, wantState) {
			t.Fatal("cold process state/independent identity mismatch", phase, result.State)
		}
		if result.IdentityCollected {
			t.Fatal("startup collected reporting identity", phase)
		}
		if uid != "" && uid != result.UID {
			t.Fatal("independent UID changed on process restart")
		}
		uid = result.UID
		if requests.Load() != 0 {
			t.Fatalf("%s made %d disabled startup requests", phase, requests.Load())
		}
	}
	seed([]string{}, true, "", false)
	run("empty inventory creates no receipt", nil)
	seed([]string{"Owned"}, false, "", false)
	run("sharing off creates no receipt", nil)
	seed([]string{"Owned"}, true, "", false)
	run("unavailable center is never contacted", nil)
	status.Store(http.StatusOK)
	run("available center is never contacted", nil)
	run("repeated cold startup remains silent", nil)
	seed([]string{"Owned"}, true, "", true)
	legacy := map[string]string{"install_report": "1", "owned_legacy_state": "preserve-me"}
	run("legacy receipt is preserved", legacy)
	seed([]string{"Owned"}, true, "/changed-prefix", false)
	run("changed center does not resend legacy receipt", legacy)
}

func TestOriginalPluginCenterActualInstallNeverReportsAndPreservesMaterialQueries(t *testing.T) {
	originalCenterTestEnvironment(t)
	index, payload := originalMarketOwnedIndex(t, "OWNED_STANDARD_FIXTURE_BINARY")
	updatedIndex, updatedPayload := originalMarketOwnedIndex(t, "OWNED_STANDARD_FIXTURE_V2_BINARY")
	if index.PluginID != updatedIndex.PluginID || index.CurrentVersion != "1.0" || updatedIndex.CurrentVersion != "2.0" || index.Releases[0].Artifacts[0].SHA256 == updatedIndex.Releases[0].Artifacts[0].SHA256 {
		t.Fatal("requires genuinely distinct 1.0 and 2.0 owned workers")
	}
	const indexQuery = "channel=owned%2Fstable&version=one&version=two&token=OWNED_NOT_A_SECRET"
	const artifactQuery = "signature=OWNED_NOT_A_SECRET&path=owned%2Fworker&part=one&part=two"
	var reports, indexes, artifacts atomic.Int32
	var invalidArtifact atomic.Bool
	var peer *httptest.Server
	peer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index":
			indexes.Add(1)
			if r.Method != http.MethodGet || r.URL.RawQuery != indexQuery || r.Header.Get("X-MoviePilot-User-Uid") != "" {
				t.Error("repository query or identity boundary changed")
			}
			_ = json.NewEncoder(w).Encode(index)
		case "/artifact", "/artifact-fail":
			artifacts.Add(1)
			if r.Method != http.MethodGet || r.URL.RawQuery != artifactQuery || r.Header.Get("X-MoviePilot-User-Uid") != "" {
				t.Error("artifact query or identity boundary changed")
			}
			if invalidArtifact.Load() {
				_, _ = w.Write([]byte("owned malformed artifact"))
			} else {
				_, _ = w.Write(payload)
			}
		case "/plugin/install/" + index.PluginID, "/plugin/install":
			reports.Add(1)
			http.Error(w, "owned center unavailable", http.StatusServiceUnavailable)
		default:
			t.Errorf("unexpected owned repository request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer peer.Close()
	index.Releases[0].Artifacts[0].URL = peer.URL + "/artifact?" + artifactQuery
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
		body, _ := json.Marshal(map[string]any{"plugin_id": index.PluginID, "repo_url": peer.URL + "/index?" + indexQuery, "force": force})
		return callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	}
	assertNoReporting := func(phase string, wantIndexes, wantArtifacts int32) {
		t.Helper()
		if reports.Load() != 0 || a.pluginOriginalUserUID.Load() != nil || originalPluginReportState(t, a) != nil {
			t.Fatal("installation reporting had a side effect", phase)
		}
		if indexes.Load() != wantIndexes || artifacts.Load() != wantArtifacts {
			t.Fatalf("%s material requests: index=%d artifact=%d; want %d/%d", phase, indexes.Load(), artifacts.Load(), wantIndexes, wantArtifacts)
		}
	}
	readVersion := func(want string) ownedStandardVersionSnapshot {
		t.Helper()
		value, loaded := a.pluginRunningInstances.Load(index.PluginID)
		if !loaded {
			t.Fatal("actual installed worker is not running")
		}
		snapshot := ownedStandardVersionRead(t, value.(*originalPluginInstance))
		if snapshot.Version != want || snapshot.Nonce <= 0 {
			t.Fatal("actual installed worker has wrong version", snapshot.Version, want)
		}
		return snapshot
	}
	response := request(false)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	assertNoReporting("first install", 1, 1)
	readVersion("1.0")
	// The existing-code/nonforce branch still checks repository compatibility,
	// without downloading the package again or reporting the action.
	response = request(false)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	assertNoReporting("repeat install", 2, 1)
	shared = false
	settings.PluginStatisticShare = &shared
	if e := a.saveSettings(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	response = request(true)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	assertNoReporting("sharing disabled force reinstall", 3, 2)
	beforeUpgrade := readVersion("1.0")
	shared = true
	settings.PluginStatisticShare = &shared
	settings.PluginMarkets = []string{peer.URL + "/index?" + indexQuery}
	if e := a.saveSettings(context.Background(), settings); e != nil {
		t.Fatal(e)
	}
	// Exercise the real startup path, including discovery, acquisition,
	// version replacement and reinitialization, without skipping synchronization.
	index, payload = updatedIndex, updatedPayload
	index.Releases[0].Artifacts[0].URL = peer.URL + "/artifact?" + artifactQuery
	a.synchronizeOriginalPluginsAtStartup(context.Background())
	assertNoReporting("startup sync upgrades 1.0 to 2.0", 5, 3)
	afterUpgrade := readVersion("2.0")
	if beforeUpgrade.Nonce == afterUpgrade.Nonce {
		t.Fatal("startup upgrade did not replace the actual worker")
	}
	response = request(true)
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	assertNoReporting("sharing enabled force reinstall after update", 6, 4)
	readVersion("2.0")
	index.CurrentVersion = "9.0"
	index.Releases[0].Version = "9.0"
	index.Releases[0].Manifest.Version = "9.0"
	index.Releases[0].Artifacts[0].URL = peer.URL + "/artifact-fail?" + artifactQuery
	index.Releases[0].Artifacts[0].SHA256 = strings.Repeat("a", 64)
	index.Releases[0].Artifacts[0].ExecutableSHA256 = strings.Repeat("a", 64)
	invalidArtifact.Store(true)
	response = request(true)
	if response.Code == 200 && strings.Contains(response.Body.String(), `"success":true`) {
		t.Fatal("malformed owned artifact installed")
	}
	assertNoReporting("failed install", 7, 5)
	installed, err := a.pluginStore.Installation(context.Background(), index.PluginID)
	if err != nil || installed.Version != "2.0" {
		t.Fatal("failed replacement changed the installed release", err)
	}
	readVersion("2.0")
}

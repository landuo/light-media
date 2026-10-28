package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func originalServerURLPointer(value string) *string { return &value }

func TestOriginalPluginServerActualHTTPProtocolAndFailureSemantics(t *testing.T) {
	a, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	cookie := setupTestAdmin(t, a)
	h := a.Handler()
	unconfigured := callTest(t, h, "GET", "/api/plugins/statistic", "", cookie, "")
	if unconfigured.Code != 200 || !strings.Contains(unconfigured.Body.String(), `"data":{}`) {
		t.Fatal("unconfigured statistics did not preserve source empty fallback")
	}
	id := "OwnedCenterFixture"
	index := guideMarketIndex(id, "https://owned.invalid/not-downloaded")
	raw, _ := json.Marshal(index.Releases[0].Manifest)
	if err = a.pluginStore.Install(context.Background(), PluginInstallation{InstanceID: id, PluginID: id, Version: "2.0", ManifestSchemaVersion: 1, Manifest: raw}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	type received struct {
		Method, Path, Query, UID string
		Body                     map[string]any
	}
	var calls []received
	var malformed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := received{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, UID: r.Header.Get("X-MoviePilot-User-Uid")}
		if r.Method == http.MethodPost {
			json.NewDecoder(r.Body).Decode(&call.Body)
		}
		mu.Lock()
		calls = append(calls, call)
		mu.Unlock()
		if malformed.Load() {
			w.Write([]byte(`{"success":false,"message":"owned center said no"}`))
			return
		}
		rating := map[string]any{"plugin_id": id, "average_rating": 4.2, "rating_count": 3, "user_rating": 4.5}
		switch r.URL.Path {
		case "/prefix/plugin/statistic":
			json.NewEncoder(w).Encode(map[string]int{id: 23})
		case "/prefix/plugin/rating":
			json.NewEncoder(w).Encode(map[string]any{id: rating})
		case "/prefix/plugin/rating/" + id:
			json.NewEncoder(w).Encode(rating)
		case "/prefix/plugin/install/" + id, "/prefix/plugin/install":
			w.Write([]byte(`{"success":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	settings := a.Settings()
	settings.PluginServerURL = originalServerURLPointer(server.URL + "/prefix")
	if err = a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/plugins/statistic", "/api/plugins/rating?plugin_ids=" + id, "/api/plugins/rating/" + id} {
		res := callTest(t, h, "GET", path, "", cookie, "")
		if res.Code != 200 {
			t.Fatalf("real local replay failed: %s", res.Body.String())
		}
		if strings.Contains(path, "statistic") && !strings.Contains(res.Body.String(), ":23") {
			t.Fatal("actual count replaced")
		}
	}
	rated := callTest(t, h, "POST", "/api/plugins/rating/"+id, `{"rating":4.5}`, cookie, "")
	if rated.Code != 200 || !strings.Contains(rated.Body.String(), `"success":true`) || !strings.Contains(rated.Body.String(), `"rating_count":3`) {
		t.Fatal("actual rating discarded")
	}
	mu.Lock()
	before := len(calls)
	firstUID := calls[0].UID
	if len(firstUID) != 64 || calls[1].Query != "plugin_ids="+id || calls[3].Body["rating"] != 4.5 {
		t.Fatalf("protocol mismatch: %+v", calls)
	}
	mu.Unlock()
	for _, rating := range []string{"0", "5.1", "4.55"} {
		res := callTest(t, h, "POST", "/api/plugins/rating/"+id, `{"rating":`+rating+"}", cookie, "")
		if res.Code == 200 {
			t.Fatal("invalid rating accepted")
		}
	}
	if res := callTest(t, h, "POST", "/api/plugins/rating/NotInstalled", `{"rating":4.5}`, cookie, ""); res.Code == 200 {
		t.Fatal("uninstalled rating accepted")
	}
	mu.Lock()
	if len(calls) != before {
		t.Fatal("invalid request reached center")
	}
	mu.Unlock()
	a.reportOriginalPluginInstallation(context.Background(), id, "local://"+id+"?path=%2Fprivate%2Fsource&version=v2")
	mu.Lock()
	report := calls[len(calls)-1]
	mu.Unlock()
	if report.Path != "/prefix/plugin/install/"+id || report.Body["repo_url"] != "local://"+id+"?version=v2" || report.UID != firstUID {
		t.Fatalf("install report not sanitized/stable: %+v", report)
	}
	disabled := false
	settings.PluginStatisticShare = &disabled
	if err = a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	before = len(calls)
	mu.Unlock()
	if res := callTest(t, h, "GET", "/api/plugins/statistic", "", cookie, ""); res.Code != 200 {
		t.Fatal("disabled stats query failed")
	}
	a.reportOriginalPluginInstallation(context.Background(), id, "https://owned.invalid/index")
	mu.Lock()
	if len(calls) != before {
		t.Fatal("disabled sharing still sent request")
	}
	mu.Unlock()
	if res := callTest(t, h, "GET", "/api/plugins/rating/"+id, "", cookie, ""); res.Code != 200 {
		t.Fatal("rating incorrectly gated by installation sharing")
	}
	malformed.Store(true)
	res := callTest(t, h, "POST", "/api/plugins/rating/"+id, `{"rating":4.5}`, cookie, "")
	var sourceResponse struct {
		Data struct {
			Success bool
			Data    map[string]any
		}
	}
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &sourceResponse) != nil || !sourceResponse.Data.Success || sourceResponse.Data.Data["success"] != false {
		t.Fatal("source HTTP200 write-envelope behavior changed")
	}
	uid, err := a.originalPluginServerUID(context.Background())
	if err != nil || uid != firstUID {
		t.Fatal("instance UID changed")
	}
}

func TestOriginalPluginServerFirstReportRetriesAndUIDSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	a, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if a != nil {
			a.Close()
		}
	}()
	var reports atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/plugin/install" {
			t.Error("wrong first-report endpoint")
		}
		var body struct {
			Plugins []map[string]any `json:"plugins"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Plugins) != 1 || body.Plugins[0]["plugin_id"] != "OwnedReportFixture" {
			t.Error("wrong report shape")
		}
		if reports.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"success":false}`))
			return
		}
		if reports.Load() == 3 {
			w.Write([]byte(`{"success":false}`))
			return
		}
		w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	settings := a.Settings()
	settings.PluginServerURL = originalServerURLPointer(server.URL)
	if err = a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	index := guideMarketIndex("OwnedReportFixture", "https://owned.invalid/binary")
	raw, _ := json.Marshal(index.Releases[0].Manifest)
	if err = a.pluginStore.Install(context.Background(), PluginInstallation{InstanceID: index.PluginID, PluginID: index.PluginID, Version: "2.0", ManifestSchemaVersion: 1, Manifest: raw}); err != nil {
		t.Fatal(err)
	}
	a.reportOriginalPluginInstallations(context.Background())
	a.reportOriginalPluginInstallations(context.Background())
	a.reportOriginalPluginInstallations(context.Background())
	if reports.Load() != 2 {
		t.Fatal("failed report marked complete or completed report repeated")
	}
	uid, _ := a.originalPluginServerUID(context.Background())
	a.Close()
	a = nil
	a, err = New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	next, _ := a.originalPluginServerUID(context.Background())
	a.reportOriginalPluginInstallations(context.Background())
	if next != uid || reports.Load() != 2 {
		t.Fatal("identity/receipt not persisted")
	}
	if _, err := a.Store.DB.Exec(`DELETE FROM plugin_market_server_state WHERE key='install_report'`); err != nil {
		t.Fatal(err)
	}
	a.reportOriginalPluginInstallations(context.Background())
	a.reportOriginalPluginInstallations(context.Background())
	var reported string
	if err := a.Store.DB.QueryRow(`SELECT value FROM plugin_market_server_state WHERE key='install_report'`).Scan(&reported); err != nil || reported != "1" || reports.Load() != 3 {
		t.Fatal("source HTTP200-only receipt behavior differs", err)
	}
}

func TestOriginalPluginServerSettingsAndRedirectBoundary(t *testing.T) {
	a, err := New(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	cookie := setupTestAdmin(t, a)
	res := callTest(t, a.Handler(), "PUT", "/api/plugins/markets", `{"plugin_markets":[],"plugin_server_url":"https://user:pass@owned.invalid","plugin_statistic_share":false}`, cookie, "")
	if res.Code == 200 || *a.Settings().PluginServerURL != "" {
		t.Fatal("credential-bearing server saved")
	}
	var leaked atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer other.Close()
	center := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, other.URL, http.StatusFound) }))
	defer center.Close()
	body, _ := json.Marshal(map[string]any{"plugin_markets": []string{}, "plugin_server_url": center.URL, "plugin_statistic_share": false})
	res = callTest(t, a.Handler(), "PUT", "/api/plugins/markets", string(body), cookie, "")
	if res.Code != 200 || a.Settings().PluginStatisticShare == nil || *a.Settings().PluginStatisticShare {
		t.Fatal("explicit false lost")
	}
	settings := a.Settings()
	settings.PluginStatisticShare = nil
	a.saveSettings(context.Background(), settings)
	if !originalPluginStatisticShared(a.Settings()) {
		t.Fatal("nil did not preserve original default")
	}
	res = callTest(t, a.Handler(), "GET", "/api/plugins/statistic", "", cookie, "")
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"data":{}`) || leaked.Load() != 0 {
		t.Fatal("redirect forwarded stable instance identity")
	}
	if got := originalPluginReportedRepository("https://user:password@owned.invalid/index?token=private#secret"); !reflect.DeepEqual(got, "https://owned.invalid/index") {
		t.Fatal("report leaked source credentials")
	}
}

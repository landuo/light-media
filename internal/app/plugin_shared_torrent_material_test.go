package app

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

	"light-media/internal/plugins"
)

type ownedMaterialHostResult struct {
	PID, Invocation, Requested, Completed, Succeeded, Failed int
	HadPrevious                                              bool `json:"had_previous"`
	OK                                                       bool
	StaleDenied                                              *bool `json:"stale_denied"`
	ReassembledBytes                                         int64 `json:"reassembled_bytes"`
	Results                                                  []struct {
		Index       int
		OK          bool
		Error       string
		ErrorCode   string `json:"error_code"`
		SourceError string `json:"source_error"`
		Result      *struct {
			Kind   string
			Length *int
			SHA256 *string
			Text   *string
			Folder string
			Files  []string
		}
		HostCalls struct{ Total, Prepare, Read, Release int } `json:"host_calls"`
	}
}

func ownedMaterialHostTorrent(t *testing.T) []byte {
	t.Helper()
	// Exact source parser fixture cb7d78a5... and raw source result 8c86d061...
	// establish folder Root/files [sub/x]; this is not a target-generated oracle.
	raw, err := base64.StdEncoding.DecodeString("ZDQ6aW5mb2Q1OmZpbGVzbGQ2Omxlbmd0aGkxZTQ6cGF0aGw0OnN1Yi8xOnhlZWU0Om5hbWU0OlJvb3QxMjpwaWVjZSBsZW5ndGhpMTYzODRlNjpwaWVjZXMyMDoAAAAAAAAAAAAAAAAAAAAAAAAAAGVl")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if len(raw) != 114 || hex.EncodeToString(sum[:]) != "cb7d78a570a9413fb369cb07376533ed42526d530395ba325f249f9c6bede203" {
		t.Fatal("source fixture bytes drifted")
	}
	return raw
}

func ownedMaterialHostInput(t *testing.T, enclosure string) plugins.DownloadTorrentRequest {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"enclosure": enclosure, "title": "Owned material", "site_name": "Owned Site", "site_cookie": "owned=cookie", "site_ua": "Owned-Material-UA", "site_proxy": false})
	if err != nil {
		t.Fatal(err)
	}
	channel, source := "Web", "OwnedMaterial"
	return plugins.DownloadTorrentRequest{Torrent: raw, Channel: &channel, Source: &source, UserID: json.RawMessage(`17`)}
}

func ownedMaterialHostBody(t *testing.T, requests ...plugins.DownloadTorrentRequest) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"requests": requests, "continue_on_error": true, "probe_previous": true})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func ownedMaterialHostQuery(t *testing.T, a *App, cookie *http.Cookie, id string, requests ...plugins.DownloadTorrentRequest) ownedMaterialHostResult {
	t.Helper()
	response := callTest(t, a.Handler(), "POST", "/api/v1/plugin/"+id+"/material", ownedMaterialHostBody(t, requests...), cookie, "")
	if response.Code != 200 {
		t.Fatalf("owned material HTTP %d: %s", response.Code, response.Body)
	}
	var result ownedMaterialHostResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.PID <= 0 || result.PID == os.Getpid() || result.Requested != len(requests) || result.Completed != len(requests) || len(result.Results) != len(requests) {
		t.Fatalf("not an actual complete worker observation: %+v %v", result, err)
	}
	if result.HadPrevious && (result.StaleDenied == nil || !*result.StaleDenied) {
		t.Fatal("worker retained a completed invocation callback")
	}
	return result
}

func ownedMaterialHostBytes(t *testing.T, result ownedMaterialHostResult, index int, wanted []byte) {
	t.Helper()
	attempt := result.Results[index]
	sum := sha256.Sum256(wanted)
	if !attempt.OK || attempt.Result == nil || attempt.Result.Kind != "bytes" || attempt.Result.Length == nil || *attempt.Result.Length != len(wanted) || attempt.Result.SHA256 == nil || *attempt.Result.SHA256 != hex.EncodeToString(sum[:]) || attempt.Result.Folder != "Root" || !reflect.DeepEqual(attempt.Result.Files, []string{"sub/x"}) {
		t.Fatalf("worker did not reconstruct exact source-shaped bytes: %+v", attempt)
	}
	wantReads := (len(wanted) + plugins.TorrentMaterialChunkBytes - 1) / plugins.TorrentMaterialChunkBytes
	if attempt.HostCalls.Prepare != 1 || attempt.HostCalls.Read != wantReads || attempt.HostCalls.Release != 1 || attempt.HostCalls.Total != wantReads+2 {
		t.Fatalf("SDK prepare/read/release differed: %+v", attempt.HostCalls)
	}
}

func ownedMaterialHostSite(t *testing.T, a *App, cookie *http.Cookie, address, name string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"id": "owned-material-site", "kind": "jiayuan", "name": name, "base_url": address, "cookie": "owned=cookie", "enabled": false, "daily_budget": 600})
	if err != nil {
		t.Fatal(err)
	}
	response := callTest(t, a.Handler(), "POST", "/api/sites/manage", string(raw), cookie, "")
	if response.Code != 200 {
		t.Fatalf("real owned site configuration %d: %s", response.Code, response.Body)
	}
}

// Saving a site emits SiteUpdated and the existing userdata consumer fetches
// its root, even for a disabled site. Record that owned request independently;
// it is not a material fetch and must not consume a material barrier/counter.
type ownedMaterialSiteProbe struct {
	count    atomic.Int32
	observed chan struct{}
}

func newOwnedMaterialSiteProbe() *ownedMaterialSiteProbe {
	return &ownedMaterialSiteProbe{observed: make(chan struct{}, 4)}
}

func (p *ownedMaterialSiteProbe) serve(t *testing.T, w http.ResponseWriter, r *http.Request) bool {
	t.Helper()
	if r.URL.Path != "/" {
		return false
	}
	if r.Method != "GET" {
		t.Errorf("unexpected owned site refresh method %s", r.Method)
	}
	p.count.Add(1)
	select {
	case p.observed <- struct{}{}:
	default:
		t.Error("unexpected repeated owned site refresh")
	}
	http.NotFound(w, r)
	return true
}

func (p *ownedMaterialSiteProbe) wait(t *testing.T, wanted int32) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for p.count.Load() < wanted {
		select {
		case <-p.observed:
		case <-deadline.C:
			t.Fatalf("owned SiteUpdated root requests = %d, want %d", p.count.Load(), wanted)
		}
	}
	if p.count.Load() != wanted {
		t.Fatalf("owned SiteUpdated root requests = %d, want %d", p.count.Load(), wanted)
	}
}

func installOwnedMaterialWorker(t *testing.T, a *App, cookie *http.Cookie, denied bool) string {
	t.Helper()
	environment, id := "OWNED_TORRENT_MATERIAL_WORKER", "OwnedTorrentMaterial"
	if denied {
		environment, id = "OWNED_TORRENT_MATERIAL_DENIED_WORKER", "OwnedTorrentMaterialDenied"
	}
	index, payload := originalMarketOwnedIndex(t, environment)
	manifest := index.Releases[0].Manifest
	if manifest.Validate() != nil || manifest.PluginID != id || len(manifest.APIs) != 1 || manifest.APIs[0].Path != "/material" || manifest.APIs[0].Methods[0] != "POST" {
		t.Fatal("unexpected actual material worker manifest")
	}
	if denied && len(manifest.Capabilities) != 0 || !denied && !reflect.DeepEqual(manifest.Capabilities, []string{plugins.DownloadTorrentCapability}) {
		t.Fatal("capability-positive/negative worker differs")
	}
	index.Releases[0].Artifacts[0].HostContract = pluginResourcesSDKContract
	var indexHits, artifactHits, unexpected atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			unexpected.Add(1)
			http.Error(w, "owned read-only artifacts", 405)
			return
		}
		switch r.URL.Path {
		case "/index":
			indexHits.Add(1)
			_ = json.NewEncoder(w).Encode(index)
		case "/worker":
			artifactHits.Add(1)
			_, _ = w.Write(payload)
		default:
			unexpected.Add(1)
			http.NotFound(w, r)
		}
	}))
	index.Releases[0].Artifacts[0].URL = "http://" + server.Listener.Addr().String() + "/worker"
	server.Start()
	t.Cleanup(func() {
		server.Close()
		if indexHits.Load() != 1 || artifactHits.Load() != 1 || unexpected.Load() != 0 {
			t.Errorf("actual artifact acquisition drifted: %d/%d/%d", indexHits.Load(), artifactHits.Load(), unexpected.Load())
		}
	})
	body, _ := json.Marshal(map[string]string{"plugin_id": id, "repo_url": server.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	loaded, ok := a.pluginRunningInstances.Load(id)
	if !ok {
		t.Fatal("actual material worker not loaded")
	}
	object := loaded.(*originalPluginInstance)
	if !object.external || object.hostContract != pluginResourcesSDKContract {
		t.Fatal("material worker replaced by mock/native object")
	}
	installation, err := a.pluginStore.Installation(context.Background(), id)
	if err != nil || !installation.Installed {
		t.Fatalf("real SQLite installation absent: %+v %v", installation, err)
	}
	return id
}

func ownedMaterialHostFixture(t *testing.T) (*App, *http.Cookie, string) {
	t.Helper()
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a) // Before installation: no statistics/center delivery.
	cookie := setupTestAdmin(t, a)
	id := installOwnedMaterialWorker(t, a, cookie, false)
	return a, cookie, id
}

func TestSharedTorrentMaterialActualWorkerValuesAuthorityRecovery(t *testing.T) {
	torrent := ownedMaterialHostTorrent(t)
	setup := newOwnedMaterialSiteProbe()
	var directHits, ticketHits, resolvedHits, formGets, formPosts, missingLocations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if setup.serve(t, w, r) {
			return
		}
		switch r.URL.Path {
		case "/missing-location":
			missingLocations.Add(1)
			w.WriteHeader(http.StatusMovedPermanently)
			return
		case "/bytes":
			directHits.Add(1)
		case "/ticket":
			ticketHits.Add(1)
			body, _ := io.ReadAll(r.Body)
			if r.Method != "POST" || r.URL.RawQuery != "id=owned+ticket" || len(body) != 0 || r.Header.Get("Cookie") != "owned=cookie" {
				t.Error("descriptor actual POST query/cookie differs")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true,"data":{"url":"/resolved"}}`)
			return
		case "/resolved":
			resolvedHits.Add(1)
			if r.Header.Get("Cookie") != "" {
				t.Error("indirect material restored stripped cookie")
			}
		case "/first-form":
			if r.Method == "GET" {
				formGets.Add(1)
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = io.WriteString(w, `<form action="?"><input name="b" value="first"><input name="a" value="middle"><input name="b" value="last"></form>下载种子文件`)
				return
			}
			formPosts.Add(1)
			body, _ := io.ReadAll(r.Body)
			if r.Method != "POST" || string(body) != "b=last&a=middle" {
				t.Errorf("real OrderedTorrentHTTP bridge body = %q", body)
			}
		default:
			t.Errorf("unexpected owned material path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/resolved" && r.Header.Get("Cookie") != "owned=cookie" {
			t.Error("owned current credential absent")
		}
		w.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = w.Write(torrent)
	}))
	defer server.Close()
	a, cookie, id := ownedMaterialHostFixture(t)
	ownedMaterialHostSite(t, a, cookie, server.URL, "Owned Material Site")
	setup.wait(t, 1)
	path := "/api/v1/plugin/" + id + "/material"
	if response := callTest(t, a.Handler(), "POST", path, ownedMaterialHostBody(t, ownedMaterialHostInput(t, "")), nil, ""); response.Code != 401 {
		t.Fatal("material API lost authentication")
	}
	if response := callTest(t, a.Handler(), "GET", path, "", cookie, ""); response.Code != 405 {
		t.Fatal("material API accepted wrong method")
	}
	magnet := "magnet:?xt=urn:btih:OWNED&dn=Owned"
	first := ownedMaterialHostQuery(t, a, cookie, id, ownedMaterialHostInput(t, ""), ownedMaterialHostInput(t, magnet))
	if !first.OK || !first.Results[0].OK || first.Results[0].Result == nil || first.Results[0].Result.Kind != "none" || first.Results[0].Result.Files == nil || !first.Results[1].OK || first.Results[1].Result == nil || first.Results[1].Result.Kind != "string" || first.Results[1].Result.Text == nil || *first.Results[1].Result.Text != magnet {
		t.Fatalf("source None/magnet differs: %+v", first)
	}
	if directHits.Load()+ticketHits.Load()+formGets.Load() != 0 {
		t.Fatal("non-network source branches performed HTTP")
	}
	badJSON := "[" + base64.StdEncoding.EncodeToString([]byte("{")) + "]" + server.URL + "/ticket"
	recovered := ownedMaterialHostQuery(t, a, cookie, id, ownedMaterialHostInput(t, badJSON), ownedMaterialHostInput(t, server.URL+"/bytes"))
	if recovered.OK || recovered.PID != first.PID || recovered.Failed != 1 || recovered.Succeeded != 1 || recovered.Results[0].OK || recovered.Results[0].SourceError != "JSONDecodeError" || recovered.Results[0].Result != nil {
		t.Fatalf("typed source exception/same-worker recovery differs: %+v", recovered)
	}
	ownedMaterialHostBytes(t, recovered, 1, torrent)
	params, _ := json.Marshal(map[string]any{"method": "POST", "cookie": true, "params": map[string]string{"id": "owned ticket"}, "result": "data.url", "success": "ok", "result_base_url": server.URL})
	descriptor := "[" + base64.StdEncoding.EncodeToString(params) + "]" + server.URL + "/ticket"
	bridged := ownedMaterialHostQuery(t, a, cookie, id, ownedMaterialHostInput(t, descriptor), ownedMaterialHostInput(t, server.URL+"/first-form"))
	if !bridged.OK || bridged.PID != first.PID {
		t.Fatalf("actual descriptor/form bridge failed: %+v", bridged)
	}
	ownedMaterialHostBytes(t, bridged, 0, torrent)
	ownedMaterialHostBytes(t, bridged, 1, torrent)
	missing := ownedMaterialHostQuery(t, a, cookie, id, ownedMaterialHostInput(t, server.URL+"/missing-location"), ownedMaterialHostInput(t, server.URL+"/bytes"))
	if missing.OK || missing.PID != first.PID || missing.Failed != 1 || missing.Succeeded != 1 || missing.Results[0].OK || missing.Results[0].SourceError != "KeyError" || missing.Results[0].Result != nil || missingLocations.Load() != 1 {
		t.Fatalf("missing Location must raise source KeyError without a second GET: %+v", missing)
	}
	ownedMaterialHostBytes(t, missing, 1, torrent)
	notifications := callTest(t, a.Handler(), "GET", "/api/notifications", "", cookie, "")
	var history struct {
		Data struct{ Items []Notification } `json:"data"`
	}
	if notifications.Code != 200 || json.Unmarshal(notifications.Body.Bytes(), &history) != nil {
		t.Fatal("actual notification history unavailable after source exception")
	}
	for _, item := range history.Data.Items {
		if item.Title == "Owned material 种子下载失败！" {
			t.Fatal("uncaught source KeyError was converted into a None/failure notification")
		}
	}
	deniedID := installOwnedMaterialWorker(t, a, cookie, true)
	denied := ownedMaterialHostQuery(t, a, cookie, deniedID, ownedMaterialHostInput(t, server.URL+"/bytes"))
	if denied.OK || denied.Failed != 1 || denied.Results[0].OK || denied.Results[0].ErrorCode != "host_operation_failed" || denied.Results[0].Result != nil {
		t.Fatalf("undeclared capability accepted: %+v", denied)
	}
	if directHits.Load() != 1 || ticketHits.Load() != 1 || resolvedHits.Load() != 1 || formGets.Load() != 1 || formPosts.Load() != 1 {
		t.Fatalf("actual owned requests differ: %d/%d/%d/%d/%d", directHits.Load(), ticketHits.Load(), resolvedHits.Load(), formGets.Load(), formPosts.Load())
	}
	setup.wait(t, 1)
}

func ownedMaterialCachePath(a *App, address string) string {
	sum := md5.Sum([]byte(address))
	return filepath.Join(a.Dir, "cache", "plugin-torrent-material", hex.EncodeToString(sum[:])+".torrent")
}

func ownedMaterialFailureHistory(t *testing.T, a *App, cookie *http.Cookie) Notification {
	t.Helper()
	response := callTest(t, a.Handler(), "GET", "/api/notifications", "", cookie, "")
	var result struct {
		Data struct {
			Items []Notification `json:"items"`
		} `json:"data"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
		t.Fatalf("actual notification history failed: %d %s", response.Code, response.Body)
	}
	for _, n := range result.Data.Items {
		if n.Title == "Owned material 种子下载失败！" {
			if n.Message != "错误信息：下载种子出错，状态码：503\n站点：Owned Site" || n.MType != "手动处理" || n.Channel != "Web" || n.Source != "OwnedMaterial" || n.UserID == nil || *n.UserID != "17" || n.Link != "" {
				t.Fatalf("source failure history projection differs: %+v", n)
			}
			var raw []byte
			if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind='notification' AND id=?`, n.ID).Scan(&raw); err != nil || len(raw) == 0 || bytes.Contains(raw, []byte(n.Title)) {
				t.Fatalf("real protected SQLite notification absent/leaked: %v", err)
			}
			return n
		}
	}
	t.Fatalf("actual failed material notification absent: %s", response.Body)
	return Notification{}
}

func ownedMaterialFailureLog(t *testing.T, a *App, cookie *http.Cookie, instance string) (string, string) {
	t.Helper()
	flushTestLogs(t, a)
	response := callTest(t, a.Handler(), "GET", "/api/plugins/"+instance+"/logs", "", cookie, "")
	var result struct {
		Data struct {
			History logPage `json:"history"`
		} `json:"data"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
		t.Fatalf("actual material log history failed: %d %s", response.Code, response.Body)
	}
	projectionCount := 0
	for _, row := range result.Data.History.Entries {
		if row["message"] == "下载种子文件失败：Owned material" {
			projectionCount++
			raw, _ := row["raw"].(string)
			if row["plugin_instance_id"] != instance || !strings.HasPrefix(raw, "【ERROR】") {
				t.Fatalf("source text log projection differs: %+v", row)
			}
		}
	}
	if projectionCount != 1 {
		t.Fatalf("actual source log projection count = %d", projectionCount)
	}
	// The source-like per-plugin text projection intentionally has no call
	// fields. Attribution belongs to the existing canonical JSON log API.
	response = callTest(t, a.Handler(), "GET", "/api/logs?plugin_instance_id="+instance+"&from=0001-01-01T00:00:00Z&limit=100", "", cookie, "")
	var canonical struct{ Data logPage }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &canonical) != nil {
		t.Fatalf("actual canonical material log history failed: %d %s", response.Code, response.Body)
	}
	var logID, callID string
	for _, row := range canonical.Data.Entries {
		if row["message"] != "下载种子文件失败：Owned material" {
			continue
		}
		currentID, _ := row["id"].(string)
		currentCall, _ := row["plugin_call_id"].(string)
		operation, _ := row["operation"].(string)
		if logID != "" || currentID == "" || currentCall == "" || operation == "" || row["event"] != "torrent_material" || row["level"] != "ERROR" || row["module"] != "plugins" || row["plugin_id"] != instance || row["plugin_instance_id"] != instance {
			t.Fatalf("real failure log lost exact plugin/call attribution: %+v", row)
		}
		logID, callID = currentID, currentCall
	}
	if logID == "" {
		t.Fatalf("source failure log absent from actual persisted history: %s", response.Body)
	}
	return logID, callID
}

func TestSharedTorrentMaterialCacheColdAndFailureHistory(t *testing.T) {
	torrent := ownedMaterialHostTorrent(t)
	setup := newOwnedMaterialSiteProbe()
	var fetches, failures atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if setup.serve(t, w, r) {
			return
		}
		if r.URL.Path == "/failure" {
			failures.Add(1)
			// The source reads GET .text only for status 200. This marker must
			// not turn a non-200 failure into a decoding error or first-form POST.
			w.Header().Set("Content-Type", "text/html; charset=x-owned-unknown")
			w.WriteHeader(503)
			_, _ = io.WriteString(w, "下载种子文件")
			return
		}
		if r.URL.Path != "/cached" {
			t.Error("unexpected cache fixture route")
			http.NotFound(w, r)
			return
		}
		fetches.Add(1)
		_, _ = w.Write(torrent)
	}))
	defer server.Close()
	a, cookie, id := ownedMaterialHostFixture(t)
	ownedMaterialHostSite(t, a, cookie, server.URL, "Owned Cache Site")
	setup.wait(t, 1)
	address := server.URL + "/cached"
	warm := ownedMaterialHostQuery(t, a, cookie, id, ownedMaterialHostInput(t, address), ownedMaterialHostInput(t, address))
	if !warm.OK {
		t.Fatalf("warm actual cache failed: %+v", warm)
	}
	ownedMaterialHostBytes(t, warm, 0, torrent)
	ownedMaterialHostBytes(t, warm, 1, torrent)
	if fetches.Load() != 1 {
		t.Fatal("warm second request ignored persistent cache")
	}
	cachePath := ownedMaterialCachePath(a, address)
	stored, err := os.ReadFile(cachePath)
	if err != nil || !bytes.Equal(stored, torrent) {
		t.Fatalf("actual FileTorrentCache object differs: %v", err)
	}
	failed := ownedMaterialHostQuery(t, a, cookie, id, ownedMaterialHostInput(t, server.URL+"/failure"))
	if !failed.OK || failed.PID != warm.PID || !failed.Results[0].OK || failed.Results[0].Result == nil || failed.Results[0].Result.Kind != "none" || failures.Load() != 1 {
		t.Fatalf("source HTTP failure must return None and retain worker: %+v", failed)
	}
	notice := ownedMaterialFailureHistory(t, a, cookie)
	logID, callID := ownedMaterialFailureLog(t, a, cookie, id)
	dir := a.Dir
	a.pluginOriginalTorrentMaterial.mu.Lock()
	oldCache := a.pluginOriginalTorrentMaterial.cache
	a.pluginOriginalTorrentMaterial.mu.Unlock()
	if oldCache == nil {
		t.Fatal("real host cache owner absent")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := oldCache.GetTorrent(context.Background(), filepath.Base(cachePath)); err == nil {
		t.Fatal("App.Close left old filesystem cache root open")
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	login := callTest(t, cold.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	var coldCookie *http.Cookie
	for _, value := range login.Result().Cookies() {
		if value.Name == "lm_session" {
			coldCookie = value
		}
	}
	if login.Code != 200 || coldCookie == nil {
		t.Fatal("real cold login failed")
	}
	restored := ownedMaterialHostQuery(t, cold, coldCookie, id, ownedMaterialHostInput(t, address))
	if !restored.OK || restored.PID == warm.PID || fetches.Load() != 1 || failures.Load() != 1 {
		t.Fatalf("cold cache reacquired torrent or reused dead worker: %+v fetches=%d", restored, fetches.Load())
	}
	ownedMaterialHostBytes(t, restored, 0, torrent)
	if again := ownedMaterialFailureHistory(t, cold, coldCookie); again.ID != notice.ID {
		t.Fatal("cold SQLite history was recreated rather than retained")
	}
	if coldLog, coldCall := ownedMaterialFailureLog(t, cold, coldCookie, id); coldLog != logID || coldCall != callID {
		t.Fatal("cold failure log was recreated or lost its original call attribution")
	}
	setup.wait(t, 1)
}

func TestSharedTorrentMaterialFullBytesActualWorker(t *testing.T) {
	base := ownedMaterialHostTorrent(t)
	padding := plugins.TorrentMaterialMaxBytes - len(base) - 20
	var torrent []byte
	for i := 0; i < 4; i++ {
		prefix := []byte(fmt.Sprintf("7:padding%d:", padding))
		torrent = append(append(append(bytes.Clone(base[:len(base)-1]), prefix...), bytes.Repeat([]byte{'P'}, padding)...), 'e')
		if len(torrent) == plugins.TorrentMaterialMaxBytes {
			break
		}
		padding += plugins.TorrentMaterialMaxBytes - len(torrent)
	}
	if len(torrent) != plugins.TorrentMaterialMaxBytes {
		t.Fatal("owned exact 16MiB input construction failed")
	}
	setup := newOwnedMaterialSiteProbe()
	var fetches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if setup.serve(t, w, r) {
			return
		}
		if r.URL.Path != "/full" {
			t.Errorf("unexpected full material path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		fetches.Add(1)
		w.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = w.Write(torrent)
	}))
	defer server.Close()
	a, cookie, id := ownedMaterialHostFixture(t)
	ownedMaterialHostSite(t, a, cookie, server.URL, "Owned Full Material Site")
	setup.wait(t, 1)
	input := ownedMaterialHostInput(t, server.URL+"/full")
	result := ownedMaterialHostQuery(t, a, cookie, id, input, input)
	if !result.OK || result.ReassembledBytes != 2*int64(plugins.TorrentMaterialMaxBytes) || fetches.Load() != 1 {
		t.Fatalf("two complete 16MiB SDK returns did not finish: %+v fetches=%d", result, fetches.Load())
	}
	ownedMaterialHostBytes(t, result, 0, torrent)
	ownedMaterialHostBytes(t, result, 1, torrent)
	setup.wait(t, 1)
	t.Logf("actual worker PID %d reconstructed %d bytes in two full SDK calls; 36 counted callbacks, not a fabricated physical wire-byte metric", result.PID, result.ReassembledBytes)
}

func TestSharedTorrentMaterialCurrentGuardAndCancellation(t *testing.T) {
	torrent := ownedMaterialHostTorrent(t)
	setup := newOwnedMaterialSiteProbe()
	started, release := make(chan struct{}, 1), make(chan struct{})
	drainRelease, drained := make(chan struct{}), make(chan bool, 1)
	var releaseOnce, drainOnce sync.Once
	var staleCalls, cancelCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if setup.serve(t, w, r) {
			return
		}
		if r.URL.Path != "/stale" && r.URL.Path != "/cancel" {
			t.Errorf("unexpected guard material path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/stale" && staleCalls.Add(1) == 1 {
			started <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		if r.URL.Path == "/cancel" {
			cancelCalls.Add(1)
			started <- struct{}{}
			select {
			case <-drainRelease:
				if r.Context().Err() != nil {
					drained <- false
					return
				}
				n, err := w.Write(torrent)
				drained <- err == nil && n == len(torrent)
			case <-r.Context().Done():
				drained <- false
			}
			return
		}
		_, _ = w.Write(torrent)
	}))
	defer func() {
		releaseOnce.Do(func() { close(release) })
		drainOnce.Do(func() { close(drainRelease) })
		server.Close()
	}()
	a, cookie, id := ownedMaterialHostFixture(t)
	ownedMaterialHostSite(t, a, cookie, server.URL, "Owned Guard Original")
	setup.wait(t, 1)
	path := "/api/v1/plugin/" + id + "/material"
	invoke := func(ctx context.Context, body string) <-chan *httptest.ResponseRecorder {
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			req := httptest.NewRequest("POST", path, strings.NewReader(body)).WithContext(ctx)
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(cookie)
			response := httptest.NewRecorder()
			a.Handler().ServeHTTP(response, req)
			done <- response
		}()
		return done
	}
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	done := invoke(ctx, ownedMaterialHostBody(t, ownedMaterialHostInput(t, server.URL+"/stale")))
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("actual guarded host request did not start")
	}
	ownedMaterialHostSite(t, a, cookie, server.URL, "Owned Guard Changed")
	setup.wait(t, 2) // Wait only for the owned setup request, never a.wg.
	releaseOnce.Do(func() { close(release) })
	var failed ownedMaterialHostResult
	select {
	case response := <-done:
		// Host wire uses HOST_DENIED; the ordinary worker intentionally exposes
		// only host_operation_failed instead of arbitrary host error codes.
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &failed) != nil || failed.OK || failed.Failed != 1 || len(failed.Results) != 1 || failed.Results[0].OK || failed.Results[0].ErrorCode != "host_operation_failed" {
			t.Fatalf("changed snapshot was published: %d %s", response.Code, response.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stale request did not exit")
	}
	if _, err := os.Stat(ownedMaterialCachePath(a, server.URL+"/stale")); !os.IsNotExist(err) {
		t.Fatalf("stale response was cached: %v", err)
	}
	recovered := ownedMaterialHostQuery(t, a, cookie, id, ownedMaterialHostInput(t, server.URL+"/stale"))
	if !recovered.OK || recovered.PID != failed.PID {
		t.Fatalf("same worker did not recover from stale host error: %+v", recovered)
	}
	ownedMaterialHostBytes(t, recovered, 0, torrent)
	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done = invoke(cancelCtx, ownedMaterialHostBody(t, ownedMaterialHostInput(t, server.URL+"/cancel")))
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("actual cancellable request did not start")
	}
	// This fixture negotiates legacy serial Serve. Its already admitted call
	// deliberately uses WithoutCancel: a disconnected caller must not kill the
	// persistent worker. Release the owned peer so that bounded exchange drains;
	// do not assert immediate cancellation, extend a timeout, or switch protocols.
	cancel()
	drainOnce.Do(func() { close(drainRelease) })
	select {
	case response := <-done:
		// The final existing authority check still uses the canceled HTTP
		// context. Its 502 is not evidence that the admitted material failed.
		if response.Code != http.StatusBadGateway {
			t.Fatalf("canceled outer HTTP caller response = %d, want 502", response.Code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("admitted serial exchange did not drain after owned peer release")
	}
	select {
	case completed := <-drained:
		if !completed {
			t.Fatal("HTTP caller cancellation aborted the admitted serial material request")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("owned peer did not finish the released serial exchange")
	}
	stored, err := os.ReadFile(ownedMaterialCachePath(a, server.URL+"/cancel"))
	if err != nil || !bytes.Equal(stored, torrent) {
		t.Fatalf("admitted material did not finish its real cache after caller disconnect: %v", err)
	}
	afterDrain := ownedMaterialHostQuery(t, a, cookie, id, ownedMaterialHostInput(t, server.URL+"/cancel"))
	if !afterDrain.OK || afterDrain.PID != recovered.PID || !afterDrain.HadPrevious || afterDrain.StaleDenied == nil || !*afterDrain.StaleDenied {
		t.Fatalf("serial drain lost worker identity or retained stale callback authority: %+v", afterDrain)
	}
	ownedMaterialHostBytes(t, afterDrain, 0, torrent)
	if staleCalls.Load() != 2 || cancelCalls.Load() != 1 {
		t.Fatalf("actual guard/cancel requests = %d/%d", staleCalls.Load(), cancelCalls.Load())
	}
	setup.wait(t, 2)
}

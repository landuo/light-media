package app

// Actual installed ordinary worker/SDK -> fixed host/native -> owned qB peer.
// Handwritten integration assertions, not a new original/vendor HTTP oracle.
import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

func qbCookieAppListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return listener
}
func qbCookieAppPeer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	peer := &httptest.Server{Listener: qbCookieAppListener(t), Config: &http.Server{Handler: handler}}
	peer.Start()
	t.Cleanup(peer.Close)
	return peer
}

type qbCookieAppObservation struct {
	Requests, Login, Versions, Reads, Sets, Adds, FailedSelected int
	Store                                                        []map[string]any
	AddCookies                                                   []*string
}
type qbCookieAppState struct {
	mu          sync.Mutex
	version     string
	failNextSet bool
	observation qbCookieAppObservation
}

func (p *qbCookieAppState) snapshot() qbCookieAppObservation {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.observation
	out.Store = make([]map[string]any, len(p.observation.Store))
	for i, row := range p.observation.Store {
		out.Store[i] = map[string]any{}
		for k, v := range row {
			out.Store[i][k] = v
		}
	}
	out.AddCookies = append([]*string{}, p.observation.AddCookies...)
	return out
}
func (p *qbCookieAppState) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.observation.Requests++
		r.Body = http.MaxBytesReader(w, r.Body, 128<<10)
		if r.URL.RawQuery != "" {
			t.Error("unexpected qB query")
			w.WriteHeader(400)
			return
		}
		if r.URL.Path == "/api/v2/auth/login" {
			p.observation.Login++
			if r.Method != "POST" || r.ParseForm() != nil || r.PostForm.Get("username") != ownedCommandUsername || r.PostForm.Get("password") != ownedCommandPassword || len(r.PostForm) != 2 {
				t.Error("fixed qB login fields")
				w.WriteHeader(400)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: ownedCommandSession, Path: "/"})
			io.WriteString(w, "Ok.")
			return
		}
		auth, err := r.Cookie("SID")
		if err != nil || auth.Value != ownedCommandSession {
			t.Error("configured qB session absent")
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/api/v2/app/webapiVersion":
			if r.Method != "GET" {
				t.Error("version method")
			}
			p.observation.Versions++
			io.WriteString(w, p.version)
		case "/api/v2/app/cookies":
			if r.Method != "GET" {
				t.Error("cookies method")
			}
			p.observation.Reads++
			json.NewEncoder(w).Encode(p.observation.Store)
		case "/api/v2/app/setCookies":
			if r.Method != "POST" || r.ParseForm() != nil || len(r.PostForm) != 1 {
				t.Error("setCookies form")
				w.WriteHeader(400)
				return
			}
			var rows []map[string]any
			if json.Unmarshal([]byte(r.PostForm.Get("cookies")), &rows) != nil {
				t.Error("setCookies ordered JSON")
				w.WriteHeader(400)
				return
			}
			p.observation.Sets++
			p.observation.Store = rows // Possible side effect before an error is deliberate.
			if p.failNextSet {
				p.failNextSet = false
				p.observation.FailedSelected++
				w.WriteHeader(500)
				io.WriteString(w, "owned selected response failure")
				return
			}
			io.WriteString(w, "ignored False")
		case "/api/v2/torrents/add":
			if r.Method != "POST" || r.ParseForm() != nil {
				t.Error("add form")
				w.WriteHeader(400)
				return
			}
			want := map[string][]string{"urls": {"https://owned-cookie.invalid/owned.torrent"}, "autoTMM": {"False"}, "sequentialDownload": {"False"}, "firstLastPiecePrio": {"False"}}
			if cookie, exists := r.PostForm["cookie"]; exists {
				if len(cookie) != 1 {
					t.Error("multiple cookie values")
				}
				value := cookie[0]
				p.observation.AddCookies = append(p.observation.AddCookies, &value)
				want["cookie"] = []string{value}
			} else {
				p.observation.AddCookies = append(p.observation.AddCookies, nil)
			}
			if !reflect.DeepEqual(map[string][]string(r.PostForm), want) {
				t.Error("fixed add fields differ")
			}
			p.observation.Adds++
			io.WriteString(w, `{"added_torrent_ids":[9007199254740993]}`)
		default:
			t.Error("unlisted qB route", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
}

// Same standard installation/manifest mechanism as the delivered S03 fixture,
// but an explicit tcp4 listener replaces old httptest's implicit dual-stack fallback.
func qbCookieAppInstall(t *testing.T, a *App, cookie *http.Cookie) (*atomic.Int32, *atomic.Int32) {
	t.Helper()
	if os.Getenv("OWNED_DOWNLOADER_COMMAND_WORKER") == "" {
		t.Fatal("precompiled owned command worker required")
	}
	index, payload := originalMarketOwnedIndex(t, "OWNED_DOWNLOADER_COMMAND_WORKER")
	manifest := index.Releases[0].Manifest
	if manifest.Validate() != nil || manifest.PluginID != ownedCommandWorkerID || len(manifest.APIs) != 1 || manifest.APIs[0].Key != "commands" || manifest.APIs[0].Path != "/commands" {
		t.Fatal("fixed ordinary worker manifest differs")
	}
	found := false
	for _, capability := range manifest.Capabilities {
		found = found || capability == plugins.QBittorrentAddCapability
	}
	if !found {
		t.Fatal("ordinary qB add capability missing")
	}
	index.Releases[0].Artifacts[0].HostContract = pluginResourcesSDKContract
	var indexHits, artifactHits atomic.Int32
	listener := qbCookieAppListener(t)
	index.Releases[0].Artifacts[0].URL = "http://" + listener.Addr().String() + "/worker"
	peer := &httptest.Server{Listener: listener, Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.RawQuery != "" {
			t.Error("nonfixed artifact request")
			w.WriteHeader(405)
			return
		}
		switch r.URL.Path {
		case "/index":
			indexHits.Add(1)
			json.NewEncoder(w).Encode(index)
		case "/worker":
			artifactHits.Add(1)
			w.Write(payload)
		default:
			t.Error("unexpected artifact route")
			w.WriteHeader(404)
		}
	})}}
	peer.Start()
	t.Cleanup(peer.Close)
	body, _ := json.Marshal(map[string]string{"plugin_id": ownedCommandWorkerID, "repo_url": peer.URL + "/index"})
	response := callTest(t, a.Handler(), "POST", "/api/plugins/install", string(body), cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	a.wg.Wait()
	_ = ownedCommandWorkerObject(t, a)
	if indexHits.Load() != 1 || artifactHits.Load() != 1 {
		t.Fatal("installation did not use exact two artifact GETs")
	}
	return &indexHits, &artifactHits
}
func qbCookieAppOperation(value string) map[string]any {
	return map[string]any{"kind": "qbittorrent", "op": "add", "downloader_id": ownedCommandQBID, "args": map[string]any{"content": "https://owned-cookie.invalid/owned.torrent", "cookie": value}}
}
func qbCookieAppAssertSuccess(t *testing.T, out ownedCommandWorkerResult) {
	t.Helper()
	if !out.OK || out.Succeeded != 1 || out.Failed != 0 {
		t.Fatal("ordinary SDK add failed")
	}
	ownedCommandAssertQB(t, out.Results[0], true, []string{"9007199254740993"})
	calls := out.Results[0].HostCalls
	if calls.Total != 1 || calls.Invoke != 1 || calls.Failed != 0 || calls.Succeeded != 1 {
		t.Fatal("one ordinary Add did not use one current invoke")
	}
}
func qbCookieAppAssertMerge(t *testing.T, got []map[string]any, sid string) {
	t.Helper()
	want := []map[string]any{{"domain": "other.invalid", "path": "/", "name": "keep", "value": "owned-other", "expirationDate": float64(0)}, {"domain": "owned-cookie.invalid", "path": "/", "name": "sid", "value": sid}, {"domain": "owned-cookie.invalid", "path": "/", "name": "token", "value": "owned value"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("real wire ordered Cookie merge differs")
	}
}

func TestSharedQBCookieActualWorkerRecoveryAndCold(t *testing.T) {
	state := &qbCookieAppState{version: "2.11.3", observation: qbCookieAppObservation{Store: []map[string]any{{"domain": "other.invalid", "path": "/", "name": "keep", "value": "owned-other", "expirationDate": float64(0)}, {"domain": "owned-cookie.invalid", "name": "sid", "value": "owned-old"}}}}
	peer := qbCookieAppPeer(t, state.handler(t))
	a := newTestApp(t)
	disableResetFixtureStatistics(t, a)
	cfg := a.Settings()
	cfg.PluginMarkets, cfg.PluginLocalRepoPaths = []string{}, []string{}
	cfg.PluginAutoReload = false
	enabled := true
	cfg.Downloaders = []DownloaderConfig{{Config: download.Config{ID: ownedCommandQBID, Kind: "qb", BaseURL: peer.URL, Username: ownedCommandUsername, Password: ownedCommandPassword}, SharedPaths: true, Enabled: &enabled}}
	if err := a.saveSettings(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	cookie := setupTestAdmin(t, a)
	indexHits, artifactHits := qbCookieAppInstall(t, a, cookie)
	t.Cleanup(a.closeOriginalSessions)
	const firstCookie = `sid=owned-new; token="owned value"`
	first := ownedCommandWorkerQuery(t, a, cookie, false, qbCookieAppOperation(firstCookie))
	qbCookieAppAssertSuccess(t, first)
	if first.HadPrevious || first.Invocation != 1 {
		t.Fatal("fresh worker identity")
	}
	afterFirst := state.snapshot()
	if afterFirst.Requests != 8 || afterFirst.Login != 1 || afterFirst.Versions != 4 || afterFirst.Reads != 1 || afterFirst.Sets != 1 || afterFirst.Adds != 1 || afterFirst.AddCookies[0] != nil {
		t.Fatalf("first aggregate counts requests=%d login=%d versions=%d reads=%d sets=%d adds=%d", afterFirst.Requests, afterFirst.Login, afterFirst.Versions, afterFirst.Reads, afterFirst.Sets, afterFirst.Adds)
	}
	qbCookieAppAssertMerge(t, afterFirst.Store, "owned-new")

	state.mu.Lock()
	state.version = "2.11.2"
	state.mu.Unlock()
	fallback := ownedCommandWorkerQuery(t, a, cookie, true, qbCookieAppOperation(firstCookie))
	qbCookieAppAssertSuccess(t, fallback)
	ownedCommandWorkerWarm(t, first, fallback)
	ownedCommandWorkerProbe(t, fallback, "qbittorrent", "add", "invoke")
	afterFallback := state.snapshot()
	if afterFallback.Requests-afterFirst.Requests != 4 || afterFallback.Sets != 1 || afterFallback.Reads != 1 || afterFallback.Adds != 2 || afterFallback.AddCookies[1] == nil || *afterFallback.AddCookies[1] != firstCookie {
		t.Fatal("old-version fallback or stale callback dispatched incorrectly")
	}
	qbCookieAppAssertMerge(t, afterFallback.Store, "owned-new")

	state.mu.Lock()
	state.version = "2.11.3"
	state.failNextSet = true
	state.mu.Unlock()
	failed := ownedCommandWorkerQuery(t, a, cookie, false, qbCookieAppOperation(`sid=owned-failed; token="owned value"`))
	ownedCommandWorkerWarm(t, fallback, failed)
	attempt := failed.Results[0]
	if failed.OK || failed.Failed != 1 || failed.Succeeded != 0 || attempt.OK || attempt.ErrorCode != "host_operation_failed" || len(attempt.Result) != 0 || attempt.SourceTruthy != nil || attempt.HostCalls.Total != 1 || attempt.HostCalls.Failed != 1 {
		t.Fatal("delegated set failure became source False or lost error")
	}
	afterFailure := state.snapshot()
	if afterFailure.Requests-afterFallback.Requests != 6 || afterFailure.Sets != 2 || afterFailure.Adds != 2 || afterFailure.FailedSelected != 1 {
		t.Fatal("delegated failed set retried/fell back to Add")
	}
	qbCookieAppAssertMerge(t, afterFailure.Store, "owned-failed")

	recovery := ownedCommandWorkerQuery(t, a, cookie, false, qbCookieAppOperation(`sid=owned-recovered; token="owned value"`))
	qbCookieAppAssertSuccess(t, recovery)
	ownedCommandWorkerWarm(t, failed, recovery)
	afterRecovery := state.snapshot()
	if afterRecovery.Requests-afterFailure.Requests != 8 || afterRecovery.Adds != 3 || afterRecovery.Sets != 3 || afterRecovery.AddCookies[2] != nil || afterRecovery.FailedSelected != 1 {
		t.Fatal("same-worker explicit recovery differs")
	}
	qbCookieAppAssertMerge(t, afterRecovery.Store, "owned-recovered")
	installation, err := a.pluginStore.Installation(context.Background(), ownedCommandWorkerID)
	if err != nil || !installation.Installed {
		t.Fatal("owned installation absent")
	}
	saved := a.Settings().Downloaders
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.snapshot(), afterRecovery) {
		t.Fatal("Close repeated downloader mutation")
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cold.Close() })
	if err = cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cold.Settings().Downloaders, saved) {
		t.Fatal("cold configured downloader changed")
	}
	again, err := cold.pluginStore.Installation(context.Background(), ownedCommandWorkerID)
	if err != nil || !again.Installed || again.Generation != installation.Generation || again.Version != installation.Version {
		t.Fatal("cold artifact identity changed")
	}
	_ = ownedCommandWorkerObject(t, cold)
	if !reflect.DeepEqual(state.snapshot(), afterRecovery) || indexHits.Load() != 1 || artifactHits.Load() != 1 {
		t.Fatal("cold init replayed mutation or refetched artifact")
	}
	login := callTest(t, cold.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	var coldCookie *http.Cookie
	for _, c := range login.Result().Cookies() {
		if c.Name == "lm_session" {
			coldCookie = c
		}
	}
	if login.Code != http.StatusOK || coldCookie == nil {
		t.Fatal("cold ordinary login failed")
	}
	reopened := ownedCommandWorkerQuery(t, cold, coldCookie, false, qbCookieAppOperation(firstCookie))
	qbCookieAppAssertSuccess(t, reopened)
	if reopened.PID == first.PID || reopened.Invocation != 1 || reopened.HadPrevious || reopened.InitCount != first.InitCount {
		t.Fatal("cold worker lifecycle not fresh")
	}
	final := state.snapshot()
	if final.Requests-afterRecovery.Requests != 8 || final.Requests != 34 || final.Login != 5 || final.Versions != 17 || final.Reads != 4 || final.Sets != 4 || final.Adds != 4 || final.FailedSelected != 1 || indexHits.Load() != 1 || artifactHits.Load() != 1 {
		t.Fatal("final finite request/callback sequence differs")
	}
	qbCookieAppAssertMerge(t, final.Store, "owned-new")
	t.Logf("actual qB-cookie chain: worker %d -> %d; five current SDK callbacks plus one rejected prior callback; fixed peer HTTP=%d, set=%d add=%d, failed_selected=%d; artifact GETs=2", first.PID, reopened.PID, final.Requests, final.Sets, final.Adds, final.FailedSelected)
}

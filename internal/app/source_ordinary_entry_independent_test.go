package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/sites"
)

type ordinaryEntryPeer struct {
	server   *httptest.Server
	entered  chan struct{}
	canceled chan struct{}
	release  chan struct{}
	once     sync.Once
	calls    atomic.Int64
}

func ordinaryEntryServer(t *testing.T) *ordinaryEntryPeer {
	t.Helper()
	p := &ordinaryEntryPeer{entered: make(chan struct{}), canceled: make(chan struct{}, 1), release: make(chan struct{})}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.calls.Add(1) == 1 {
			close(p.entered)
			select {
			case <-p.release:
			case <-r.Context().Done():
				p.canceled <- struct{}{}
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `<html><body><a href="logout.php">退出</a><span>签到成功</span></body></html>`)
	}))
	t.Cleanup(func() { p.unblock(); p.server.Close() })
	return p
}
func (p *ordinaryEntryPeer) unblock() { p.once.Do(func() { close(p.release) }) }
func ordinaryEntryUninstall(t *testing.T, a *App, cookie *http.Cookie) {
	t.Helper()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- callTest(t, a.Handler(), "DELETE", "/api/plugins/AutoSignIn", "", cookie, "") }()
	select {
	case r := <-done:
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		in, e := a.pluginStore.Installation(context.Background(), "AutoSignIn")
		if e != nil || in.Installed {
			t.Fatal("actual source-base uninstall did not commit", e, in.Installed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("individual uninstall joined an already-entered ordinary source callback")
	}
	if _, loaded := a.pluginRunningInstances.Load("AutoSignIn"); loaded {
		t.Fatal("uninstall left old object logically loaded")
	}
}
func ordinaryEntryHistory64(a *App) (int, error) {
	raw, e := a.pluginStore.originalBaseGetData(context.Background(), "AutoSignIn", nil)
	if e != nil {
		return 0, e
	}
	var rows []struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if e = json.Unmarshal(raw, &rows); e != nil {
		return 0, e
	}
	matches := 0
	for _, r := range rows {
		if !strings.Contains(r.Key, "月") {
			continue
		}
		var values []struct {
			Success  bool `json:"success"`
			LoggedIn bool `json:"logged_in"`
		}
		if json.Unmarshal(r.Value, &values) != nil || len(values) != 64 {
			continue
		}
		for _, v := range values {
			if !v.Success || !v.LoggedIn {
				return 0, fmt.Errorf("source history lacks positive parsed result")
			}
		}
		matches++
	}
	return matches, nil
}
func ordinaryEntryMessages(t *testing.T, a *App, cookie *http.Cookie) (int, int) {
	t.Helper()
	r := callTest(t, a.Handler(), "GET", "/api/message/web?count=100", "", cookie, "")
	var out struct {
		Data []struct {
			Title *string `json:"title"`
		} `json:"data"`
	}
	if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil {
		t.Fatal("actual Web message history invalid", r.Code, r.Body.String())
	}
	start, finish := 0, 0
	for _, r := range out.Data {
		if r.Title == nil {
			continue
		}
		switch *r.Title {
		case "开始站点签到 ...":
			start++
		case "站点签到完成！":
			finish++
		}
	}
	return start, finish
}
func ordinaryEntryAwait(t *testing.T, a *App) {
	t.Helper()
	done := make(chan struct{})
	go func() { a.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(14 * time.Second):
		t.Fatal("actual accepted entry did not physically return")
	}
}

// Source44c: exact source EventManager -> sign_in(event) survives full manager
// stop and later writes64 success rows/config. The real Web route selects that
// same registered event; no direct worker invocation substitutes for ingress.
func TestIndependentOrdinaryWebCommandAcceptedCallback(t *testing.T) {
	if os.Getenv("INDEPENDENT_ORDINARY_ASI") == "" {
		t.Skip("opt-in actual SDK fixture: INDEPENDENT_ORDINARY_ASI required")
	}
	for _, mode := range []string{"no-stop-control", "uninstall-active"} {
		t.Run(mode, func(t *testing.T) {
			peer := ordinaryEntryServer(t)
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			rows, ids := []any{}, []any{}
			for n := 0; n < 64; n++ {
				id := fmt.Sprint(97000 + n)
				ids = append(ids, id)
				rows = append(rows, map[string]any{"id": id, "name": fmt.Sprintf("Owned ordinary%d", n), "url": peer.server.URL + fmt.Sprintf("/site-%d/", n), "cookie": "owned=synthetic", "ua": "OwnedOrdinaryUA", "render": false, "timeout": 10})
			}
			retainedStoreSaveConfig(t, a, "CustomSites", map[string]any{"enabled": true, "sites": rows})
			retainedStoreSaveConfig(t, a, "AutoSignIn", map[string]any{"enabled": true, "notify": false, "cron": "25", "onlyonce": false, "queue_cnt": 1, "sign_sites": ids, "login_sites": []any{}, "retry_keyword": "^NEVER-MATCH$", "auto_cf": 0, "clean": false})
			m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("INDEPENDENT_ORDINARY_ASI"))
			if m.PluginID != "AutoSignIn" {
				t.Fatal("real ASI worker required")
			}
			object := originalBaseSDKLoaded(t, a, m.PluginID)
			object.sessionMu.Lock()
			acceptedClient := object.session
			object.sessionMu.Unlock()
			if acceptedClient == nil {
				t.Fatal("actual source client missing")
			}
			r := callTest(t, a.Handler(), "POST", "/api/message/web", `{"text":"/site_signin"}`, cookie, "")
			var accepted struct {
				Data struct {
					Success  bool `json:"success"`
					Accepted bool `json:"accepted"`
				} `json:"data"`
			}
			if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &accepted) != nil || !accepted.Data.Success || !accepted.Data.Accepted {
				t.Fatal("real Web command was not routed", r.Code, r.Body.String())
			}
			select {
			case <-peer.entered:
			case <-time.After(8 * time.Second):
				t.Fatal("registered PluginAction did not enter actual source HTTP")
			}
			object.submitted.mu.Lock()
			private := 0
			for lease := range object.submitted.leases {
				if lease.privateScheduler {
					private++
				}
			}
			object.submitted.mu.Unlock()
			if private != 0 {
				t.Fatal("ordinary command selected private scheduler", private)
			}
			// Verification setup only: change persistent config without reinitializing
			// the held object. Exact source __update_config140 constructs its known
			// fields and drops this unrelated marker when the accepted callback ends.
			storedBefore, configErr := a.pluginStore.Config(context.Background(), "AutoSignIn")
			if configErr != nil {
				t.Fatal(configErr)
			}
			var markedConfig map[string]any
			if json.Unmarshal(storedBefore.Value, &markedConfig) != nil {
				t.Fatal("config setup is not an object")
			}
			const marker = "_independent_after_entry_marker"
			markedConfig[marker] = true
			marked := retainedStoreSaveConfig(t, a, "AutoSignIn", markedConfig)
			if mode == "uninstall-active" {
				ordinaryEntryUninstall(t, a, cookie)
				newRequest := callTest(t, a.Handler(), "POST", "/api/message/web", `{"text":"/site_signin"}`, cookie, "")
				var out struct {
					Data struct {
						Success  bool `json:"success"`
						Accepted bool `json:"accepted"`
					} `json:"data"`
				}
				if newRequest.Code != 200 || json.Unmarshal(newRequest.Body.Bytes(), &out) != nil || !out.Data.Success || out.Data.Accepted {
					t.Fatal("new command borrowed old accepted callback", newRequest.Code, newRequest.Body.String())
				}
			}
			canceled := false
			select {
			case <-peer.canceled:
				canceled = true
			default:
			}
			peer.unblock()
			ordinaryEntryAwait(t, a)
			n, e := ordinaryEntryHistory64(a)
			if e != nil {
				t.Fatal(e)
			}
			start, finish := ordinaryEntryMessages(t, a, cookie)
			storedAfter, configErr := a.pluginStore.Config(context.Background(), "AutoSignIn")
			if configErr != nil {
				t.Fatal(configErr)
			}
			var finalConfig map[string]any
			if json.Unmarshal(storedAfter.Value, &finalConfig) != nil {
				t.Fatal("final config invalid")
			}
			_, markerRemains := finalConfig[marker]
			if markerRemains || storedAfter.Revision <= marked.Revision {
				t.Error("accepted command lost post-stop source configuration write", markerRemains, marked.Revision, storedAfter.Revision)
			}
			if canceled {
				t.Error("individual stop canceled already-entered command HTTP; source44c keeps it")
			}
			if peer.calls.Load() != 64 || n != 1 {
				t.Error("accepted command lost source64 successful business after stop", peer.calls.Load(), n)
			}
			if start != 1 || finish != 1 {
				t.Error("accepted command lost actual Web replies", start, finish)
			}
			object.sessionMu.Lock()
			sameClient := object.session == acceptedClient
			object.sessionMu.Unlock()
			t.Logf("mode=%s actualHTTP=%d history64=%d WebStart=%d WebFinish=%d firstRequestCanceled=%v sameCapturedClient=%v privateLeases=%d", mode, peer.calls.Load(), n, start, finish, canceled, sameClient, private)
		})
	}
}

// Source44c: an already-entered bound signin_by_domain still returns its source
// Response after manager removal. Uninstall additionally unregisters future
// routes; that must not invalidate the response of the earlier accepted call.
func TestIndependentOrdinaryAPIAcceptedBoundResponse(t *testing.T) {
	if os.Getenv("INDEPENDENT_ORDINARY_ASI") == "" {
		t.Skip("opt-in actual SDK fixture: INDEPENDENT_ORDINARY_ASI required")
	}
	for _, mode := range []string{"no-stop-control", "uninstall-active"} {
		t.Run(mode, func(t *testing.T) {
			peer := ordinaryEntryServer(t)
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			settings := a.Settings()
			settings.APIToken = "owned-ordinary-synthetic-api-key"
			u, e := url.Parse(peer.server.URL)
			if e != nil {
				t.Fatal(e)
			}
			settings.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-ordinary-api", Name: "Owned API", Kind: "ubits", BaseURL: peer.server.URL + "/", Cookie: "owned=synthetic", UserAgent: "OwnedOrdinaryUA"}, SiteOptions: SiteOptions{Domain: u.Host}, Enabled: true}}
			ownedDownloaderSave(t, a, settings)
			if !a.addOriginalSourceSiteIndexer(u.Host, map[string]any{"name": "Owned API", "domain": peer.server.URL + "/", "schema": "NexusPhp", "public": false}) {
				t.Fatal("owned source template admission failed")
			}
			retainedStoreSaveConfig(t, a, "AutoSignIn", map[string]any{"enabled": true, "notify": false, "cron": "25", "onlyonce": false, "queue_cnt": 1, "sign_sites": []any{}, "login_sites": []any{}, "retry_keyword": "^NEVER-MATCH$", "auto_cf": 0, "clean": false})
			m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("INDEPENDENT_ORDINARY_ASI"))
			if m.PluginID != "AutoSignIn" {
				t.Fatal("real ASI required")
			}
			object := originalBaseSDKLoaded(t, a, m.PluginID)
			object.sessionMu.Lock()
			acceptedClient := object.session
			object.sessionMu.Unlock()
			if acceptedClient == nil {
				t.Fatal("actual source client missing")
			}
			address := "/api/v1/plugin/AutoSignIn/signin_by_domain?url=" + url.QueryEscape(peer.server.URL+"/") + "&apikey=" + url.QueryEscape(settings.APIToken)
			wrong := callTest(t, a.Handler(), "GET", strings.Replace(address, settings.APIToken, "wrong-synthetic-key", 1), "", nil, "")
			foreign := callTest(t, a.Handler(), "GET", strings.Replace(address, "/AutoSignIn/", "/OwnedForeign/", 1), "", nil, "")
			if wrong.Code != 401 || foreign.Code != 404 || peer.calls.Load() != 0 {
				t.Fatal("unrelated or unauthenticated API reached source HTTP", wrong.Code, foreign.Code, peer.calls.Load())
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- callTest(t, a.Handler(), "GET", address, "", nil, "") }()
			select {
			case <-peer.entered:
			case r := <-done:
				t.Fatal("API returned before reaching owned HTTP", r.Code, r.Body.String())
			case <-time.After(8 * time.Second):
				t.Fatal("registered source API did not reach actual HTTP")
			}
			if mode == "uninstall-active" {
				ordinaryEntryUninstall(t, a, cookie)
				next := callTest(t, a.Handler(), "GET", address, "", nil, "")
				if next.Code != 404 {
					t.Fatal("new API request borrowed detached old client", next.Code, next.Body.String())
				}
			}
			canceled := false
			select {
			case <-peer.canceled:
				canceled = true
			default:
			}
			peer.unblock()
			var r *httptest.ResponseRecorder
			select {
			case r = <-done:
			case <-time.After(12 * time.Second):
				t.Fatal("accepted source API never returned")
			}
			var response struct {
				Success bool `json:"success"`
				Data    struct {
					Signin bool `json:"signin_success"`
					Login  bool `json:"login_success"`
				} `json:"data"`
			}
			if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &response) != nil || !response.Success || !response.Data.Signin || !response.Data.Login {
				t.Error("adapter discarded accepted old bound API result; source44c returns actual success", r.Code, r.Body.String())
			}
			if canceled {
				t.Error("individual stop canceled already-entered API HTTP; source44c keeps it")
			}
			ordinaryEntryAwait(t, a)
			object.sessionMu.Lock()
			sameClient := object.session == acceptedClient
			object.sessionMu.Unlock()
			t.Logf("mode=%s actualHTTP=%d status=%d sourceSuccess=%v signin=%v login=%v firstRequestCanceled=%v sameCapturedClient=%v wrongKeyStatus=%d foreignStatus=%d", mode, peer.calls.Load(), r.Code, response.Success, response.Data.Signin, response.Data.Login, canceled, sameClient, wrong.Code, foreign.Code)
		})
	}
}

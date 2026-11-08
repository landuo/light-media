package app

import (
	"encoding/json"
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

// Expected results are the selected fixed route/Depends + installed ASI oracle
// in this work's source-index.json; no assertion comes from the target guard.
func TestIndependentBoundAPIKeyRotation(t *testing.T) {
	if os.Getenv("INDEPENDENT_ROTATION_ASI") == "" {
		t.Skip("opt-in actual SDK fixture: INDEPENDENT_ROTATION_ASI required")
	}
	for _, mode := range []string{"unchanged-key-control", "rotate-after-entry"} {
		t.Run(mode, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			var calls atomic.Int64
			var canceled atomic.Bool
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
						canceled.Store(true)
						return
					}
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				io.WriteString(w, `<html><body><a href="logout.php">退出</a><span>签到成功</span></body></html>`)
			}))
			defer peer.Close()
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			settings := a.Settings()
			const oldKey = "owned-rotation-original-synthetic-key"
			const newKey = "owned-rotation-next-synthetic-key"
			settings.APIToken = oldKey
			u, err := url.Parse(peer.URL)
			if err != nil {
				t.Fatal(err)
			}
			settings.Sites = []SiteConfig{{Config: sites.Config{ID: "owned-rotation-api", Name: "Owned rotation", Kind: "ubits", BaseURL: peer.URL + "/", Cookie: "owned=synthetic", UserAgent: "OwnedRotationUA"}, SiteOptions: SiteOptions{Domain: u.Host}, Enabled: true}}
			ownedDownloaderSave(t, a, settings)
			if !a.addOriginalSourceSiteIndexer(u.Host, map[string]any{"name": "Owned rotation", "domain": peer.URL + "/", "schema": "NexusPhp", "public": false}) {
				t.Fatal("owned source template admission failed")
			}
			retainedStoreSaveConfig(t, a, "AutoSignIn", map[string]any{"enabled": true, "notify": false, "cron": "25", "onlyonce": false, "queue_cnt": 1, "sign_sites": []any{}, "login_sites": []any{}, "retry_keyword": "^NEVER-MATCH$", "auto_cf": 0, "clean": false})
			m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("INDEPENDENT_ROTATION_ASI"))
			if m.PluginID != "AutoSignIn" {
				t.Fatal("actual ASI worker required")
			}
			a.wg.Wait()
			address := func(key string) string {
				return "/api/v1/plugin/AutoSignIn/signin_by_domain?url=" + url.QueryEscape(peer.URL+"/") + "&apikey=" + url.QueryEscape(key)
			}
			wrong := callTest(t, a.Handler(), "GET", address("owned-wrong-synthetic-key"), "", nil, "")
			foreign := callTest(t, a.Handler(), "GET", strings.Replace(address(oldKey), "/AutoSignIn/", "/OwnedForeign/", 1), "", nil, "")
			if wrong.Code != 401 || foreign.Code != 404 || calls.Load() != 0 {
				t.Fatal("new-request authority/route guard failed", wrong.Code, foreign.Code, calls.Load())
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- callTest(t, a.Handler(), "GET", address(oldKey), "", nil, "") }()
			select {
			case <-entered:
			case r := <-done:
				t.Fatal("actual API did not enter owned source HTTP", r.Code, r.Body.String())
			case <-time.After(8 * time.Second):
				t.Fatal("actual API did not reach HTTP")
			}
			if mode == "rotate-after-entry" {
				changed := a.Settings()
				changed.APIToken = newKey
				ownedDownloaderSave(t, a, changed)
				nextOld := callTest(t, a.Handler(), "GET", address(oldKey), "", nil, "")
				if nextOld.Code != 401 || calls.Load() != 1 {
					t.Fatal("rotated old key gained new admission", nextOld.Code, calls.Load())
				}
			}
			unblock()
			var active *httptest.ResponseRecorder
			select {
			case active = <-done:
			case <-time.After(12 * time.Second):
				t.Fatal("accepted bound method did not return")
			}
			assertSourceSuccess := func(label string, r *httptest.ResponseRecorder) {
				t.Helper()
				var out struct {
					Success bool `json:"success"`
					Data    struct {
						Signin bool `json:"signin_success"`
						Login  bool `json:"login_success"`
					} `json:"data"`
				}
				if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &out) != nil || !out.Success || !out.Data.Signin || !out.Data.Login {
					t.Errorf("%s: completed accepted source result discarded; original returns 200 dualtrue: status=%d body=%s", label, r.Code, r.Body.String())
				}
			}
			assertSourceSuccess("already-admitted", active)
			key := oldKey
			if mode == "rotate-after-entry" {
				key = newKey
			}
			nextNew := callTest(t, a.Handler(), "GET", address(key), "", nil, "")
			assertSourceSuccess("current-key-new-admission", nextNew)
			if canceled.Load() || calls.Load() != 2 {
				t.Error("source HTTP side effect was canceled or new guards changed", canceled.Load(), calls.Load())
			}
			t.Logf("mode=%s admittedStatus=%d currentKeyNewStatus=%d actualHTTP=%d firstCanceled=%v initialWrong=%d foreign=%d", mode, active.Code, nextNew.Code, calls.Load(), canceled.Load(), wrong.Code, foreign.Code)
		})
	}
}

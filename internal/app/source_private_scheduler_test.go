package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func privateAwait(t *testing.T, label string, f func() bool) {
	t.Helper()
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		if f() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timeout: " + label)
}
func privateConfig(ids []any, once bool) map[string]any {
	return map[string]any{"enabled": true, "notify": true, "cron": "25", "onlyonce": once, "queue_cnt": 1, "sign_sites": ids, "login_sites": []any{}, "retry_keyword": []any{}, "auto_cf": 0, "clean": false}
}
func privateRows(base string, start, n int) ([]any, []any) {
	rows, ids := []any{}, []any{}
	for i := start; i < start+n; i++ {
		id := fmt.Sprint(20000 + i)
		rows = append(rows, map[string]any{"id": id, "name": fmt.Sprintf("OwnedPrivate%d", i), "url": base + fmt.Sprintf("/site-%d/", i), "cookie": "owned=yes", "ua": "OwnedPrivateUA", "render": false, "timeout": 10})
		ids = append(ids, id)
	}
	return rows, ids
}
func privateHistory(t *testing.T, a *App, id string, count int, success bool) {
	t.Helper()
	raw, e := a.pluginStore.originalBaseGetData(context.Background(), id, nil)
	if e != nil {
		t.Error(e)
		return
	}
	var rows []struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if json.Unmarshal(raw, &rows) != nil {
		t.Error("actual private data rows", string(raw))
		return
	}
	found := 0
	for _, row := range rows {
		if !strings.Contains(row.Key, "月") {
			continue
		}
		var values []struct {
			Success  bool `json:"success"`
			LoggedIn bool `json:"logged_in"`
		}
		if json.Unmarshal(row.Value, &values) != nil {
			continue
		}
		if len(values) == count {
			found++
			for _, v := range values {
				if v.Success != success || v.LoggedIn != success {
					t.Error("source parsed private business result differs", v, success)
				}
			}
		}
	}
	if found != 1 {
		t.Error("actual private callback history did not finish before stop", found, count, string(raw))
	}
}
func TestSourcePrivateSchedulerStopActualASI(t *testing.T) {
	for _, mode := range []string{"active", "pending", "HTTP-error", "global-close"} {
		t.Run(mode, func(t *testing.T) {
			const sites = 64
			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			var count atomic.Int64
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if count.Add(1) == 1 {
					entered <- struct{}{}
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				if mode == "HTTP-error" {
					w.WriteHeader(500)
					io.WriteString(w, "owned unavailable")
					return
				}
				io.WriteString(w, `<html><body><a href="logout.php">退出</a><span>签到成功</span></body></html>`)
			}))
			defer func() { unblock(); peer.Close() }()
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			rows, ids := privateRows(peer.URL, 0, sites)
			retainedStoreSaveConfig(t, a, "CustomSites", map[string]any{"enabled": true, "sites": rows})
			retainedStoreSaveConfig(t, a, "AutoSignIn", privateConfig(ids, false))
			m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("SOURCE_PRIVATE_ASI"))
			if m.PluginID != "AutoSignIn" {
				t.Fatal("true production ASI required")
			}
			p := originalBaseSDKLoaded(t, a, m.PluginID)
			retainedStoreSaveConfig(t, a, m.PluginID, privateConfig(ids, true))
			if e := p.init(context.Background()); e != nil {
				t.Fatal("actual source +3sec once init", e)
			}
			stored, e := a.pluginStore.Config(context.Background(), m.PluginID)
			var saved map[string]any
			if e != nil || json.Unmarshal(stored.Value, &saved) != nil || saved["onlyonce"] != false {
				t.Fatal("onlyonce not cleared before future date", e, string(stored.Value))
			}
			if mode != "pending" {
				select {
				case <-entered:
				case <-time.After(10 * time.Second):
					t.Fatal("actual private date never entered source HTTP")
				}
			}
			stopDone := make(chan error, 1)
			start := time.Now()
			go func() {
				if mode == "global-close" {
					stopDone <- a.Close()
				} else {
					stopDone <- a.stopOriginalPlugin(context.Background(), m.PluginID)
				}
			}()
			privateAwait(t, "actual manager stop began", func() bool { return p.stopping.Load() })
			if mode == "pending" {
				select {
				case e := <-stopDone:
					if e != nil {
						t.Error(e)
					}
					if time.Since(start) > 1500*time.Millisecond {
						t.Error("future date cancellation waited for fire", time.Since(start))
					}
				case <-time.After(2 * time.Second):
					t.Fatal("pending private date stop did not return promptly")
				}
				unblock()
				time.Sleep(3300 * time.Millisecond)
				if count.Load() != 0 {
					t.Error("future private callback ran after stop", count.Load())
				}
				return
			}
			select {
			case e := <-stopDone:
				t.Error("manager stop returned while actual private callback still held", e)
				stopDone <- e
			case <-time.After(200 * time.Millisecond):
			}
			unblock()
			select {
			case e := <-stopDone:
				if e != nil {
					t.Error(e)
				}
			case <-time.After(35 * time.Second):
				t.Fatal("active private stop never joined actual business")
			}
			wantRequests := int64(sites)
			if mode == "HTTP-error" {
				wantRequests *= 2
			}
			if n := count.Load(); n != wantRequests {
				t.Error("active private callback lost source HTTP/48RPC continuation", n, wantRequests)
			}
			view := a
			if mode == "global-close" {
				fresh, e := New(Options{DataDir: a.Dir})
				if e != nil {
					t.Fatal(e)
				}
				defer fresh.Close()
				view = fresh
			}
			privateHistory(t, view, m.PluginID, sites, mode != "HTTP-error")
			notices, e := view.Store.List(context.Background(), "notification", 20, 0)
			if e != nil || len(notices) == 0 {
				t.Error("private stop lost actual notification history", len(notices), e)
			}
			if _, ok := a.pluginRunningInstances.Load(m.PluginID); ok {
				t.Error("manager stop retained running object")
			}
		})
	}
}
func TestSourcePrivateSchedulerCloneIsolationActualASI(t *testing.T) {
	const sites = 64
	sourceEntered, cloneEntered := make(chan struct{}, 1), make(chan struct{}, 1)
	sourceRelease, cloneRelease := make(chan struct{}), make(chan struct{})
	var so, co sync.Once
	sourceUnblock := func() { so.Do(func() { close(sourceRelease) }) }
	cloneUnblock := func() { co.Do(func() { close(cloneRelease) }) }
	defer sourceUnblock()
	defer cloneUnblock()
	var sourceCount, cloneCount atomic.Int64
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clone := false
		for i := sites; i < sites*2; i++ {
			if strings.HasPrefix(r.URL.Path, fmt.Sprintf("/site-%d/", i)) {
				clone = true
				break
			}
		}
		counter, entered, release := &sourceCount, sourceEntered, sourceRelease
		if clone {
			counter, entered, release = &cloneCount, cloneEntered, cloneRelease
		}
		if counter.Add(1) == 1 {
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		io.WriteString(w, `<html><body><a href="logout.php">退出</a><span>签到成功</span></body></html>`)
	}))
	defer func() { sourceUnblock(); cloneUnblock(); peer.Close() }()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	sourceRows, sourceIDs := privateRows(peer.URL, 0, sites)
	cloneRows, cloneIDs := privateRows(peer.URL, sites, sites)
	retainedStoreSaveConfig(t, a, "CustomSites", map[string]any{"enabled": true, "sites": append(sourceRows, cloneRows...)})
	retainedStoreSaveConfig(t, a, "AutoSignIn", privateConfig(sourceIDs, false))
	m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("SOURCE_PRIVATE_ASI"))
	source := originalBaseSDKLoaded(t, a, m.PluginID)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/AutoSignIn/clone", `{"suffix":"owned"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	clone := originalBaseSDKLoaded(t, a, "AutoSignInowned")
	retainedStoreSaveConfig(t, a, "AutoSignIn", privateConfig(sourceIDs, true))
	retainedStoreSaveConfig(t, a, "AutoSignInowned", privateConfig(cloneIDs, true))
	if e := source.init(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := clone.init(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, ch := range []chan struct{}{sourceEntered, cloneEntered} {
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			t.Fatal("source/clone private once did not start")
		}
	}
	stopped := make(chan error, 1)
	go func() { stopped <- a.stopOriginalPlugin(context.Background(), "AutoSignIn") }()
	privateAwait(t, "source stop began", func() bool { return source.stopping.Load() })
	select {
	case e := <-stopped:
		t.Error("source stop did not wait source private callback", e)
		stopped <- e
	case <-time.After(200 * time.Millisecond):
	}
	if clone.stopping.Load() {
		t.Error("source stop changed clone ownership")
	}
	sourceUnblock()
	select {
	case e := <-stopped:
		if e != nil {
			t.Error(e)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("source private stop did not finish")
	}
	if sourceCount.Load() != sites {
		t.Error("source private callbacks truncated", sourceCount.Load())
	}
	privateHistory(t, a, "AutoSignIn", sites, true)
	if cloneCount.Load() != 1 {
		t.Error("held clone was altered by source stop", cloneCount.Load())
	}
	cloneUnblock()
	privateAwait(t, "clone all source HTTP", func() bool { return cloneCount.Load() == sites })
	privateAwait(t, "clone private phases drained", func() bool {
		clone.submitted.mu.Lock()
		defer clone.submitted.mu.Unlock()
		return len(clone.submitted.leases) == 0
	})
	privateHistory(t, a, "AutoSignInowned", sites, true)
	if e := a.stopOriginalPlugin(context.Background(), "AutoSignInowned"); e != nil {
		t.Fatal(e)
	}
}

// On an opted-in object, ordinary Submit still revokes promptly. Only the real
// pending/private root's HostCaller lineage survives the pre-stop join.
func TestSourcePrivateSchedulerOrdinarySubmitStillRevokesActualASI(t *testing.T) {
	const sites = 64
	privateEntered, ordinaryEntered := make(chan struct{}, 1), make(chan struct{}, 1)
	privateCanceled, ordinaryCanceled := make(chan struct{}, 1), make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	var count atomic.Int64
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := count.Add(1)
		if n <= 2 {
			entered, canceled := privateEntered, privateCanceled
			if n == 2 {
				entered, canceled = ordinaryEntered, ordinaryCanceled
			}
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				canceled <- struct{}{}
				return
			}
		}
		io.WriteString(w, `<html><body><a href="logout.php">退出</a><span>签到成功</span></body></html>`)
	}))
	defer func() { unblock(); peer.Close() }()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	rows, ids := privateRows(peer.URL, 0, sites)
	retainedStoreSaveConfig(t, a, "CustomSites", map[string]any{"enabled": true, "sites": rows})
	retainedStoreSaveConfig(t, a, "AutoSignIn", privateConfig(ids, false))
	m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("SOURCE_PRIVATE_ASI"))
	p := originalBaseSDKLoaded(t, a, m.PluginID)
	retainedStoreSaveConfig(t, a, m.PluginID, privateConfig(ids, true))
	if e := p.init(context.Background()); e != nil {
		t.Fatal(e)
	}
	select {
	case <-privateEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("owned private date did not reach first HTTP")
	}
	ordinaryDone := make(chan error, 1)
	go func() {
		_, e := p.callOriginalMethod(context.Background(), "sign_in", json.RawMessage(`{}`))
		ordinaryDone <- e
	}()
	select {
	case <-ordinaryEntered:
	case <-time.After(4 * time.Second):
		t.Fatal("ordinary real Submit lineage did not enter HTTP")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- a.stopOriginalPlugin(context.Background(), m.PluginID) }()
	privateAwait(t, "same-object manager stop began", func() bool { return p.stopping.Load() })
	select {
	case <-ordinaryCanceled:
	case <-time.After(3 * time.Second):
		t.Error("old ordinary Submit retained authority during private join")
	}
	select {
	case <-privateCanceled:
		t.Error("private owner was canceled together with ordinary Submit")
	default:
	}
	select {
	case e := <-ordinaryDone:
		if e == nil {
			t.Error("revoked ordinary root falsely completed")
		}
	case <-time.After(3 * time.Second):
		t.Error("ordinary source pass did not observe revocation")
	}
	select {
	case e := <-stopped:
		t.Error("private stop returned before private source root", e)
		stopped <- e
	default:
	}
	unblock()
	select {
	case e := <-stopped:
		if e != nil {
			t.Error(e)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("mixed stop did not join private callback")
	}
	if n := count.Load(); n != sites+1 {
		t.Error("ordinary/private HTTP lineage separated incorrectly", n, sites+1)
	}
	privateHistory(t, a, m.PluginID, sites, true)
}

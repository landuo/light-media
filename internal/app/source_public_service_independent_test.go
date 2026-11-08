package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIndependentPublicIndividualStopPreservesAcceptedSourceCallback(t *testing.T) {
	for _, mode := range []string{"remove-registration-only", "full-object-stop"} {
		t.Run(mode, func(t *testing.T) {
			entered, release, canceled := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			var gets atomic.Int64
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if gets.Add(1) == 1 {
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
						canceled <- struct{}{}
						return
					}
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				io.WriteString(w, `<html><body><a href="logout.php">退出</a><span>签到成功</span></body></html>`)
			}))
			defer func() { unblock(); peer.Close() }()
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			const total = 64
			rows, ids := []any{}, []any{}
			for n := 0; n < total; n++ {
				id := fmt.Sprint(96000 + n)
				ids = append(ids, id)
				rows = append(rows, map[string]any{"id": id, "name": fmt.Sprintf("IndependentPublicStop%d", n), "url": peer.URL + fmt.Sprintf("/site-%d/", n), "cookie": "owned=synthetic", "ua": "IndependentPublicStopUA", "render": false, "timeout": 10})
			}
			retainedStoreSaveConfig(t, a, "CustomSites", map[string]any{"enabled": true, "sites": rows})
			retainedStoreSaveConfig(t, a, "AutoSignIn", map[string]any{"enabled": true, "notify": true, "cron": "25", "onlyonce": false, "queue_cnt": 1, "sign_sites": ids, "login_sites": []any{}, "retry_keyword": []any{}, "auto_cf": 0, "clean": false})
			m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("INDEPENDENT_PUBLIC_ASI"))
			if m.PluginID != "AutoSignIn" {
				t.Fatal("actual source ASI command required")
			}
			p := originalBaseSDKLoaded(t, a, m.PluginID)
			if e := a.runOriginalPublicService(context.Background(), m.PluginID, "AutoSignIn"); e != nil {
				t.Fatal(e)
			}
			select {
			case <-entered:
			case <-time.After(8 * time.Second):
				t.Fatal("public service did not enter real owned source HTTP")
			}
			// The public service owns ordinary submitted phases, not a private date.
			p.submitted.mu.Lock()
			privateCount := 0
			for lease := range p.submitted.leases {
				if lease.privateScheduler {
					privateCount++
				}
			}
			p.submitted.mu.Unlock()
			if privateCount != 0 {
				t.Fatal("test selected a private callback instead of the ordinary public service", privateCount)
			}
			if mode == "remove-registration-only" {
				a.removeOriginalPluginJobs(m.PluginID)
				if p.stopping.Load() {
					t.Fatal("removing only registration stopped object")
				}
			} else {
				stopped := make(chan error, 1)
				go func() { stopped <- a.stopOriginalPlugin(context.Background(), m.PluginID) }()
				select {
				case e := <-stopped:
					if e != nil {
						t.Fatal(e)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("object stop unexpectedly joins a held ordinary public callback")
				}
				if _, loaded := a.pluginRunningInstances.Load(m.PluginID); loaded {
					t.Error("full object stop did not remove running instance")
				}
			}
			select {
			case <-canceled:
				t.Error("accepted public source HTTP was canceled by manager operation; fixed source continues after stop")
			default:
			}
			unblock()
			status := ""
			deadline := time.Now().Add(12 * time.Second)
			for time.Now().Before(deadline) {
				a.pluginOriginalDirectJobs.mu.Lock()
				progress := a.pluginOriginalDirectJobs.progress["AutoSignIn_AutoSignIn"]
				if progress != nil {
					_ = json.Unmarshal(progress.data["status"], &status)
				}
				a.pluginOriginalDirectJobs.mu.Unlock()
				if status == "success" || status == "failed" {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if status != "success" {
				t.Error("accepted public source business did not finish successfully after manager returned", status)
			}
			if n := gets.Load(); n != total {
				t.Error("accepted ordinary callback source HTTP was truncated", n, total)
			}
			raw, e := a.pluginStore.originalBaseGetData(context.Background(), m.PluginID, nil)
			if e != nil {
				t.Fatal(e)
			}
			var data []struct {
				Key   string          `json:"key"`
				Value json.RawMessage `json:"value"`
			}
			if json.Unmarshal(raw, &data) != nil {
				t.Fatal("actual durable data projection invalid")
			}
			found := 0
			for _, r := range data {
				var history []struct {
					Success  bool `json:"success"`
					LoggedIn bool `json:"logged_in"`
				}
				if json.Unmarshal(r.Value, &history) != nil || len(history) != total {
					continue
				}
				found++
				for _, item := range history {
					if !item.Success || !item.LoggedIn {
						t.Error("retained public callback history is not parsed successful business", item)
					}
				}
			}
			if found != 1 {
				t.Error("complete source64 history missing after manager operation", found)
			}
			t.Logf("mode=%s sourceHTTP=%d history64=%d progress=%s privateLeases=%d", mode, gets.Load(), found, status, privateCount)
		})
	}
}

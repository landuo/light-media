package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourcePrivateSchedulerMixedManagerStopOrderActualSDK(t *testing.T) {
	for _, layout := range []string{"legacy-private-clone", "clone-private-legacy", "legacy-only"} {
		t.Run(layout, func(t *testing.T) {
			var mu sync.Mutex
			trace := []string{}
			snapshot := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), trace...) }
			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			var gets atomic.Int64
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/trace/") {
					parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/trace/"), "/")
					if len(parts) != 2 {
						t.Error("owned trace invalid path", r.URL.Path)
						http.Error(w, "bad", 400)
						return
					}
					mu.Lock()
					trace = append(trace, parts[0]+":"+parts[1])
					mu.Unlock()
					io.WriteString(w, "recorded")
					return
				}
				if gets.Add(1) == 1 {
					entered <- struct{}{}
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				io.WriteString(w, `<html><body><a href="logout.php">退出</a><span>签到成功</span></body></html>`)
			}))
			defer func() { unblock(); peer.Close() }()
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			retainedStoreSaveConfig(t, a, "OwnedStopOrder", map[string]any{"owned_trace_endpoint": peer.URL})
			installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("SOURCE_PRIVATE_LEGACY_TRACE"))
			clone := func(suffix string) {
				r := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedStopOrder/clone", `{"suffix":"`+suffix+`"}`, cookie, "")
				assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			}
			var p *originalPluginInstance
			var ids []any
			installPrivate := func() {
				rows, selected := privateRows(peer.URL, 0, 64)
				ids = selected
				retainedStoreSaveConfig(t, a, "CustomSites", map[string]any{"enabled": true, "sites": rows})
				cfg := privateConfig(ids, false)
				cfg["owned_trace_endpoint"] = peer.URL
				retainedStoreSaveConfig(t, a, "AutoSignIn", cfg)
				installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("SOURCE_PRIVATE_ASI_TRACE"))
				p = originalBaseSDKLoaded(t, a, "AutoSignIn")
			}
			order := []string{}
			if layout == "legacy-only" {
				clone("one")
				clone("two")
				order = []string{"OwnedStopOrder", "OwnedStopOrderone", "OwnedStopOrdertwo"}
			} else if layout == "legacy-private-clone" {
				installPrivate()
				clone("owned")
				order = []string{"OwnedStopOrder", "AutoSignIn", "OwnedStopOrderowned"}
			} else {
				clone("owned")
				installPrivate()
				if _, e := a.reloadOriginalPlugin(context.Background(), "OwnedStopOrder"); e != nil {
					t.Fatal(e)
				}
				order = []string{"OwnedStopOrderowned", "AutoSignIn", "OwnedStopOrder"}
			}
			loaded := a.originalLoadedPluginSnapshot()
			observed := []string{}
			for _, o := range loaded {
				observed = append(observed, o.installation.InstanceID)
			}
			if !reflect.DeepEqual(order, observed) {
				t.Fatal("actual source insertion order setup", observed, order)
			}
			mu.Lock()
			trace = nil
			mu.Unlock()
			if p != nil {
				cfg := privateConfig(ids, true)
				cfg["owned_trace_endpoint"] = peer.URL
				retainedStoreSaveConfig(t, a, "AutoSignIn", cfg)
				if e := p.init(context.Background()); e != nil {
					t.Fatal(e)
				}
				select {
				case <-entered:
				case <-time.After(10 * time.Second):
					t.Fatal("actual private callback missing")
				}
			}
			closed := make(chan error, 1)
			go func() { closed <- a.Close() }()
			if p != nil {
				privateAwait(t, "real private stop_service dispatch reached", func() bool {
					for _, entry := range snapshot() {
						if entry == "AutoSignIn:stop_service" {
							return true
						}
					}
					return false
				})
				for _, entry := range snapshot() {
					if strings.HasPrefix(entry, order[2]+":") {
						t.Error("later legacy object overtook held private callback", entry)
					}
				}
				select {
				case e := <-closed:
					t.Error("global manager stop returned before held private callback", e)
					closed <- e
				case <-time.After(200 * time.Millisecond):
				}
				unblock()
			}
			select {
			case e := <-closed:
				if e != nil {
					t.Error(e)
				}
			case <-time.After(35 * time.Second):
				t.Fatal("actual global stop did not complete")
			}
			want := []string{}
			for _, id := range order {
				for _, phase := range []string{"close", "stop_service", "stop"} {
					want = append(want, id+":"+phase)
				}
			}
			got := snapshot()
			if !reflect.DeepEqual(got, want) {
				t.Error("fixed source per-object close→stop_service insertion order (plus explicit Go physical stop) differs", got, want)
			}
			fresh, e := New(Options{DataDir: a.Dir})
			if e != nil {
				t.Fatal(e)
			}
			defer fresh.Close()
			if p != nil {
				if gets.Load() != 64 {
					t.Error("ordered join truncated private64", gets.Load())
				}
				privateHistory(t, fresh, "AutoSignIn", 64, true)
			}
			for _, id := range order {
				if id == "AutoSignIn" {
					continue
				}
				for _, phase := range []string{"close", "stop_service", "stop"} {
					key := "actual_" + phase
					raw, e := fresh.pluginStore.originalBaseGetData(context.Background(), id, &key)
					var record struct{ Instance, Method string }
					if e != nil || json.Unmarshal(raw, &record) != nil || record.Instance != id || record.Method != phase {
						t.Error("actual legacy scoped phase write missing", id, phase, e, string(raw))
					}
				}
			}
			t.Log("actual SDK lifecycle trace", got, "private source HTTP", gets.Load(), "trace ASI wraps actual production Handler for observation only; unwrapped production ASI exercised in other roots")
		})
	}
}

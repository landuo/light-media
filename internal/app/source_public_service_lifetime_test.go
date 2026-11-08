package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"light-media/internal/plugins"
	"light-media/internal/plugins/schedule"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

func publicOwnedData(t *testing.T, a *App, id string) map[string]json.RawMessage {
	t.Helper()
	raw, e := a.pluginStore.originalBaseGetData(context.Background(), id, nil)
	if e != nil {
		t.Fatal(e)
	}
	var rows []struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	if json.Unmarshal(raw, &rows) != nil {
		t.Fatal("durable rows invalid")
	}
	out := map[string]json.RawMessage{}
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out
}
func publicProgressStatus(a *App, id string) string {
	a.pluginOriginalDirectJobs.mu.Lock()
	defer a.pluginOriginalDirectJobs.mu.Unlock()
	out := ""
	if p := a.pluginOriginalDirectJobs.progress[id+"_owned"]; p != nil {
		json.Unmarshal(p.data["status"], &out)
	}
	return out
}
func TestSourcePublicServiceChildLifetimeActualSDK(t *testing.T) {
	for _, mode := range []string{"individual", "global-after-root-return", "hook-failure", "ordinary-submit-default", "accepted-before-start", "clone-isolation", "reload", "source-uninstall", "clone-uninstall"} {
		t.Run(mode, func(t *testing.T) {
			entered, release, canceled := make(chan struct{}, 1), make(chan struct{}), make(chan struct{}, 1)
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entered <- struct{}{}
				select {
				case <-release:
					io.WriteString(w, "owned")
				case <-r.Context().Done():
					canceled <- struct{}{}
				}
			}))
			defer func() { unblock(); peer.Close() }()
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			retainedStoreSaveConfig(t, a, "OwnedPublicService", map[string]any{"stop_error": mode == "hook-failure"})
			m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("SOURCE_PUBLIC_FIXTURE"))
			p := originalBaseSDKLoaded(t, a, m.PluginID)
			id := m.PluginID
			if mode == "clone-isolation" || mode == "clone-uninstall" {
				r := callTest(t, a.Handler(), "POST", "/api/plugins/"+id+"/clone", `{"suffix":"owned"}`, cookie, "")
				assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
				p = originalBaseSDKLoaded(t, a, id+"owned")
				id += "owned"
			}
			baselineStop := append(json.RawMessage(nil), publicOwnedData(t, a, id)["hook_stop"]...)
			if mode == "clone-uninstall" {
				baselineStop = nil
			}
			input, _ := json.Marshal(map[string]string{"url": peer.URL})
			def := pluginScheduleDefinition{ID: "owned", Name: "Owned", Handler: "root", Input: input, Async: true, Trigger: schedule.Spec{Kind: "interval", IntervalMS: 3600000, Timezone: "Asia/Shanghai"}}
			if e := a.addOriginalPluginJob(context.Background(), p.installation, def); e != nil {
				t.Fatal(e)
			}
			if mode == "accepted-before-start" {
				// Instrumented existing admission seam: accepted lease before the actual
				// SDK invocation starts. No product hook or second executor is introduced.
				st := &a.pluginOriginalDirectJobs
				st.mu.Lock()
				job := st.jobs[originalServiceKey{id, "owned"}]
				lease, _, e := p.reserveOriginalPublicService()
				if e != nil {
					st.mu.Unlock()
					t.Fatal(e)
				}
				a.prepareOriginalServiceLocked(job)
				st.mu.Unlock()
				if e = a.stopOriginalPlugin(context.Background(), id); e != nil {
					t.Fatal(e)
				}
				ctx := context.WithValue(context.Background(), originalSubmittedObjectKey{}, lease)
				// Platform getter reentry on the exact retained root uses its captured
				// Client too; this is an instrumented host scope, not an ASI branch.
				if enabled, e := p.getState(ctx); e != nil || !enabled {
					t.Fatal("retained source getter did not use captured client", enabled, e)
				}
				a.executeOriginalService(ctx, job)
				p.releaseOriginalSubmission(lease)
				a.wg.Done()
			} else if mode == "ordinary-submit-default" {
				if _, e := p.callOriginalMethod(context.Background(), "root", input); e != nil {
					t.Fatal(e)
				}
			} else {
				if e := a.runOriginalPublicService(context.Background(), id, "owned"); e != nil {
					t.Fatal(e)
				}
			}
			select {
			case <-entered:
			case <-time.After(7 * time.Second):
				t.Fatal("actual child HTTP never entered")
			}
			if mode != "ordinary-submit-default" {
				privateAwait(t, "public root returned before its child", func() bool { return publicProgressStatus(a, id) == "success" })
			}
			if mode == "global-after-root-return" {
				drained := make(chan struct{})
				go func() { a.drainOriginalPluginScheduler(); close(drained) }()
				select {
				case <-drained:
					t.Fatal("global drain missed real child after root returned")
				case <-time.After(200 * time.Millisecond):
				}
				unblock()
				select {
				case <-drained:
				case <-time.After(10 * time.Second):
					t.Fatal("global source child/grandchild never drained")
				}
			} else if mode != "accepted-before-start" {
				stopped := make(chan error, 1)
				go func() {
					if mode == "reload" {
						_, e := a.reloadOriginalPlugin(context.Background(), id)
						stopped <- e
					} else if mode == "source-uninstall" || mode == "clone-uninstall" {
						r := callTest(t, a.Handler(), "DELETE", "/api/plugins/"+id, "", cookie, "")
						var result struct {
							Data struct {
								Success     bool `json:"success"`
								Uninstalled bool `json:"uninstalled"`
							} `json:"data"`
						}
						in, e := a.pluginStore.Installation(context.Background(), id)
						if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &result) != nil || !result.Data.Success || !result.Data.Uninstalled || e != nil || in.Installed {
							stopped <- fmt.Errorf("uninstall business failed: %d %s installed=%v err=%v", r.Code, r.Body.String(), in.Installed, e)
						} else {
							stopped <- nil
						}
					} else {
						stopped <- a.stopOriginalPlugin(context.Background(), id)
					}
				}()
				select {
				case e := <-stopped:
					if e != nil {
						t.Fatal(e)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("individual manager stop joined held public child")
				}
			}
			if mode == "ordinary-submit-default" {
				privateAwait(t, "ordinary child cancellation", func() bool {
					select {
					case <-canceled:
						return true
					default:
						return false
					}
				})
				unblock()
				a.wg.Wait()
				if _, ok := publicOwnedData(t, a, id)["child_done"]; ok {
					t.Fatal("ordinary Submit acquired source public retention")
				}
				return
			}
			select {
			case <-canceled:
				t.Fatal("retained child canceled")
			default:
			}
			unblock()
			privateAwait(t, "real grandchild and child durable writes", func() bool {
				d := publicOwnedData(t, a, id)
				return d["child_done"] != nil && d["grandchild_done"] != nil
			})
			d := publicOwnedData(t, a, id)
			var child struct {
				PID    int  `json:"pid"`
				Denied bool `json:"progress_denied"`
			}
			if json.Unmarshal(d["child_done"], &child) != nil || !child.Denied {
				t.Fatal("child borrowed root progress", string(d["child_done"]))
			}
			if mode != "global-after-root-return" {
				if d["hook_close"] == nil || d["hook_stop_service"] == nil || !bytes.Equal(d["hook_stop"], baselineStop) {
					t.Fatal("source stop hooks missing/duplicated by final retire", d)
				}
				for _, key := range []string{"hook_close", "hook_stop_service"} {
					var hook struct {
						PID int `json:"pid"`
					}
					if json.Unmarshal(d[key], &hook) != nil || hook.PID != child.PID {
						t.Fatal("stop hook did not run on captured actual source client", key, string(d[key]), child.PID)
					}
				}
				privateAwait(t, "captured old process physically reaped", func() bool { return syscall.Kill(child.PID, 0) == syscall.ESRCH })
				if current, ok := a.pluginRunningInstances.Load(id); ok && current == p {
					t.Fatal("old object remained in manager")
				}
			}
			if mode == "clone-isolation" || mode == "clone-uninstall" {
				source := originalBaseSDKLoaded(t, a, m.PluginID)
				if source.stopping.Load() {
					t.Fatal("clone stop stopped source")
				}
				if _, ok := publicOwnedData(t, a, m.PluginID)["child_done"]; ok {
					t.Fatal("clone wrote source namespace")
				}
			}
		})
	}
}
func TestSourcePublicServiceAdmissionCapabilityBoundaries(t *testing.T) {
	a := newTestApp(t)
	p := &originalPluginInstance{app: a, manifest: plugins.Manifest{ManagedPublicServices: true, Capabilities: []string{plugins.CleanupSubmitCapability}}}
	if pluginErrorCode(p.validateOriginalPublicServiceClient()) != "PLUGIN_SERVICE_CLEANUP_UNSUPPORTED" {
		t.Fatal("cleanup combination admitted")
	}
	p = &originalPluginInstance{app: a}
	if pluginErrorCode(p.validateOriginalPublicServiceClient()) != "PLUGIN_SERVICE_STOP_UNSUPPORTED" {
		t.Fatal("missing/legacy/serial negotiated client admitted")
	}
}

func TestSourcePublicServiceActualOldAndSerialWorkerRefused(t *testing.T) {
	for _, mode := range []string{"LEGACY", "SERIAL"} {
		t.Run(mode, func(t *testing.T) {
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("SOURCE_PUBLIC_"+mode))
			if _, loaded := a.pluginRunningInstances.Load(m.PluginID); loaded {
				t.Fatal("worker lacking actual opt-in feature reached published source object")
			}
			a.pluginOriginalDirectJobs.mu.Lock()
			registered := len(a.pluginOriginalDirectJobs.jobs)
			a.pluginOriginalDirectJobs.mu.Unlock()
			if registered != 0 {
				t.Fatal("unsupported source callback silently registered", registered)
			}
			if d := publicOwnedData(t, a, m.PluginID); d["child_done"] != nil || d["init_ran"] != nil {
				t.Fatal("unsupported worker reached business invocation", d)
			}
		})
	}
}

// Authentication negatives use explicit host-scope bridges and two real
// negotiated Clients; they are not presented as whole HTTP API operations.
func TestSourcePublicServiceActualClientScopeBoundaries(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("SOURCE_PUBLIC_FIXTURE"))
	p := originalBaseSDKLoaded(t, a, m.PluginID)
	lease, ctx, e := p.reserveOriginalPublicService()
	if e != nil {
		t.Fatal(e)
	}
	returned := false
	defer func() {
		if !returned {
			p.releaseOriginalSubmission(lease)
			a.wg.Done()
		}
		p.stopping.Store(false)
	}()
	p.stopping.Store(true)
	if !p.originalPublicServiceScopeAllowed(ctx) {
		t.Fatal("actual captured source scope missing")
	}
	enabled, e := p.getState(ctx)
	if e != nil || !enabled {
		t.Fatal("live source getter did not use captured Client", enabled, e)
	}
	if p.originalPublicServiceScopeAllowed(context.Background()) {
		t.Fatal("ordinary scope acquired source retention")
	}
	foreign := &originalPluginInstance{app: a, manifest: p.manifest, installation: p.installation}
	if foreign.originalPublicServiceScopeAllowed(ctx) {
		t.Fatal("foreign object acquired accepted source lease")
	}
	cb, revoke := p.originalBaseSessionHost(ctx, "root", nil)
	defer revoke()
	identity := plugins.Identity{PluginID: p.installation.PluginID, InstanceID: p.installation.InstanceID, Version: p.installation.Version}
	wrong := identity
	wrong.InstanceID += "foreign"
	if _, e = cb(context.Background(), wrong, plugins.BaseGetConfig, json.RawMessage(`{}`)); pluginErrorCode(e) != "PLUGIN_IDENTITY_MISMATCH" {
		t.Fatal("wrong transport identity acquired old source", e)
	}
	second, e := (plugins.Runner{Timeout: 3 * time.Second, ConcurrentCalls: true}).Start(context.Background(), plugins.Command{Path: os.Getenv("SOURCE_PUBLIC_FIXTURE")}, m, plugins.SessionBinding{Identity: identity, Generation: p.installation.Generation})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := second.CloseTransport(ctx); e != nil {
			t.Error(e)
		}
	}()
	p.sessionMu.Lock()
	first := p.session
	p.session = second
	p.sessionMu.Unlock()
	denied := !p.originalPublicServiceScopeAllowed(ctx)
	p.sessionMu.Lock()
	p.session = first
	p.sessionMu.Unlock()
	if !denied {
		t.Fatal("replacement actual Client borrowed old accepted source lease")
	}
	p.releaseOriginalSubmission(lease)
	a.wg.Done()
	returned = true
	if p.originalPublicServiceScopeAllowed(ctx) {
		t.Fatal("returned source lease regained lifetime")
	}
	if _, e = cb(context.Background(), identity, plugins.BaseGetConfig, json.RawMessage(`{}`)); e == nil {
		t.Fatal("returned source scope bypassed stopped object")
	}
}

package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins/schedule"
	"net/http/httptest"
	"os"
	"syscall"
	"testing"
	"time"
)

func ordinaryData(t *testing.T, a *App, id, key string) json.RawMessage {
	t.Helper()
	raw, e := a.pluginStore.originalBaseGetData(context.Background(), id, &key)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func TestOrdinaryBoundSourceProgressDoesNotCrossEntry(t *testing.T) {
	if os.Getenv("OWNED_ORDINARY_ENTRY") == "" {
		t.Skip("opt-in actual SDK fixture: OWNED_ORDINARY_ENTRY required")
	}
	peer := ordinaryEntryServer(t)
	defer peer.unblock()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	retainedStoreSaveConfig(t, a, "OwnedOrdinaryEntry", map[string]string{"url": peer.server.URL})
	m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("OWNED_ORDINARY_ENTRY"))
	p := originalBaseSDKLoaded(t, a, m.PluginID)
	if e := a.addOriginalPluginJob(context.Background(), p.installation, pluginScheduleDefinition{ID: "progress", Name: "Owned progress", Handler: "work", Input: json.RawMessage(`{}`), Trigger: schedule.Spec{Kind: "interval", IntervalMS: 3600000, Timezone: "Asia/Shanghai"}}); e != nil {
		t.Fatal(e)
	}
	if e := a.runOriginalPublicService(context.Background(), m.PluginID, "progress"); e != nil {
		t.Fatal(e)
	}
	select {
	case <-peer.entered:
	case <-time.After(6 * time.Second):
		t.Fatal("real service not running")
	}
	privateAwait(t, "real registered same-method event finished", func() bool { return string(ordinaryData(t, a, m.PluginID, "progress_denied")) != "null" })
	if string(ordinaryData(t, a, m.PluginID, "progress_denied")) != "true" {
		t.Fatal("registered event borrowed sender's running scheduler progress")
	}
	a.pluginOriginalDirectJobs.mu.Lock()
	text := a.pluginOriginalDirectJobs.progress[m.PluginID+"_progress"].text
	a.pluginOriginalDirectJobs.mu.Unlock()
	if text == "event must not own scheduler progress" {
		t.Fatal("event changed root progress")
	}
	peer.unblock()
	ordinaryEntryAwait(t, a)
}
func TestOrdinaryBoundAPILateMechanicalSubmitAndFailure(t *testing.T) {
	if os.Getenv("OWNED_ORDINARY_ENTRY") == "" {
		t.Skip("opt-in actual SDK fixture: OWNED_ORDINARY_ENTRY required")
	}
	for _, mode := range []string{"global-late-child", "individual-invalid-response", "event-error"} {
		t.Run(mode, func(t *testing.T) {
			peer := ordinaryEntryServer(t)
			defer peer.unblock()
			a := newTestApp(t)
			cookie := setupTestAdmin(t, a)
			settings := a.Settings()
			settings.APIToken = "owned-entry-synthetic-key"
			ownedDownloaderSave(t, a, settings)
			retainedStoreSaveConfig(t, a, "OwnedOrdinaryEntry", map[string]any{"url": peer.server.URL, "invalid": mode == "individual-invalid-response"})
			m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("OWNED_ORDINARY_ENTRY"))
			p := originalBaseSDKLoaded(t, a, m.PluginID)
			done := make(chan *httptest.ResponseRecorder, 1)
			if mode == "event-error" {
				a.broadcastOriginalPluginEvent(context.Background(), "owned.failure", json.RawMessage(`{}`))
			} else {
				go func() {
					done <- callTest(t, a.Handler(), "GET", "/api/v1/plugin/"+m.PluginID+"/owned?apikey="+settings.APIToken, "", nil, "")
				}()
			}
			select {
			case <-peer.entered:
			case <-time.After(6 * time.Second):
				t.Fatal("entry did not reach actual HTTP")
			}
			var closed chan error
			if mode == "global-late-child" {
				closed = make(chan error, 1)
				go func() { closed <- a.Close() }()
				privateAwait(t, "global BG admission sealed while API body active", func() bool {
					a.pluginOriginalBackground.mu.Lock()
					defer a.pluginOriginalBackground.mu.Unlock()
					return a.pluginOriginalBackground.closed
				})
				select {
				case e := <-closed:
					t.Fatal("global close failed to retain actual API root", e)
				default:
				}
			} else {
				if e := a.stopOriginalPlugin(context.Background(), m.PluginID); e != nil {
					t.Fatal(e)
				}
			}
			peer.unblock()
			if mode == "event-error" {
				ordinaryEntryAwait(t, a)
				p.sessionMu.Lock()
				client := p.session
				p.sessionMu.Unlock()
				if client != nil {
					t.Fatal("failed event did not retire old Client")
				}
				return
			}
			select {
			case r := <-done:
				if mode == "individual-invalid-response" {
					if r.Code != 502 {
						t.Fatal("invalid accepted response bypassed validation", r.Code, r.Body.String())
					}
				} else {
					var body struct {
						Success bool `json:"success"`
					}
					if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &body) != nil || !body.Success {
						t.Fatal("actual live API child was denied after global root seal", r.Code, r.Body.String())
					}
				}
			case <-time.After(8 * time.Second):
				t.Fatal("actual API entry never returned")
			}
			view := a
			if closed != nil {
				select {
				case e := <-closed:
					if e != nil {
						t.Fatal(e)
					}
				case <-time.After(8 * time.Second):
					t.Fatal("API descendant did not finish before Go Store.Close")
				}
				var e error
				view, e = New(Options{DataDir: a.Dir})
				if e != nil {
					t.Fatal(e)
				}
				defer view.Close()
			} else {
				ordinaryEntryAwait(t, a)
			}
			var child struct {
				PID int `json:"pid"`
			}
			raw := ordinaryData(t, view, m.PluginID, "mechanical_child")
			if json.Unmarshal(raw, &child) != nil || child.PID <= 0 {
				t.Fatal("actual API mechanical child side effect missing", string(raw))
			}
			privateAwait(t, "API captured old process physically reaped", func() bool { return syscall.Kill(child.PID, 0) == syscall.ESRCH })
		})
	}
}

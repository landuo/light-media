package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Original PM stops A before B; A is disabled while its close sends an event.
// Real source priority-queue/ThreadHelper execution admits B during that close.
func TestIndependentEventAdmissionDuringManagerStop(t *testing.T) {
	if os.Getenv("INDEPENDENT_EVENT_STOP_A") == "" || os.Getenv("INDEPENDENT_EVENT_STOP_B") == "" {
		t.Skip("opt-in actual SDK fixture: INDEPENDENT_EVENT_STOP_A, INDEPENDENT_EVENT_STOP_B required")
	}
	aClose, bInitial, bDuring := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseA, releaseB := make(chan struct{}), make(chan struct{})
	var aOnce, bOnce, initialOnce, duringOnce, closeOnce sync.Once
	freeA := func() { aOnce.Do(func() { close(releaseA) }) }
	freeB := func() { bOnce.Do(func() { close(releaseB) }) }
	var mu sync.Mutex
	trace := []string{}
	aUnexpected := 0
	sendAccepted := false
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		trace = append(trace, r.URL.Path)
		if r.URL.Path == "/OwnedEventStopA/during_A_close" {
			aUnexpected++
		}
		if r.URL.Path == "/OwnedEventStopA/close" {
			sendAccepted = r.URL.Query().Get("send_ok") == "true"
		}
		mu.Unlock()
		switch r.URL.Path {
		case "/OwnedEventStopB/preexisting":
			initialOnce.Do(func() { close(bInitial) })
			select {
			case <-releaseB:
			case <-r.Context().Done():
				return
			}
		case "/OwnedEventStopA/close":
			closeOnce.Do(func() { close(aClose) })
			select {
			case <-releaseA:
			case <-r.Context().Done():
				return
			}
		case "/OwnedEventStopB/during_A_close":
			duringOnce.Do(func() { close(bDuring) })
		}
		w.WriteHeader(200)
	}))
	defer peer.Close()
	defer freeA()
	defer freeB()
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	for _, x := range []struct{ id, env string }{{"OwnedEventStopA", "INDEPENDENT_EVENT_STOP_A"}, {"OwnedEventStopB", "INDEPENDENT_EVENT_STOP_B"}} {
		retainedStoreSaveConfig(t, a, x.id, map[string]any{"owned_trace_endpoint": peer.URL})
		m := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv(x.env))
		if m.PluginID != x.id {
			t.Fatal("actual SDK identity mismatch", m.PluginID, x.id)
		}
	}
	a.wg.Wait()
	objects := a.originalLoadedPluginSnapshot()
	if len(objects) != 2 || objects[0].installation.InstanceID != "OwnedEventStopA" || objects[1].installation.InstanceID != "OwnedEventStopB" {
		t.Fatal("actual load order differs from source A then B")
	}
	aObject, bObject := objects[0], objects[1]
	event, err := originalNewEvent("owned.stop.broadcast", json.RawMessage(`{"phase":"preexisting"}`), nil)
	if err != nil || !a.enqueueOriginalBroadcast(context.Background(), event) {
		t.Fatal("preexisting real event rejected", err)
	}
	select {
	case <-bInitial:
	case <-time.After(5 * time.Second):
		t.Fatal("initial real registered B event did not enter")
	}
	// The existing event keeps the original event pool live at global PM stage;
	// it is not the new root whose premature admission seal is under test.
	stopped := make(chan error, 1)
	go func() { stopped <- a.Close() }()
	select {
	case <-aClose:
	case e := <-stopped:
		t.Fatal("global close returned before A.close", e)
	case <-time.After(6 * time.Second):
		t.Fatal("real A.close did not reach held owned HTTP")
	}
	if !aObject.stopping.Load() || bObject.stopping.Load() {
		t.Error("stop did not reach source A-disabled/B-enabled stage")
	}
	entered := false
	select {
	case <-bDuring:
		entered = true
	case <-time.After(300 * time.Millisecond):
	}
	mu.Lock()
	send := sendAccepted
	unexpected := aUnexpected
	mu.Unlock()
	freeA()
	freeB()
	select {
	case e := <-stopped:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(14 * time.Second):
		t.Fatal("global close failed to finish after all gates released")
	}
	if !send {
		t.Error("real stop-scope Event.Send was rejected before queue admission")
	}
	if !entered {
		t.Error("ordinary B broadcast root was rejected during A.close although source B is still enabled")
	}
	if unexpected != 0 {
		t.Error("disabled A received its own stop event", unexpected)
	}
	fresh, e := New(Options{DataDir: a.Dir})
	if e != nil {
		t.Fatal(e)
	}
	defer fresh.Close()
	key := "event_during_A_close"
	raw, e := fresh.pluginStore.originalBaseGetData(context.Background(), "OwnedEventStopB", &key)
	if e != nil || !strings.Contains(string(raw), `"phase":"during_A_close"`) {
		t.Error("new B source callback lost actual durable result", e, string(raw))
	}
	mu.Lock()
	t.Logf("actualTrace=%v stopSendAccepted=%v BEnteredDuringAclose=%v disabledAEvents=%d", trace, send, entered, unexpected)
	mu.Unlock()
}

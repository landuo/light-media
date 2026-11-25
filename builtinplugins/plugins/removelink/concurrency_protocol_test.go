package removelink

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	plugins "light-media/builtinplugins/internal/sdk"
)

func concurrencyEventually(t *testing.T, f *protocolFixture, check func() bool) {
	t.Helper()
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if check() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("controlled independent callback did not complete")
		case <-tick.C:
		}
	}
}
func concurrencyHasEvent(f *protocolFixture, path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.trace {
		if r["op"] == "event" && r["path"] == path {
			return true
		}
	}
	return false
}
func concurrencyInject(f *protocolFixture, root, kind, path string, at int64, directory bool) {
	f.call("test_observer_event", map[string]any{"Root": root, "Kind": kind, "Path": path, "At": at, "Directory": directory})
}
func TestActualWorkerIndependentTimerAndObserverCallers(t *testing.T) {
	f := startProtocol(t)
	root := filepath.Join(f.root, "monitor")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	a, b, c := filepath.Join(root, "a.mkv"), filepath.Join(root, "b.mkv"), filepath.Join(root, "new.mkv")
	if err := os.WriteFile(a, []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, b); err != nil {
		t.Fatal(err)
	}
	f.call("init", map[string]any{"enabled": true, "monitor_dirs": root, "delayed_deletion": true, "delay_seconds": 30, "delete_scrap_infos": true, "delete_torrents": true, "delete_history": true})
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	base := int64(1760000000)
	concurrencyInject(f, root, "deleted", a, base+100, false)
	concurrencyEventually(t, f, func() bool { return len(f.snapshot().Queue) == 1 })
	f.mu.Lock()
	f.block = plugins.StorageExistsCapability
	f.blockPath = a
	f.mu.Unlock()
	f.call("test_clock", map[string]any{"At": base + 132})
	select {
	case <-f.entered:
	case <-time.After(4 * time.Second):
		t.Fatal("timer never reached task.exists barrier")
	}
	if err := os.Link(b, c); err != nil {
		t.Fatal(err)
	}
	concurrencyInject(f, root, "created", c, base+132, false)
	concurrencyEventually(t, f, func() bool {
		for _, v := range f.snapshot().State {
			if v.Path == c {
				return true
			}
		}
		return false
	})
	f.mu.Lock()
	timerPhase, observerPhase := 0, 0
	for _, call := range f.calls {
		if call.Method == plugins.StorageExistsCapability && call.Path == a {
			timerPhase = call.Phase
		}
		if call.Method == plugins.StorageExistsCapability && call.Path == c {
			observerPhase = call.Phase
		}
	}
	f.mu.Unlock()
	if timerPhase == 0 || observerPhase == 0 || timerPhase == observerPhase {
		t.Fatal("timer and observer borrowed one caller", timerPhase, observerPhase)
	}
	f.mu.Lock()
	resumeIndex := len(f.calls)
	f.mu.Unlock()
	close(f.release)
	concurrencyEventually(t, f, func() bool { return len(f.snapshot().Queue) == 0 })
	for _, p := range []string{b, c} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal("source recent-rehardlink protection lost", p, err)
		}
	}
	f.mu.Lock()
	resumed := false
	for _, call := range f.calls[resumeIndex:] {
		if call.Phase == timerPhase && call.Path == root && call.Method == plugins.StorageExistsCapability {
			resumed = true
		}
	}
	f.mu.Unlock()
	if !resumed {
		t.Fatal("timer did not resume with its own effect pair after observer returned")
	}
}
func TestActualWorkerObserverLanesSerialPerRootAndIndependentAcrossRoots(t *testing.T) {
	f := startProtocol(t)
	one, two := filepath.Join(f.root, "one"), filepath.Join(f.root, "two")
	for _, p := range []string{one, two} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.call("init", map[string]any{"enabled": true, "monitor_dirs": one + "\n" + two, "delete_torrents": true, "delayed_deletion": false})
	first, second, other := filepath.Join(one, "first"), filepath.Join(one, "second"), filepath.Join(two, "other")
	f.mu.Lock()
	f.block = plugins.EventSendCapability
	f.blockPath = first
	f.mu.Unlock()
	concurrencyInject(f, one, "deleted", first, 1760000001, true)
	select {
	case <-f.entered:
	case <-time.After(4 * time.Second):
		t.Fatal("first observer barrier absent")
	}
	concurrencyInject(f, one, "deleted", second, 1760000001, true)
	concurrencyInject(f, two, "deleted", other, 1760000001, true)
	concurrencyEventually(t, f, func() bool { return concurrencyHasEvent(f, other) })
	if concurrencyHasEvent(f, second) {
		t.Fatal("same observer executed concurrently with its preceding callback")
	}
	close(f.release)
	concurrencyEventually(t, f, func() bool { return concurrencyHasEvent(f, second) })
}
func TestActualWorkerObserverStartsBeforeInitialBulkScanCompletes(t *testing.T) {
	f := startProtocol(t)
	root := filepath.Join(f.root, "monitor")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.block = plugins.LogWriteCapability
	f.blockPath = root + " 的硬链接监控服务启动"
	f.mu.Unlock()
	done := make(chan error, 1)
	raw, _ := json.Marshal(map[string]any{"enabled": true, "monitor_dirs": root, "delete_torrents": true})
	go func() {
		_, err := f.client.CallWithOptions(f.ctx, "init", raw, f.phase("init"), plugins.CallOptions{CallerDeadlineOnly: true})
		done <- err
	}()
	select {
	case <-f.entered:
	case <-time.After(4 * time.Second):
		t.Fatal("post-observer/pre-bulk barrier absent")
	}
	path := filepath.Join(root, "directory")
	concurrencyInject(f, root, "deleted", path, 1760000001, true)
	concurrencyEventually(t, f, func() bool { return concurrencyHasEvent(f, path) })
	select {
	case err := <-done:
		t.Fatal("initializer completed before controlled release", err)
	default:
	}
	close(f.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
func TestActualWorkerStopWaitsForIndependentObserverAndTimerWork(t *testing.T) {
	f := startProtocol(t)
	root := filepath.Join(f.root, "monitor")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	f.call("init", map[string]any{"enabled": true, "monitor_dirs": root, "delete_torrents": true})
	path := filepath.Join(root, "blocked")
	f.mu.Lock()
	f.block = plugins.EventSendCapability
	f.blockPath = path
	f.mu.Unlock()
	concurrencyInject(f, root, "deleted", path, 1760000001, true)
	select {
	case <-f.entered:
	case <-time.After(4 * time.Second):
		t.Fatal("callback barrier absent")
	}
	stop := make(chan error, 1)
	go func() {
		_, err := f.client.CallWithOptions(context.Background(), "stop_service", json.RawMessage(`null`), f.phase("stop_service"), plugins.CallOptions{CallerDeadlineOnly: true})
		stop <- err
	}()
	select {
	case err := <-stop:
		t.Fatal("stop returned before the physical callback completed", err)
	case <-time.After(80 * time.Millisecond):
	}
	close(f.release)
	select {
	case err := <-stop:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("stop did not join callbacks")
	}
}

type concurrencySnapshot struct {
	State []Tracked
	Queue []Task
}

func (f *protocolFixture) snapshot() concurrencySnapshot {
	out := f.call("test_snapshot", nil)
	var value concurrencySnapshot
	if err := json.Unmarshal(out.Result, &value); err != nil {
		f.t.Fatal(err)
	}
	return value
}

func TestActualWorkerTypedSourceAttributeStopsOnlyItsObserver(t *testing.T) {
	f := startProtocol(t)
	one, two := filepath.Join(f.root, "one"), filepath.Join(f.root, "two")
	for _, p := range []string{one, two} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.call("init", map[string]any{"enabled": true, "monitor_dirs": one + "\n" + two, "exclude_keywords": 1, "delete_torrents": true})
	concurrencyInject(f, one, "created", filepath.Join(one, "bad.mkv"), 1760000001, false)
	concurrencyEventually(t, f, func() bool {
		out := f.call("test_snapshot", nil)
		var s struct {
			Closed bool `json:"watch_closed"`
			Lanes  []struct {
				Path   string
				Closed bool
			} `json:"watch_lanes"`
		}
		if json.Unmarshal(out.Result, &s) != nil {
			t.Fatal("snapshot")
		}
		return !s.Closed && len(s.Lanes) == 2 && s.Lanes[0].Closed && !s.Lanes[1].Closed
	})
	path := filepath.Join(two, "directory")
	concurrencyInject(f, two, "deleted", path, 1760000002, true)
	concurrencyEventually(t, f, func() bool { return concurrencyHasEvent(f, path) })
}
func TestActualWorkerStopJoinsTimerBeforeQueueFlush(t *testing.T) {
	f := startProtocol(t)
	root := filepath.Join(f.root, "monitor")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join(root, "a.mkv"), filepath.Join(root, "b.mkv")
	if err := os.WriteFile(a, []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, b); err != nil {
		t.Fatal(err)
	}
	f.call("init", map[string]any{"enabled": true, "monitor_dirs": root, "delayed_deletion": true, "delay_seconds": 30, "delete_scrap_infos": false, "delete_torrents": true, "delete_history": true})
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	concurrencyInject(f, root, "deleted", a, 1760000100, false)
	concurrencyEventually(t, f, func() bool { return len(f.snapshot().Queue) == 1 })
	f.mu.Lock()
	f.block = plugins.StorageExistsCapability
	f.blockPath = a
	f.mu.Unlock()
	f.call("test_clock", map[string]any{"At": 1760000132})
	select {
	case <-f.entered:
	case <-time.After(4 * time.Second):
		t.Fatal("timer did not reach controlled exists")
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.client.CallWithOptions(f.ctx, "stop_service", json.RawMessage(`null`), f.phase("stop_service"), plugins.CallOptions{CallerDeadlineOnly: true})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatal("stop did not physically join the old timer", err)
	case <-time.After(80 * time.Millisecond):
	}
	close(f.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("timer stop did not complete")
	}
	f.mu.Lock()
	events, histories := 0, 0
	for _, r := range f.trace {
		if r["op"] == "event" && r["path"] == a {
			events++
		}
	}
	for _, r := range f.historyReads {
		if r.Source == a {
			histories++
		}
	}
	f.mu.Unlock()
	if events != 1 || histories != 1 {
		t.Fatal("stop raced queue flush with active source task", events, histories)
	}
	if _, err := os.Stat(b); !os.IsNotExist(err) {
		t.Fatal("timer's confirmed final unlink was not awaited", err)
	}
}

func TestActualWorkerTenObserverRootsFairWhileSixCallbacksBlocked(t *testing.T) {
	f := startProtocol(t)
	roots := make([]string, 10)
	for i := range roots {
		roots[i] = filepath.Join(f.root, fmt.Sprintf("root-%02d", i+1))
		if err := os.Mkdir(roots[i], 0700); err != nil {
			t.Fatal(err)
		}
	}
	a, b := filepath.Join(roots[9], "timer-a.mkv"), filepath.Join(roots[9], "timer-b.mkv")
	if err := os.WriteFile(a, []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, b); err != nil {
		t.Fatal(err)
	}
	f.call("init", map[string]any{"enabled": true, "monitor_dirs": strings.Join(roots, "\n"), "delayed_deletion": true, "delay_seconds": 30, "delete_torrents": true})
	f.mu.Lock()
	for _, phase := range f.phases {
		if phase.Method == "private_observer" || phase.Method == "private_timer" {
			f.t.Fatal("idle native roots retained effect callers", phase.Method)
		}
	}
	f.mu.Unlock()
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	concurrencyInject(f, roots[9], "deleted", a, 1760000100, false)
	concurrencyEventually(t, f, func() bool { return len(f.snapshot().Queue) == 1 })

	blocked := make([]*protocolGate, 7)
	paths := make([]string, 7)
	f.mu.Lock()
	f.gates = map[string]*protocolGate{}
	for i := range blocked {
		root := roots[i%6]
		if i == 6 {
			root = roots[5] // A second event in the sixth, continuously busy lane.
		}
		paths[i] = filepath.Join(root, fmt.Sprintf("blocked-%d", i))
		blocked[i] = &protocolGate{entered: make(chan struct{}), release: make(chan struct{})}
		f.gates[plugins.EventSendCapability+"\n"+paths[i]] = blocked[i]
	}
	f.mu.Unlock()
	// Always release the owned gates, including when a fairness assertion fails.
	defer func() {
		for _, gate := range blocked {
			select {
			case <-gate.release:
			default:
				close(gate.release)
			}
		}
	}()
	for i := 0; i < 6; i++ {
		concurrencyInject(f, roots[i], "deleted", paths[i], 1760000100, true)
		select {
		case <-blocked[i].entered:
		case <-time.After(4 * time.Second):
			t.Fatalf("root %d never received its independent caller", i+1)
		}
	}
	concurrencyInject(f, roots[5], "deleted", paths[6], 1760000100, true)
	for i := 6; i < 10; i++ {
		concurrencyInject(f, roots[i], "deleted", filepath.Join(roots[i], "later-root"), 1760000100, true)
	}
	// All six observer slots are blocked. The separate timer still gets a real
	// caller and completes its queued source task before any slot is released.
	f.call("test_clock", map[string]any{"At": 1760000132})
	concurrencyEventually(t, f, func() bool { return concurrencyHasEvent(f, a) })
	for i := 6; i < 10; i++ {
		if concurrencyHasEvent(f, filepath.Join(roots[i], "later-root")) {
			t.Fatal("effect callback exceeded the active admission bound")
		}
	}
	// Exactly one slot becomes available. It must rotate to roots 7–10 rather
	// than let root 6's next blocking event starve all of those native observers.
	close(blocked[5].release)
	for i := 6; i < 10; i++ {
		path := filepath.Join(roots[i], "later-root")
		concurrencyEventually(t, f, func() bool { return concurrencyHasEvent(f, path) })
	}
	select {
	case <-blocked[6].entered:
	case <-time.After(4 * time.Second):
		t.Fatal("round-robin forgot the busy lane after serving the other roots")
	}
	f.budget()
}

func TestActualWorkerUnknownHomeKeepsSourceCatchAndObserverBoundary(t *testing.T) {
	for _, delayed := range []bool{false, true} {
		t.Run(fmt.Sprintf("delayed=%t", delayed), func(t *testing.T) {
			f := startProtocol(t)
			one, two := filepath.Join(f.root, "one"), filepath.Join(f.root, "two")
			for _, root := range []string{one, two} {
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
			}
			a, b := filepath.Join(one, "a.mkv"), filepath.Join(one, "b.mkv")
			if err := os.WriteFile(a, []byte("owned"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(a, b); err != nil {
				t.Fatal(err)
			}
			f.call("init", map[string]any{"enabled": true, "monitor_dirs": one + "\n" + two, "exclude_dirs": "~__lm_review_no_such_user_20261106__/protected", "delete_scrap_infos": true, "delete_torrents": true, "delete_history": true, "delayed_deletion": delayed, "delay_seconds": 30})
			if err := os.Remove(a); err != nil {
				t.Fatal(err)
			}
			concurrencyInject(f, one, "deleted", a, 1760000100, false)
			if delayed {
				concurrencyEventually(t, f, func() bool { return len(f.snapshot().Queue) == 1 })
				f.call("test_clock", map[string]any{"At": 1760000131})
			}
			concurrencyEventually(t, f, func() bool {
				out := f.call("test_snapshot", nil)
				var s struct {
					Closed      bool `json:"watch_closed"`
					TimerActive bool `json:"timer_active"`
					Queue       []Task
					Lanes       []struct{ Closed bool } `json:"watch_lanes"`
				}
				if err := json.Unmarshal(out.Result, &s); err != nil {
					t.Fatal(err)
				}
				return !s.Closed && !s.TimerActive && len(s.Queue) == 0 && len(s.Lanes) == 2 && s.Lanes[0].Closed == !delayed && !s.Lanes[1].Closed
			})
			if _, err := os.Stat(b); err != nil {
				t.Fatal("unknown protection path allowed unlink", err)
			}
			f.mu.Lock()
			mutations := len(f.trace) + len(f.historyReads)
			f.mu.Unlock()
			if mutations != 0 {
				t.Fatal("continued side effects after source RuntimeError", mutations)
			}
			other := filepath.Join(two, "directory")
			concurrencyInject(f, two, "deleted", other, 1760000132, true)
			concurrencyEventually(t, f, func() bool { return concurrencyHasEvent(f, other) })
			f.call("stop_service", nil)
			if _, err := os.Stat(b); err != nil {
				t.Fatal("stop replayed the caught/failed source attempt", err)
			}
		})
	}
}

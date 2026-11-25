package removelink

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func linkedMachine(t *testing.T, delayed bool) (*Machine, *fixtureOps, string, string, string, *time.Time) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "m")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	a, b, c := filepath.Join(dir, "a.mkv"), filepath.Join(dir, "b.mkv"), filepath.Join(dir, "c.mkv")
	if e := os.WriteFile(a, []byte("synthetic fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{b, c} {
		if e := os.Link(a, p); e != nil {
			t.Fatal(e)
		}
	}
	ops := &fixtureOps{root: root, trace: []map[string]any{}, failUnlink: map[string]error{}}
	m := New(ops)
	now := time.Unix(1000, 0)
	m.Now = func() time.Time { return now }
	config, _ := json.Marshal(map[string]any{"enabled": true, "monitor_dirs": dir, "delayed_deletion": delayed, "delay_seconds": 30, "delete_torrents": true, "delete_history": true, "notify": true})
	if e := m.Init(config); e != nil {
		t.Fatal(e)
	}
	return m, ops, a, b, c, &now
}
func TestRealMissingAndDeniedTrackedLinksDoNotStopOtherCleanup(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "permission"}[denied], func(t *testing.T) {
			m, ops, a, b, c, _ := linkedMachine(t, false)
			if denied {
				ops.failUnlink[b] = fs.ErrPermission
			} else if e := os.Remove(b); e != nil {
				t.Fatal(e)
			}
			if e := os.Remove(a); e != nil {
				t.Fatal(e)
			}
			if e := m.Deleted(a, "hardlink", false); e != nil {
				t.Fatal(e)
			}
			if ops.Exists(c) || ops.Exists(b) != denied {
				t.Fatal("stale/failed link stopped remaining real unlink")
			}
			_, tracked := m.find(b)
			if tracked != denied {
				t.Fatal("source missing-vs-permission state handling changed")
			}
			last := ops.trace[len(ops.trace)-1]
			if last["op"] != "notify" || !strings.Contains(last["text"].(string), c) || strings.Contains(last["text"].(string), "2 个") {
				t.Fatal("notification counted unsuccessful unlink", last)
			}
		})
	}
}
func TestTimerDoesNotResetAndStopDrainsBeforeDue(t *testing.T) {
	m, ops, a, b, _, now := linkedMachine(t, true)
	if e := os.Remove(a); e != nil {
		t.Fatal(e)
	}
	if e := m.Deleted(a, "hardlink", false); e != nil {
		t.Fatal(e)
	}
	due := *m.TimerDue
	other := filepath.Join(filepath.Dir(a), "other.mkv")
	if e := os.WriteFile(other, []byte("other"), 0600); e != nil {
		t.Fatal(e)
	}
	*now = now.Add(10 * time.Second)
	if e := m.Created(other, false); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(other); e != nil {
		t.Fatal(e)
	}
	if e := m.Deleted(other, "hardlink", false); e != nil {
		t.Fatal(e)
	}
	if len(m.Queue) != 2 || !m.TimerDue.Equal(due) {
		t.Fatal("new event reset original timer")
	}
	*now = due.Add(-time.Second)
	m.Tick()
	if len(m.Queue) != 2 || !ops.Exists(b) {
		t.Fatal("task executed before delay")
	}
	m.Stop()
	if len(m.Queue) != 0 || m.TimerDue != nil || len(m.Watches) != 0 || ops.Exists(b) {
		t.Fatal("source stop did not synchronously drain not-yet-due work")
	}
}
func TestConcurrentDuplicateEventsHaveOneDeletionOutcome(t *testing.T) {
	m, ops, a, b, c, now := linkedMachine(t, true)
	if e := os.Remove(a); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := m.Deleted(a, "hardlink", false); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if len(m.Queue) != 1 {
		t.Fatal("duplicate filesystem event created duplicate task")
	}
	*now = now.Add(31 * time.Second)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); m.Tick(); m.StateValue() }()
	}
	wg.Wait()
	if len(m.Queue) != 0 || ops.Exists(b) || ops.Exists(c) {
		t.Fatal("concurrent timer failed real cleanup")
	}
	notices := 0
	for _, x := range ops.trace {
		if x["op"] == "notify" {
			notices++
		}
	}
	if notices != 1 {
		t.Fatal("duplicate task notification", notices)
	}
}
func TestLegacyTrickplayStateAndQueueNeverDeleteOtherIdentityLinks(t *testing.T) {
	m, ops, a, b, _, now := linkedMachine(t, true)
	info, e := ops.Stat(a)
	if e != nil {
		t.Fatal(e)
	}
	info.Added = *now
	legacy := filepath.Join(filepath.Dir(a), "old.TRICKPLAY", "deleted.mkv")
	m.put(Tracked{legacy, info})
	m.Queue = append(m.Queue, &Task{Path: legacy, Identity: info, At: now.Add(-time.Hour)})
	m.Tick()
	if len(m.Queue) != 0 || !ops.Exists(a) || !ops.Exists(b) {
		t.Fatal("legacy trickplay queue removed real media links")
	}
	if _, exists := m.find(legacy); exists {
		t.Fatal("legacy trickplay state was not discarded")
	}
}

package removelink

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestActualWorkerObserverToOwnedFilesystemCleanup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("actual source chooses Linux inotify; polling hardlink alias behavior is separately checked")
	}
	f := startProtocolMode(t, true)
	a, links := f.links(75)
	f.init(false)
	if e := os.Remove(a); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, e := os.Stat(links[len(links)-1])
		if errors.Is(e, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ordinary OS deletion never reached real SDK cleanup", e)
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.call("test_snapshot", nil)
	f.mu.Lock()
	notices := 0
	for _, x := range f.trace {
		if x["op"] == "notify" {
			notices++
		}
	}
	f.mu.Unlock()
	if notices != 1 {
		t.Fatal("observer owner did not finish notification", notices)
	}
	f.call("stop_service", nil)
	f.budget()
	t.Logf("actual observer→worker→SDK→owned local files; OS=%s (Linux=inotify, other development OS=polling)", runtime.GOOS)
}
func TestActualWorkerObserverRenameAndStopBeforeDue(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("actual Linux observer branch")
	}
	f := startProtocolMode(t, true)
	a, links := f.links(70)
	f.init(true)
	dest := filepath.Join(filepath.Dir(a), "renamed.mkv")
	if e := os.Rename(a, dest); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(6 * time.Second)
	for {
		out := f.call("test_snapshot", nil)
		var snap struct {
			State []Tracked
			Queue []Task
		}
		if e := json.Unmarshal(out.Result, &snap); e != nil {
			t.Fatal(e)
		}
		found := false
		for _, x := range snap.State {
			if x.Path == dest {
				found = true
			}
		}
		if found {
			if len(snap.Queue) != 0 {
				t.Fatal("same-identity rename became deletion")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rename not observed")
		}
		time.Sleep(30 * time.Millisecond)
	}
	if e := os.Remove(dest); e != nil {
		t.Fatal(e)
	}
	deadline = time.Now().Add(6 * time.Second)
	for {
		out := f.call("test_snapshot", nil)
		var snap struct{ Queue []Task }
		_ = json.Unmarshal(out.Result, &snap)
		if len(snap.Queue) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("deletion not queued")
		}
		time.Sleep(30 * time.Millisecond)
	}
	f.call("stop_service", nil)
	for _, p := range links {
		if _, e := os.Stat(p); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("stop before due did not flush", p, e)
		}
	}
	newFile := filepath.Join(filepath.Dir(a), "after-stop.mkv")
	if e := os.WriteFile(newFile, []byte("owned after stop"), 0600); e != nil {
		t.Fatal(e)
	}
	time.Sleep(1100 * time.Millisecond)
	out := f.call("test_snapshot", nil)
	if strings.Contains(string(out.Result), "after-stop.mkv") {
		t.Fatal("observer ran after joined stop")
	}
	f.budget()
}

func TestActualWorkerObserverRealTimerDoesNotRunEarly(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("actual Linux inotify and process-local wall clock")
	}
	f := startProtocolMode(t, true)
	a, links := f.links(75)
	f.call("init", map[string]any{"enabled": true, "monitor_dirs": filepath.Dir(a), "delayed_deletion": true, "delay_seconds": 10, "notify": true})
	start := time.Now()
	if e := os.Remove(a); e != nil {
		t.Fatal(e)
	}
	time.Sleep(2 * time.Second)
	if _, e := os.Stat(links[0]); e != nil {
		t.Fatal("source delay ran early", e)
	}
	deadline := start.Add(15 * time.Second)
	for {
		if _, e := os.Stat(links[len(links)-1]); errors.Is(e, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("real source timer never executed")
		}
		time.Sleep(25 * time.Millisecond)
	}
	f.call("test_snapshot", nil)
	if elapsed := time.Since(start); elapsed < 9500*time.Millisecond {
		t.Fatal("source wait shortened", elapsed)
	} else {
		t.Log("actual owned Linux timer elapsed", elapsed)
	}
	f.budget()
}

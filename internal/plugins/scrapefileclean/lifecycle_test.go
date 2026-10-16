package scrapefileclean

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentPendingCancellationDoesNotDeleteOwnedFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "owned.mkv")
	if e := os.WriteFile(path, []byte("owned synthetic"), 0600); e != nil {
		t.Fatal(e)
	}
	ops := &fixtureOps{root: root, trace: []map[string]any{}}
	m := New(ops)
	config, _ := json.Marshal(map[string]any{"enabled": true, "monitor_dirs": root, "delayed_deletion": true})
	if e := m.Init(config); e != nil {
		t.Fatal(e)
	}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for j := 0; j < 20; j++ {
				if e := m.Created(path, false); e != nil {
					t.Error(e)
				}
				if e := m.Deleted(path, false); e != nil {
					t.Error(e)
				}
				m.GetState()
				m.Stop()
			}
		}()
	}
	group.Wait()
	if len(m.Queue) != 0 || m.TimerDue != nil || !ops.Exists(path) || len(ops.trace) != 0 {
		t.Fatal("stop executed pending side effects")
	}
}
func TestNamedHomeRequiresExplicitResolver(t *testing.T) {
	if got := normalized("~fixture/a/../b", "", func(name string) (string, error) {
		if name != "fixture" {
			t.Fatal(name)
		}
		return "/synthetic-home", nil
	}); got != "/synthetic-home/b" {
		t.Fatal(got)
	}
}

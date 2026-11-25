package removelink

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

type sourceObserverEvent struct {
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Dest      string `json:"dest,omitempty"`
}
type observerSourceSuite struct {
	WatchdogVersion string  `json:"watchdog_version"`
	BufferDelay     float64 `json:"buffer_delay"`
	Observations    []struct {
		Mode  string
		Steps []struct {
			Step   string
			Events []sourceObserverEvent
		}
	} `json:"observations"`
	Special []struct {
		Mode, Action string
		Events       []sourceObserverEvent
	} `json:"special"`
}

func observerSource(t *testing.T) observerSourceSuite {
	t.Helper()
	raw, e := os.ReadFile("reference/observer-source312.json")
	if e != nil {
		t.Fatal(e)
	}
	var suite observerSourceSuite
	if e := json.Unmarshal(raw, &suite); e != nil || suite.WatchdogVersion != "6.0.0" || suite.BufferDelay != 0.5 {
		t.Fatal("wrong actual source observer evidence", e)
	}
	return suite
}
func collectObserver(t *testing.T, w observer, root, prefix string, want []sourceObserverEvent) {
	t.Helper()
	got := []sourceObserverEvent{}
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	for len(got) < len(want) {
		select {
		case e, ok := <-w.Events():
			if !ok {
				t.Fatal("observer ended before expected events", got, want)
			}
			if e.Err != nil {
				t.Fatal(e.Err)
			}
			got = append(got, sourceObserverEvent{Kind: e.Kind, Path: strings.Replace(e.Path, root, prefix, 1), Dest: strings.Replace(e.Dest, root, prefix, 1), Directory: e.Directory})
		case <-timer.C:
			t.Fatal("observer timeout", got, want)
		}
	}
	if prefix == "" {
		for i := range got {
			got[i].Path = strings.TrimPrefix(got[i].Path, "/")
			got[i].Dest = strings.TrimPrefix(got[i].Dest, "/")
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("actual3.12 source=%+v Go=%+v", want, got)
	}
}
func TestObserverMatchesExecutedSourceMatrix(t *testing.T) {
	for _, row := range observerSource(t).Observations {
		t.Run(row.Mode, func(t *testing.T) {
			if row.Mode == "actual-source-selected" && runtime.GOOS != "linux" {
				t.Skip("actual source selects Linux inotify")
			}
			root := t.TempDir()
			factory := newPollingObserver
			if row.Mode == "actual-source-selected" {
				factory = sourceObserver
			}
			w, e := factory(Watch{root, "hardlink"})
			if e != nil {
				t.Fatal(e)
			}
			defer w.Close()
			for _, step := range row.Steps {
				switch step.Step {
				case "create":
					e = os.WriteFile(filepath.Join(root, "a.mkv"), []byte("owned"), 0600)
				case "rename-same-object":
					e = os.Rename(filepath.Join(root, "a.mkv"), filepath.Join(root, "b.mkv"))
				case "delete":
					e = os.Remove(filepath.Join(root, "b.mkv"))
				case "recursive-create":
					e = os.Mkdir(filepath.Join(root, "nested"), 0700)
					if e == nil {
						e = os.WriteFile(filepath.Join(root, "nested/child.mkv"), []byte("owned child"), 0600)
					}
				case "directory-move":
					e = os.Rename(filepath.Join(root, "nested"), filepath.Join(root, "renamed"))
				default:
					t.Fatal(step.Step)
				}
				if e != nil {
					t.Fatal(e)
				}
				collectObserver(t, w, root, "", step.Events)
			}
		})
	}
}
func TestObserverMatchesExecutedRootAndHardlinkBranches(t *testing.T) {
	for _, row := range observerSource(t).Special {
		t.Run(row.Mode+"/"+row.Action, func(t *testing.T) {
			if row.Mode == "actual-source-selected" && runtime.GOOS != "linux" {
				t.Skip("actual source selects Linux inotify")
			}
			outer := t.TempDir()
			root := filepath.Join(outer, "watch")
			if e := os.Mkdir(root, 0700); e != nil {
				t.Fatal(e)
			}
			if row.Action == "hardlink-path-delete" {
				if e := os.WriteFile(filepath.Join(root, "source.mkv"), []byte("owned"), 0600); e != nil {
					t.Fatal(e)
				}
				if e := os.Link(filepath.Join(root, "source.mkv"), filepath.Join(root, "linked.mkv")); e != nil {
					t.Fatal(e)
				}
			}
			factory := newPollingObserver
			if row.Mode == "actual-source-selected" {
				factory = sourceObserver
			}
			w, e := factory(Watch{root, "hardlink"})
			if e != nil {
				t.Fatal(e)
			}
			defer w.Close()
			switch row.Action {
			case "root-move":
				e = os.Rename(root, filepath.Join(outer, "moved"))
				if e == nil {
					time.Sleep(1200 * time.Millisecond)
					e = os.WriteFile(filepath.Join(outer, "moved/after.mkv"), []byte("owned"), 0600)
				}
			case "root-delete":
				e = os.Remove(root)
			case "hardlink-path-delete":
				e = os.Remove(filepath.Join(root, "source.mkv"))
			default:
				t.Fatal(row.Action)
			}
			if e != nil {
				t.Fatal(e)
			}
			collectObserver(t, w, outer, "/owned", row.Events)
		})
	}
}

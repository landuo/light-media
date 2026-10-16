package scrapefileclean

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type oracleCase struct {
	Name           string
	Config         json.RawMessage
	Files          map[string]string
	Links          []struct{ Path, Target string }
	Dirs           []string
	MissingHistory []string `json:"missing_history"`
	DenyUnlink     []string `json:"deny_unlink"`
	Steps          []struct {
		Op, Path, Dest, Link string
		Directory, Missing   bool
		At                   float64
		Config               json.RawMessage
	}
}

func TestActualInstalledOracle(t *testing.T) {
	data, e := os.ReadFile("../../../testdata/plugins/scrape-file-clean-1.0.7/oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var reference struct {
		Source string `json:"source_sha256"`
		Cases  []struct {
			Case   oracleCase
			Result json.RawMessage
			Order  []string `json:"scan_order"`
		}
	}
	if e = json.Unmarshal(data, &reference); e != nil {
		t.Fatal(e)
	}
	if reference.Source != "c14c36e17112605e6c856614e88ea425530be2462123126a85e1adfea009a3af" || len(reference.Cases) != 67 {
		t.Fatal("reference attestation")
	}
	for _, c := range reference.Cases {
		t.Run(c.Case.Name, func(t *testing.T) {
			got := runCase(t, c.Case)
			var want any
			json.Unmarshal(c.Result, &want)
			if !reflect.DeepEqual(got, want) {
				a, _ := json.MarshalIndent(got, "", "  ")
				b, _ := json.MarshalIndent(want, "", "  ")
				t.Fatalf("source mismatch %s\ngot: %s\nwant: %s", c.Case.Name, a, b)
			}
		})
	}
}
func runCase(t *testing.T, c oracleCase) any {
	t.Helper()
	root, tempErr := os.MkdirTemp("", "scrapefileclean-owned-")
	if tempErr != nil {
		t.Fatal(tempErr)
	}
	t.Cleanup(func() {
		if e := os.RemoveAll(root); e != nil {
			t.Error(e)
		}
	})
	path := func(p string) string { return filepath.Join(root, p) }
	must := func(e error) {
		if e != nil {
			t.Fatal(e)
		}
	}
	must(os.WriteFile(path(".owned-fixture"), []byte("owned"), 0600))
	for name, content := range c.Files {
		must(os.MkdirAll(filepath.Dir(path(name)), 0700))
		must(os.WriteFile(path(name), []byte(content), 0600))
	}
	for _, l := range c.Links {
		must(os.MkdirAll(filepath.Dir(path(l.Path)), 0700))
		must(os.Link(path(l.Target), path(l.Path)))
	}
	for _, dir := range c.Dirs {
		must(os.MkdirAll(path(dir), 0700))
	}
	ops := &fixtureOps{root: root, trace: []map[string]any{}, deny: map[string]bool{}, missingHistory: map[string]bool{}}
	for _, p := range c.DenyUnlink {
		ops.deny[path(p)] = true
	}
	for _, p := range c.MissingHistory {
		ops.missingHistory[strings.ReplaceAll(p, "@ROOT@", root)] = true
	}
	m := New(ops)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start
	m.Now = func() time.Time { return now }
	err := m.Init(json.RawMessage(strings.ReplaceAll(string(c.Config), "@ROOT@", root)))
	var sourceError any
	if err != nil {
		sourceError = err.Error()
		c.Steps = nil
	}
	for _, s := range c.Steps {
		now = start.Add(time.Duration(s.At * float64(time.Second)))
		p := path(s.Path)
		switch s.Op {
		case "delete", "external_remove":
			if info, e := os.Stat(p); e == nil {
				if info.IsDir() {
					must(os.RemoveAll(p))
				} else {
					must(os.Remove(p))
				}
			}
			if s.Op == "delete" {
				err = m.Deleted(p, s.Directory)
			}
		case "create", "create_event":
			if s.Op == "create" {
				must(os.MkdirAll(filepath.Dir(p), 0700))
				if s.Link != "" {
					must(os.Link(path(s.Link), p))
				} else {
					must(os.WriteFile(p, []byte("new"), 0600))
				}
			}
			err = m.Created(p, false)
		case "move":
			d := path(s.Dest)
			must(os.MkdirAll(filepath.Dir(d), 0700))
			must(os.Rename(p, d))
			if s.Missing {
				must(os.Remove(d))
			}
			err = m.Moved(p, d, false)
		case "tick":
			m.Tick()
		case "stop":
			m.Stop()
		case "reload":
			err = m.Init(json.RawMessage(strings.ReplaceAll(string(s.Config), "@ROOT@", root)))
		default:
			t.Fatal(s.Op)
		}
		if err != nil {
			sourceError = err.Error()
			break
		}
	}
	files, e := finalFiles(root)
	must(e)
	state := []string{}
	for _, r := range m.State {
		state = append(state, r.Path)
	}
	queue := []map[string]any{}
	for _, task := range m.Queue {
		queue = append(queue, map[string]any{"path": task.Path, "at": task.At.Sub(start).Seconds(), "known_paths": task.KnownPaths, "processed": task.Processed})
	}
	var timer any
	if m.TimerDue != nil {
		timer = m.TimerDue.Sub(start).Seconds()
	}
	result := map[string]any{"files": files, "state": state, "queue": queue, "timer_due": timer, "trace": ops.trace, "delay": m.Config.Delay, "enabled": m.GetState(), "error": sourceError, "logged_errors": len(m.Errors)}
	b, e := json.Marshal(result)
	must(e)
	b = bytes.ReplaceAll(b, []byte(root), []byte("/fixture"))
	var normalized any
	must(json.Unmarshal(b, &normalized))
	return normalized
}

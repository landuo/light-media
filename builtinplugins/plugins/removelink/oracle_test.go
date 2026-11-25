package removelink

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type oracleCase struct {
	DenyUnlink     []string                               `json:"deny_unlink"`
	StorageErrors  []string                               `json:"storage_errors"`
	MissingHistory []string                               `json:"missing_history"`
	HistoryErrors  []string                               `json:"history_failures"`
	Identities     map[string]struct{ Dev, Inode uint64 } `json:"identities"`
	Name           string                                 `json:"name"`
	Config         json.RawMessage                        `json:"config"`
	Files          map[string]string                      `json:"files"`
	Links          []struct{ Path, Target string }        `json:"links"`
	Dirs           []string                               `json:"dirs"`
	Steps          []struct {
		Op, Path, Dest, Mode, Link, Content string
		At                                  float64
		Directory, Missing                  bool
		Config                              json.RawMessage
	} `json:"steps"`
	Storage json.RawMessage `json:"storage"`
	Media   []string        `json:"media_exts"`
	Order   []string        `json:"scan_order"`
}

func TestInstalledASTOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var suite struct {
		Source string `json:"source_sha256"`
		Cases  []struct {
			Input    oracleCase      `json:"input"`
			Expected json.RawMessage `json:"expected"`
		}
	}
	if json.Unmarshal(raw, &suite) != nil || suite.Source != "22fb375e531378b3454607c7bca305b637abd60ee8ed69e14e32fbc2ddfc5485" {
		t.Fatal("unattested oracle")
	}
	for _, row := range suite.Cases {
		t.Run(row.Input.Name, func(t *testing.T) {
			got := runOracle(t, row.Input)
			var expected any
			if json.Unmarshal(row.Expected, &expected) != nil {
				t.Fatal("bad expected")
			}
			if !reflect.DeepEqual(got, expected) {
				a, _ := json.MarshalIndent(expected, "", "  ")
				b, _ := json.MarshalIndent(got, "", "  ")
				t.Fatalf("source mismatch\nexpected %s\ngot %s", a, b)
			}
		})
	}
}
func runOracle(t *testing.T, c oracleCase) any {
	t.Helper()
	root := t.TempDir()
	must := func(e error) {
		if e != nil {
			t.Fatal(e)
		}
	}
	must(os.WriteFile(filepath.Join(root, ".fixture-owner"), []byte("owned"), 0600))
	p := func(s string) string { return filepath.Join(root, s) }
	for name, content := range c.Files {
		must(os.MkdirAll(filepath.Dir(p(name)), 0700))
		must(os.WriteFile(p(name), []byte(content), 0600))
	}
	for _, pair := range c.Links {
		must(os.MkdirAll(filepath.Dir(p(pair.Path)), 0700))
		must(os.Link(p(pair.Target), p(pair.Path)))
	}
	for _, d := range c.Dirs {
		must(os.MkdirAll(p(d), 0700))
	}
	ops := &fixtureOps{failUnlink: map[string]error{}, storageErrors: map[string]bool{}, historyMissing: map[string]bool{}, historyErrors: map[string]bool{}, identities: map[string]Identity{}, root: root, trace: []map[string]any{}, storage: storageJSON(c.Storage), media: c.Media}
	for _, x := range c.DenyUnlink {
		ops.failUnlink[p(x)] = fs.ErrPermission
	}
	for _, x := range c.StorageErrors {
		ops.storageErrors[x] = true
	}
	for _, x := range c.MissingHistory {
		ops.historyMissing[strings.ReplaceAll(x, "@ROOT@", root)] = true
	}
	for _, x := range c.HistoryErrors {
		ops.historyErrors[strings.ReplaceAll(x, "@ROOT@", root)] = true
	}
	for name, x := range c.Identities {
		ops.identities[p(name)] = Identity{Dev: x.Dev, Inode: x.Inode}
	}
	if ops.media == nil {
		ops.media = []string{".mkv", ".mp4"}
	}
	for _, name := range c.Order {
		ops.scanOrder = append(ops.scanOrder, strings.ReplaceAll(name, "/fixture", root))
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	start := now
	m := New(ops)
	m.Now = func() time.Time { return now }
	config := strings.ReplaceAll(string(c.Config), "@ROOT@", root)
	initError := m.Init(json.RawMessage(config))
	var sourceError any
	if initError != nil {
		sourceError = initError.Error()
		c.Steps = nil
	}
steps:
	for _, s := range c.Steps {
		now = start.Add(time.Duration(s.At * float64(time.Second)))
		path := p(s.Path)
		switch s.Op {
		case "delete":
			if x, e := os.Stat(path); e == nil {
				if x.IsDir() {
					must(os.RemoveAll(path))
				} else {
					must(os.Remove(path))
				}
			}
			if e := m.Deleted(path, s.Mode, s.Directory); e != nil {
				sourceError = e.Error()
				break steps
			}
		case "create":
			must(os.MkdirAll(filepath.Dir(path), 0700))
			if s.Link != "" {
				must(os.Link(p(s.Link), path))
			} else {
				content := s.Content
				if content == "" {
					content = "new"
				}
				must(os.WriteFile(path, []byte(content), 0600))
			}
			m.Created(path, false)
		case "move":
			dest := p(s.Dest)
			must(os.MkdirAll(filepath.Dir(dest), 0700))
			must(os.Rename(path, dest))
			if s.Missing {
				must(os.Remove(dest))
			}
			if e := m.Moved(path, dest, s.Mode, false); e != nil {
				sourceError = e.Error()
				break steps
			}
		case "tick":
			m.Tick()
		case "stop":
			m.Stop()
		case "reload":
			must(m.Init(json.RawMessage(strings.ReplaceAll(string(s.Config), "@ROOT@", root))))
		case "external_remove":
			must(os.Remove(path))
		case "replace_without_event":
			must(os.Remove(path))
			must(os.WriteFile(path, []byte("replacement"), 0600))
		default:
			t.Fatal("unknown source step", s.Op)
		}
	}
	files := []string{}
	must(filepath.WalkDir(root, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if path == root || d.Name() == ".fixture-owner" {
			return nil
		}
		name, _ := filepath.Rel(root, path)
		if d.IsDir() {
			name += "/"
		}
		files = append(files, name)
		return nil
	}))
	sort.Strings(files)
	state := []string{}
	for _, x := range m.State {
		state = append(state, x.Path)
	}
	queue := []map[string]any{}
	for _, x := range m.Queue {
		queue = append(queue, map[string]any{"path": x.Path, "at": x.At.Sub(start).Seconds(), "processed": x.Processed})
	}
	result := map[string]any{"files": files, "state": state, "queue": queue, "trace": ops.trace, "delay": m.Config.Delay, "enabled": m.StateValue(), "error": sourceError, "logged_errors": len(m.Errors)}
	out, _ := json.Marshal(result)
	out = []byte(strings.ReplaceAll(string(out), root, "/fixture"))
	var generic any
	must(json.Unmarshal(out, &generic))
	return generic
}

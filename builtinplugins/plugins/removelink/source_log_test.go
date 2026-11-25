package removelink

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

func TestPythonLogReprActualSource312(t *testing.T) {
	raw, err := os.ReadFile("reference/log-repr-source312.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		SourceLogs struct {
			Source string `json:"source_sha256"`
			Cases  []struct {
				Input    oracleCase
				Expected struct{ Logs [][2]string }
			}
		} `json:"source_logs"`
		Builtin struct {
			Unicode     string `json:"unicode_version"`
			Contexts    []string
			ScalarCount int    `json:"scalar_count"`
			Count       int    `json:"repr_count"`
			SHA         string `json:"repr_sha256"`
			Cases       []struct{ Value, Repr string }
			Lists       []struct {
				Values []string
				Repr   string
			}
		}
	}
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	if suite.SourceLogs.Source != "22fb375e531378b3454607c7bca305b637abd60ee8ed69e14e32fbc2ddfc5485" || len(suite.SourceLogs.Cases) != 3 || suite.Builtin.Unicode != unicode.Version || len(suite.Builtin.Contexts) != 4 || suite.Builtin.ScalarCount != 0x110000-0x800 {
		t.Fatal("unattested fixed source repr fixture")
	}
	for _, row := range suite.SourceLogs.Cases {
		t.Run(row.Input.Name, func(t *testing.T) {
			got := runSourceLogOracle(t, row.Input).(map[string]any)["logs"]
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(row.Expected.Logs)
			if string(a) != string(b) {
				t.Fatalf("source logs %s; Go %s", b, a)
			}
		})
	}
	for _, row := range suite.Builtin.Cases {
		if got := pythonLogStrings([]string{row.Value}); got != "["+row.Repr+"]" {
			t.Errorf("repr of %q: source %s; Go %s", row.Value, row.Repr, got)
		}
	}
	for _, row := range suite.Builtin.Lists {
		if got := pythonLogStrings(row.Values); got != row.Repr {
			t.Errorf("list repr: source %s; Go %s", row.Repr, got)
		}
	}
	h := sha256.New()
	count := 0
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		for _, prefix := range suite.Builtin.Contexts {
			got := pythonLogStrings([]string{prefix + string(r)})
			h.Write([]byte(got[1:len(got)-1] + "\n"))
			count++
		}
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != suite.Builtin.SHA || count != suite.Builtin.Count {
		t.Fatalf("all-scalar source repr: count %d SHA %s; Go count %d SHA %s", suite.Builtin.Count, suite.Builtin.SHA, count, got)
	}
}

type sourceLogOps struct {
	*fixtureOps
	logs [][2]string
}

func (o *sourceLogOps) Log(level, message string) error {
	message = regexp.MustCompile(`耗时 [0-9]+\.[0-9]+ 秒`).ReplaceAllString(message, "耗时 0.00 秒")
	o.logs = append(o.logs, [2]string{strings.ToLower(level), message})
	return nil
}
func (o *sourceLogOps) StartWatch(w Watch) {
	kind := "硬链接"
	if w.Mode == "strm" {
		kind = " STRM "
	}
	o.Log("INFO", w.Path+" 的"+kind+"监控服务启动")
}
func (o *sourceLogOps) HistoryRecord(kind, path string) (string, bool, error) {
	ok, e := o.History(kind, path)
	return kind + ":" + path, ok, e
}
func TestSourceLogOracle312(t *testing.T) {
	raw, e := os.ReadFile("reference/log-source312.json")
	if e != nil {
		t.Fatal(e)
	}
	var suite struct {
		Source string `json:"source_sha256"`
		Cases  []struct {
			Input    oracleCase `json:"input"`
			Expected struct {
				Logs [][2]string `json:"logs"`
			} `json:"expected"`
		}
	}
	if json.Unmarshal(raw, &suite) != nil || suite.Source != "22fb375e531378b3454607c7bca305b637abd60ee8ed69e14e32fbc2ddfc5485" {
		t.Fatal("unattested original log fixture")
	}
	for _, row := range suite.Cases {
		t.Run(row.Input.Name, func(t *testing.T) {
			result := runSourceLogOracle(t, row.Input)
			b, _ := json.Marshal(result.(map[string]any)["logs"])
			var logs [][2]string
			json.Unmarshal(b, &logs)
			if !reflect.DeepEqual(logs, row.Expected.Logs) {
				want, _ := json.MarshalIndent(row.Expected.Logs, "", "  ")
				got, _ := json.MarshalIndent(logs, "", "  ")
				t.Fatalf("originalsource logs %s\ncurrentGo logs %s", want, got)
			}
		})
	}
}

func TestUnstartedObserverStopSourceLogs(t *testing.T) {
	raw, err := os.ReadFile("reference/observer-start-failure-source312.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Source string      `json:"source_sha256"`
		Stop   [][2]string `json:"stop_logs"`
	}
	if json.Unmarshal(raw, &source) != nil || source.Source != "22fb375e531378b3454607c7bca305b637abd60ee8ed69e14e32fbc2ddfc5485" {
		t.Fatal("unattested source stop logs")
	}
	ops := &sourceLogOps{fixtureOps: &fixtureOps{}, logs: [][2]string{}}
	m := New(ops)
	m.unstartedObservers = 1
	m.Stop()
	if !reflect.DeepEqual(ops.logs, source.Stop) || m.unstartedObservers != 0 {
		t.Fatal("source failed-start observer was not retained until join", ops.logs, source.Stop)
	}
	ops.logs = [][2]string{}
	m.Stop()
	for _, entry := range ops.logs {
		if entry[0] == "error" {
			t.Fatal("failed observer join replayed", ops.logs)
		}
	}
}

func runSourceLogOracle(t *testing.T, c oracleCase) any {
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
	baseOps := &fixtureOps{failUnlink: map[string]error{}, storageErrors: map[string]bool{}, historyMissing: map[string]bool{}, historyErrors: map[string]bool{}, identities: map[string]Identity{}, root: root, trace: []map[string]any{}, storage: storageJSON(c.Storage), media: c.Media}
	ops := &sourceLogOps{fixtureOps: baseOps, logs: [][2]string{}}
	for _, x := range c.DenyUnlink {
		ops.failUnlink[p(x)] = errors.New("synthetic unlink denied")
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
	result := map[string]any{"files": files, "state": state, "queue": queue, "trace": ops.trace, "delay": m.Config.Delay, "enabled": m.StateValue(), "error": sourceError, "logged_errors": len(m.Errors), "logs": ops.logs}
	out, _ := json.Marshal(result)
	out = []byte(strings.ReplaceAll(string(out), root, "/fixture"))
	var generic any
	must(json.Unmarshal(out, &generic))
	return generic
}

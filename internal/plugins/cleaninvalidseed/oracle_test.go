package cleaninvalidseed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type syntheticHost struct {
	spec     map[string]any
	services []Service
	config   map[string]map[string]any
	files    map[string]map[string]any
	order    []string
	trace    []map[string]any
}

func newSyntheticHost(spec map[string]any) *syntheticHost {
	h := &syntheticHost{spec: spec, config: map[string]map[string]any{}, files: map[string]map[string]any{}, order: []string{}, trace: []map[string]any{}}
	a, _ := spec["services"].([]any)
	for _, v := range a {
		s := v.(map[string]any)
		name := s["name"].(string)
		kind, _ := s["kind"].(string)
		active := true
		if v, ok := s["active"]; ok {
			active = Truth(v)
		}
		h.services = append(h.services, Service{Name: name, Kind: kind, Inactive: !active, FalseInstance: Truth(s["false_instance"])})
		h.config[name] = s
	}
	fs, _ := spec["files"].([]any)
	for _, v := range fs {
		f := v.(map[string]any)
		p := f["path"].(string)
		h.files[p] = f
		h.order = append(h.order, p)
	}
	return h
}
func (h *syntheticHost) Services(_ context.Context, selected any) ([]Service, error) {
	a, e := listValue(selected)
	if e != nil {
		return nil, e
	}
	out := []Service{}
	for _, s := range h.services {
		for _, v := range a {
			if eq(s.Name, v) {
				out = append(out, s)
				break
			}
		}
	}
	return out, nil
}
func (h *syntheticHost) Torrents(_ context.Context, s Service) ([]*Object, bool, error) {
	h.trace = append(h.trace, map[string]any{"op": "list", "service": s.Name})
	c := h.config[s.Name]
	if Truth(c["list_throw"]) {
		return nil, false, source("RuntimeError", "synthetic list failure")
	}
	if Truth(c["native"]) {
		return (NativeDownloader{Service: s, Call: h.nativeCall(s)}).List(context.Background())
	}
	a, _ := c["torrents"].([]any)
	out := []*Object{}
	for _, v := range a {
		out = append(out, Convert(v).(*Object))
	}
	return out, Truth(c["list_error"]), nil
}
func (h *syntheticHost) nativeCall(s Service) NativeCall {
	return func(_ context.Context, op string, args map[string]any) (json.RawMessage, error) {
		c := h.config[s.Name]
		switch op {
		case "qb.torrents_info", "tr.torrent_get":
			return json.Marshal(c["native_torrents"])
		case "qb.torrents_trackers":
			h.trace = append(h.trace, map[string]any{"op": "trackers", "service": s.Name, "hash": args["torrent_hash"]})
			values, _ := c["tracker_by_hash"].(map[string]any)
			v := values[str(args["torrent_hash"])]
			if v == nil {
				v = []any{}
			}
			return json.Marshal(v)
		}
		return nil, source("RuntimeError", "ungranted synthetic RPC")
	}
}
func (h *syntheticHost) SetTags(_ context.Context, s Service, hash any, tags []any) (any, error) {
	op := "qb_tags"
	if s.Kind == "transmission" {
		op = "tr_tags"
	}
	h.trace = append(h.trace, map[string]any{"op": op, "service": s.Name, "hash": hash, "tags": tags})
	if Truth(h.config[s.Name]["tag_throw"]) {
		return nil, source("RuntimeError", "synthetic label failure")
	}
	if v, ok := h.config[s.Name]["tag_return"]; ok {
		return v, nil
	}
	return true, nil
}
func (h *syntheticHost) DeleteTorrent(_ context.Context, s Service, hash any, deleteFiles bool) (any, error) {
	h.trace = append(h.trace, map[string]any{"op": "delete_torrent", "service": s.Name, "hash": hash, "delete_files": deleteFiles})
	if Truth(h.config[s.Name]["delete_throw"]) {
		return nil, source("RuntimeError", "synthetic delete failure")
	}
	if v, ok := h.config[s.Name]["delete_return"]; ok {
		return v, nil
	}
	return true, nil
}
func (h *syntheticHost) Exists(p string) bool { _, ok := h.files[p]; return ok }
func (h *syntheticHost) IsFile(p string) bool {
	return h.files[p] != nil && h.files[p]["kind"] == "file"
}
func (h *syntheticHost) IsDir(p string) bool { return h.files[p] != nil && h.files[p]["kind"] == "dir" }
func (h *syntheticHost) Stat(p string) (FileStat, error) {
	f := h.files[p]
	if f == nil || Truth(f["stat_error"]) {
		return FileStat{}, source("OSError", "synthetic stat failure")
	}
	n := func(v any) float64 {
		if v == nil {
			return 0
		}
		r, _ := number(v)
		f, _ := r.Float64()
		return f
	}
	size, _ := integer(f["size"])
	return FileStat{Size: size.Int64(), MTime: n(f["mtime"])}, nil
}
func (h *syntheticHost) Children(p string) ([]string, error) {
	if Truth(h.files[p]["list_error"]) {
		return nil, source("OSError", "synthetic list failure")
	}
	out := []string{}
	for _, v := range h.order {
		if h.Exists(v) && v != p && path.Dir(v) == p {
			out = append(out, v)
		}
	}
	return out, nil
}
func (h *syntheticHost) Descendants(p string) ([]string, error) {
	out := []string{}
	for _, v := range h.order {
		if h.Exists(v) && strings.HasPrefix(v, strings.TrimRight(p, "/")+"/") {
			out = append(out, v)
		}
	}
	return out, nil
}
func (h *syntheticHost) Unlink(p string) error {
	h.trace = append(h.trace, map[string]any{"op": "unlink", "path": p})
	if Truth(h.files[p]["delete_error"]) {
		return source("OSError", "synthetic unlink failure")
	}
	delete(h.files, p)
	return nil
}
func (h *syntheticHost) RemoveTree(p string) error {
	h.trace = append(h.trace, map[string]any{"op": "rmtree", "path": p})
	if Truth(h.files[p]["delete_error"]) {
		return source("OSError", "synthetic rmtree failure")
	}
	for v := range h.files {
		if v == p || strings.HasPrefix(v, strings.TrimRight(p, "/")+"/") {
			delete(h.files, v)
		}
	}
	return nil
}
func (h *syntheticHost) Notice(_ context.Context, n map[string]any) error {
	h.trace = append(h.trace, map[string]any{"op": "notice"})
	for k, v := range n {
		h.trace[len(h.trace)-1][k] = v
	}
	return nil
}
func (h *syntheticHost) SaveConfig(_ context.Context, c map[string]any) error {
	h.trace = append(h.trace, map[string]any{"op": "config", "config": c})
	return nil
}
func canonical(v any) any {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	var out any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e = d.Decode(&out); e != nil {
		panic(e)
	}
	return out
}
func referenceFile(p string) string {
	if strings.HasPrefix(p, "reference/") || p == "source-checksums.json" {
		return filepath.Join("../../../testdata/plugins/clean-invalid-seed-2.1", p)
	}
	return p
}
func decodeFile(t *testing.T, p string, v any) {
	t.Helper()
	f, e := os.Open(referenceFile(p))
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.UseNumber()
	if e = d.Decode(v); e != nil {
		t.Fatal(e)
	}
}
func TestInstalledASTBusinessReference(t *testing.T) {
	var corpus struct {
		Cases []struct {
			ID       string
			Input    map[string]any
			Expected any
		}
	}
	decodeFile(t, "reference/oracle.json", &corpus)
	for _, c := range corpus.Cases {
		t.Run(c.ID, func(t *testing.T) {
			h := newSyntheticHost(c.Input)
			m := New(h)
			now := int64(2000000000)
			if v, ok := c.Input["now"]; ok {
				r, _ := number(v)
				now = r.Num().Int64()
			}
			m.Now = func() time.Time { return time.Unix(now, 0) }
			steps, _ := c.Input["steps"].([]any)
			if steps == nil {
				steps = []any{map[string]any{"action": "init", "config": c.Input["config"]}, map[string]any{"action": "clean"}}
			}
			results := []any{}
			ctx := context.Background()
			for _, step := range steps {
				s := step.(map[string]any)
				var result any
				var e error
				switch s["action"] {
				case "init":
					cfg, _ := s["config"].(map[string]any)
					e = m.Init(ctx, cfg)
				case "clean":
					e = m.Clean(ctx)
				case "files":
					e = m.DetectFiles(ctx)
				case "command":
					ev, _ := s["event"].(map[string]any)
					e = m.HandleCommand(ctx, ev)
				case "service":
					result, e = m.GetService()
				case "state":
					result = m.GetState()
				case "stop":
					m.StopService()
				default:
					t.Fatal("unknown step", s)
				}
				if e != nil {
					result = nil
				}
				results = append(results, map[string]any{"result": result, "error": ErrorType(e)})
			}
			files := []string{}
			for _, p := range h.order {
				if h.Exists(p) {
					files = append(files, p)
				}
			}
			got := canonical(map[string]any{"steps": results, "trace": h.trace, "state": m.Config, "jobs": m.Schedulers, "files": files})
			if !reflect.DeepEqual(got, c.Expected) {
				g, _ := json.MarshalIndent(got, "", " ")
				w, _ := json.MarshalIndent(c.Expected, "", " ")
				t.Fatalf("actual installation AST differs\ngot %s\nwant %s", g, w)
			}
		})
	}
}
func TestInstalledStringUtilsReference(t *testing.T) {
	var corpus struct {
		Helpers []struct {
			Method               string
			Input, Result, Error any
		}
	}
	decodeFile(t, "reference/oracle.json", &corpus)
	for i, c := range corpus.Helpers {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var got any
			var e error
			if c.Method == "str_filesize" {
				got = FileSize(c.Input)
			} else {
				got, e = URLNetloc(c.Input)
			}
			if e != nil {
				got = nil
			}
			if !reflect.DeepEqual(canonical(got), c.Result) || !reflect.DeepEqual(ErrorType(e), c.Error) {
				t.Fatal(c.Method, c.Input, "got", got, ErrorType(e), "want", c.Result, c.Error)
			}
		})
	}
}

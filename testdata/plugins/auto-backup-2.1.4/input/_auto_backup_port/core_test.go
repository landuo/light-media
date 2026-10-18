package autobackup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

func decode(t *testing.T, b []byte, v any) {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if e := d.Decode(v); e != nil {
		t.Fatal(e)
	}
}
func canonical(v any) string { b, _ := json.Marshal(v); return string(b) }

type oracle struct {
	Kind       string            `json:"kind"`
	Input      any               `json:"input"`
	Repeat     bool              `json:"repeat"`
	Fault      string            `json:"fault"`
	Files      map[string]Object `json:"files"`
	ZipSuccess bool              `json:"zip_success"`
	Dav        Object            `json:"dav"`
	Logs       []any             `json:"logs"`
	Trace      []any             `json:"trace"`
	State      any               `json:"state"`
	Service    any               `json:"service"`
	Error      any               `json:"error"`
	Result     any               `json:"result"`
}
type rig struct {
	trace []any
	logs  []any
	fault string
	dav   Object
	files map[string]Object
	zip   bool
}

func (r *rig) log(v ...any) { r.trace = append(r.trace, v) }
func (r *rig) deps() Dependencies {
	return Dependencies{Log: func(_ context.Context, l, m string) { r.logs = append(r.logs, []any{l, m}) }, Now: func() time.Time { v, _ := time.Parse(time.RFC3339, "2026-10-18T09:02:03+08:00"); return v }, Timezone: "Asia/Shanghai", DataPath: func() string { return "/owned/plugin" }, NewScheduler: func(ctx context.Context, zone string) (Scheduler, error) {
		r.log("scheduler_new", Object{"timezone": zone})
		return &testScheduler{r: r}, nil
	}, SaveConfig: func(ctx context.Context, m Object) error {
		r.log("config", m)
		if r.fault == "config" {
			return errors.New("config failed")
		}
		return nil
	}, PostMessage: func(ctx context.Context, m Object) error {
		r.log("notice", m)
		if r.fault == "notice" {
			return errors.New("notice failed")
		}
		return nil
	}, BackupFile: func(ctx context.Context, path string) (*string, error) {
		r.log("backup_file", path)
		if !r.zip {
			return nil, nil
		}
		s := "/owned/backup/bk_20261018090203.zip"
		return &s, nil
	}, ListBackups: func(ctx context.Context, path string) ([]Entry, error) {
		names := []string{}
		for name := range r.files {
			names = append(names, name)
		}
		sort.Strings(names)
		items := []Entry{}
		for _, name := range names {
			f := r.files[name]
			n, _ := f["ctime"].(json.Number).Float64()
			items = append(items, Entry{name, n, text(f["kind"])})
		}
		return items, nil
	}, RemoveFile: func(ctx context.Context, path string) error { r.log("unlink", path); return nil }, RemoveTree: func(ctx context.Context, path string) error { r.log("rmtree", path); return nil }, NewDAV: func(ctx context.Context, options Object) (DAV, error) {
		r.log("dav_init", options)
		if truth(r.dav["init_error"]) {
			return nil, errors.New(text(r.dav["init_error"]))
		}
		return &testDAV{r}, nil
	}}
}

type testScheduler struct {
	r       *rig
	running bool
	jobs    int
}

func (s *testScheduler) AddDate(ctx context.Context, t time.Time, name string) error {
	s.jobs++
	s.r.log("scheduler_add", Object{"trigger": "date", "run_date": t.Format(time.RFC3339), "name": name})
	return nil
}
func (s *testScheduler) Jobs(context.Context) (int, error) { return s.jobs, nil }
func (s *testScheduler) Print(context.Context) error       { s.r.log("scheduler_print"); return nil }
func (s *testScheduler) Start(context.Context) error {
	s.running = true
	s.r.log("scheduler_start")
	return nil
}
func (s *testScheduler) Clear(context.Context) error {
	s.jobs = 0
	s.r.log("scheduler_clear")
	return nil
}
func (s *testScheduler) Running() bool { return s.running }
func (s *testScheduler) Shutdown(context.Context) error {
	s.running = false
	s.r.log("scheduler_shutdown")
	return nil
}

type testDAV struct{ r *rig }

func (d *testDAV) List(ctx context.Context, path string) ([]string, error) {
	d.r.log("dav_list", path)
	if truth(d.r.dav["list_error"]) {
		return nil, errors.New(text(d.r.dav["list_error"]))
	}
	files := []string{}
	if list, ok := d.r.dav["files"].([]any); ok {
		for _, v := range list {
			files = append(files, text(v))
		}
	}
	return files, nil
}
func (d *testDAV) Upload(ctx context.Context, remote, local string, callback func() error) error {
	d.r.log("dav_upload", remote, local)
	if truth(d.r.dav["upload_error"]) {
		return errors.New(text(d.r.dav["upload_error"]))
	}
	return callback()
}
func (d *testDAV) Check(ctx context.Context, path string) (bool, error) {
	d.r.log("dav_check", path)
	if truth(d.r.dav["check_error"]) {
		return false, errors.New(text(d.r.dav["check_error"]))
	}
	v, present := d.r.dav["check"]
	return !present || truth(v), nil
}
func (d *testDAV) Delete(ctx context.Context, path string) error {
	d.r.log("dav_delete", path)
	if list, ok := d.r.dav["delete_errors"].([]any); ok {
		for _, v := range list {
			if text(v) == path {
				return errors.New("delete failed")
			}
		}
	}
	return nil
}
func TestInstalledBusinessOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/business-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Cases []oracle `json:"cases"`
	}
	decode(t, raw, &fixture)
	if len(fixture.Cases) != 82 {
		t.Fatal("oracle count changed")
	}
	for i, c := range fixture.Cases {
		t.Run(c.Kind+"/"+string(rune('A'+i)), func(t *testing.T) {
			r := &rig{trace: []any{}, fault: c.Fault, dav: c.Dav, files: c.Files, zip: c.ZipSuccess}
			p := New(r.deps())
			var err error
			var result any
			var state, service any
			if c.Kind == "init" {
				err = p.Init(context.Background(), c.Input)
				if err == nil && c.Repeat {
					err = p.Init(context.Background(), Object{})
				}
				if err == nil {
					state = p.GetState()
					service, err = p.GetService()
				}
			} else {
				if e := p.Init(context.Background(), c.Input); e != nil {
					t.Fatal(e)
				}
				r.trace = []any{}
				var v Result
				v, err = p.Backup(context.Background())
				if err == nil {
					result = []any{v.Success, v.Message}
				}
			}
			if canonical(ErrorType(err)) != canonical(c.Error) {
				t.Fatalf("error got%v expected%v trace%s", err, c.Error, canonical(r.trace))
			}
			gotLogs, wantLogs := []any{}, []any{}
			for _, item := range r.logs {
				v := item.([]any)
				msg := v[1].(string)
				if strings.HasPrefix(msg, "获取WebDAV文件列表失败:") {
					msg = "获取WebDAV文件列表失败: <source-exception>"
				}
				gotLogs = append(gotLogs, []any{v[0], msg})
			}
			for _, item := range c.Logs {
				v := item.([]any)
				msg := v[1].(string)
				if strings.HasPrefix(msg, "获取WebDAV文件列表失败:") {
					msg = "获取WebDAV文件列表失败: <source-exception>"
				}
				wantLogs = append(wantLogs, []any{v[0], msg})
			}
			if canonical(gotLogs) != canonical(wantLogs) {
				t.Errorf("logs got%s want%s", canonical(gotLogs), canonical(wantLogs))
			}
			if canonical(r.trace) != canonical(c.Trace) {
				t.Errorf("trace got%s\nwant%s", canonical(r.trace), canonical(c.Trace))
			}
			if c.Kind == "backup" && canonical(result) != canonical(c.Result) {
				t.Errorf("result got%s want%s", canonical(result), canonical(c.Result))
			}
			if c.Kind == "init" && c.Error == nil && (canonical(state) != canonical(c.State) || canonical(service) != canonical(c.Service)) {
				t.Errorf("state/service got%s/%s want%s/%s", canonical(state), canonical(service), canonical(c.State), canonical(c.Service))
			}
		})
	}
}

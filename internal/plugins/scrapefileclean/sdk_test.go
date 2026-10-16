package scrapefileclean

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func TestSDKWorkerFixture(t *testing.T) {
	for _, a := range os.Args {
		if a == "--scrape-file-clean-worker" {
			if e := plugins.Serve(context.Background(), os.Stdin, os.Stdout, Manifest(), Handle); e != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
	}
}
func sdkCommand(t *testing.T) plugins.Command {
	p, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	return plugins.Command{Path: p, Args: []string{"-test.run=^TestSDKWorkerFixture$", "--", "--scrape-file-clean-worker"}}
}
func TestActualSDKProcessKeepsHostQueueUntilBoundEvent(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "m")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(dir, "a.mkv")
	target := filepath.Join(dir, "b.mkv")
	if e := os.WriteFile(source, []byte("owned synthetic video"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Link(source, target); e != nil {
		t.Fatal(e)
	}
	ops := &fixtureOps{root: root, trace: []map[string]any{}, deny: map[string]bool{}, missingHistory: map[string]bool{}}
	m := New(ops)
	now := time.Unix(1000, 0)
	m.Now = func() time.Time { return now }
	config, _ := json.Marshal(map[string]any{"enabled": true, "monitor_dirs": dir, "delayed_deletion": true, "delay_seconds": 30, "delete_history": true, "delete_torrents": true})
	calls := 0
	host := func(_ context.Context, id plugins.Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
		if id.PluginID != "ScrapeFileClean" || id.Version != "1.0.7" || id.InstanceID != "ScrapeFileClean" {
			return nil, errors.New("wrong identity")
		}
		calls++
		switch method {
		case ConfigureCapability:
			if string(raw) != "{}" {
				return nil, errors.New("worker supplied configuration")
			}
			if e := m.Init(config); e != nil {
				return nil, e
			}
			if e := os.Remove(source); e != nil {
				return nil, e
			}
			now = now.Add(time.Second)
			m.Deleted(source, false)
			return json.RawMessage(`{"configured":true}`), nil
		case ApplyCapability:
			if string(raw) != `{"event_id":"owned-event"}` {
				return nil, errors.New("event does not belong to invocation")
			}
			now = now.Add(31 * time.Second)
			m.Tick()
			return json.RawMessage(`{"processed":true}`), nil
		}
		return nil, errors.New("ungranted method")
	}
	raw, _ := json.Marshal(plugins.Invocation{Action: "init", Config: config})
	out, e := (plugins.Runner{}).Run(context.Background(), sdkCommand(t), Manifest(), "ScrapeFileClean", "invoke", raw, host)
	if e != nil || calls != 1 || out.HostCalls != 1 || len(m.Queue) != 1 || !ops.Exists(target) {
		t.Fatal("SDK stop prematurely drained source stop_service", e, calls, len(m.Queue))
	}
	raw, _ = json.Marshal(plugins.Invocation{Action: "event", Config: config, Input: json.RawMessage(`{"event_id":"owned-event"}`)})
	out, e = (plugins.Runner{}).Run(context.Background(), sdkCommand(t), Manifest(), "ScrapeFileClean", "invoke", raw, host)
	if e != nil || out.HostCalls != 1 || len(m.Queue) != 0 || ops.Exists(target) || len(ops.trace) != 7 {
		t.Fatal("real SDK did not apply the host-bound deletion event", e, len(ops.trace), len(m.Queue))
	}
}
func TestSDKExactFormAndNoInventedSourceInterfaces(t *testing.T) {
	out, e := (plugins.Runner{}).Run(context.Background(), sdkCommand(t), Manifest(), "ScrapeFileClean", "config_form", nil, nil)
	if e != nil || string(out.Result) != strings.TrimSpace(string(Form())) || out.HostCalls != 0 {
		t.Fatal("original form changed", e)
	}
	var f struct {
		Conf  []any          `json:"conf"`
		Model map[string]any `json:"model"`
	}
	if json.Unmarshal(Form(), &f) != nil || len(f.Model) != 11 || f.Model["delayed_deletion"] != true || f.Model["delay_seconds"] != float64(30) {
		t.Fatal("original defaults missing")
	}
	if Manifest().HasPage || len(Manifest().APIs) != 0 || len(Manifest().Methods) != 3 || len(Manifest().Dashboards) != 0 || len(Manifest().Events) != 0 {
		t.Fatal("invented source page/API/command/period/events")
	}
	calls := 0
	deny := func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		calls++
		return nil, errors.New("unexpected host call")
	}
	for _, raw := range []string{`{"action":"event","config":{},"input":{"event_id":"x","path":"/outside"}}`, `{"action":"event","config":{},"input":null}`, `{"action":"service","config":{}}`} {
		if _, e := Handle(context.Background(), "invoke", json.RawMessage(raw), deny); e == nil {
			t.Fatal("unbound destructive input accepted", raw)
		}
	}
	if calls != 0 {
		t.Fatal("invalid SDK input reached host")
	}
}

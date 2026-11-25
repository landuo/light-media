// Package torrenttransfer implements the installed TorrentTransfer 1.10.3 as
// one persistent Go object. Clone identity and durable data belong to the host.
package torrenttransfer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"light-media/builtinplugins/internal/sdk"
)

func Manifest() plugins.Manifest {
	return plugins.Manifest{
		SchemaVersion: 1, PluginID: "TorrentTransfer", Name: "自动转移做种", Description: "定期转移下载器中的做种任务到另一个下载器。", Icon: "seed.png", Author: "jxxghp", AuthorURL: "https://github.com/jxxghp", Version: "1.0.0", PluginOrder: 18, Runtime: "go-executable", Entry: "torrent-transfer", HostAPIMin: 1, HostAPIMax: 1,
		Methods:        []string{"init", "get_state", "get_service", "get_api", "get_render_mode", "get_form", "get_page", "close", "stop_service", "stop", "transfer", "check_recheck", "private_scheduler", "transfer_continue"},
		Capabilities:   []string{plugins.DownloaderConfigurationsReadCapability, plugins.DownloaderSourceConnectionCapability, plugins.DownloaderSourceCompletedCapability, plugins.DownloaderSourceTasksCapability, plugins.DownloaderSourceAddCapability, plugins.DownloaderSourceFindByTagCapability, plugins.DownloaderSourceControlCapability, plugins.BaseUpdateConfig, plugins.BaseGetDataPath, plugins.BaseSaveData, plugins.BaseGetInstanceMetadata, plugins.BackgroundSubmitCapability, plugins.SystemMessagePutCapability, plugins.PostMessageCapability, plugins.LogWriteCapability},
		RuntimeSupport: &plugins.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}},
	}
}

type privateScheduler struct {
	id                                                                uint64
	stop, done                                                        chan struct{}
	start                                                             time.Time
	jobs                                                              []Job
	started, removed, closed, initialized, onceConsumed, checkRunning bool
	active                                                            int
	reserved                                                          map[uint64]bool
	onceDue, nextCheck                                                time.Time
	interval                                                          time.Duration
}
type Plugin struct {
	mu               sync.Mutex
	initMu           sync.Mutex
	config           Config
	state            *runtimeState
	scheduler        *privateScheduler
	generation       uint64
	now              func() time.Time
	waitUntil        func(context.Context, <-chan struct{}, time.Time) error
	nextPass         uint64
	passes           map[uint64]*transferPass
	publicZone       string
	publicZoneCached bool
}

func New() *Plugin {
	return &Plugin{config: NewConfig(), state: newRuntimeState(), now: time.Now, passes: map[uint64]*transferPass{}}
}
func (p *Plugin) currentConfig() Config {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, _ := cloneMap(p.config.values)
	return Config{values: v, tags: append([]string{}, p.config.tags...), skipRaw: append(json.RawMessage{}, p.config.skipRaw...)}
}
func (p *Plugin) setConfig(c Config) {
	v, _ := cloneMap(c.values)
	p.mu.Lock()
	p.config = Config{values: v, tags: append([]string{}, c.tags...), skipRaw: append(json.RawMessage{}, c.skipRaw...)}
	p.mu.Unlock()
}
func (p *Plugin) readConfig() Config { p.mu.Lock(); defer p.mu.Unlock(); return p.config }
func (p *Plugin) core(host plugins.HostCaller) *Core {
	return &Core{Config: p.currentConfig(), Host: sdkHost{caller: host}, state: p.state, configSnapshot: p.readConfig}
}
func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	switch method {
	case "init":
		return nil, p.init(ctx, raw, host)
	case "get_state":
		return json.Marshal(p.currentConfig().State())
	case "get_form":
		inv, e := configurations(ctx, host)
		if e != nil {
			return nil, e
		}
		names := []string{}
		seen := map[string]bool{}
		for _, d := range inv.Downloaders {
			if d.Enabled && d.Name != "" && d.Kind != "" && !seen[d.Name] {
				seen[d.Name] = true
				names = append(names, d.Name)
			}
		}
		f, defaults, e := Form(names)
		if e != nil {
			return nil, e
		}
		return json.Marshal([]any{f, defaults})
	case "get_service":
		cfg := p.currentConfig()
		if !cfg.State() {
			return json.RawMessage(`[]`), nil
		}
		cron, e := asString(cfg.values["cron"])
		if e != nil {
			return nil, e
		}
		if len(strings.FieldsFunc(cron, sourcePythonSpace)) != 5 {
			return nil, errCron
		}
		zone, e := p.publicTimezone(ctx, host)
		if e != nil {
			return nil, e
		}
		validationZone := zone
		if zone == "local" {
			validationZone = "UTC"
		}
		jobs, e := cfg.Services(validationZone)
		if e != nil {
			return nil, e
		}
		out := []any{}
		for _, j := range jobs {
			trigger := map[string]any{"kind": "cron", "cron": j.Cron}
			if zone == "local" {
				trigger["local_timezone"] = true
			} else {
				trigger["timezone"] = zone
			}
			out = append(out, map[string]any{"id": j.ID, "name": j.Name, "handler": "transfer", "trigger": trigger, "kwargs": map[string]any{}})
		}
		return json.Marshal(out)
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "get_command", "get_api", "get_page", "get_dashboard", "get_dashboard_meta", "get_auth_providers", "get_module", "close":
		return json.RawMessage(`null`), nil
	case "stop_service", "stop":
		return nil, p.stopService(ctx)
	case "transfer":
		return nil, p.runTransfer(ctx, host)
	case "transfer_continue":
		return nil, p.continueTransfer(ctx, raw, host)
	case "check_recheck":
		return nil, p.core(host).CheckRecheck(ctx)
	case "private_scheduler":
		var in struct {
			Generation uint64 `json:"generation"`
			Cycle      uint64 `json:"cycle"`
		}
		if e := json.Unmarshal(raw, &in); e != nil {
			return nil, e
		}
		return nil, p.runSchedulerPhase(ctx, in.Generation, in.Cycle, host)
	default:
		return nil, errors.New("unsupported torrent transfer method")
	}
}
func (p *Plugin) init(ctx context.Context, raw json.RawMessage, host plugins.HostCaller) error {
	p.initMu.Lock()
	defer p.initMu.Unlock()
	var input any
	var e error
	if len(raw) > 0 {
		input, e = decode(raw)
		if e != nil {
			return e
		}
	}
	cfg := p.currentConfig()
	if truth(input) {
		var fields map[string]json.RawMessage
		if e = json.Unmarshal(raw, &fields); e != nil {
			return e
		}
		cfg.skipRaw = append(json.RawMessage{}, fields["skipverify"]...)
	}
	r, e := cfg.initialize(ctx, input, sdkHost{caller: host}, func() error { return p.stopService(ctx) }, p.setConfig, p.now)
	if e != nil {
		return e
	}
	if !r.Scheduler {
		return nil
	}
	p.mu.Lock()
	p.generation++
	s := &privateScheduler{id: p.generation, stop: make(chan struct{}), done: make(chan struct{}), start: r.StartAt, jobs: r.Jobs, reserved: map[uint64]bool{}}
	p.scheduler = s
	p.mu.Unlock()
	if len(r.Jobs) > 0 {
		if e = p.submitScheduler(ctx, host, s, 0); e != nil {
			return e
		}
	}
	return nil
}
func (p *Plugin) stopService(ctx context.Context) error {
	p.mu.Lock()
	s := p.scheduler
	if s == nil {
		p.mu.Unlock()
		return nil
	}
	s.removed = true
	if s.active == 0 && !s.closed {
		s.closed = true
		close(s.done)
	}
	running := s.started
	if running {
		p.state.stopped.Store(true)
	}
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	p.mu.Unlock()
	if running {
		select {
		case <-s.done:
		case <-ctx.Done():
			return ctx.Err()
		}
		p.state.stopped.Store(false)
	}
	p.mu.Lock()
	if p.scheduler == s {
		p.scheduler = nil
	}
	p.mu.Unlock()
	return nil
}

func (p *Plugin) publicTimezone(ctx context.Context, host plugins.HostCaller) (string, error) {
	p.mu.Lock()
	name, cached := p.publicZone, p.publicZoneCached
	p.mu.Unlock()
	if cached {
		return name, nil
	}
	name, warnings, e := resolveSourcePublicTimezone("/")
	for _, message := range warnings {
		_ = (plugins.Logger{Host: host}).Log(ctx, plugins.LogWarning, message)
	}
	if e == nil {
		p.mu.Lock()
		p.publicZone = name
		p.publicZoneCached = true
		p.mu.Unlock()
	}
	return name, e
}

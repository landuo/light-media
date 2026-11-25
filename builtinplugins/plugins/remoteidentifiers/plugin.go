package remoteidentifiers

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"
	_ "time/tzdata"

	sdk "light-media/builtinplugins/internal/sdk"
)

const Marker = "#========以下识别词由 RemoteIdentifiers 插件添加========#"

//go:embed form.json
var form json.RawMessage

func Manifest() sdk.Manifest {
	return sdk.Manifest{
		SchemaVersion: 1, PluginID: "RemoteIdentifiers", Name: "共享识别词",
		Description: "从Github、Etherpad远程文件中获取共享识别词并应用", Icon: "words.png",
		Version: "1.0.0", Author: "honue", AuthorURL: "https://github.com/honue",
		PluginOrder: 10, Runtime: "go-executable", Entry: "remote-identifiers",
		HostAPIMin: 1, HostAPIMax: 1, ManagedPrivateScheduler: true, ManagedPublicServices: true,
		Methods: []string{
			"init", "get_state", "get_form", "get_render_mode", "get_service",
			"get_page", "get_command", "get_api", "refresh", "private_once", "refresh_continue",
			"stop_service", "stop", "close",
		},
		Capabilities: []string{sdk.BaseUpdateConfig, sdk.BackgroundSubmitCapability,
			sdk.NetworkGetCapability, sdk.SettingsReadCapability, sdk.SettingsWriteCapability, sdk.LogWriteCapability},
		RuntimeSupport: &sdk.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}},
	}
}

type onceJob struct {
	id      uint64
	due     time.Time
	stop    chan struct{}
	done    chan struct{}
	claimed bool
	active  bool
}

type Plugin struct {
	initMu sync.Mutex
	mu     sync.Mutex
	state  State
	next   uint64
	job    *onceJob
	passes map[uint64]*refreshPass
}

func NewPlugin() *Plugin {
	return &Plugin{state: Cold(), passes: make(map[uint64]*refreshPass)}
}

func (p *Plugin) snapshot() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, caller sdk.HostCaller) (json.RawMessage, error) {
	switch method {
	case "init":
		return nil, p.initialize(ctx, raw, caller)
	case "get_state":
		return json.Marshal(p.snapshot().Enable)
	case "get_form":
		return form, nil
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "get_service":
		state := p.snapshot()
		if !Truth(state.Enable) || !Truth(state.Cron) {
			return nil, nil
		}
		cron, ok := state.Cron.(string)
		if !ok {
			return nil, errors.New("invalid source crontab expression")
		}
		return json.Marshal([]any{map[string]any{
			"id": "RemoteIdentifiers", "name": "获取远端识别词", "handler": "refresh",
			"trigger": map[string]any{"kind": "cron", "cron": cron, "local_timezone": true},
			"kwargs":  map[string]any{},
		}})
	case "refresh":
		return nil, p.task(ctx, caller)
	case "private_once":
		return nil, p.runOnce(ctx, raw, caller)
	case "refresh_continue":
		return nil, p.continueRefresh(ctx, raw, caller)
	case "stop_service", "stop":
		p.initMu.Lock()
		defer p.initMu.Unlock()
		return nil, p.stopOnce(ctx)
	case "get_page", "get_command", "get_api", "close":
		return nil, nil
	default:
		return nil, errors.New("unsupported RemoteIdentifiers method")
	}
}

func (p *Plugin) initialize(ctx context.Context, raw json.RawMessage, caller sdk.HostCaller) error {
	p.initMu.Lock()
	defer p.initMu.Unlock()
	if err := p.stopOnce(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	err := p.state.Init(raw)
	once := Truth(p.state.OnlyOnce)
	p.mu.Unlock()
	if err != nil || !once {
		return err
	}
	zone, exists := os.LookupEnv("TZ")
	if exists {
		zone = Strip(zone)
	} else {
		zone = "Asia/Shanghai"
	}
	if _, err = time.LoadLocation(zone); zone == "" || zone == "Local" || err != nil {
		return errors.New("invalid private scheduler timezone")
	}
	_ = (sdk.Logger{Host: caller}).Log(ctx, sdk.LogInfo, "获取远端识别词,订阅服务启动，立即运行一次")
	p.mu.Lock()
	p.next++
	job := &onceJob{id: p.next, due: time.Now().Add(3 * time.Second), stop: make(chan struct{}), done: make(chan struct{})}
	p.job = job
	p.state.OnlyOnce = false
	state := p.state
	p.mu.Unlock()
	if _, err = (sdk.Base{Host: caller}).UpdateConfig(ctx, state); err != nil {
		return err
	}
	return (sdk.Manager{Host: caller}).SubmitPrivateScheduler(ctx, "private_once", job.id)
}

func (p *Plugin) stopOnce(ctx context.Context) error {
	p.mu.Lock()
	job := p.job
	if job == nil {
		p.mu.Unlock()
		return nil
	}
	p.job = nil
	close(job.stop)
	active := job.active
	p.mu.Unlock()
	if active {
		select {
		case <-job.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (p *Plugin) runOnce(ctx context.Context, raw json.RawMessage, caller sdk.HostCaller) error {
	var id uint64
	if err := json.Unmarshal(raw, &id); err != nil {
		return err
	}
	p.mu.Lock()
	job := p.job
	if job == nil || job.id != id || job.claimed {
		p.mu.Unlock()
		return nil
	}
	job.claimed = true
	p.mu.Unlock()
	defer close(job.done)
	timer := time.NewTimer(time.Until(job.due))
	defer timer.Stop()
	select {
	case <-job.stop:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	p.mu.Lock()
	if p.job != job || time.Since(job.due) > time.Second {
		p.mu.Unlock()
		return nil
	}
	job.active = true
	p.mu.Unlock()
	return p.task(ctx, caller)
}

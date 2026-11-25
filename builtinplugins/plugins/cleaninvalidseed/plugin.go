package cleaninvalidseed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"sync"
	"time"
	_ "time/tzdata"

	sdk "light-media/builtinplugins/internal/sdk"
)

func Manifest() sdk.Manifest {
	return sdk.Manifest{
		SchemaVersion: 1, PluginID: "CleanInvalidSeed", Name: "清理无效做种",
		Description: "清理已经被站点删除的种子及源文件，支持qBittorrent和Transmission",
		Icon:        "clean_a.png", Version: "1.0.0", Author: "DzAvril", AuthorURL: "https://github.com/DzAvril", PluginOrder: 1,
		Runtime: "go-executable", Entry: "clean-invalid-seed", HostAPIMin: 1, HostAPIMax: 1,
		ManagedPrivateScheduler: true, ManagedPublicServices: true,
		RuntimeSupport: &sdk.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}},
		Methods: []string{"init", "get_state", "get_form", "get_render_mode", "get_command", "get_service", "get_api", "get_page",
			"handle_commands", "clean_invalid_seed", "private_once", "stop_service", "close", "stop"},
		BroadcastHandlers: []sdk.BroadcastHandler{{Event: "plugin.action", Method: "handle_commands"}},
		Capabilities: []string{sdk.BaseUpdateConfig, sdk.BackgroundSubmitCapability, sdk.DownloaderConfigurationsReadCapability,
			sdk.DownloaderSourceConnectionCapability, sdk.PostMessageCapability, sdk.LogWriteCapability,
			tasksCapability, trackersCapability, controlCapability, filesCapability, removeCapability},
	}
}

type onceJob struct {
	id         uint64
	due        time.Time
	stop, done chan struct{}
	claimed    bool
	active     bool
}

type Plugin struct {
	lifeMu, runMu sync.Mutex
	mu            sync.RWMutex
	config        map[string]any
	next          uint64
	once          *onceJob
}

func NewPlugin() *Plugin {
	return &Plugin{config: New(nil).Config}
}

func (p *Plugin) configuration() map[string]any {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return maps.Clone(p.config)
}

// The original commands temporarily change flags on the loaded instance.
// Serialize those mutations with scheduled work and initialization. Getters
// can still read the last configuration during a nested host callback.
func (p *Plugin) run(ctx context.Context, caller sdk.HostCaller, fn func(*Machine) error) error {
	p.runMu.Lock()
	defer p.runMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	host := newLiveHost(ctx, caller)
	machine := New(host)
	machine.Config = p.configuration()
	host.config = machine.Config
	err := fn(machine)
	p.mu.Lock()
	p.config = machine.Config
	p.mu.Unlock()
	return errors.Join(err, host.failure)
}

func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, caller sdk.HostCaller) (json.RawMessage, error) {
	switch method {
	case "init":
		return nil, p.initialize(ctx, raw, caller)
	case "get_state":
		return json.Marshal(p.configuration()["enabled"])
	case "get_form":
		return liveForm(ctx, caller)
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "get_command":
		return Commands(), nil
	case "get_service":
		service, err := (&Machine{Config: p.configuration()}).GetService()
		if err != nil {
			return nil, err
		}
		return json.Marshal(service)
	case "handle_commands":
		var event sdk.MutableEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, err
		}
		if event.Type != "plugin.action" {
			return nil, nil
		}
		fields, err := decodeObject(event.Data)
		if err != nil {
			return nil, err
		}
		return nil, p.run(ctx, caller, func(m *Machine) error { return m.HandleCommand(ctx, fields) })
	case "clean_invalid_seed":
		return nil, p.run(ctx, caller, func(m *Machine) error { return m.Clean(ctx) })
	case "private_once":
		return nil, p.runOnce(ctx, raw, caller)
	case "get_api", "get_page", "close":
		return nil, nil
	case "stop_service", "stop":
		p.lifeMu.Lock()
		defer p.lifeMu.Unlock()
		return nil, p.stopOnce(ctx)
	default:
		return nil, errors.New("unsupported CleanInvalidSeed method")
	}
}

func (p *Plugin) initialize(ctx context.Context, raw json.RawMessage, caller sdk.HostCaller) error {
	p.lifeMu.Lock()
	defer p.lifeMu.Unlock()
	if err := p.stopOnce(ctx); err != nil {
		return err
	}
	fields, err := decodeObject(raw)
	if err != nil {
		return err
	}
	var due time.Time
	if err := p.run(ctx, caller, func(m *Machine) error {
		m.Timezone = "Asia/Shanghai"
		if zone, present := os.LookupEnv("TZ"); present {
			m.Timezone = zone
		}
		var err error
		due, err = m.Init(ctx, fields)
		return err
	}); err != nil {
		return err
	}
	if due.IsZero() {
		return nil
	}
	p.mu.Lock()
	p.next++
	job := &onceJob{id: p.next, due: due, stop: make(chan struct{}), done: make(chan struct{})}
	p.once = job
	p.mu.Unlock()
	if err := (sdk.Manager{Host: caller}).SubmitPrivateScheduler(ctx, "private_once", job.id); err != nil {
		_ = p.stopOnce(ctx)
		return err
	}
	return nil
}

func (p *Plugin) stopOnce(ctx context.Context) error {
	p.mu.Lock()
	job := p.once
	if job == nil {
		p.mu.Unlock()
		return nil
	}
	p.once = nil
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
	job := p.once
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
	if p.once != job || time.Since(job.due) > time.Second {
		p.mu.Unlock()
		return nil
	}
	job.active = true
	p.mu.Unlock()
	return p.run(ctx, caller, func(m *Machine) error { return m.Clean(ctx) })
}

func decodeObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	err := decoder.Decode(&value)
	return value, err
}

func liveForm(ctx context.Context, caller sdk.HostCaller) (json.RawMessage, error) {
	inventory, err := (sdk.Downloaders{Host: caller}).Configurations(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(inventory.Downloaders))
	for _, item := range inventory.Downloaders {
		names = append(names, item.Name)
	}
	var form struct{ Conf, Model json.RawMessage }
	if err := json.Unmarshal(Form(names), &form); err != nil {
		return nil, err
	}
	return json.Marshal([]json.RawMessage{form.Conf, form.Model})
}

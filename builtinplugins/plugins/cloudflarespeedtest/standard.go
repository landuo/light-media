package cloudflarespeedtest

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	sdk "light-media/builtinplugins/internal/sdk"
)

func Manifest() sdk.Manifest {
	return sdk.Manifest{
		SchemaVersion: 1, PluginID: PluginID, Name: "Cloudflare IP优选", Version: Version,
		Description: "🌩 测试 Cloudflare CDN 延迟和速度，自动优选IP。", Icon: "cloudflare.jpg", Author: "thsrite",
		AuthorURL: "https://github.com/thsrite", PluginOrder: 12,
		Runtime: "go-executable", Entry: "cloudflare-speed-test", HostAPIMin: 1, HostAPIMax: 1,
		Methods: []string{"init", "get_state", "get_form", "get_render_mode", "get_command", "get_service", "get_api", "get_page",
			"handle_commands", Action, "private_once", "http", "stop_service", "close", "stop"},
		BroadcastHandlers: []sdk.BroadcastHandler{{Event: "plugin.action", Method: "handle_commands"}},
		Capabilities: []string{sdk.BaseUpdateConfig, sdk.LogWriteCapability, sdk.PostMessageCapability, sdk.SystemMessagePutCapability,
			sdk.BackgroundSubmitCapability, customHostsCapability, saveHostsCapability, reloadHostsCapability, platformCapability,
			existsCapability, ensureCapability, resetCapability, latestCapability, downloadCapability, extractCapability,
			removeCapability, runCapability, readResultCapability},
		ManagedPrivateScheduler: true, ManagedPublicServices: true, APIs: httpAPIs(),
		RuntimeSupport: &sdk.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}},
	}
}

type onceJob struct {
	id      uint64
	due     time.Time
	stop    chan struct{}
	done    chan struct{}
	claimed bool
	running bool
}

type Plugin struct {
	machine *Machine
	lifeMu  sync.Mutex
	onceMu  sync.Mutex
	once    *onceJob
	nextID  uint64
}

func NewPlugin() *Plugin {
	p := &Plugin{}
	host := standardHost{plugin: p}
	p.machine, _ = New(host, host)
	return p
}

func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, caller sdk.HostCaller) (json.RawMessage, error) {
	ctx = context.WithValue(ctx, callbackKey{}, caller)
	switch method {
	case "init":
		p.lifeMu.Lock()
		defer p.lifeMu.Unlock()
		if err := p.stopOnce(ctx, true); err != nil {
			return nil, err
		}
		var config Object
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &config); err != nil {
				return nil, err
			}
		}
		return nil, p.machine.Init(ctx, config)
	case "get_state":
		return json.Marshal(p.machine.State())
	case "get_form":
		return formPair()
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "get_command":
		return json.Marshal(commands())
	case "get_service":
		services, err := p.machine.services()
		if err != nil {
			return nil, err
		}
		return json.Marshal(services)
	case "get_api":
		return json.Marshal(httpAPIs())
	case "get_page", "close":
		return nil, nil
	case "handle_commands":
		var event sdk.MutableEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return nil, err
		}
		if event.Type != "plugin.action" || len(event.Data) == 0 {
			return nil, nil
		}
		var fields Object
		if err := json.Unmarshal(event.Data, &fields); err != nil {
			return nil, err
		}
		_, err := p.machine.Main(ctx, &Event{Data: fields})
		return nil, err
	case Action:
		_, err := p.machine.Main(ctx, nil)
		return nil, err
	case "private_once":
		var id uint64
		if err := json.Unmarshal(raw, &id); err != nil {
			return nil, err
		}
		return nil, p.runOnce(ctx, id)
	case "http":
		var request sdk.HTTPRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if request.Key != Action || request.Method != "GET" || request.Path != "/cloudflare_speedtest" {
			return nil, errors.New("unsupported CloudflareSpeedTest route")
		}
		result, err := p.machine.api(ctx, request.CredentialMatches["apikey"])
		if err != nil {
			return nil, err
		}
		var message any
		if result.Message != "" {
			message = result.Message
		}
		return sdk.JSONHTTPResponse(200, Object{"success": result.Success, "message": message,
			"message_i18n": message, "data": Object{}})
	case "stop_service", "stop":
		p.lifeMu.Lock()
		defer p.lifeMu.Unlock()
		return nil, p.stopOnce(ctx, true)
	default:
		return nil, errors.New("unsupported CloudflareSpeedTest method")
	}
}

func (p *Plugin) scheduleOnce(ctx context.Context, due time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.onceMu.Lock()
	defer p.onceMu.Unlock()
	p.nextID++
	p.once = &onceJob{id: p.nextID, due: due, stop: make(chan struct{}), done: make(chan struct{})}
	return nil
}

func (p *Plugin) startOnce(ctx context.Context) error {
	p.onceMu.Lock()
	job := p.once
	p.onceMu.Unlock()
	if job == nil {
		return errors.New("CloudflareSpeedTest one-shot has no pending schedule")
	}
	return (sdk.Manager{Host: activeCaller(ctx)}).SubmitPrivateScheduler(ctx, "private_once", job.id)
}

func (p *Plugin) stopOnce(ctx context.Context, join bool) error {
	p.onceMu.Lock()
	job := p.once
	if job == nil {
		p.onceMu.Unlock()
		return nil
	}
	p.once = nil
	close(job.stop)
	running := job.running
	p.onceMu.Unlock()
	if running && join {
		select {
		case <-job.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (p *Plugin) runOnce(ctx context.Context, id uint64) error {
	p.onceMu.Lock()
	job := p.once
	if job == nil || job.id != id || job.claimed {
		p.onceMu.Unlock()
		return nil
	}
	job.claimed = true
	p.onceMu.Unlock()
	defer func() {
		p.onceMu.Lock()
		if p.once == job {
			p.once = nil
		}
		close(job.done)
		p.onceMu.Unlock()
	}()
	timer := time.NewTimer(time.Until(job.due))
	defer timer.Stop()
	select {
	case <-job.stop:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	p.onceMu.Lock()
	if p.once != job || time.Since(job.due) > time.Second {
		p.onceMu.Unlock()
		return nil
	}
	job.running = true
	p.onceMu.Unlock()
	_, err := p.machine.Main(ctx, nil)
	return err
}

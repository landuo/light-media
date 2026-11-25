package removelink

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"os/user"
	"sync"
	"time"

	plugins "light-media/builtinplugins/internal/sdk"
)

//go:embed form.json
var form json.RawMessage

//go:embed defaults.json
var defaults json.RawMessage

func Manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "RemoveLink", Name: "清理媒体文件", Description: "媒体文件清理工具：支持硬链接文件清理、STRM文件清理、刮削文件清理（元数据、图片、字幕）、转移记录清理、种子联动删除等功能", Icon: "Ombi_A.png", Version: "1.0.0", Author: "DzAvril", AuthorURL: "https://github.com/DzAvril", PluginOrder: 0, Runtime: "go-executable", Entry: "remove-link", HostAPIMin: 1, HostAPIMax: 1,
		Methods:        []string{"init", "get_state", "get_form", "get_render_mode", "get_api", "get_page", "close", "stop_service", "stop", "cleanup_continue", "private_watch", "private_observer", "private_timer"},
		Capabilities:   []string{plugins.StorageExistsCapability, plugins.StorageListCapability, plugins.StorageDeleteCapability, plugins.TransferHistoryReadCapability, plugins.TransferHistoryDeleteCapability, plugins.EventSendCapability, plugins.PostMessageCapability, plugins.SystemMessagePutCapability, plugins.LogWriteCapability, plugins.SettingsReadCapability, plugins.BackgroundSubmitCapability, plugins.CleanupSubmitCapability},
		Frontend:       &plugins.Frontend{Contract: plugins.FrontendFederationContract, APIContract: plugins.FrontendAPIContract, Root: "frontend", Entry: "remoteEntry.js", Exposes: []string{"Config"}, Shared: []string{"vue"}},
		RuntimeSupport: &plugins.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}}}
}

type Plugin struct {
	mu, lifeMu  sync.Mutex
	machine     *Machine
	nextPass    uint64
	passes      map[uint64]*cleanupPass
	nextWatch   uint64
	watch       *watchRuntime
	newObserver func(Watch) (observer, error)
}

func NewPlugin() *Plugin {
	m := New(nil)
	if home, present := os.LookupEnv("HOME"); present {
		if home == "" {
			home = "/"
		}
		m.Home = home
	} else if account, err := user.Current(); err == nil {
		m.Home = account.HomeDir
	} else {
		// Preserve expanduser's unresolved marker until a path actually uses it.
		m.Home = "~"
	}
	return &Plugin{machine: m, passes: map[uint64]*cleanupPass{}, newObserver: sourceObserver}
}
func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	switch method {
	case "init":
		p.lifeMu.Lock()
		defer p.lifeMu.Unlock()
		if e := (plugins.Logger{Host: host}).Log(ctx, plugins.LogInfo, "初始化媒体文件清理插件"); e != nil {
			return nil, e
		}
		if e := p.machine.applyConfig(raw); e != nil {
			return nil, e
		}
		// Observer dispatch finishes before flushing source tasks. Reinit loads
		// the new configuration before Machine.stop drains its old queue.
		if e := (plugins.Logger{Host: host}).Log(ctx, plugins.LogDebug, "开始停止服务"); e != nil {
			return nil, e
		}
		if e := p.stopObservers(ctx); e != nil {
			return nil, e
		}
		p.mu.Lock()
		p.nextWatch++
		w := newWatchRuntime(p.nextWatch)
		p.watch = w
		p.machine.timerWake = w.signal
		p.mu.Unlock()
		e := p.run(ctx, host, func(op *operation) error {
			ops := op.Ops.(*sdkOps)
			ops.watch = func(spec Watch) error { return p.addObserver(w, spec, ops) }
			ops.watchStartFailed = func() { p.machine.metaMu.Lock(); p.machine.unstartedObservers++; p.machine.metaMu.Unlock() }
			return op.initConfigured(true)
		})
		if e != nil {
			w.abort()
			<-w.done
			return nil, e
		}
		w.mu.Lock()
		empty := len(w.lanes) == 0
		w.mu.Unlock()
		if empty {
			w.close()
		} else {
			w.signal()
		}
		return json.RawMessage(`null`), nil
	case "get_state":
		return json.Marshal(p.machine.StateValue())
	case "get_form":
		return json.Marshal([]json.RawMessage{form, defaults})
	case "get_render_mode":
		return json.RawMessage(`["vue","frontend"]`), nil
	case "cleanup_continue":
		return nil, p.continueCleanup(ctx, raw, host)
	case "private_watch":
		return nil, p.runWatch(ctx, raw, host)
	case "private_observer":
		return nil, p.runObserver(ctx, raw, host)
	case "private_timer":
		return nil, p.runTimer(ctx, raw, host)
	case "stop_service":
		p.lifeMu.Lock()
		defer p.lifeMu.Unlock()
		if e := (plugins.Logger{Host: host}).Log(ctx, plugins.LogDebug, "开始停止服务"); e != nil {
			return nil, e
		}
		if e := p.stopObservers(ctx); e != nil {
			return nil, e
		}

		// A real host cleanup-phase continuation is required for large flushes.
		// A denied admission remains an error; tasks are not silently dropped.
		return nil, p.run(ctx, host, func(op *operation) error {
			op.stopStarted(true)
			return nil
		})
	case "close", "stop", "get_service", "get_command", "get_api", "get_page", "get_dashboard", "get_dashboard_meta", "get_auth_providers", "get_module":
		return json.RawMessage(`null`), nil
	}
	return nil, errors.New("unsupported RemoveLink method")
}

func (p *Plugin) timerDue() *time.Time {
	p.machine.queueMu.Lock()
	defer p.machine.queueMu.Unlock()
	if p.machine.TimerDue == nil {
		return nil
	}
	t := *p.machine.TimerDue
	return &t
}

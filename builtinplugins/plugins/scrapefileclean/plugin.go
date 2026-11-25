package scrapefileclean

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	sdk "light-media/builtinplugins/internal/sdk"
)

//go:embed form.json
var formJSON json.RawMessage

func Manifest() sdk.Manifest {
	return sdk.Manifest{
		SchemaVersion: 1, PluginID: "ScrapeFileClean", Name: "源文件联动清理",
		Description: "为手动清理下载目录设计：手动删除源文件后，自动联动清理媒体库中对应的硬链接文件、刮削文件（元数据、图片、字幕）与转移记录，支持延迟删除防止误删",
		Version:     "1.0.0", Icon: "clean.png", Author: "xlmc", AuthorURL: "https://github.com/xlmc",
		Runtime: "go-executable", Entry: "scrape-file-clean", HostAPIMin: 1, HostAPIMax: 1,
		ManagedPrivateScheduler: true,
		RuntimeSupport:          &sdk.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}},
		Methods:                 []string{"init", "get_state", "get_form", "get_render_mode", "get_api", "get_page", "get_command", "close", "stop_service", "stop", "private_watch"},
		Capabilities: []string{sdk.StorageExistsCapability, sdk.ScrapeCleanRemoveCapability, sdk.TransferHistoryReadCapability, sdk.TransferHistoryDeleteCapability,
			sdk.EventSendCapability, sdk.PostMessageCapability, sdk.SystemMessagePutCapability, sdk.LogWriteCapability, sdk.BackgroundSubmitCapability},
	}
}

type Event struct {
	Kind, Path, Dest string
	Directory        bool
}

type Watcher interface {
	Events() <-chan Event
	Close() error
}

type monitor struct {
	id       uint64
	ctx      context.Context
	cancel   context.CancelFunc
	watchers []Watcher
	roots    []authorizedRoot
	pumps    sync.WaitGroup
	observed chan Event
	done     chan struct{}
	claimed  bool
	lost     chan struct{}
	lossOnce sync.Once
	lossErr  error
}

func (m *monitor) reportLoss(err error) {
	m.lossOnce.Do(func() {
		m.lossErr = err
		close(m.lost)
	})
}

func (m *monitor) loss() error {
	select {
	case <-m.lost:
		return m.lossErr
	default:
		return nil
	}
}

func (m *monitor) pump(w Watcher) {
	m.pumps.Add(1)
	go func() {
		defer m.pumps.Done()
		for {
			var event Event
			var ok bool
			select {
			case <-m.ctx.Done():
				return
			case event, ok = <-w.Events():
				if !ok {
					return
				}
			}
			select {
			case m.observed <- event:
			case <-m.ctx.Done():
				return
			default:
				m.reportLoss(errors.New("plugin observation queue overflow; reload to rescan"))
				return
			}
		}
	}()
}

type Plugin struct {
	lifeMu     sync.Mutex
	mu         sync.Mutex
	machine    *Machine
	monitor    *monitor
	generation uint64
}

func New() *Plugin {
	m := NewMachine(nil)
	m.Home, _ = os.UserHomeDir()
	m.UserHome = func(name string) (string, error) {
		account, err := user.Lookup(name)
		if err != nil {
			return "", err
		}
		return account.HomeDir, nil
	}
	return &Plugin{machine: m}
}

func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, host sdk.HostCaller) (json.RawMessage, error) {
	switch method {
	case "init":
		p.lifeMu.Lock()
		defer p.lifeMu.Unlock()
		return nil, p.init(ctx, raw, host)
	case "get_state":
		return json.Marshal(p.machine.GetState())
	case "get_form":
		var form struct{ Conf, Model json.RawMessage }
		if err := json.Unmarshal(formJSON, &form); err != nil {
			return nil, err
		}
		return json.Marshal([]json.RawMessage{form.Conf, form.Model})
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "private_watch":
		return nil, p.watch(ctx, raw, host)
	case "stop_service":
		p.lifeMu.Lock()
		defer p.lifeMu.Unlock()
		p.stop(ctx, host)
		return nil, nil
	case "close", "stop", "get_api", "get_command", "get_page":
		return nil, nil
	default:
		return nil, errors.New("unsupported source cleanup method")
	}
}

func (p *Plugin) init(ctx context.Context, raw json.RawMessage, host sdk.HostCaller) error {
	p.stop(ctx, host)
	p.generation++
	lifetime, cancel := context.WithCancel(context.Background())
	w := &monitor{id: p.generation, ctx: lifetime, cancel: cancel, observed: make(chan Event, 1024), done: make(chan struct{}), lost: make(chan struct{})}
	o := &localOps{ctx: ctx, host: host}
	p.machine.Ops = o
	if len(raw) == 0 {
		raw = json.RawMessage(`null`)
	}
	err := p.machine.init(raw, func(path string) {
		path = cleanPath(path)
		var native Watcher
		err := o.authorize(path)
		if err == nil {
			pin := o.roots[len(o.roots)-1]
			native, err = NativeWatch(w.ctx, path, func(dir string) (*os.File, error) {
				if err := w.ctx.Err(); err != nil {
					return nil, err
				}
				if !inside(pin.path, dir) || internalPath(dir) {
					return nil, fs.ErrPermission
				}
				return openNativeDirectory(dir, &pin)
			}, w.reportLoss)
		}
		if err != nil {
			message := "目录监控启动失败：" + path + " - " + err.Error()
			log(ctx, host, sdk.LogError, message)
			payload, _ := json.Marshal(map[string]string{"message": message, "title": "源文件联动清理"})
			_, _ = host(ctx, sdk.SystemMessagePutCapability, payload)
			return
		}
		w.watchers = append(w.watchers, native)
		w.pump(native)
	})
	p.machine.Ops = nil
	p.flushErrors(ctx, host)
	if err == nil && !p.machine.Config.Enabled {
		log(ctx, host, sdk.LogInfo, "源文件联动清理插件未启用")
	}
	if err != nil || len(w.watchers) == 0 || ctx.Err() != nil {
		w.cancel()
		for _, watcher := range w.watchers {
			_ = watcher.Close()
		}
		w.pumps.Wait()
		if err == nil {
			err = ctx.Err()
		}
		return err
	}
	w.roots = o.roots
	go func() {
		w.pumps.Wait()
		close(w.observed)
	}()
	p.mu.Lock()
	p.monitor = w
	p.mu.Unlock()
	err = (sdk.Manager{Host: host}).SubmitPrivateScheduler(ctx, "private_watch", map[string]uint64{"generation": w.id})
	if err != nil {
		p.stop(ctx, host)
	}
	return err
}

func (p *Plugin) stop(ctx context.Context, host sdk.HostCaller) {
	log(ctx, host, sdk.LogDebug, "开始停止服务")
	p.mu.Lock()
	w := p.monitor
	p.monitor = nil
	p.mu.Unlock()
	if w != nil {
		w.cancel()
		for _, watcher := range w.watchers {
			_ = watcher.Close()
		}
		w.pumps.Wait()
		if w.claimed {
			<-w.done
		}
	}
	if pending := len(p.machine.Queue); pending > 0 {
		log(ctx, host, sdk.LogInfo, fmt.Sprintf("放弃 %d 个未到期的延迟删除任务（停止服务时不提前执行）", pending))
	}
	p.machine.Stop()
	log(ctx, host, sdk.LogDebug, "服务停止完成")
}

func (p *Plugin) watch(ctx context.Context, raw json.RawMessage, host sdk.HostCaller) error {
	var input struct{ Generation uint64 }
	if err := json.Unmarshal(raw, &input); err != nil {
		return err
	}
	p.mu.Lock()
	w := p.monitor
	if w == nil || input.Generation != w.id || w.claimed {
		p.mu.Unlock()
		return nil
	}
	w.claimed = true
	p.mu.Unlock()
	defer close(w.done)
	defer w.cancel()
	defer func() { p.machine.Ops = nil }()
	o := &localOps{ctx: ctx, host: host, roots: w.roots, loss: w.loss}
	p.machine.Ops = o
	for {
		if w.ctx.Err() != nil || ctx.Err() != nil {
			return ctx.Err()
		}
		var timer *time.Timer
		var alarm <-chan time.Time
		if due := p.machine.TimerDue; due != nil {
			timer = time.NewTimer(due.Sub(p.machine.Now()))
			alarm = timer.C
		}
		var event Event
		var ready, tick bool
		select {
		case <-w.ctx.Done():
		case <-ctx.Done():
		case <-w.lost:
		case <-alarm:
			tick = true
		case event, ready = <-w.observed:
		}
		if timer != nil {
			timer.Stop()
		}
		if w.ctx.Err() != nil || ctx.Err() != nil {
			return ctx.Err()
		}
		if err := w.loss(); err != nil {
			log(ctx, host, sdk.LogError, "目录监控已停止，需重载后重新扫描："+err.Error())
			return err
		}
		if !ready && !tick {
			return nil
		}
		o.listed = map[string]Identity{}
		queued := len(p.machine.Queue)
		var err error
		switch {
		case tick:
			p.machine.Tick()
		case event.Kind == "created":
			err = p.machine.Created(event.Path, event.Directory)
		case event.Kind == "moved":
			err = p.machine.Moved(event.Path, event.Dest, event.Directory)
		case event.Kind == "deleted":
			err = p.machine.Deleted(event.Path, event.Directory)
		}
		if len(p.machine.Queue) > queued {
			log(ctx, host, sdk.LogInfo, fmt.Sprintf("文件 %s 加入延迟删除队列，延迟 %d 秒", filepath.Base(p.machine.Queue[queued].Path), p.machine.Config.Delay))
		}
		if err != nil {
			log(ctx, host, sdk.LogError, "处理目录变更失败："+err.Error())
		}
		p.flushErrors(ctx, host)
		if o.fence != nil {
			return o.fence
		}
	}
}

func (p *Plugin) flushErrors(ctx context.Context, host sdk.HostCaller) {
	for _, err := range p.machine.Errors {
		log(ctx, host, sdk.LogError, "联动清理失败："+err.Error())
	}
	p.machine.Errors = nil
}

func log(ctx context.Context, host sdk.HostCaller, level sdk.LogLevel, message string) {
	if len(message) > sdk.LogMessageLimit {
		message = message[:sdk.LogMessageLimit]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	_ = (sdk.Logger{Host: host}).Log(ctx, level, message)
}

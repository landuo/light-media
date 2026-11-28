package sitemessageautoread

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	sdk "light-media/builtinplugins/internal/sdk"
	"light-media/internal/plugins/schedule"
)

const (
	ID      = "SiteMessageAutoRead"
	name    = "站点短讯自动已读"
	run     = "run"
	runOnce = "run_once"
)

func Manifest() sdk.Manifest {
	return sdk.Manifest{
		SchemaVersion: 1, PluginID: ID, Name: name, Version: "1.0.1", Icon: "world.png",
		Description: "所选站点的未读短讯标题包含任一关键词时，自动标为已读。",
		Author:      "landuo", AuthorURL: "https://github.com/landuo", PluginOrder: 67,
		Runtime: "go-executable", Entry: "site-message-autoread", HostAPIMin: 1, HostAPIMax: 1,
		ManagedPublicServices: true,
		Methods: []string{
			"init", "get_state", "get_form", "get_render_mode", "get_service", "get_api",
			run, runOnce, "close", "stop_service", "stop",
		},
		Capabilities: []string{
			sdk.SitesReadCapability, sdk.SitesMessagesMarkReadCapability, sdk.LogWriteCapability,
			sdk.BaseConfigSaveOrigin, sdk.BaseUpdateConfig, sdk.BackgroundSubmitCapability,
		},
	}
}

type runInput struct {
	Generation uint64 `json:"generation"`
}

type Plugin struct {
	mu          sync.Mutex
	runMu       sync.Mutex
	config      Config
	generation  uint64
	oncePending bool
	cancelRun   context.CancelFunc
}

func New() *Plugin { return &Plugin{} }

func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, host sdk.HostCaller) (json.RawMessage, error) {
	switch method {
	case "init":
		return nil, p.initialize(ctx, raw, host)
	case "get_state":
		p.mu.Lock()
		defer p.mu.Unlock()
		return json.Marshal(p.config.Scheduled())
	case "get_service":
		return p.services()
	case "get_form":
		return getForm(ctx, host)
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "get_api":
		return json.RawMessage(`[]`), nil
	case run, runOnce:
		var input runInput
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		return p.execute(ctx, host, input, method == runOnce)
	case "close", "stop_service", "stop":
		p.replaceConfig(Config{})
		return nil, nil
	default:
		return nil, errors.New("unsupported site message auto read method")
	}
}

func (p *Plugin) replaceConfig(config Config) uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancelRun != nil {
		p.cancelRun()
	}
	p.generation++
	p.config = config
	p.oncePending = false
	return p.generation
}

func (p *Plugin) initialize(ctx context.Context, raw json.RawMessage, host sdk.HostCaller) error {
	config, err := ParseConfig(raw)
	generation := p.replaceConfig(config)
	if err != nil || !config.OnlyOnce {
		return err
	}
	base := sdk.Base{Host: host}
	saved, err := base.ConfigSaveOrigin(ctx)
	if err != nil || !saved {
		return err
	}
	config.OnlyOnce = false
	if _, err := base.UpdateConfig(ctx, config); err != nil {
		return err
	}
	p.mu.Lock()
	if p.generation != generation {
		p.mu.Unlock()
		return nil
	}
	p.oncePending = true
	p.mu.Unlock()
	err = (sdk.Manager{Host: host}).Submit(ctx, runOnce, runInput{Generation: generation})
	if err != nil {
		p.mu.Lock()
		if p.generation == generation {
			p.oncePending = false
		}
		p.mu.Unlock()
	}
	return err
}

func (p *Plugin) services() (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.config.Scheduled() {
		return json.RawMessage(`[]`), nil
	}
	return json.Marshal([]any{map[string]any{
		"id": ID, "name": name, "handler": run,
		"trigger": schedule.Spec{Kind: "cron", Cron: p.config.Cron, LocalTimezone: true},
		"input":   runInput{Generation: p.generation},
	}})
}

func (p *Plugin) execute(ctx context.Context, host sdk.HostCaller, input runInput, once bool) (json.RawMessage, error) {
	p.runMu.Lock()
	defer p.runMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	config := p.config
	allowed := config.Scheduled()
	if once {
		allowed = p.oncePending
	}
	if input.Generation != p.generation || !allowed {
		p.mu.Unlock()
		return nil, nil
	}
	if once {
		p.oncePending = false
	}
	ctx, cancel := context.WithCancel(ctx)
	p.cancelRun = cancel
	p.mu.Unlock()
	defer func() {
		cancel()
		p.mu.Lock()
		p.cancelRun = nil
		p.mu.Unlock()
	}()

	success, summary, err := markRead(ctx, host, config)
	if err != nil {
		return nil, err
	}
	if once && !success {
		return nil, errors.New(summary)
	}
	return sdk.ServiceResultTuple(success, summary)
}

func markRead(ctx context.Context, host sdk.HostCaller, config Config) (bool, string, error) {
	sites := sdk.Sites{Host: host}
	logger := sdk.Logger{Host: host}
	active, err := sites.Read(ctx, sdk.SiteHostRequestV1{ActiveOnly: true})
	if err != nil {
		_ = logger.Log(ctx, sdk.LogError, "读取已启用的站点列表失败")
		return false, "", err
	}
	siteNames := make(map[string]string, len(active.Items))
	for _, site := range active.Items {
		if site.Enabled {
			siteNames[site.ID] = site.Name
		}
	}

	titles := config.Titles()
	scanned, matched, marked, failed, failedSites, skippedSites := 0, 0, 0, 0, 0, 0
	for _, siteID := range config.Sites {
		siteName, ok := siteNames[siteID]
		if !ok {
			skippedSites++
			_ = logger.Log(ctx, sdk.LogWarning, fmt.Sprintf("跳过站点 %s：站点已停用或不存在", siteID))
			continue
		}
		result, err := sites.MarkMessagesRead(ctx, sdk.SiteMessagesMarkReadRequestV1{SiteID: siteID, Titles: titles})
		if ctx.Err() != nil {
			return false, "", ctx.Err()
		}
		if err != nil {
			failedSites++
			_ = logger.Log(ctx, sdk.LogError, fmt.Sprintf("%s：站内短讯处理失败", siteName))
			continue
		}
		if result.SkippedReason != "" {
			skippedSites++
			_ = logger.Log(ctx, sdk.LogWarning, fmt.Sprintf("%s：该站点的短讯收件箱暂未支持", siteName))
			continue
		}
		scanned += result.Scanned
		matched += result.Matched
		marked += result.Marked
		failed += result.Failed
		level := sdk.LogInfo
		if result.Failed > 0 || result.ErrorReason != "" {
			failedSites++
			level = sdk.LogWarning
		}
		message := fmt.Sprintf("%s：检查 %d 条，匹配 %d 条，已读 %d 条，失败 %d 条", siteName, result.Scanned, result.Matched, result.Marked, result.Failed)
		if result.ErrorReason != "" {
			message += "。 " + result.ErrorReason
		}
		_ = logger.Log(ctx, level, message)
	}
	summary := fmt.Sprintf("站内短讯检查 %d 条，匹配 %d 条，已读 %d 条，失败 %d 条。跳过 %d 个站点，%d 个站点处理失败。", scanned, matched, marked, failed, skippedSites, failedSites)
	level := sdk.LogInfo
	if failedSites > 0 || skippedSites > 0 {
		level = sdk.LogWarning
	}
	_ = logger.Log(ctx, level, summary)
	return failedSites == 0, summary, nil
}

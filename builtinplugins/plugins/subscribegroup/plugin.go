package subscribegroup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	sdk "light-media/builtinplugins/internal/sdk"
	"light-media/builtinplugins/plugins/removelink"
)

func Manifest() sdk.Manifest {
	return sdk.Manifest{
		SchemaVersion: 1, PluginID: "SubscribeGroup", Name: "订阅规则自动填充",
		Description: "电视剧下载后自动添加官组等信息到订阅；添加订阅后根据二级分类名称自定义订阅规则。",
		Icon:        "teamwork.png", Version: "1.0.0", Author: "thsrite", AuthorURL: "https://github.com/thsrite",
		PluginOrder: 26, Runtime: "go-executable", Entry: "subscribe-group", HostAPIMin: 1, HostAPIMax: 1,
		ManagedPublicServices: true, HasPage: true,
		Methods: []string{
			"init", "get_state", "get_form", "get_render_mode", "get_page",
			"get_command", "get_api", "get_service", "subscribe_notice", "download_notice",
			"close", "stop_service", "stop",
		},
		Capabilities: []string{
			sdk.BaseGetData, sdk.BaseDelData, sdk.BaseUpdateConfig, sdk.SitesReadCapability,
			sdk.MediaRecognizeCapability, sdk.SubscriptionsReadCapability, sdk.SubscriptionsUpdateCapability,
			sdk.DownloadHistoryReadCapability, sdk.SettingsReadCapability, sdk.LogWriteCapability,
		},
		BroadcastHandlers: []sdk.BroadcastHandler{{Event: "subscribe.added", Method: "subscribe_notice"}, {Event: "download.added", Method: "download_notice"}},
		RuntimeSupport:    &sdk.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}},
	}
}

type groupConfig struct {
	Enabled       any `json:"enabled"`
	Category      any `json:"category"`
	Clear         any `json:"clear"`
	ClearHandle   any `json:"clear_handle"`
	UpdateDetails any `json:"update_details"`
	UpdateConfs   any `json:"update_confs"`
}

type Plugin struct {
	mu     sync.Mutex
	config groupConfig
	rules  map[string]Rule
}

func NewPlugin() *Plugin {
	return &Plugin{
		config: groupConfig{Enabled: false, Category: false, Clear: false, ClearHandle: false, UpdateDetails: []string{}},
		rules:  make(map[string]Rule),
	}
}

func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, caller sdk.HostCaller) (json.RawMessage, error) {
	switch method {
	case "get_form":
		return Form(), nil
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "get_page":
		page, err := Page(ctx, caller)
		if err != nil {
			return nil, err
		}
		return json.Marshal(page)
	case "get_command", "get_api", "get_service", "close", "stop_service", "stop":
		return nil, nil
	}
	// Each instance has one source cache and history order. Keep that state
	// serialized across reinitialization and its two ordinary event handlers.
	p.mu.Lock()
	defer p.mu.Unlock()
	switch method {
	case "init":
		return nil, p.initialize(ctx, raw, caller)
	case "get_state":
		if removelink.Truth(p.config.Enabled) {
			return json.Marshal(p.config.Enabled)
		}
		return json.Marshal(p.config.Category)
	case "subscribe_notice":
		return nil, subscribeAdded(ctx, caller, raw, removelink.Truth(p.config.Category), p.rules, time.Time{})
	case "download_notice":
		if !removelink.Truth(p.config.Enabled) {
			return nil, nil
		}
		details, err := sourceUpdateDetails(p.config.UpdateDetails)
		if err != nil {
			return nil, err
		}
		return nil, downloadNotice(ctx, raw, caller, true, details, time.Time{})
	default:
		return nil, errors.New("unsupported SubscribeGroup method")
	}
}

func (p *Plugin) initialize(ctx context.Context, raw json.RawMessage, caller sdk.HostCaller) error {
	if len(raw) == 0 {
		return nil
	}
	value, err := removelink.Decode(raw)
	if err != nil || !removelink.Truth(value) {
		return err
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return errors.New("SubscribeGroup configuration has no get method")
	}
	p.config = groupConfig{
		Enabled: fields["enabled"], Category: fields["category"], Clear: fields["clear"], ClearHandle: fields["clear_handle"],
		UpdateDetails: fields["update_details"], UpdateConfs: fields["update_confs"],
	}
	if !removelink.Truth(p.config.UpdateDetails) {
		p.config.UpdateDetails = []string{}
	}
	logger := sdk.Logger{Host: caller}
	if removelink.Truth(p.config.UpdateConfs) {
		records, err := (sdk.Sites{Host: caller}).Read(ctx, sdk.SiteHostRequestV1{ActiveOnly: true})
		if err != nil {
			return err
		}
		sites := make([]Site, 0, len(records.Items))
		for _, row := range records.Items {
			sites = append(sites, Site{ID: row.ID, SourceID: row.SourceID, Name: row.Name, Active: row.Enabled})
		}
		for category, rule := range ParseRules(fmt.Sprint(p.config.UpdateConfs), sites) {
			p.rules[category] = rule
		}
		_ = logger.Log(ctx, sdk.LogInfo, fmt.Sprintf("获取到二级分类自定义配置 %d 个", len(p.rules)))
	} else {
		p.rules = make(map[string]Rule)
	}
	base := sdk.Base{Host: caller}
	for _, clear := range []struct {
		flag *any
		key  string
		log  string
	}{{&p.config.ClearHandle, "history_handle", "已处理历史清理完成"}, {&p.config.Clear, "history", "历史记录清理完成"}} {
		if !removelink.Truth(*clear.flag) {
			continue
		}
		if err := base.DelData(ctx, &clear.key); err != nil {
			return err
		}
		*clear.flag = false
		if _, err := base.UpdateConfig(ctx, p.config); err != nil {
			return err
		}
		_ = logger.Log(ctx, sdk.LogInfo, clear.log)
	}
	return nil
}

func sourceUpdateDetails(value any) ([]string, error) {
	if !removelink.Truth(value) {
		return nil, nil
	}
	var details []string
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			if name, ok := item.(string); ok {
				details = append(details, name)
			}
		}
	case []string:
		details = v
	case map[string]any:
		for name := range v {
			details = append(details, name)
		}
	case string:
		for _, name := range []string{"分辨率", "资源质量", "特效", "制作组", "站点"} {
			if strings.Contains(v, name) {
				details = append(details, name)
			}
		}
	default:
		return nil, errors.New("SubscribeGroup update details have no length")
	}
	return details, nil
}

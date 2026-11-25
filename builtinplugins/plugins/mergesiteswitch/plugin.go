package mergesiteswitch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"sync"

	sdk "light-media/builtinplugins/internal/sdk"
)

const switchCapability = "sites.switch"

type selection struct {
	field, plugin string
	custom        bool
}

func (s selection) follow() string {
	return s.field[:len(s.field)-len("_sites")] + "_follow_enable_sites"
}

var selections = []selection{
	{"search_sites", "", false},
	{"rss_sites", "", false},
	{"signin_sites", "AutoSignIn", true},
	{"login_sites", "AutoSignIn", true},
	{"statistic_sites", "SiteStatistic", true},
	{"iyuu_seed_sites", "IYUUAutoSeed", true},
	{"brush_flow_sites", "BrushFlow", false},
	{"cross_seed_sites", "CrossSeed", true},
}

func Manifest() sdk.Manifest {
	return sdk.Manifest{
		SchemaVersion: 1, PluginID: "MergeSiteSwitch", Name: "聚合站点开关", Description: "统一管理所有与站点相关的开关。",
		Icon: "world.png", Version: "1.0.0", Author: "hotlcc", AuthorURL: "https://github.com/hotlcc",
		PluginOrder: 66, Runtime: "go-executable", Entry: "merge-site-switch", HostAPIMin: 1, HostAPIMax: 1,
		ManagedPublicServices: true,
		Methods:               []string{"init", "get_state", "get_form", "get_render_mode", "get_page", "get_api", "get_command", "get_service", "listen_site_updated_event", "listen_site_deleted_event", "close", "stop_service", "stop"},
		Capabilities:          []string{sdk.BaseGetConfig, sdk.BaseUpdateConfig, sdk.BaseConfigSaveOrigin, sdk.SitesReadCapability, sdk.LogWriteCapability, switchCapability},
		BroadcastHandlers: []sdk.BroadcastHandler{
			{Event: "site.updated", Method: "listen_site_updated_event"},
			{Event: "site.deleted", Method: "listen_site_deleted_event"},
		},
	}
}

type Plugin struct {
	mu     sync.Mutex
	config map[string]json.RawMessage
}

func New() *Plugin { return &Plugin{} }

type request struct {
	Action string          `json:"action"`
	Field  string          `json:"field,omitempty"`
	Value  json.RawMessage `json:"value,omitempty"`
	Domain string          `json:"domain,omitempty"`
}

type customSite struct {
	ID   json.RawMessage `json:"id"`
	Name string          `json:"name"`
}

func call(ctx context.Context, host sdk.HostCaller, in request, out any) error {
	if host == nil {
		return errors.New("site switch requires an active host caller")
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	value, err := host(ctx, switchCapability, raw)
	if err != nil || out == nil {
		return err
	}
	return json.Unmarshal(value, out)
}

func installed(ctx context.Context, host sdk.HostCaller) ([]string, error) {
	var ids []string
	err := call(ctx, host, request{Action: "installed"}, &ids)
	return ids, err
}

func (s selection) applies(ids []string) bool {
	return s.plugin == "" || slices.Contains(ids, s.plugin)
}

func anyFollow(config map[string]json.RawMessage, ids []string) bool {
	for _, s := range selections {
		// In the original, CrossSeed follows events only while another
		// installed, enabled follow switch opens the event gate.
		if s.plugin != "CrossSeed" && s.applies(ids) && truth(config[s.follow()]) {
			return true
		}
	}
	return false
}

func truth(raw json.RawMessage) bool {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return false
	}
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	case json.Number:
		n, _ := v.Float64()
		return n != 0
	}
	return false
}

func siteList(raw json.RawMessage) ([]json.RawMessage, error) {
	ids := []json.RawMessage{}
	if !truth(raw) {
		return ids, nil
	}
	err := json.Unmarshal(raw, &ids)
	return ids, err
}

func sameID(a, b json.RawMessage) bool {
	a = bytes.TrimSpace(a)
	b = bytes.TrimSpace(b)
	if bytes.Equal(a, b) {
		return true
	}
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	if a[0] == '"' || b[0] == '"' {
		var sa, sb string
		return a[0] == '"' && b[0] == '"' && json.Unmarshal(a, &sa) == nil && json.Unmarshal(b, &sb) == nil && sa == sb
	}
	// Native source identifiers are integers. Preserve their precision and
	// never match them against the numeric-looking strings of custom sites.
	na, ea := strconv.ParseInt(string(a), 10, 64)
	nb, eb := strconv.ParseInt(string(b), 10, 64)
	return ea == nil && eb == nil && na == nb
}

func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, host sdk.HostCaller) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch method {
	case "init":
		var config map[string]json.RawMessage
		if len(raw) != 0 {
			if err := json.Unmarshal(raw, &config); err != nil {
				return nil, err
			}
		}
		p.config = config
		fromSave, err := (sdk.Base{Host: host}).ConfigSaveOrigin(ctx)
		if err != nil || !fromSave {
			return nil, err
		}
		return nil, p.save(ctx, host)
	case "get_state":
		ids, err := installed(ctx, host)
		if err != nil {
			return nil, err
		}
		return json.Marshal(anyFollow(p.config, ids))
	case "get_form":
		return form(ctx, host)
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "listen_site_updated_event", "listen_site_deleted_event":
		return nil, p.siteEvent(ctx, host, method == "listen_site_updated_event", raw)
	case "get_page", "get_api", "get_command", "get_service", "close", "stop_service", "stop":
		return nil, nil
	}
	return nil, errors.New("unsupported site switch method")
}

func get(ctx context.Context, host sdk.HostCaller, key string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := call(ctx, host, request{Action: "get", Field: key}, &raw)
	if err == nil && !truth(raw) {
		raw = json.RawMessage(`[]`)
	}
	return raw, err
}

func set(ctx context.Context, host sdk.HostCaller, key string, raw json.RawMessage) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`null`)
	}
	return call(ctx, host, request{Action: "set", Field: key, Value: raw}, nil)
}

func (p *Plugin) save(ctx context.Context, host sdk.HostCaller) error {
	config := p.config
	if config == nil {
		config = map[string]json.RawMessage{}
	}
	enabled, err := siteList(config["enable_sites"])
	if err != nil {
		return err
	}
	var custom []customSite
	if err = call(ctx, host, request{Action: "custom"}, &custom); err != nil {
		return err
	}
	for _, s := range selections {
		if !truth(config[s.follow()]) {
			continue
		}
		ids := slices.Clone(enabled)
		if s.custom {
			previous, err := siteList(config[s.field])
			if err != nil {
				return err
			}
			for _, id := range previous {
				if truth(id) && slices.ContainsFunc(custom, func(site customSite) bool { return sameID(id, site.ID) }) {
					ids = append(ids, id)
				}
			}
		}
		config[s.field], err = json.Marshal(ids)
		if err != nil {
			return err
		}
	}
	if _, err = (sdk.Base{Host: host}).UpdateConfig(ctx, config); err != nil {
		return err
	}
	logger := sdk.Logger{Host: host}
	_ = logger.Log(ctx, sdk.LogInfo, "插件配置更新完成")
	for _, key := range []string{"enable_sites", "search_sites", "rss_sites"} {
		if err := set(ctx, host, key, config[key]); err != nil {
			return err
		}
	}
	ids, err := installed(ctx, host)
	if err != nil {
		return err
	}
	for _, s := range selections {
		if s.plugin != "" && s.applies(ids) {
			if err := set(ctx, host, s.field, config[s.field]); err != nil {
				return err
			}
		}
	}
	return nil
}

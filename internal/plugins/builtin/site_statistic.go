package builtin

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"slices"

	"light-media/internal/plugins"
	"light-media/internal/plugins/statistic"
)

type SiteStatisticConfiguration struct {
	Enabled       bool   `json:"enabled"`
	OnlyOnce      bool   `json:"onlyonce"`
	DashboardType string `json:"dashboard_type"`
	NotifyType    string `json:"notify_type"`
	// MergeSiteSwitch stores this field in the source. SiteStatistic 1.9.7
	// never reads it; it does not filter charts, notifications or collection.
	StatisticSites []SiteReference `json:"statistic_sites,omitempty"`
}

func (c SiteStatisticConfiguration) Validate() error {
	if !slices.Contains([]string{"", "today", "total", "all"}, c.DashboardType) || !slices.Contains([]string{"", "inc", "all"}, c.NotifyType) || len(c.StatisticSites) > 2048 {
		return errors.New("invalid statistic configuration")
	}
	return nil
}

//go:embed site_statistic_form.json
var siteStatisticForm json.RawMessage

func SiteStatisticManifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "SiteStatistic", Name: "站点数据统计", Description: "站点统计数据图表。", Icon: "statistic.png", Version: "1.9.7", Author: "lightolly,jxxghp", AuthorURL: "https://github.com/lightolly", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "get_page", "get_dashboard", "page", "dashboard", "stop"}, Capabilities: []string{"site.statistics.read", "site.statistics.configure", "site.statistics.refresh", "site.statistics.notify", "site.statistics.refresh_domain"}, HasPage: true, Dashboards: []string{"default"}, APIs: []plugins.HTTPAPI{{Key: "refresh_by_domain", Path: "/refresh_by_domain", Methods: []string{"GET"}, Auth: "apikey", Query: []plugins.HTTPParameter{{Name: "domain", Kind: "string", Required: true}, {Name: "apikey", Kind: "api_key", Required: true}}}}}
}

// OriginalSiteStatistic retains the actual initialized members. Its caller serializes
// Handle and State; legacy SDK calls keep their historical per-call lifetime.
type OriginalSiteStatistic struct{ config SiteStatisticConfiguration }

func NewOriginalSiteStatistic() *OriginalSiteStatistic {
	return &OriginalSiteStatistic{config: SiteStatisticConfiguration{DashboardType: "today"}}
}
func (p *OriginalSiteStatistic) State() bool { return p.config.Enabled }
func (p *OriginalSiteStatistic) Configuration() SiteStatisticConfiguration {
	c := p.config
	c.StatisticSites = append([]SiteReference(nil), c.StatisticSites...)
	return c
}

func HandleSiteStatistic(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	p := NewOriginalSiteStatistic()
	// Legacy single-call SDK callers supply configuration on each invocation.
	// Only this compatibility wrapper seeds non-init calls; a retained object's
	// Handle always reads its own initialized members instead.
	if method != "stop" && method != "config_form" {
		var invocation plugins.Invocation
		if json.Unmarshal(raw, &invocation) != nil {
			return nil, errors.New("invalid statistic invocation")
		}
		if invocation.Action != "init" && invocation.Action != "reload" {
			if json.Unmarshal(invocation.Config, &p.config) != nil {
				return nil, errors.New("invalid statistic invocation")
			}
		}
	}
	return p.Handle(ctx, method, raw, host)
}

func (p *OriginalSiteStatistic) Handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	// Keep legacy SDK aliases while exposing the canonical extension methods.
	if method == "get_page" {
		method = "page"
	}
	if method == "get_dashboard" {
		method = "dashboard"
	}
	if method == "stop" {
		return json.RawMessage(`null`), nil
	}
	if method == "config_form" {
		return json.Marshal(map[string]any{"conf": siteStatisticForm, "model": map[string]any{"enabled": false, "onlyonce": false, "dashboard_type": "today"}})
	}
	var invocation plugins.Invocation
	var config SiteStatisticConfiguration
	if json.Unmarshal(raw, &invocation) != nil {
		return nil, errors.New("invalid statistic invocation")
	}
	if method == "invoke" && (invocation.Action == "init" || invocation.Action == "reload") {
		fields, err := originalStatelessConfigFields(invocation.Config)
		if err != nil {
			return nil, errors.New("invalid statistic invocation")
		}
		if len(fields) > 0 {
			if json.Unmarshal(invocation.Config, &config) != nil {
				return nil, errors.New("invalid statistic invocation")
			}
			if config.DashboardType == "" {
				config.DashboardType = "today"
			}
			p.config = config
		}
		config = p.config
		// Source writes config["onlyonce"] before its host work. A None input
		// with a retained true member fails there; do not clear that member.
		if fields == nil && config.OnlyOnce {
			return nil, errors.New("SiteStatistic onlyonce requires a configuration object")
		}
	} else {
		config = p.config
	}
	if config.Validate() != nil {
		return nil, errors.New("invalid statistic invocation")
	}
	if method == "invoke" {
		switch invocation.Action {
		case "api":
			var request plugins.HTTPRequest
			if json.Unmarshal(invocation.Input, &request) != nil || request.Key != "refresh_by_domain" || request.Method != "GET" {
				return nil, errors.New("invalid statistic HTTP invocation")
			}
			if !request.CredentialMatches["apikey"] {
				return plugins.JSONHTTPResponse(200, SiteStatisticResponse(false, "API密钥错误", map[string]any{}))
			}
			input, _ := json.Marshal(map[string]string{"domain": request.LastQuery("domain")})
			return host(ctx, "site.statistics.refresh_domain", input)
		case "init", "reload":
			return host(ctx, "site.statistics.configure", json.RawMessage(`{}`))
		case "service":
			return host(ctx, "site.statistics.refresh", invocation.Input)
		case "event":
			return host(ctx, "site.statistics.notify", invocation.Input)
		default:
			return nil, errors.New("unsupported statistic action")
		}
	}
	if method != "page" && method != "dashboard" {
		return nil, errors.New("unsupported statistic method")
	}
	raw, err := host(ctx, "site.statistics.read", json.RawMessage(`{}`))
	if err != nil {
		return nil, err
	}
	var snapshot statistic.Snapshot
	if json.Unmarshal(raw, &snapshot) != nil {
		return nil, errors.New("invalid statistic snapshot")
	}
	selected, err := statistic.Select(snapshot)
	if err != nil {
		return nil, err
	}
	if method == "page" {
		return json.Marshal(map[string]any{"render_mode": "vuetify", "page": statistic.Page(selected)})
	}
	mode := config.DashboardType
	if mode == "" {
		mode = "today"
	}
	return json.Marshal(map[string]any{"cols": map[string]int{"cols": 12}, "attrs": map[string]any{}, "elements": []map[string]any{{"component": "VRow", "content": statistic.Elements(selected, mode)}}})
}

func SiteStatisticResponse(success bool, message string, data any) map[string]any {
	return map[string]any{"success": success, "message": message, "message_i18n": message, "data": data}
}

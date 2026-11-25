package sitestatistic

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"sync"
	"time"

	plugins "light-media/builtinplugins/internal/sdk"
)

//go:embed form.json
var form json.RawMessage

//go:embed defaults.json
var defaults json.RawMessage

func Manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "SiteStatistic", Name: "站点数据统计", Description: "站点统计数据图表。", Icon: "statistic.png", Version: "1.0.0", Author: "lightolly,jxxghp", AuthorURL: "https://github.com/lightolly", PluginOrder: 1, Runtime: "go-executable", Entry: "site-statistic", HostAPIMin: 1, HostAPIMax: 1,
		Methods:           []string{"init", "get_state", "get_form", "get_page", "get_dashboard", "get_api", "get_render_mode", "stop_service", "stop", "close", "send_msg", "http", "private_refresh", "data_continue"},
		Capabilities:      []string{plugins.SitesReadCapability, plugins.SitesSourceAccountsCapability, plugins.SitesSourceIndexerCapability, plugins.SitesSourceRefreshCapability, plugins.SitesSourceRefreshAllCapability, plugins.BaseUpdateConfig, plugins.BaseGetData, plugins.BaseSaveData, plugins.PostMessageCapability, plugins.LogWriteCapability, plugins.BackgroundSubmitCapability},
		BroadcastHandlers: []plugins.BroadcastHandler{{Event: "site.refreshed", Method: "send_msg"}}, HasPage: true,
		APIs:           []plugins.HTTPAPI{{Key: "refresh_by_domain", Path: "/refresh_by_domain", Methods: []string{"GET"}, Auth: "apikey", Query: []plugins.HTTPParameter{{Name: "domain", Kind: "string", Required: true}, {Name: "apikey", Kind: "api_key", Required: true}}, Headers: []string{"x-moviepilot-locale", "x-locale", "accept-language"}}},
		RuntimeSupport: &plugins.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}}}
}

type dateJob struct {
	id     uint64
	due    time.Time
	active bool
}
type Plugin struct {
	mu, initMu, notifyMu sync.Mutex
	config               Config
	now                  func() time.Time
	wait                 func(context.Context, time.Time) error
	nextJob, nextPass    uint64
	jobs                 map[uint64]*dateJob
	passes               map[uint64]*dataPass
}

func New() *Plugin {
	return &Plugin{config: NewConfig(), now: time.Now, jobs: map[uint64]*dateJob{}, passes: map[uint64]*dataPass{}}
}
func (p *Plugin) currentConfig() Config { p.mu.Lock(); defer p.mu.Unlock(); return p.config }
func (p *Plugin) setConfig(c Config)    { p.mu.Lock(); p.config = c; p.mu.Unlock() }
func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, caller plugins.HostCaller) (result json.RawMessage, err error) {
	defer recoverError(&err)
	h := sdkHost{caller: caller}
	switch method {
	case "init":
		h.initConfig = raw
		p.initMu.Lock()
		defer p.initMu.Unlock()
		var input any
		var e error
		if len(raw) > 0 {
			input, e = decode(raw)
		}
		if e != nil {
			return nil, e
		}
		c := p.currentConfig()
		loaded := func(c Config) {
			if truth(input) {
				var fields map[string]json.RawMessage
				_ = json.Unmarshal(raw, &fields)
				if truth(c.Notify) {
					c.NotifyRaw = append(json.RawMessage(nil), fields["notify_type"]...)
				}
			}
			p.setConfig(c)
		}
		return nil, initialize(ctx, input, &c, h, p.now, func(due time.Time) error { return p.schedule(ctx, caller, due) }, loaded)
	case "get_state":
		return json.Marshal(p.currentConfig().Enabled)
	case "get_form":
		return json.Marshal([]json.RawMessage{form, defaults})
	case "get_page":
		selected, e := p.getSelected(ctx, caller)
		if e != nil {
			return nil, e
		}
		page, e := Page(selected)
		if e != nil {
			return nil, e
		}
		return json.Marshal(page)
	case "get_dashboard":
		selected, e := p.getSelected(ctx, caller)
		if e != nil {
			return nil, e
		}
		elements, e := Elements(selected, p.currentConfig().Dashboard)
		if e != nil {
			return nil, e
		}
		return json.Marshal([]any{map[string]int{"cols": 12}, map[string]any{}, []Node{{"component": "VRow", "content": elements}}})
	case "get_service", "get_command", "stop_service", "stop", "close":
		// Source stop_service is literally pass: pending date jobs survive
		// stop and reinit. Manager retirement still revokes callback scopes.
		return json.RawMessage(`null`), nil
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "get_api":
		api, _ := json.Marshal(Manifest().APIs[0])
		var fields map[string]any
		_ = json.Unmarshal(api, &fields)
		fields["endpoint"] = "refresh_by_domain"
		fields["summary"] = "刷新站点数据"
		fields["description"] = "刷新对应域名的站点数据"
		return json.Marshal([]any{fields})
	case "send_msg":
		config := p.currentConfig()
		mode := config.Notify
		if !truth(mode) {
			return nil, nil
		}
		if len(raw) == 0 {
			return nil, nil
		}
		var event *plugins.MutableEvent
		if e := json.Unmarshal(raw, &event); e != nil {
			return nil, e
		}
		if event == nil {
			return nil, nil
		}
		data, e := decode(event.Data)
		if e != nil {
			return nil, e
		}
		fields, ok := data.(map[string]any)
		if !ok {
			return nil, errors.New("event_data object has no attribute 'get'")
		}
		if !sourceEqual(fields["site_id"], "*") {
			return nil, nil
		}
		// Source reads the member again for the debug message, independently
		// of its initial falsey gate, and again during each arithmetic branch.
		config = p.currentConfig()
		mode = config.Notify
		modeText := pyText(mode)
		if len(config.NotifyRaw) != 0 {
			modeText, e = sourceConfigText(config.NotifyRaw)
			if e != nil {
				return nil, e
			}
		}
		if e = h.Log(ctx, "DEBUG", "收到站点数据刷新事件，开始计算"+modeText+"数据通知 ..."); e != nil {
			return nil, e
		}
		selected, e := p.getSelected(ctx, caller)
		if e != nil {
			return nil, e
		}
		return nil, sendSelected(ctx, h, selected, func() any { return p.currentConfig().Notify }, p.now, &p.notifyMu)
	case "http":
		var request plugins.HTTPRequest
		if e := json.Unmarshal(raw, &request); e != nil {
			return nil, e
		}
		if request.Key != "refresh_by_domain" || request.Method != "GET" {
			return nil, errors.New("unsupported source API request")
		}
		response, e := RefreshByDomain(ctx, h, request.LastQuery("domain"), request.CredentialMatches["apikey"])
		if e != nil {
			return nil, e
		}
		response.MessageI18n = translateMessage(response.Message, requestLocale(request))
		return plugins.JSONHTTPResponse(200, response)
	case "private_refresh":
		return nil, p.runPrivate(ctx, raw, h)
	case "data_continue":
		return nil, p.continueData(ctx, raw, caller)
	}
	return nil, errors.New("unsupported source method")
}
func (p *Plugin) schedule(ctx context.Context, caller plugins.HostCaller, due time.Time) error {
	p.mu.Lock()
	p.nextJob++
	job := &dateJob{id: p.nextJob, due: due}
	p.jobs[job.id] = job
	p.mu.Unlock()
	e := (plugins.Manager{Host: caller}).Submit(ctx, "private_refresh", map[string]any{"job": job.id})
	if e != nil {
		p.mu.Lock()
		if !job.active {
			delete(p.jobs, job.id)
		}
		p.mu.Unlock()
	}
	return e
}
func (p *Plugin) waitDue(ctx context.Context, due time.Time) error {
	if p.wait != nil {
		return p.wait(ctx, due)
	}
	for p.now().Before(due) {
		timer := time.NewTimer(due.Sub(p.now()))
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
	return ctx.Err()
}
func (p *Plugin) runPrivate(ctx context.Context, raw json.RawMessage, h Host) error {
	var input struct{ Job uint64 }
	if e := json.Unmarshal(raw, &input); e != nil {
		return e
	}
	p.mu.Lock()
	job := p.jobs[input.Job]
	if job == nil || job.active {
		p.mu.Unlock()
		return nil
	}
	job.active = true
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.jobs, job.id); p.mu.Unlock() }()
	if e := p.waitDue(ctx, job.due); e != nil {
		return e
	}
	if p.now().After(job.due.Add(time.Second)) {
		return nil
	}
	return h.RefreshAll(ctx)
}

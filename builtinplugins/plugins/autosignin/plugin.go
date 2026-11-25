package autosignin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "light-media/builtinplugins/internal/sdk"
)

func Manifest() sdk.Manifest {
	return sdk.Manifest{ManagedPublicServices: true, ManagedPrivateScheduler: true, SchemaVersion: 1, PluginID: "AutoSignIn", Name: "站点自动签到", Description: "自动模拟登录、签到站点。", Icon: "signin.png", Version: "1.0.0", Author: "thsrite", AuthorURL: "https://github.com/thsrite", Runtime: "go-executable", Entry: "auto-sign-in", HostAPIMin: 1, HostAPIMax: 1, HasPage: true,
		Methods:           []string{"init", "get_state", "get_form", "get_page", "get_service", "get_command", "get_api", "get_render_mode", "sign_in", "site_deleted", "http", "stop_service", "stop", "close", "private_once", "signin_continue"},
		Capabilities:      []string{sdk.BaseGetConfig, sdk.BaseUpdateConfig, sdk.BaseGetData, sdk.BaseSaveData, sdk.BaseDelData, sdk.SettingsReadCapability, sdk.BaseGetInstanceMetadata, sdk.SitesReadCapability, sdk.SitesSourceIndexerCapability, sdk.SitesSourceIndexersCapability, sdk.SitesSourceCustomConfigCapability, sdk.SitesSourceCredentialsCapability, sdk.SitesSourceRequestCapability, sdk.SitesSourceResultWriteCapability, sdk.NetworkGetCapability, sdk.SiteRenderCapability, sdk.CaptchaOCRTextCapability, sdk.EventSendCapability, sdk.PostMessageCapability, sdk.LogWriteCapability, sdk.BackgroundSubmitCapability},
		BroadcastHandlers: []sdk.BroadcastHandler{{Event: "plugin.action", Method: "sign_in"}, {Event: "site.deleted", Method: "site_deleted"}},
		APIs:              []sdk.HTTPAPI{{Key: "signin_by_domain", Path: "/signin_by_domain", Methods: []string{"GET"}, Auth: "apikey", Query: []sdk.HTTPParameter{{Name: "url", Kind: "string", Required: true}, {Name: "apikey", Kind: "api_key", Required: true}}, Headers: []string{"x-moviepilot-locale", "x-locale", "accept-language"}}},
		RuntimeSupport:    &sdk.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}}}
}

type onceScheduler struct {
	id                                 uint64
	due                                time.Time
	removed, active, started, finished bool
	stop, done                         chan struct{}
}
type Plugin struct {
	mu                      sync.Mutex
	state                   *sourceState
	now                     func() time.Time
	wait                    func(context.Context, <-chan struct{}, time.Time) error
	scheduler               *onceScheduler
	nextScheduler, nextPass uint64
	passes                  map[uint64]*signPass
	sourceLogSecrets        sourceCredentialLogSecrets
}

func NewPlugin() *Plugin {
	e := New(Inventory{}, time.Now)
	state := &sourceState{values: Object{}}
	for _, key := range []string{"Enabled", "Notify", "Cron", "OnlyOnce", "QueueCount", "SignSites", "LoginSites", "RetryKeyword", "AutoCF", "Clean", "StartTime", "EndTime", "SchemaLoaded"} {
		state.values[key] = e.field(key)
	}
	return &Plugin{state: state, now: time.Now, passes: map[uint64]*signPass{}}
}
func recoverSource(err *error) {
	if caught := recover(); caught != nil {
		switch value := caught.(type) {
		case *boundaryFailure:
			*err = value.cause
		case *sourceException:
			*err = value.cause
		case error:
			*err = value
		default:
			*err = fmt.Errorf("source panic: %v", value)
		}
	}
}
func (p *Plugin) frame(ctx context.Context, host sdk.HostCaller, private bool) (*Engine, sdkBackend) {
	b := sdkBackend{ctx: ctx, host: host, now: p.now, logSecrets: &p.sourceLogSecrets}
	e := New(Inventory{}, p.now)
	e.shared = p.state
	e.ConfiguredLive = b.configured
	e.IndexersLive = func() []Object { return b.indexers(private) }
	e.CustomLive = b.custom
	e.LoadInitInventory = func() (Inventory, error) { return b.inventory(), nil }
	e.HistoryLive = b.history
	e.StopLive = func() error { return p.stopScheduler(ctx) }
	e.LogLive = b.log
	e.Effects = func(effect Effect) error {
		switch effect.Kind {
		case "config":
			_, err := (sdk.Base{Host: host}).UpdateConfig(ctx, sourceBusinessValue(effect.Value))
			return err
		case "history.put":
			return (sdk.Base{Host: host}).SaveData(ctx, effect.Key, sourceBusinessValue(effect.Value))
		case "history.delete":
			key := effect.Key
			return (sdk.Base{Host: host}).DelData(ctx, &key)
		case "load_modules":
			return nil // The22 ordinary adapters are compiled into this executable.
		case "scheduler_new":
			zone, err := privateTimezone()
			if err != nil {
				return err
			}
			location, err := time.LoadLocation(zone)
			if err != nil {
				return err
			}
			e.ScheduleTimezone = zone
			e.ScheduleZone = location
			p.mu.Lock()
			p.nextScheduler++
			p.scheduler = &onceScheduler{id: p.nextScheduler, stop: make(chan struct{}), done: make(chan struct{})}
			p.mu.Unlock()
			return nil
		case "scheduler_job":
			p.mu.Lock()
			if p.scheduler != nil {
				p.scheduler.due = p.now().Add(3 * time.Second)
			}
			p.mu.Unlock()
			return nil
		case "scheduler_print":
			return nil // APScheduler print_jobs is runtime stdout, not a business log.
		case "scheduler_start":
			p.mu.Lock()
			job := p.scheduler
			if job == nil || job.started {
				p.mu.Unlock()
				return errors.New("private scheduler unavailable")
			}
			job.started = true
			p.mu.Unlock()
			err := (sdk.Manager{Host: host}).SubmitPrivateScheduler(ctx, "private_once", map[string]uint64{"scheduler": job.id})
			if err != nil {
				p.finishOnce(job)
			}
			return err
		}
		return fmt.Errorf("unsupported compiled source effect %q", effect.Kind)
	}
	return e, b
}
func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, host sdk.HostCaller) (result json.RawMessage, err error) {
	defer recoverSource(&err)
	e, b := p.frame(ctx, host, false)
	switch method {
	case "init":
		var config any
		if len(raw) > 0 {
			if config, err = sourceRestoreSavedJSON(raw); err != nil {
				return nil, err
			}
		}
		return nil, e.Init(config)
	case "get_state":
		return json.Marshal(e.field("Enabled"))
	case "get_form":
		// Source reads CustomSites before querying ordered managed rows.
		enabled, custom := b.customInventory()
		inventory := Inventory{Configured: b.configured(), Custom: custom, CustomEnabled: enabled}
		form, err := Form(inventory)
		if err != nil {
			return nil, err
		}
		return json.Marshal([]any{form["conf"], form["model"]})
	case "get_page":
		page, err := e.Page()
		if err != nil {
			return nil, err
		}
		// A raw method returns the data. The final App HTTP serializer owns
		// the source Pydantic failure; a method error would be swallowed by
		// the ordinary view fallback before reaching that response boundary.
		return sourceMarshalBusinessJSON(page)
	case "get_service":
		return p.services(ctx, host, e, b)
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "get_command":
		return json.Marshal([]any{Object{"cmd": "/site_signin", "event": "plugin.action", "desc": "站点签到", "category": "站点", "data": Object{"action": "site_signin"}}})
	case "get_api":
		return json.Marshal(Manifest().APIs)
	case "get_dashboard", "get_dashboard_meta", "get_auth_providers", "get_module", "close":
		return json.RawMessage(`null`), nil
	case "stop_service", "stop":
		return nil, p.stopScheduler(ctx)
	case "signin_continue":
		return nil, p.continuePass(ctx, raw, host)
	case "private_once":
		return nil, p.runOnce(ctx, raw, host)
	case "sign_in":
		var event *EventEnvelope
		if len(raw) > 0 && string(raw) != "null" && string(raw) != "{}" {
			var input *sdk.MutableEvent
			if err = json.Unmarshal(raw, &input); err != nil {
				return nil, err
			}
			if input != nil {
				fields, decodeErr := sourceRestoreSavedJSON(input.Data)
				if decodeErr != nil {
					return nil, decodeErr
				}
				object, ok := fields.(Object)
				if Truth(fields) && !ok {
					return nil, fmt.Errorf("'%s' object has no attribute 'get'", typeName(fields))
				}
				event = &EventEnvelope{Data: object}
			}
		}
		return nil, p.signIn(ctx, host, event)
	case "site_deleted":
		var event sdk.MutableEvent
		if err = json.Unmarshal(raw, &event); err != nil {
			return nil, err
		}
		var fields Object
		if err = Decode(event.Data, &fields); err != nil {
			return nil, err
		}
		config, err := (sdk.Base{Host: host}).GetConfig(ctx)
		if err != nil {
			return nil, err
		}
		var value any
		if err = Decode(config, &value); err != nil {
			return nil, err
		}
		if !Truth(value) {
			return nil, nil
		}
		var ok bool
		e.Config, ok = value.(Object)
		if !ok {
			return nil, fmt.Errorf("'%s' object has no attribute 'get'", typeName(value))
		}
		return nil, e.SiteDeleted(fields["site_id"])
	case "http":
		var request sdk.HTTPRequest
		if err = json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if request.Key != "signin_by_domain" || request.Method != "GET" {
			return nil, errors.New("unsupported source API")
		}
		var response Object
		err = p.runPass(ctx, host, func(call context.Context, caller sdk.HostCaller) error {
			frame, backend := p.frame(call, caller, true)
			var e error
			response, e = SigninByDomain(call, request.LastQuery("url"), DomainAPIHooks{KeyAccepted: request.CredentialMatches["apikey"], Lookup: func(_ context.Context, domain string) (Object, error) { return backend.indexer(domain, true), nil }, Visit: func(ctx context.Context, site Object) (Result, error) {
				return backend.visitor(site).Visit(ctx, site, "签到", frame.schemaLoaded())
			}})
			return e
		})
		if err != nil {
			return nil, err
		}
		// Source schemas.Response defaults data to an empty object. Locale
		// translation is bound separately from the independently tested body.
		if _, exists := response["data"]; !exists {
			response["data"] = Object{}
		}
		response["message_i18n"] = translateMessage(textValue(response["message"]), requestLocale(request))
		if err := sourceWebSerializationFailure(response); err != nil {
			return nil, err
		}
		return sdk.JSONHTTPResponse(200, response)
	}
	return nil, errors.New("unsupported AutoSignIn method")
}
func (p *Plugin) signIn(ctx context.Context, host sdk.HostCaller, event *EventEnvelope) error {
	return p.runPass(ctx, host, func(ctx context.Context, caller sdk.HostCaller) error {
		e, b := p.frame(ctx, caller, true)
		hooks := EntryHooks{HistoryKeys: b.historyKeys, Run: RunHooks{Emit: b.emit, Notify: b.notify}}
		hooks.Run.Visit = func(ctx context.Context, site Object, operation string, loaded bool) (Result, error) {
			return b.visitor(site).Visit(ctx, site, operation, loaded)
		}
		return e.SignIn(ctx, event, hooks)
	})
}
func (p *Plugin) finishOnce(job *onceScheduler) {
	p.mu.Lock()
	if !job.finished {
		job.finished = true
		close(job.done)
	}
	p.mu.Unlock()
}
func (p *Plugin) stopScheduler(ctx context.Context) error {
	p.mu.Lock()
	job := p.scheduler
	if job == nil {
		p.mu.Unlock()
		return nil
	}
	if !job.removed {
		job.removed = true
		close(job.stop)
	}
	active := job.active
	if !active && !job.finished {
		job.finished = true
		close(job.done)
	}
	p.mu.Unlock()
	if active {
		select {
		case <-job.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p.mu.Lock()
	if p.scheduler == job {
		p.scheduler = nil
	}
	p.mu.Unlock()
	return nil
}
func (p *Plugin) runOnce(ctx context.Context, raw json.RawMessage, host sdk.HostCaller) error {
	var input struct{ Scheduler uint64 }
	if err := json.Unmarshal(raw, &input); err != nil {
		return err
	}
	p.mu.Lock()
	job := p.scheduler
	if job == nil || job.id != input.Scheduler || job.removed || job.active || job.finished {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()
	defer p.finishOnce(job)
	if p.wait != nil {
		if err := p.wait(ctx, job.stop, job.due); err != nil {
			return err
		}
	} else {
		timer := time.NewTimer(time.Until(job.due))
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-job.stop:
			return nil
		case <-ctx.Done():
			p.finishOnce(job)
			return ctx.Err()
		}
	}
	p.mu.Lock()
	if job.removed || job.finished {
		p.mu.Unlock()
		return nil
	}
	job.active = true
	p.mu.Unlock()
	return p.signIn(ctx, host, nil)
}
func (p *Plugin) services(ctx context.Context, host sdk.HostCaller, e *Engine, b sdkBackend) (json.RawMessage, error) {
	if !Truth(e.field("Enabled")) {
		return json.RawMessage(`[]`), nil
	}
	zone := "UTC"
	if !Truth(e.field("Cron")) {
		var err error
		zone, err = privateTimezone()
		if err != nil {
			return nil, err
		}
		e.ScheduleTimezone = zone
		e.ScheduleZone, err = time.LoadLocation(zone)
		if err != nil {
			return nil, err
		}
	} else if strings.Count(strings.TrimFunc(pythonScalar(e.field("Cron")), sourcePythonSpace), " ") == 4 {
		var warnings []string
		var err error
		zone, warnings, err = resolveSourcePublicTimezone("/")
		if err != nil {
			return nil, err
		}
		for _, warning := range warnings {
			if err = b.log("WARNING", warning); err != nil {
				return nil, err
			}
		}
	}
	validationZone := zone
	if validationZone == "local" {
		validationZone = "UTC"
	}
	jobs, err := e.Services(nil, validationZone)
	if err != nil {
		return nil, err
	}
	out := []any{}
	for _, item := range jobs {
		job := item.(Object)
		kwargs := job["kwargs"].(Object)
		trigger := Object{}
		if descriptor, ok := job["trigger"].(Object); ok {
			trigger["kind"] = "cron"
			trigger["cron"] = pythonScalar(e.field("Cron"))
			_ = descriptor
		} else if job["trigger"] == "cron" {
			trigger["kind"] = "cron"
			trigger["fields"] = Object{"hour": fmt.Sprint(kwargs["hour"]), "minute": fmt.Sprint(kwargs["minute"])}
		} else {
			hours, ok := kwargs["hours"].(float64)
			// Services retains the offline reference's nonfinite Object encoding.
			// Restore its float only at this actual host transport boundary.
			if encoded, tagged := kwargs["hours"].(Object); !ok && tagged && len(encoded) == 1 {
				switch encoded["nonfinite"] {
				case "nan":
					hours, ok = math.NaN(), true
				case "inf":
					hours, ok = math.Inf(1), true
				case "-inf":
					hours, ok = math.Inf(-1), true
				}
			}
			if !ok {
				return nil, errors.New("invalid source interval descriptor")
			}
			// Do not pre-construct timedelta, round to milliseconds or supply a
			// timezone/start. The host's existing source owner registers the logical
			// method before applying the source APS trigger/clock semantics.
			trigger["kind"] = "source_interval"
			trigger["source_interval_hours"] = strconv.FormatFloat(hours, 'g', -1, 64)
		}
		if trigger["kind"] != "source_interval" {
			if zone == "local" {
				trigger["local_timezone"] = true
			} else {
				trigger["timezone"] = zone
			}
		}
		out = append(out, Object{"id": job["id"], "name": job["name"], "handler": "sign_in", "trigger": trigger, "kwargs": Object{}})
	}
	return json.Marshal(out)
}

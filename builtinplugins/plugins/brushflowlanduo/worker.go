package brushflowlanduo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"time"

	sdk "light-media/builtinplugins/internal/sdk"
)

func Manifest() sdk.Manifest {
	return sdk.Manifest{
		SchemaVersion: 1, PluginID: PluginID, Name: "站点刷流-landuo",
		Description: "自动托管多个站点刷流任务，并独立调度、统计与诊断。",
		Icon:        "brush-flow.png", Version: Version, Author: "landuo",
		AuthorURL: "https://github.com/landuo", PluginOrder: 21,
		Runtime: "go-executable", Entry: "brush-flow-landuo", HostAPIMin: 1, HostAPIMax: 1,
		Methods:      []string{"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "get_dashboard", "get_sidebar_nav", "http", "brush", "check", "promotion_expiry", "cleanup_tags", "cleanup_task_tag", "reload_event", "close", "stop_service", "stop"},
		Capabilities: brushCapabilities(), ManagedPublicServices: true,
		BroadcastHandlers: []sdk.BroadcastHandler{{Event: "plugin.reload", Method: "reload_event"}},
		HasPage:           true, Dashboards: []string{"default"}, APIs: originalHTTPAPIs(),
		Frontend: Frontend(), RuntimeSupport: &sdk.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}},
	}
}

// The standard manager owns instance identity, background submission and
// clocks. One Core stays with the loaded object. Resource authority lives in
// the current callback context, including for nested getters and jobs.
type Standard struct {
	host standardHost
	core *Core
}

func NewStandard() *Standard {
	p := &Standard{}
	p.core = NewUninitialized(&p.host)
	return p
}

func (p *Standard) Handle(ctx context.Context, method string, raw json.RawMessage, caller sdk.HostCaller) (json.RawMessage, error) {
	ctx = withBrushHost(ctx, caller)
	switch method {
	case "init":
		config := Row{}
		if len(raw) > 0 && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			var err error
			config, err = standardObject(raw)
			if err != nil {
				return nil, err
			}
		}
		p.host.resetAliases()
		return nil, p.core.Init(ctx, config)
	case "get_state":
		return json.Marshal(p.core.Enabled())
	case "get_service":
		services := []Row{}
		for _, service := range p.core.Services(ctx) {
			trigger := Row{"kind": "interval", "timezone": p.host.Now().Location().String(), "interval_ms": int64(service.Minutes) * int64(time.Minute/time.Millisecond)}
			if service.Cron != "" {
				trigger = Row{"kind": "cron", "cron": service.Cron, "local_timezone": true}
			}
			services = append(services, Row{"id": service.ID, "name": service.Name, "handler": service.Operation,
				"trigger": trigger, "input": Row{"task_id": service.TaskID},
				// Brush runs can outlive their interval. Admit the tick and let the
				// task lock skip it quietly instead of logging a host clock warning.
				"kwargs": Row{"max_instances": 3, "coalesce": true, "misfire_grace_time": 60}})
		}
		return json.Marshal(services)
	case "get_command", "get_page":
		return json.RawMessage("[]"), nil
	case "get_api":
		return json.Marshal(originalHTTPAPIs())
	case "get_render_mode":
		return json.RawMessage(`["vue","dist/assets"]`), nil
	case "get_form":
		return json.Marshal([]any{[]any{}, p.core.Config().Map()})
	case "get_sidebar_nav":
		return json.Marshal(p.core.Sidebar())
	case "get_dashboard":
		if !p.core.Enabled() {
			return nil, nil
		}
		return json.Marshal([]any{Row{"cols": 12, "sm": 6, "md": 6}, Row{"title": "站点刷流", "subtitle": "多任务运行概览", "refresh": 30, "border": true}, nil})
	case "http":
		return p.http(ctx, raw)
	case "reload_event":
		return nil, refreshBrushReloadEvent(ctx, raw, caller)
	case "brush", "check", "promotion_expiry", "cleanup_tags", "cleanup_task_tag":
		return nil, p.run(ctx, method, raw)
	case "stop_service", "stop":
		p.core.Stop()
		return nil, nil
	case "close":
		return nil, nil
	default:
		return nil, errors.New("unsupported BrushFlowLanduo method")
	}
}

func refreshBrushReloadEvent(ctx context.Context, raw json.RawMessage, caller sdk.HostCaller) error {
	var event *sdk.MutableEvent
	if err := json.Unmarshal(raw, &event); err != nil || event == nil {
		return err
	}
	var data struct {
		PluginID string `json:"plugin_id"`
	}
	if err := json.Unmarshal(event.Data, &data); err != nil || data.PluginID == "" {
		return err
	}
	metadata, err := (sdk.Base{Host: caller}).GetInstanceMetadata(ctx)
	if err != nil || data.PluginID != metadata.InstanceID {
		return err
	}
	// The API declarations are static and already registered on the current
	// object. The original reload callback also refreshes its public clocks.
	return (sdk.Manager{Host: caller}).RefreshServices(ctx)
}

func (p *Standard) run(ctx context.Context, method string, raw json.RawMessage) error {
	if method == "cleanup_tags" {
		return p.core.CleanupTags(ctx)
	}
	var args struct {
		TaskID string `json:"task_id"`
		Task   Row    `json:"task"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return err
	}
	switch method {
	case "brush":
		return p.core.Brush(ctx, args.TaskID)
	case "check":
		return p.core.Check(ctx, args.TaskID, false)
	case "promotion_expiry":
		return p.core.CheckPromotion(ctx, args.TaskID)
	case "cleanup_task_tag":
		if args.Task == nil || text(args.Task["id"]) != args.TaskID {
			return errors.New("removed BrushFlow task input is missing")
		}
		return p.core.CleanupTaskTag(ctx, Task{args.Task}, nil)
	}
	return errors.New("unsupported BrushFlowLanduo task method")
}

func originalHTTPAPIs() []sdk.HTTPAPI {
	return []sdk.HTTPAPI{
		{Key: "status", Path: "/status", Methods: []string{"GET"}, Auth: "bear"},
		{Key: "settings", Path: "/settings", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true},
		{Key: "create", Path: "/tasks", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true},
		{Key: "detail", Path: "/tasks/{task_id}", Methods: []string{"GET"}, Auth: "bear", Query: []sdk.HTTPParameter{{Name: "state", Kind: "string"}, {Name: "page", Kind: "string"}, {Name: "page_size", Kind: "string"}}},
		{Key: "update", Path: "/tasks/{task_id}", Methods: []string{"PUT"}, Auth: "bear", Body: "json", BodyRequired: true},
		{Key: "delete", Path: "/tasks/{task_id}", Methods: []string{"DELETE"}, Auth: "bear"},
		{Key: "state", Path: "/tasks/{task_id}/state", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true},
		{Key: "run", Path: "/tasks/{task_id}/run", Methods: []string{"POST"}, Auth: "bear", Body: "json"},
		{Key: "check", Path: "/tasks/{task_id}/check", Methods: []string{"POST"}, Auth: "bear", Body: "json"},
		{Key: "clear", Path: "/tasks/{task_id}/clear", Methods: []string{"POST"}, Auth: "bear", Body: "json"},
	}
}

func standardObject(raw []byte) (Row, error) {
	var value Row
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&value) != nil || value == nil || d.Decode(new(any)) != io.EOF {
		return nil, errors.New("expected a JSON object")
	}
	return value, nil
}

func standardResponse(status int, value any) (json.RawMessage, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(sdk.HTTPResponse{Status: status, Body: body, Headers: map[string]string{"Content-Type": "application/json"}})
}
func (p *Standard) http(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var request sdk.HTTPRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	matched := false
	for _, api := range originalHTTPAPIs() {
		params, ok := api.Match(request.Path)
		if api.Key == request.Key && slices.Contains(api.Methods, request.Method) && ok {
			request.Params, matched = params, true
			break
		}
	}
	if !matched {
		return standardResponse(404, Row{"detail": "Not Found"})
	}
	id := request.Params["task_id"]
	var response Response
	var err error
	switch request.Key {
	case "status":
		response.Success = true
		response.Data, err = p.core.Status(ctx)
	case "detail":
		state, page, size := "active", 1, 50
		if values := request.Query["state"]; len(values) > 0 {
			state = values[len(values)-1]
		}
		problems := &ValidationError{scope: "query"}
		if !slices.Contains([]string{"active", "deleted", "all"}, state) {
			const pattern = "^(active|deleted|all)$"
			problems.add("state", "string_pattern_mismatch", "String should match pattern '"+pattern+"'", state, Row{"pattern": pattern})
		}
		if values := request.Query["page"]; len(values) > 0 {
			page = problems.integer("page", values[len(values)-1], 1, 0)
		}
		if values := request.Query["page_size"]; len(values) > 0 {
			size = problems.integer("page_size", values[len(values)-1], 10, 200)
		}
		if len(problems.details) > 0 {
			return problems.response()
		}
		if _, ok := p.core.task(id); !ok {
			response.Message = "刷流任务不存在"
		} else {
			response.Success = true
			response.Data, err = p.core.Detail(ctx, id, state, page, size)
		}
	case "settings", "create", "update", "state":
		body, decodeErr := standardObject(request.Body)
		if decodeErr != nil {
			var input any
			_ = json.Unmarshal(request.Body, &input)
			problems := &ValidationError{scope: "body"}
			if input == nil {
				problems.add("", "missing", "Field required", nil, nil)
			} else {
				problems.add("", "model_attributes_type", "Input should be a valid dictionary or object to extract fields from", input, nil)
			}
			return problems.response()
		}
		switch request.Key {
		case "settings":
			response, err = p.core.UpdateSettings(ctx, body)
		case "create":
			response, err = p.core.CreateTask(ctx, body)
		case "update":
			response, err = p.core.UpdateTask(ctx, id, body)
		case "state":
			var enabled bool
			enabled, err = ValidateTaskState(body)
			if err == nil {
				response, err = p.core.StateTask(ctx, id, enabled)
			}
		}
	case "delete":
		response, err = p.core.DeleteTask(ctx, id)
	case "clear":
		response, err = p.core.ClearTask(ctx, id)
	case "run":
		response, err = p.core.Submit(ctx, id, "brush")
	case "check":
		response, err = p.core.Submit(ctx, id, "check")
	}
	if err != nil {
		var validation *ValidationError
		if errors.As(err, &validation) {
			if validation.uncaughtRegexError {
				return json.Marshal(sdk.HTTPResponse{
					Status: 500, Body: []byte("Internal Server Error"),
					Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
				})
			}
			return validation.response()
		}
		return nil, err
	}
	var message any
	if response.Message != "" {
		message = response.Message
	}
	data := response.Data
	if data == nil {
		data = Row{}
	}
	return standardResponse(200, Row{"success": response.Success, "message": message, "message_i18n": message, "data": data})
}

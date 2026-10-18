package brushflow

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
)

const ConfigureCapability = "brushflow.configure"
const TaskCapability = "brushflow.task"
const APICapability = "brushflow.api"
const ReadCapability = "brushflow.read"

func DefaultConfig() Row {
	r := Row{"schema_version": 2, "enabled": false, "show_sidebar_nav": true, "tasks": []any{}, "global_proxy_delete": false, "global_delete_size_range": nil}
	for _, k := range globalLimitFields {
		r[k] = nil
	}
	return r
}
func NativeManifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: PluginID, Name: "站点刷流-landuo", Description: "自动托管多个站点刷流任务，并独立调度、统计与诊断。", Icon: "brush-flow.png", Version: Version, Author: "jxxghp,landuo", AuthorURL: "https://github.com/landuo", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "page", "dashboard", "stop"}, Capabilities: []string{ConfigureCapability, TaskCapability, APICapability, ReadCapability}, HasPage: true, Dashboards: []string{"default"}, APIs: []plugins.HTTPAPI{
		{Key: "status", Path: "/status", Methods: []string{"GET"}, Auth: "bear"},
		{Key: "settings", Path: "/settings", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true},
		{Key: "create", Path: "/tasks", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true},
		{Key: "detail", Path: "/tasks/{task_id}", Methods: []string{"GET"}, Auth: "bear", Query: []plugins.HTTPParameter{{Name: "state", Kind: "string"}, {Name: "page", Kind: "string"}, {Name: "page_size", Kind: "string"}}},
		{Key: "update", Path: "/tasks/{task_id}", Methods: []string{"PUT"}, Auth: "bear", Body: "json", BodyRequired: true},
		{Key: "delete", Path: "/tasks/{task_id}", Methods: []string{"DELETE"}, Auth: "bear"},
		{Key: "state", Path: "/tasks/{task_id}/state", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true},
		{Key: "run", Path: "/tasks/{task_id}/run", Methods: []string{"POST"}, Auth: "bear", Body: "json"},
		{Key: "check", Path: "/tasks/{task_id}/check", Methods: []string{"POST"}, Auth: "bear", Body: "json"},
		{Key: "clear", Path: "/tasks/{task_id}/clear", Methods: []string{"POST"}, Auth: "bear", Body: "json"},
	}}
}
func NativeHandle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	switch method {
	case "config_form":
		return json.Marshal(Row{"conf": []any{}, "model": DefaultConfig(), "render_mode": "vue", "path": "dist/assets", "entry": "remoteEntry.js", "export": "./Config"})
	case "page":
		return json.Marshal(Row{"render_mode": "vue", "path": "dist/assets", "entry": "remoteEntry.js", "export": "./Page"})
	case "dashboard":
		return json.Marshal(Row{"render_mode": "vue", "path": "dist/assets", "entry": "remoteEntry.js", "export": "./Dashboard", "cols": 12, "sm": 6, "md": 6, "title": "站点刷流", "subtitle": "多任务运行概览", "refresh": 30, "border": true})
	case "stop":
		return json.RawMessage(`null`), nil
	}
	var inv plugins.Invocation
	if method != "invoke" || json.Unmarshal(raw, &inv) != nil {
		return nil, errors.New("invalid BrushFlow invocation")
	}
	switch inv.Action {
	case "init", "reload":
		return host(ctx, ConfigureCapability, json.RawMessage(`{}`))
	case "run":
		return host(ctx, TaskCapability, json.RawMessage(`{}`))
	case "task", "service":
		var token struct {
			ID string `json:"event_id"`
		}
		if json.Unmarshal(inv.Input, &token) != nil || token.ID == "" || len(token.ID) > 128 {
			return nil, errors.New("unbound BrushFlow operation")
		}
		return host(ctx, TaskCapability, inv.Input)
	case "api":
		var token struct {
			RequestID string `json:"request_id"`
			Read      bool   `json:"read"`
		}
		if json.Unmarshal(inv.Input, &token) != nil || token.RequestID == "" || len(token.RequestID) > 128 {
			return nil, errors.New("unbound BrushFlow HTTP call")
		}
		payload, _ := json.Marshal(map[string]string{"request_id": token.RequestID})
		capability := APICapability
		if token.Read {
			capability = ReadCapability
		}
		return host(ctx, capability, payload)
	}
	return nil, errors.New("unsupported BrushFlow action")
}

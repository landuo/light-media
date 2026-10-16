package larkmessager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/plugins"
)

const ConfigureCapability = "lark.configure"
const ModuleCapability = "lark.module"
const EventCapability = "lark.event"
const FormCapability = "lark.form"
const PageCapability = "lark.page"
const APICapability = "lark.api"

func Manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "LarkMessager", Name: "Lark 应用消息通知", Description: "基于国际版飞书 Lark 开放平台应用的通知与消息交互插件，支持文本、卡片消息发送及消息回调交互。", Icon: "https://raw.githubusercontent.com/jxxghp/MoviePilot-Plugins/main/icons/FeiShu_A.png", Version: "0.9.1", Author: "ui-beam-9", AuthorURL: "https://github.com/ui-beam-9", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "page", "invoke", "http_api", "stop"}, Capabilities: []string{ConfigureCapability, ModuleCapability, EventCapability, FormCapability, PageCapability, APICapability}, Events: []string{"message.action"}, HasPage: true, APIs: []plugins.HTTPAPI{{Key: "webhook", Path: "/webhook", Methods: []string{"POST", "GET"}, Auth: "anonymous", Query: []plugins.HTTPParameter{{Name: "token", Kind: "string"}}, Headers: []string{"x-lark-signature", "x-lark-request-timestamp", "x-lark-request-nonce"}, Body: "raw"}, {Key: "test", Path: "/test", Methods: []string{"GET"}, Auth: "bear"}, {Key: "status", Path: "/status", Methods: []string{"GET"}, Auth: "bear"}, {Key: "fetch_chats", Path: "/fetch_chats", Methods: []string{"GET"}, Auth: "bear"}}}
}

// The worker forwards only opaque host-bound IDs, never recipient or config data.
func Handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	if method != "stop" && host == nil {
		return nil, ErrDependency
	}
	switch method {
	case "stop":
		return json.RawMessage(`null`), nil
	case "config_form":
		return host(ctx, FormCapability, json.RawMessage(`{}`))
	case "page":
		return host(ctx, PageCapability, json.RawMessage(`{}`))
	case "http_api":
		var r plugins.HTTPRequest
		if json.Unmarshal(raw, &r) != nil || r.RequestID == "" {
			return nil, ErrDenied
		}
		return hostID(ctx, host, APICapability, "request_id", r.RequestID)
	}
	var inv plugins.Invocation
	if method != "invoke" || json.Unmarshal(raw, &inv) != nil {
		return nil, ErrDenied
	}
	switch inv.Action {
	case "init", "reload":
		return host(ctx, ConfigureCapability, json.RawMessage(`{}`))
	case "module":
		return onlyID(ctx, host, ModuleCapability, "call_id", inv.Input)
	case "event":
		return onlyID(ctx, host, EventCapability, "event_id", inv.Input)
	}
	return nil, ErrDenied
}
func onlyID(ctx context.Context, host plugins.HostCaller, cap, key string, raw []byte) (json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	var v map[string]string
	if d.Decode(&v) != nil || d.Decode(new(any)) != io.EOF || len(v) != 1 || v[key] == "" {
		return nil, errors.New("unbound Lark invocation")
	}
	return hostID(ctx, host, cap, key, v[key])
}
func hostID(ctx context.Context, host plugins.HostCaller, cap, key, value string) (json.RawMessage, error) {
	if len(value) > 128 {
		return nil, ErrDenied
	}
	b, _ := json.Marshal(map[string]string{key: value})
	return host(ctx, cap, b)
}

package builtin

import (
	"context"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
	"light-media/internal/plugins/subscribegroup"
)

func SubscribeGroupManifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "SubscribeGroup", Name: "订阅规则自动填充", Description: "电视剧下载后自动添加官组等信息到订阅；添加订阅后根据二级分类名称自定义订阅规则。", Icon: "teamwork.png", Version: "2.8.7", Author: "thsrite", AuthorURL: "https://github.com/thsrite", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "get_page", "page", "stop"}, Capabilities: []string{"subscriptions.rules.configure", "subscriptions.rules.apply", "subscriptions.rules.history"}, Events: []string{"subscription.added", "download.accepted"}, HasPage: true}
}

func HandleSubscribeGroup(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	// Keep legacy SDK aliases while exposing the canonical extension methods.
	if method == "get_page" {
		method = "page"
	}
	switch method {
	case "stop":
		return json.RawMessage(`null`), nil
	case "config_form":
		return subscribegroup.Form(), nil
	}
	var invocation plugins.Invocation
	if json.Unmarshal(raw, &invocation) != nil {
		return nil, errors.New("invalid subscription rules invocation")
	}
	if method == "page" {
		if invocation.Action != "page" {
			return nil, errors.New("invalid subscription rules page action")
		}
		history, err := host(ctx, "subscriptions.rules.history", json.RawMessage(`{}`))
		if err != nil {
			return nil, err
		}
		page, err := subscribegroup.RawPage(history)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"render_mode": "vuetify", "page": page})
	}
	if method != "invoke" {
		return nil, errors.New("unsupported subscription rules method")
	}
	switch invocation.Action {
	case "init", "reload":
		return host(ctx, "subscriptions.rules.configure", json.RawMessage(`{}`))
	case "event":
		var input struct {
			BatchID string `json:"batch_id"`
		}
		if json.Unmarshal(invocation.Input, &input) != nil || input.BatchID == "" {
			return nil, errors.New("missing subscription rules batch")
		}
		request, _ := json.Marshal(input)
		return host(ctx, "subscriptions.rules.apply", request)
	default:
		return nil, errors.New("unsupported subscription rules action")
	}
}

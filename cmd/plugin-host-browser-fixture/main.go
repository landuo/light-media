// Acceptance-only worker for the ordinary rendered-page host boundary.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"light-media/internal/plugins"
)

var fixtureVersion = "1.0"

func manifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "OwnedHostBrowser", Name: "页面渲染验收", Version: fixtureVersion, Runtime: "go-executable", Entry: "plugin-host-browser-fixture", HostAPIMin: 1, HostAPIMax: 1, HasPage: true,
		Methods:      []string{"init", "stop", "get_state", "get_service", "get_command", "get_api", "get_render_mode", "get_form", "get_page", "resource", "http"},
		Capabilities: append(plugins.BaseMethods(), plugins.SiteRenderCapability),
		APIs:         []plugins.HTTPAPI{{Key: "run", Path: "/run", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true}, {Key: "snapshot", Path: "/snapshot", Methods: []string{"GET"}, Auth: "bear"}},
	}
}

func handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	base := plugins.Base{Host: host}
	switch method {
	case "init", "stop":
		return json.RawMessage(`null`), nil
	case "get_state":
		return json.RawMessage(`true`), nil
	case "get_service", "get_command":
		return json.RawMessage(`[]`), nil
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "get_render_mode":
		return json.RawMessage(`["vuetify",""]`), nil
	case "get_form":
		return json.RawMessage(`[[{"component":"VTextField","props":{"model":"id","label":"独占测试站点ID"}},{"component":"VTextField","props":{"model":"path","label":"站点内页面路径"}},{"component":"VSwitch","props":{"model":"headless","label":"无头浏览器"}}],{"id":"","path":"/page","headless":true,"timeout_seconds":3}]`), nil
	case "resource":
		var request plugins.SiteRenderRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		result, err := plugins.RenderSite(ctx, host, request)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	config, err := base.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	key := "last"
	last, err := base.GetData(ctx, &key)
	if err != nil {
		return nil, err
	}
	if method == "get_page" {
		identity, err := base.GetInstanceMetadata(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal([]any{
			map[string]any{"component": "VAlert", "props": map[string]any{"text": "渲染结果：" + string(last)}},
			map[string]any{"component": "VCardText", "text": "配置：" + string(config)},
			map[string]any{"component": "VBtn", "text": "渲染一次", "events": map[string]any{"click": map[string]any{"api": "plugin/" + identity.InstanceID + "/run", "method": "POST", "params": map[string]any{}}}},
		})
	}
	if method == "http" {
		var call plugins.HTTPRequest
		if err := json.Unmarshal(raw, &call); err != nil {
			return nil, err
		}
		if call.Key == "snapshot" {
			return reply(200, map[string]any{"pid": os.Getpid(), "version": fixtureVersion, "config": config, "last": last})
		}
		if call.Key != "run" {
			return reply(404, map[string]any{"success": false})
		}
		request := config
		if len(call.Body) > 0 && string(call.Body) != "{}" && string(call.Body) != "null" {
			request = call.Body
		}
		result, err := handle(ctx, "resource", request, host)
		if err != nil {
			return nil, err
		}
		var typed plugins.SiteRenderResult
		if err = json.Unmarshal(result, &typed); err != nil {
			return nil, err
		}
		if err = base.SaveData(ctx, key, typed); err != nil {
			return nil, err
		}
		return reply(200, map[string]any{"success": typed.HTML != nil, "result": typed})
	}
	return nil, errors.New("unknown browser fixture method")
}

func reply(status int, value any) (json.RawMessage, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(plugins.HTTPResponse{Status: status, Body: body, Headers: map[string]string{"Content-Type": "application/json"}})
}

func main() {
	m := manifest()
	if err := m.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) == 2 && os.Args[1] == "--manifest" {
		_ = json.NewEncoder(os.Stdout).Encode(m)
		return
	}
	if err := plugins.ServeConcurrent(context.Background(), os.Stdin, os.Stdout, m, handle); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

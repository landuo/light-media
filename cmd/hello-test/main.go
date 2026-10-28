// HelloTest is a small installable example, not a builtin catalog entry.
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

const version = "0.1.0"

func manifest() plugins.Manifest {
	return plugins.Manifest{
		SchemaVersion: 1, PluginID: "HelloTest", Name: "你好测试插件",
		Description: "保存一句话，手动运行并保存计数", Author: "light-media example",
		Version: version, Runtime: "go-executable", Entry: "hello-test",
		HostAPIMin: 1, HostAPIMax: 1, HasPage: true,
		Methods:      []string{"init", "get_state", "get_form", "get_page", "get_render_mode", "get_api", "http", "get_service", "get_command", "close", "stop_service", "stop"},
		Capabilities: []string{plugins.BaseGetConfig, plugins.BaseGetData, plugins.BaseSaveData, plugins.BaseGetInstanceMetadata},
		APIs: []plugins.HTTPAPI{
			{Key: "status", Path: "/status", Methods: []string{"GET"}, Auth: "bear"},
			{Key: "run", Path: "/run", Methods: []string{"POST"}, Auth: "bear", Body: "json"},
		},
	}
}

type config struct {
	Enabled bool   `json:"enabled"`
	Message string `json:"message"`
}
type result struct {
	Count   int    `json:"count"`
	Message string `json:"message"`
}

func handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	base := plugins.Base{Host: host} // Use only this invocation's caller and context.
	switch method {
	case "init", "close", "stop_service", "stop":
		// No work runs on install, save or restart. This example has no background tasks.
		return json.RawMessage(`null`), nil
	case "get_service", "get_command":
		return json.RawMessage(`[]`), nil
	case "get_api":
		return json.Marshal(manifest().APIs)
	case "get_render_mode":
		return json.RawMessage(`["vuetify",""]`), nil
	case "get_form":
		return json.RawMessage(`[[{"component":"VSwitch","props":{"model":"enabled","label":"启用插件"}},{"component":"VTextField","props":{"model":"message","label":"测试消息"}}],{"enabled":false,"message":"你好，light-media！"}]`), nil
	}

	cfg := config{Message: "你好，light-media！"}
	stored, err := base.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	if len(stored) > 0 {
		if err = json.Unmarshal(stored, &cfg); err != nil {
			return nil, err
		}
	}
	if method == "get_state" {
		return json.Marshal(cfg.Enabled)
	}
	key := "result"
	stored, err = base.GetData(ctx, &key)
	if err != nil {
		return nil, err
	}
	var state result
	if len(stored) > 0 {
		if err = json.Unmarshal(stored, &state); err != nil {
			return nil, err
		}
	}
	switch method {
	case "get_page":
		identity, err := base.GetInstanceMetadata(ctx)
		if err != nil {
			return nil, err
		}
		// InstanceID comes from the host; clones need no separate implementation.
		return json.Marshal([]any{
			map[string]any{"component": "VCardText", "text": fmt.Sprintf("已运行 %d 次；最近消息：%s", state.Count, state.Message)},
			map[string]any{"component": "VBtn", "text": "运行一次", "props": map[string]any{"color": "primary"}, "events": map[string]any{"click": map[string]any{"method": "POST", "api": "plugin/" + identity.InstanceID + "/run", "params": map[string]any{}}}},
		})
	case "http":
		var request plugins.HTTPRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if request.Key == "run" && request.Method == "POST" {
			if !cfg.Enabled {
				return reply(409, map[string]any{"success": false, "message": "请先在插件设置中启用并保存"})
			}
			state.Count++
			state.Message = cfg.Message
			if err = base.SaveData(ctx, key, state); err != nil {
				return nil, err
			}
			fmt.Fprintln(os.Stderr, "HelloTest: manual run completed")
		} else if request.Key != "status" || request.Method != "GET" {
			return reply(404, map[string]any{"success": false, "message": "没有这个接口"})
		}
		return reply(200, map[string]any{"success": true, "enabled": cfg.Enabled, "result": state})
	default:
		return nil, fmt.Errorf("unsupported method: %s", method)
	}
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
		if err := json.NewEncoder(os.Stdout).Encode(m); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	// Stdout belongs to the SDK protocol; ordinary logs go to stderr.
	if err := plugins.Serve(context.Background(), os.Stdin, os.Stdout, m, handle); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

package cloudflarespeedtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"light-media/internal/plugins"
)

// NativeManifest adapts the frozen source port to the actual platform catalog.
// Grants come from this compiled declaration, never a stored/worker manifest.
func NativeManifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: PluginID, Name: "Cloudflare IP优选", Description: "🌩 测试 Cloudflare CDN 延迟和速度，自动优选IP。", Icon: "cloudflare.jpg", Version: Version, Author: "thsrite", AuthorURL: "https://github.com/thsrite", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "stop"}, Capabilities: []string{ConfigureCapability, RunCapability}, APIs: []plugins.HTTPAPI{{Key: Action, Path: "/cloudflare_speedtest", Methods: []string{"GET"}, Auth: "apikey", Query: []plugins.HTTPParameter{{Name: "apikey", Kind: "api_key", Required: true}}}}}
}

func NativeHandle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	switch method {
	case "config_form":
		return Form(), nil
	case "stop":
		return json.RawMessage(`null`), nil
	}
	var inv plugins.Invocation
	if method != "invoke" || json.Unmarshal(raw, &inv) != nil {
		return nil, errors.New("invalid Cloudflare invocation")
	}
	switch inv.Action {
	case "init", "reload":
		return host(ctx, ConfigureCapability, json.RawMessage(`{}`))
	case "run", Action:
		return host(ctx, RunCapability, json.RawMessage(`{}`))
	case "once", "service", "event", "command", "api":
		var token map[string]string
		d := json.NewDecoder(bytes.NewReader(inv.Input))
		if d.Decode(&token) != nil || d.Decode(new(any)) != io.EOF || len(token) != 1 {
			return nil, errors.New("unbound Cloudflare invocation")
		}
		key := "event_id"
		if inv.Action == "api" {
			key = "request_id"
		}
		if token[key] == "" || len(token[key]) > 128 {
			return nil, errors.New("unbound Cloudflare source")
		}
		return host(ctx, RunCapability, inv.Input)
	default:
		return nil, errors.New("unsupported Cloudflare action")
	}
}

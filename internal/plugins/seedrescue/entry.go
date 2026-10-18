package seedrescue

import (
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
)

const ConfigureCapability = "seedrescue.configure"
const ScanCapability = "seedrescue.scan"
const APICapability = "seedrescue.api"
const ReadCapability = "seedrescue.read"

// NativeManifest is a compiled grant declaration. Worker input never grants a
// site, downloader, URL, token or filesystem path.
func NativeManifest() plugins.Manifest {
	query := []plugins.HTTPParameter{}
	for _, name := range []string{"view", "page", "page_size", "state", "promotion", "source_promotion", "source_site_id", "min_seeders", "max_seeders"} {
		query = append(query, plugins.HTTPParameter{Name: name, Kind: "string"})
	}
	return plugins.Manifest{SchemaVersion: 1, PluginID: PluginID, Name: "稀缺保种", Description: "读取目标站RSS，可选择跨站同源候选下载后辅种，也可直接下载保种站点原种。", Icon: "IYUU.png", Version: Version, Author: "landuo", AuthorURL: "https://github.com/landuo", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "page", "stop"}, Capabilities: []string{ConfigureCapability, ScanCapability, APICapability, ReadCapability}, HasPage: true, APIs: []plugins.HTTPAPI{
		{Key: "status", Path: "/status", Methods: []string{"GET"}, Auth: "bear", Query: query},
		{Key: "scan", Path: "/scan", Methods: []string{"POST"}, Auth: "bear", Body: "json"},
		{Key: "job", Path: "/jobs/{target_hash}", Methods: []string{"GET"}, Auth: "bear"},
		{Key: "download", Path: "/jobs/{target_hash}/download", Methods: []string{"POST"}, Auth: "bear", Body: "json", BodyRequired: true},
		{Key: "retry", Path: "/jobs/{target_hash}/retry", Methods: []string{"POST"}, Auth: "bear", Body: "json"},
		{Key: "ignore", Path: "/jobs/{target_hash}/ignore", Methods: []string{"POST"}, Auth: "bear", Body: "json"},
	}}
}

// NativeHandle only presents the source form or the immutable host-selected
// capability token. All source business code executes under the actual owner.
func NativeHandle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	switch method {
	case "config_form":
		return json.Marshal(Object{"conf": []any{}, "model": ParseConfig(Object{}).Current(), "render_mode": "vue", "path": "dist/assets", "entry": "remoteEntry.js", "export": "./Config"})
	case "page":
		return json.Marshal(Object{"render_mode": "vue", "path": "dist/assets", "entry": "remoteEntry.js", "export": "./Page"})
	case "stop":
		return json.RawMessage(`null`), nil
	}
	var inv plugins.Invocation
	if method != "invoke" || json.Unmarshal(raw, &inv) != nil {
		return nil, errors.New("invalid SeedRescue invocation")
	}
	switch inv.Action {
	case "init", "reload":
		return host(ctx, ConfigureCapability, json.RawMessage(`{}`))
	case "run":
		return host(ctx, ScanCapability, json.RawMessage(`{}`))
	case "scan", "once", "service":
		var token struct {
			ID string `json:"event_id"`
		}
		if json.Unmarshal(inv.Input, &token) != nil || token.ID == "" || len(token.ID) > 128 {
			return nil, errors.New("unbound SeedRescue schedule")
		}
		return host(ctx, ScanCapability, inv.Input)
	case "api":
		var token struct {
			RequestID string `json:"request_id"`
			Read      bool   `json:"read"`
		}
		if json.Unmarshal(inv.Input, &token) != nil || token.RequestID == "" || len(token.RequestID) > 128 {
			return nil, errors.New("unbound SeedRescue HTTP call")
		}
		payload, _ := json.Marshal(map[string]string{"request_id": token.RequestID})
		capability := APICapability
		if token.Read {
			capability = ReadCapability
		}
		return host(ctx, capability, payload)
	default:
		return nil, errors.New("unsupported SeedRescue action")
	}
}

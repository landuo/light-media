package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"light-media/internal/plugins"
)

type MediaServerRefreshConfiguration struct {
	Enabled      bool            `json:"enabled"`
	Delay        json.RawMessage `json:"delay"`
	MediaServers []string        `json:"mediaservers"`
}

func (c MediaServerRefreshConfiguration) Duration() (time.Duration, error) {
	seconds, err := pluginConfigNumber(c.Delay, 0)
	if err != nil || len(c.MediaServers) > 128 {
		return 0, errors.New("invalid refresh configuration")
	}
	// Original negative delays have an already elapsed deadline and therefore
	// refresh immediately. Bound only the actual Go duration representation.
	if seconds < 0 {
		seconds = 0
	}
	milliseconds := math.Ceil(seconds * 1000)
	if milliseconds > float64((time.Duration(1<<63 - 1)).Milliseconds()) {
		return 0, errors.New("refresh delay exceeds duration range")
	}
	seen := map[string]bool{}
	for _, server := range c.MediaServers {
		if server == "" || len(server) > 256 || strings.ContainsAny(server, "\x00\r\n") || seen[server] {
			return 0, errors.New("invalid media server reference")
		}
		seen[server] = true
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func MediaServerRefreshManifest() plugins.Manifest {
	return plugins.Manifest{SchemaVersion: 1, PluginID: "MediaServerRefresh", Name: "媒体库服务器刷新", Description: "入库后自动刷新Emby/Jellyfin/Plex服务器海报墙。", Icon: "refresh2.png", Version: "1.3.3", Author: "jxxghp", AuthorURL: "https://github.com/jxxghp", Runtime: "go-executable", Entry: "light-media", HostAPIMin: 1, HostAPIMax: 1, Methods: []string{"config_form", "invoke", "stop"}, Capabilities: []string{"media.servers", "events.configure", "media.refresh"}, Events: []string{"import.completed"}}
}

const mediaServerRefreshForm = `[{"component":"VForm","content":[
 {"component":"VRow","content":[{"component":"VCol","props":{"cols":12,"md":6},"content":[{"component":"VSwitch","props":{"model":"enabled","label":"启用插件"}}]}]},
 {"component":"VRow","content":[{"component":"VCol","props":{"cols":12},"content":[{"component":"VSelect","props":{"multiple":true,"chips":true,"clearable":true,"model":"mediaservers","label":"媒体服务器","items":__SERVER_OPTIONS__}}]}]},
 {"component":"VRow","content":[{"component":"VCol","props":{"cols":12},"content":[{"component":"VTextField","props":{"model":"delay","label":"延迟时间（秒）","placeholder":"0"}}]}]}
]}]`

// OriginalMediaServerRefresh retains the actual initialized members. Its caller serializes
// Handle and State; legacy SDK calls keep their historical per-call lifetime.
type OriginalMediaServerRefresh struct {
	config MediaServerRefreshConfiguration
}

func NewOriginalMediaServerRefresh() *OriginalMediaServerRefresh {
	return &OriginalMediaServerRefresh{}
}
func (p *OriginalMediaServerRefresh) State() bool { return p.config.Enabled }
func (p *OriginalMediaServerRefresh) Configuration() MediaServerRefreshConfiguration {
	c := p.config
	c.Delay = append(json.RawMessage(nil), c.Delay...)
	c.MediaServers = append([]string(nil), c.MediaServers...)
	return c
}

func HandleMediaServerRefresh(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	p := NewOriginalMediaServerRefresh()
	// Legacy single-call SDK callers supply configuration on each invocation.
	// Only this compatibility wrapper seeds non-init calls; a retained object's
	// Handle always reads its own initialized members instead.
	if method != "stop" && method != "config_form" {
		var invocation plugins.Invocation
		if json.Unmarshal(raw, &invocation) != nil {
			return nil, errors.New("invalid media refresh invocation")
		}
		if invocation.Action != "init" && invocation.Action != "reload" {
			if json.Unmarshal(invocation.Config, &p.config) != nil {
				return nil, errors.New("invalid media refresh invocation")
			}
		}
	}
	return p.Handle(ctx, method, raw, host)
}

func (p *OriginalMediaServerRefresh) Handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	if method == "stop" {
		return json.RawMessage(`null`), nil // The host owns durable delays and draining.
	}
	if method == "config_form" {
		options, err := host(ctx, "media.servers", json.RawMessage(`{}`))
		if err != nil {
			return nil, err
		}
		var choices []struct {
			Title string `json:"title"`
			Value string `json:"value"`
		}
		if json.Unmarshal(options, &choices) != nil || len(choices) > 128 {
			return nil, errors.New("invalid media server choices")
		}
		options, _ = json.Marshal(choices)
		if len(choices) == 0 {
			options = json.RawMessage(`[]`)
		}
		form := json.RawMessage(strings.Replace(mediaServerRefreshForm, "__SERVER_OPTIONS__", string(options), 1))
		return json.Marshal(map[string]any{"conf": form, "model": MediaServerRefreshConfiguration{Delay: json.RawMessage(`0`), MediaServers: []string{}}})
	}
	var invocation plugins.Invocation
	var config MediaServerRefreshConfiguration
	if method != "invoke" || json.Unmarshal(raw, &invocation) != nil {
		return nil, errors.New("invalid media refresh invocation")
	}
	if invocation.Action == "init" || invocation.Action == "reload" {
		fields, err := originalStatelessConfigFields(invocation.Config)
		if err != nil {
			return nil, errors.New("invalid media refresh invocation")
		}
		if len(fields) > 0 {
			if json.Unmarshal(invocation.Config, &config) != nil {
				return nil, errors.New("invalid media refresh invocation")
			}
			p.config = config
		}
		config = p.config
	} else {
		config = p.config
	}
	delay, err := config.Duration()
	if err != nil {
		return nil, err
	}
	switch invocation.Action {
	case "init", "reload":
		request, _ := json.Marshal(map[string]any{"handler": "refresh", "event_types": []string{"import.completed"}, "delay_ms": delay.Milliseconds(), "active": config.Enabled && len(config.MediaServers) > 0})
		if _, err := host(ctx, "events.configure", request); err != nil {
			return nil, err
		}
		return json.RawMessage(`{"configured":true}`), nil
	case "event":
		if !config.Enabled || len(config.MediaServers) == 0 {
			return json.RawMessage(`{"skipped":true}`), nil
		}
		var event struct {
			BatchID string `json:"batch_id"`
		}
		if json.Unmarshal(invocation.Input, &event) != nil || event.BatchID == "" {
			return nil, errors.New("missing media refresh batch")
		}
		request, _ := json.Marshal(event)
		return host(ctx, "media.refresh", request)
	default:
		return nil, errors.New("unsupported media refresh action")
	}
}

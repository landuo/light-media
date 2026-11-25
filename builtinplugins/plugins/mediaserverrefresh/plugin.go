package mediaserverrefresh

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"sync"

	sdk "light-media/builtinplugins/internal/sdk"
	"light-media/builtinplugins/plugins/removelink"
)

//go:embed form.json
var form json.RawMessage

func Manifest() sdk.Manifest {
	return sdk.Manifest{
		SchemaVersion: 1, PluginID: "MediaServerRefresh", Name: "媒体库服务器刷新",
		Description: "入库后自动刷新Emby/Jellyfin/Plex服务器海报墙。", Icon: "refresh2.png",
		Version: "1.0.0", Author: "jxxghp", AuthorURL: "https://github.com/jxxghp",
		PluginOrder: 14, Runtime: "go-executable", Entry: "media-server-refresh",
		HostAPIMin: 1, HostAPIMax: 1, ManagedPublicServices: true,
		Methods: []string{
			"init", "get_state", "get_form", "get_render_mode", "get_page",
			"get_command", "get_api", "get_service", "refresh", "stop_service", "stop", "close",
		},
		Capabilities:      []string{sdk.MediaServersListCapability, sdk.MediaServersRefreshCapability, sdk.LogWriteCapability},
		BroadcastHandlers: []sdk.BroadcastHandler{{Event: "transfer.complete", Method: "refresh"}},
		RuntimeSupport:    &sdk.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}},
	}
}

type configuration struct {
	Enabled      any      `json:"enabled"`
	Delay        any      `json:"delay"`
	MediaServers []string `json:"mediaservers"`
}

type Plugin struct {
	mu       sync.Mutex
	config   configuration
	inDelay  bool
	deadline float64
	pending  []sdk.MediaRefreshItem
}

func NewPlugin() *Plugin { return &Plugin{} }

func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, caller sdk.HostCaller) (json.RawMessage, error) {
	switch method {
	case "init":
		return nil, p.initialize(raw)
	case "get_state":
		return json.Marshal(removelink.Truth(p.configuration().Enabled))
	case "get_form":
		return getForm(ctx, sdk.MediaHost{Host: caller})
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "refresh":
		return nil, p.refresh(ctx, raw, caller)
	case "stop_service":
		p.mu.Lock()
		p.deadline = 0
		p.mu.Unlock()
		return nil, nil
	case "get_page", "get_command", "get_api", "get_service", "stop", "close":
		return nil, nil
	default:
		return nil, errors.New("unsupported MediaServerRefresh method")
	}
}

func (p *Plugin) initialize(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	value, err := removelink.Decode(raw)
	if err != nil || !removelink.Truth(value) {
		return err
	}
	if _, ok := value.(map[string]any); !ok {
		return errors.New("MediaServerRefresh configuration has no get method")
	}
	var config configuration
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&config); err != nil {
		return err
	}
	p.mu.Lock()
	p.config = config
	p.mu.Unlock()
	return nil
}

func (p *Plugin) configuration() configuration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.config
}

type formNode struct {
	Component string         `json:"component"`
	Props     map[string]any `json:"props,omitempty"`
	Content   []formNode     `json:"content,omitempty"`
}

func getForm(ctx context.Context, host sdk.MediaHost) (json.RawMessage, error) {
	servers, err := host.Servers(ctx, sdk.MediaServersRequest{})
	if err != nil {
		return nil, err
	}
	type option struct {
		Title string `json:"title"`
		Value string `json:"value"`
	}
	choices := make([]option, 0, len(servers))
	for _, server := range servers {
		choices = append(choices, option{Title: server.Name, Value: server.Name})
	}
	var schema []formNode
	if err := json.Unmarshal(form, &schema); err != nil {
		return nil, err
	}
	schema[0].Content[1].Content[0].Content[0].Props["items"] = choices
	return json.Marshal([]any{schema, map[string]any{"enabled": false, "delay": 0}})
}

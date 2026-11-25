package systemprocess

import (
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
		SchemaVersion: 1, PluginID: "SystemProcess", Name: "系统进程",
		Description: "查看系统进程，支持仪表板", Icon: "Dsfinder_A.png",
		Version: "1.0.0", Author: "hotlcc", AuthorURL: "https://github.com/hotlcc",
		PluginOrder: 66, Runtime: "go-executable", Entry: "system-process",
		HostAPIMin: 1, HostAPIMax: 1, HasPage: true,
		Methods: []string{
			"init", "get_state", "get_form", "get_page", "get_dashboard",
			"get_render_mode", "get_command", "get_api", "get_service",
			"stop_service", "stop", "close",
		},
		Capabilities:   []string{sdk.BaseGetInstanceMetadata},
		RuntimeSupport: &sdk.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}},
	}
}

type Plugin struct {
	mu     sync.RWMutex
	config any
	sample func(context.Context) ([]ProcessInfo, error)
}

func NewPlugin() *Plugin {
	return &Plugin{sample: sampleProcesses}
}

func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, caller sdk.HostCaller) (json.RawMessage, error) {
	switch method {
	case "init":
		var config any
		if len(raw) != 0 {
			value, err := removelink.Decode(raw)
			if err != nil {
				return nil, err
			}
			if removelink.Truth(value) {
				config = value
				if row, ok := value.(map[string]any); ok && len(row) == 2 && falseScalar(row["enable"]) && falseScalar(row["enabled"]) {
					config = nil
				}
			}
		}
		p.mu.Lock()
		p.config = config
		p.mu.Unlock()
		return nil, nil
	case "get_state":
		config, err := p.configuration()
		if err != nil {
			return nil, err
		}
		return json.Marshal(removelink.Truth(config["enable"]))
	case "get_form":
		return json.Marshal([]any{form, map[string]int{"dashboard_widget_size": 8, "dashboard_widget_refresh": 5}})
	case "get_page":
		items, err := p.sample(ctx)
		if err != nil {
			return nil, err
		}
		return json.Marshal([]any{processTable(items, false)})
	case "get_dashboard":
		return p.dashboard(ctx, caller)
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "get_command", "get_api", "get_service", "stop_service", "stop", "close":
		return json.RawMessage(`null`), nil
	default:
		return nil, errors.New("unsupported SystemProcess method")
	}
}

// SystemProcess has no mutable process cache. Its live configuration object
// is replaced atomically on init and read-only for every subsequent method.
func (p *Plugin) configuration() (map[string]any, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.config == nil {
		return nil, nil
	}
	row, ok := p.config.(map[string]any)
	if !ok {
		return nil, errors.New("SystemProcess configuration has no get method")
	}
	return row, nil
}

func falseScalar(value any) bool {
	switch v := value.(type) {
	case bool:
		return !v
	case json.Number:
		number, err := v.Float64()
		return err == nil && number == 0
	default:
		return false
	}
}

func (p *Plugin) dashboard(ctx context.Context, caller sdk.HostCaller) (json.RawMessage, error) {
	config, err := p.configuration()
	if err != nil {
		return nil, err
	}
	size := config["dashboard_widget_size"]
	if size == nil {
		size = 8
	}
	name, err := (sdk.Base{Host: caller}).GetName(ctx)
	if err != nil {
		return nil, err
	}
	cols := map[string]any{"cols": 12, "xxl": size, "xl": size, "lg": size, "md": size, "sm": 12, "xs": 12}
	attrs := map[string]any{"title": name}
	if refresh := config["dashboard_widget_refresh"]; removelink.Truth(refresh) {
		attrs["refresh"] = refresh
	}
	items, err := p.sample(ctx)
	if err != nil {
		return nil, err
	}
	return json.Marshal([]any{cols, attrs, []any{processTable(items, true)}})
}

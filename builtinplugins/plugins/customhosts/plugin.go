package customhosts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	sdk "light-media/builtinplugins/internal/sdk"
	"light-media/builtinplugins/plugins/removelink"
)

const (
	PluginID        = "CustomHosts"
	Version         = "1.0.0"
	applyCapability = "custom_hosts.apply"
)

func Manifest() sdk.Manifest {
	return sdk.Manifest{
		SchemaVersion: 1, PluginID: PluginID, Name: "自定义Hosts", Version: Version,
		Description: "修改系统hosts文件，加速网络访问。", Icon: "hosts.png", Author: "thsrite",
		AuthorURL: "https://github.com/thsrite", PluginOrder: 10,
		Runtime: "go-executable", Entry: "custom-hosts", HostAPIMin: 1, HostAPIMax: 1,
		Methods: []string{"init", "get_state", "get_form", "get_render_mode", "get_command", "get_service", "get_api", "get_page",
			"reload_event", "close", "stop_service", "stop"},
		Capabilities:          []string{sdk.BaseGetInstanceMetadata, sdk.BaseGetConfig, sdk.BaseUpdateConfig, applyCapability},
		BroadcastHandlers:     []sdk.BroadcastHandler{{Event: "plugin.reload", Method: "reload_event"}},
		ManagedPublicServices: true,
		RuntimeSupport:        &sdk.RuntimeSupport{Platforms: []string{"linux"}, StorageTypes: []string{"local"}},
	}
}

type Plugin struct {
	applyMu sync.Mutex
	enabled atomic.Value // immutable json.RawMessage holding the original state value
}

func NewPlugin() *Plugin {
	p := &Plugin{}
	p.enabled.Store(json.RawMessage(`false`))
	return p
}

func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, caller sdk.HostCaller) (json.RawMessage, error) {
	switch method {
	case "init":
		p.applyMu.Lock()
		defer p.applyMu.Unlock()
		return nil, p.initialize(ctx, raw, caller)
	case "get_state":
		return p.enabled.Load().(json.RawMessage), nil
	case "get_form":
		return json.RawMessage(form), nil
	case "get_render_mode":
		return json.RawMessage(`["vuetify",null]`), nil
	case "get_command", "get_service", "get_api", "get_page", "close", "stop_service", "stop":
		return nil, nil
	case "reload_event":
		return nil, p.reload(ctx, raw, caller)
	default:
		return nil, errors.New("unsupported CustomHosts method")
	}
}

func (p *Plugin) reload(ctx context.Context, raw json.RawMessage, caller sdk.HostCaller) error {
	var event sdk.MutableEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return err
	}
	if event.Type != "plugin.reload" || len(event.Data) == 0 {
		return nil
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(event.Data, &input); err != nil {
		return err
	}
	var target string
	if json.Unmarshal(input["plugin_id"], &target) != nil || target == "" {
		return nil
	}
	base := sdk.Base{Host: caller}
	metadata, err := base.GetInstanceMetadata(ctx)
	if err != nil || target != metadata.InstanceID {
		return err
	}
	p.applyMu.Lock()
	defer p.applyMu.Unlock()
	config, err := base.GetConfig(ctx)
	if err != nil {
		return err
	}
	return p.initialize(ctx, config, caller)
}

func (p *Plugin) initialize(ctx context.Context, raw json.RawMessage, caller sdk.HostCaller) error {
	if len(raw) == 0 {
		return nil
	}
	value, err := removelink.Decode(raw)
	if err != nil || !removelink.Truth(value) {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	enabled := fields["enabled"]
	if len(enabled) == 0 {
		enabled = json.RawMessage(`null`)
	}
	p.enabled.Store(enabled)
	switchValue, err := removelink.Decode(enabled)
	if err != nil {
		return err
	}
	request := applyRequest{Mode: "clear", Lines: []string{}}
	if removelink.Truth(switchValue) {
		lines, replace, err := normalizeRows(fields["hosts"])
		if err != nil {
			return err
		}
		if replace {
			request.Mode, request.Lines = "replace", lines
		}
	}
	reply, err := apply(ctx, caller, request)
	if err != nil || request.Mode == "clear" {
		return err
	}
	state := !*reply.WriteFailed
	if state {
		p.enabled.Store(json.RawMessage(`true`))
	} else {
		p.enabled.Store(json.RawMessage(`false`))
	}
	var text strings.Builder
	for _, line := range request.Lines {
		text.WriteString(line)
		text.WriteByte('\n')
	}
	_, err = (sdk.Base{Host: caller}).UpdateConfig(ctx, map[string]any{"hosts": text.String(), "err_hosts": reply.ErrHosts, "enabled": state})
	return err
}

type applyRequest struct {
	Mode  string   `json:"mode"`
	Lines []string `json:"lines"`
}

type applyResult struct {
	WriteFailed *bool    `json:"write_failed"`
	ErrHosts    []string `json:"err_hosts"`
}

func apply(ctx context.Context, caller sdk.HostCaller, request applyRequest) (applyResult, error) {
	var result applyResult
	if caller == nil {
		return result, errors.New("CustomHosts requires an active callback")
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	raw, err := caller(ctx, applyCapability, payload)
	if err != nil {
		return result, err
	}
	if json.Unmarshal(raw, &result) != nil || result.WriteFailed == nil || result.ErrHosts == nil {
		return result, errors.New("invalid CustomHosts apply result")
	}
	return result, nil
}

// Source strings are split before the truth gate. Empty text therefore enters
// the replace path, while a missing value or empty list enters the clear path.
func normalizeRows(raw json.RawMessage) ([]string, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	value, err := removelink.Decode(raw)
	if err != nil {
		return nil, false, err
	}
	if text, ok := value.(string); ok {
		lines := []string{}
		for _, line := range strings.Split(text, "\n") {
			if line != "" {
				lines = append(lines, line)
			}
		}
		return lines, true, nil
	}
	if !removelink.Truth(value) {
		return nil, false, nil
	}
	var rows []any
	switch value := value.(type) {
	case []any:
		rows = value
	case map[string]any:
		rows, err = objectKeys(raw)
		if err != nil {
			return nil, false, err
		}
	default:
		return nil, false, errors.New("CustomHosts hosts value is not iterable")
	}
	lines := []string{}
	for _, row := range rows {
		if !removelink.Truth(row) {
			continue
		}
		text, ok := row.(string)
		if !ok {
			return nil, false, errors.New("CustomHosts host row has no replace method")
		}
		if text != "\n" {
			lines = append(lines, strings.ReplaceAll(text, "\n", ""))
		}
	}
	return lines, true, nil
}

func objectKeys(raw json.RawMessage) ([]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	keys := []any{}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key := token.(string)
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
		var skip json.RawMessage
		if err := decoder.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// Package categoryeditor implements the CategoryEditor 1.3 plugin as one
// persistent standard SDK object. Its configuration and callbacks belong to
// the current host-bound instance; it has no native invoke adapter.
package categoryeditor

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"

	"light-media/internal/plugins"
)

const updateMessage = "二级分类策略已更新，请注意同步调整目录设置，插件将恢复关闭状态！"

//go:embed form.json
var form json.RawMessage

// Manifest describes the executable and source metadata, not migration status
// or an official authorization tier. The plugin has no page, service, command,
// API, event handler, dashboard or background job.
func Manifest() plugins.Manifest {
	return plugins.Manifest{
		SchemaVersion: 1, PluginID: "CategoryEditor", Name: "二级分类策略",
		Description: "编辑下载和整理时自动二级分类的目录规则。", Icon: "Bookstack_A.png",
		Author: "jxxghp", AuthorURL: "https://github.com/jxxghp", Version: "1.3",
		PluginOrder: 5, Runtime: "go-executable", Entry: "category-editor", HostAPIMin: 1, HostAPIMax: 1,
		Methods: []string{
			"init", "get_state", "get_service", "get_command", "get_api", "get_render_mode",
			"get_form", "get_page", "get_dashboard", "get_dashboard_meta", "get_auth_providers",
			"get_module", "close", "stop_service", "stop",
		},
		Capabilities: []string{
			plugins.CategoryDefaultCapability, plugins.CategoryApplyCapability,
			plugins.SystemMessagePutCapability, plugins.BaseUpdateConfig,
		},
	}
}

// Plugin holds the original Python object's members. Use New for each SDK
// process; reinitialization must not allocate a replacement object.
type Plugin struct {
	mu      sync.Mutex
	enabled json.RawMessage
	content json.RawMessage
}

func New() *Plugin {
	return &Plugin{enabled: json.RawMessage(`false`), content: json.RawMessage(`""`)}
}

// Handle receives raw configuration for init, never an Invocation envelope.
// The HostCaller is scoped to this call and is not retained on the object.
func (p *Plugin) Handle(ctx context.Context, method string, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch method {
	case "init":
		return p.init(ctx, raw, host)
	case "get_state":
		// Source annotations do not coerce the member: null and non-bools are
		// observable values, including after a failure during initialization.
		return append(json.RawMessage(nil), p.enabled...), nil
	case "get_form":
		return getForm(ctx, host)
	case "get_render_mode":
		// _PluginBase.get_render_mode returns ("vuetify", None).
		return json.RawMessage(`["vuetify",null]`), nil
	case "get_command", "get_api", "get_page", "stop_service":
		// CategoryEditor explicitly implements these methods with pass.
		return json.RawMessage(`null`), nil
	case "get_service", "get_dashboard", "get_dashboard_meta", "get_auth_providers", "get_module", "close", "stop":
		// The inherited getters and close have no body. stop is the SDK's
		// transport retirement, not another execution of init or a state reset.
		return json.RawMessage(`null`), nil
	default:
		return nil, errors.New("unsupported category editor method")
	}
}

func (p *Plugin) init(ctx context.Context, raw json.RawMessage, host plugins.HostCaller) (json.RawMessage, error) {
	config, err := decodeValue(raw)
	if err != nil {
		return nil, err
	}
	// Python's `if config` encloses assignments and all effects. In particular,
	// None and {} leave the existing object's members untouched.
	if !truthy(config) {
		return json.RawMessage(`null`), nil
	}
	values, ok := config.(map[string]any)
	if !ok {
		return nil, errors.New("category editor configuration has no get method")
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	// Keep the supplied JSON values rather than round-tripping strings through
	// Go Unicode decoding. Python can hold an unpaired surrogate; write_text
	// then raises an encoding error. Replacing it here would hide that failure
	// from the host and incorrectly write a replacement character instead.
	p.enabled = fields["enabled"]
	if len(p.enabled) == 0 {
		p.enabled = json.RawMessage(`null`)
	}
	p.content = fields["content"]
	if !truthy(values["content"]) {
		p.content = json.RawMessage(`""`)
	}
	if !truthy(values["enabled"]) {
		return json.RawMessage(`null`), nil
	}

	// The order is part of the original behavior: write_text, helper.init,
	// systemmessage.put, member assignment, then full update_config. Completed
	// effects are not rolled back when a later operation raises an exception.
	// The host performs the first two operations in CategoryApplyCapability.
	// Do not coerce truthy non-string content; write_text must reject its type.
	if _, err = callHost(ctx, host, plugins.CategoryApplyCapability, map[string]any{"content": p.content}); err != nil {
		return nil, err
	}
	if _, err = callHost(ctx, host, plugins.SystemMessagePutCapability, map[string]any{
		"message": updateMessage, "title": "二级分类策略",
	}); err != nil {
		return nil, err
	}
	p.enabled = json.RawMessage(`false`)
	// Python ignores update_config's return value (including False and None).
	// The full replacement deliberately drops unrelated configuration fields.
	if _, err = (plugins.Base{Host: host}).UpdateConfig(ctx, map[string]any{"enabled": false, "content": p.content}); err != nil {
		return nil, err
	}
	return json.RawMessage(`null`), nil
}

func getForm(ctx context.Context, host plugins.HostCaller) (json.RawMessage, error) {
	raw, err := callHost(ctx, host, plugins.CategoryDefaultCapability, map[string]any{})
	if err != nil {
		return nil, err
	}
	var content *string
	if err = json.Unmarshal(raw, &content); err != nil {
		return nil, err
	}
	if content == nil {
		return nil, errors.New("category default must be text")
	}
	// Query the host's bundled-default resource on every call, never the member
	// or user YAML. The host's immutable packaging difference is documented.
	// Preserve the original tuple-shaped SDK result.
	return json.Marshal([]any{form, map[string]any{"enabled": false, "content": *content}})
}

func callHost(ctx context.Context, host plugins.HostCaller, method string, value any) (json.RawMessage, error) {
	if host == nil {
		return nil, errors.New("category editor requires an active host caller")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return host(ctx, method, raw)
}

func decodeValue(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	// Config values are not restricted to booleans/strings, and arbitrary-size
	// JSON integers must not lose precision through an intermediate float64.
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("invalid category editor configuration JSON")
	}
	return value, nil
}

// truthy implements Python truth testing for values decoded from JSON. A
// nonempty string such as "false" is true; empty lists/maps and numeric zero
// are false. Floating-point underflow follows Python's JSON float decoding.
func truthy(value any) bool {
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	case []any:
		return len(value) != 0
	case map[string]any:
		return len(value) != 0
	case json.Number:
		if strings.ContainsAny(string(value), ".eE") {
			number, _ := strconv.ParseFloat(string(value), 64)
			return number != 0
		}
		return strings.TrimLeft(string(value), "-0") != ""
	default:
		// Only the JSON kinds above enter object members.
		return false
	}
}

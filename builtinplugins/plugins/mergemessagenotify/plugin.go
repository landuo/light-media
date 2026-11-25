package mergemessagenotify

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
)

//go:embed forms.json
var formsJSON []byte

type formSource struct {
	Name        string         `json:"name"`
	Order       int            `json:"order"`
	Defaults    map[string]any `json:"defaults"`
	Elements    []any          `json:"elements"`
	Data        map[string]any `json:"data"`
	SaveDefault bool           `json:"save_default"`
}

var definitionsOnce sync.Once
var channelDefinitions []Channel
var pluginElements []any

func definitions() ([]Channel, []any) {
	definitionsOnce.Do(loadDefinitions)
	return channelDefinitions, pluginElements
}
func loadDefinitions() {
	v, e := Decode(formsJSON)
	if e != nil {
		panic(e)
	}
	m := v.(map[string]any)
	defs := []Channel{}
	for k, x := range m {
		if k == "plugin" {
			continue
		}
		b, _ := json.Marshal(x)
		var f formSource
		d := json.NewDecoder(strings.NewReader(string(b)))
		d.UseNumber()
		if e = d.Decode(&f); e != nil {
			panic(e)
		}
		defs = append(defs, Channel{k, f.Name, f.Order, f.Defaults, f.Elements, f.Data, f.SaveDefault})
	}
	sort.Slice(defs, func(i, j int) bool {
		if defs[i].Order != defs[j].Order {
			return defs[i].Order < defs[j].Order
		}
		return defs[i].Key < defs[j].Key
	})
	channelDefinitions = defs
	pluginElements = m["plugin"].(map[string]any)["elements"].([]any)
}

type Plugin struct {
	mu             sync.Mutex
	config, stored map[string]any
	registered     []Channel
	host           Host
	qqBot          qqBot
}

func New(host Host) *Plugin              { return &Plugin{host: host} }
func (p *Plugin) Config() map[string]any { p.mu.Lock(); defer p.mu.Unlock(); return cloneMap(p.config) }
func (p *Plugin) StoredConfig() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneMap(p.stored)
}
func (p *Plugin) Channels() []Channel {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Channel, len(p.registered))
	copy(out, p.registered)
	for i := range out {
		out[i].Defaults = cloneMap(out[i].Defaults)
		out[i].Elements = clone(out[i].Elements).([]any)
		out[i].Suggested = cloneMap(out[i].Suggested)
	}
	return out
}
func (p *Plugin) State() bool { p.mu.Lock(); defer p.mu.Unlock(); return truth(p.config["enable"]) }

// Init does not replay a test-once flag during startup/reload. saveRequest is a
// trusted caller-origin fact equivalent to the original stack predicate; it is
// never inferred from plugin configuration or an untrusted event field.
func (p *Plugin) Init(ctx context.Context, config map[string]any, saveRequest bool) error {
	p.mu.Lock()
	p.stored = cloneMap(config)
	p.config = cloneMap(config)
	if len(config) == 0 || (len(config) == 2 && config["enabled"] == false && config["enable"] == false) {
		p.config = nil
	}
	defs, _ := definitions()
	p.mu.Unlock()
	for _, c := range defs {
		p.mu.Lock()
		for i, old := range p.registered {
			if old.Key == c.Key {
				p.registered = append(p.registered[:i], p.registered[i+1:]...)
				break
			}
		}
		p.mu.Unlock()
		if saveRequest {
			cfg := p.ComponentConfig(c.Key)
			if truth(cfg["test_once"]) {
				_, _ = p.send(ctx, c, Message{Title: "测试消息", Text: "这是一条测试消息，您收到此消息表示您的渠道配置无误。"}, cfg)
				cfg["test_once"] = false
				if _, e := p.UpdateComponent(ctx, c.Key, cfg); e != nil {
					continue
				}
			}
		}
		p.mu.Lock()
		p.registered = append(p.registered, c)
		p.mu.Unlock()
	}
	return nil
}
func (p *Plugin) Stop() { p.mu.Lock(); p.registered = nil; p.mu.Unlock() }
func (p *Plugin) ComponentConfig(key string) map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.componentConfig(key)
}
func (p *Plugin) componentConfig(key string) map[string]any {
	out := map[string]any{}
	if key == "" {
		return out
	}
	prefix := key + "."
	for k, v := range p.config {
		if k != "" && strings.HasPrefix(k, prefix) {
			out[strings.TrimPrefix(k, prefix)] = clone(v)
		}
	}
	return out
}
func (p *Plugin) save(ctx context.Context, config map[string]any) (bool, error) {
	ok := true
	var err error
	if p.host.SaveConfig != nil {
		ok, err = p.host.SaveConfig(ctx, cloneMap(config))
	}
	if err != nil {
		return false, err
	}
	p.mu.Lock()
	p.stored = cloneMap(config)
	p.mu.Unlock()
	return ok, nil
}
func (p *Plugin) UpdateComponent(ctx context.Context, key string, config map[string]any) (bool, error) {
	if key == "" {
		return false, nil
	}
	p.mu.Lock()
	current := cloneMap(p.config)
	p.mu.Unlock()
	if len(current) == 0 && len(config) == 0 {
		return false, nil
	}
	if current == nil {
		current = map[string]any{}
	}
	prefix := key + "."
	for k := range current {
		if strings.HasPrefix(k, prefix) {
			delete(current, k)
		}
	}
	for k, v := range config {
		if k != "" {
			current[prefix+k] = clone(v)
		}
	}
	result, err := p.save(ctx, current)
	if err != nil {
		return false, err
	}
	p.mu.Lock()
	p.config = current
	p.mu.Unlock()
	return result, nil
}
func (p *Plugin) saveChannelDefaults(ctx context.Context, c Channel) error {
	if !c.SaveDefaults || len(c.Defaults) == 0 {
		return nil
	}
	cfg := p.ComponentConfig(c.Key)
	changed := false
	for k, v := range c.Defaults {
		if k != "" {
			if _, ok := cfg[k]; !ok {
				cfg[k] = clone(v)
				changed = true
			}
		}
	}
	if changed {
		_, e := p.UpdateComponent(ctx, c.Key, cfg)
		return e
	}
	return nil
}
func (p *Plugin) Form(ctx context.Context) ([]any, map[string]any, error) {
	defs, elements := definitions()
	_ = defs
	elements = clone(elements).([]any)
	registered := p.Channels()
	data := map[string]any{"channel_strategy": "ALL_SELECTED"}
	keys := map[string]bool{}
	items := []any{}
	for _, c := range registered {
		keys[c.Key] = true
		items = append(items, map[string]any{"title": c.Name, "value": c.Key})
		if err := p.saveChannelDefaults(ctx, c); err != nil {
			return nil, nil, err
		}
		for k, v := range c.Suggested {
			if k != "" && truth(v) {
				data[c.Key+"."+k] = clone(v)
			}
		}
	}
	var walk func([]any) []any
	walk = func(elems []any) []any {
		r := []any{}
		for _, v := range elems {
			m := v.(map[string]any)
			props, _ := m["props"].(map[string]any)
			if model, _ := props["model"].(string); model == "enable_channels" {
				props["items"] = items
			}
			if value, _ := props["value"].(string); strings.HasPrefix(value, "_tab.") {
				if !keys[strings.TrimPrefix(value, "_tab.")] {
					continue
				}
			}
			if children, ok := m["content"].([]any); ok {
				m["content"] = walk(children)
			}
			r = append(r, m)
		}
		return r
	}
	elements = walk(elements)
	stored := p.StoredConfig()
	if p.host.LoadConfig != nil {
		var err error
		stored, err = p.host.LoadConfig(ctx)
		if err != nil {
			return nil, nil, err
		}
	}
	if stored == nil {
		stored = map[string]any{}
	}
	if _, exists := stored["channel_strategy"]; !exists {
		stored["channel_strategy"] = "ALL_SELECTED"
		if _, err := p.save(ctx, stored); err != nil {
			return nil, nil, err
		}
	}
	return elements, data, nil
}

func (p *Plugin) Send(ctx context.Context, key string, message Message) (any, error) {
	p.mu.Lock()
	for _, c := range p.registered {
		if c.Key == key {
			config := p.componentConfig(key)
			p.mu.Unlock()
			return p.send(ctx, c, message, config)
		}
	}
	p.mu.Unlock()
	return nil, errors.New("channel not registered")
}

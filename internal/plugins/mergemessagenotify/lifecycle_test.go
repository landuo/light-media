package mergemessagenotify

import (
	"context"
	"fmt"
	"testing"
)

func registered(p *Plugin) []string {
	out := []string{}
	for _, c := range p.Channels() {
		out = append(out, c.Key)
	}
	return out
}
func TestInstalledLifecycle(t *testing.T) {
	cases(t, "testdata/installed-lifecycle.jsonl.gz", func(i int, c map[string]any) {
		updates := []any{}
		p := New(Host{SaveConfig: func(_ context.Context, cfg map[string]any) (bool, error) {
			updates = append(updates, cfg)
			return c["update_result"].(bool), nil
		}})
		initial, _ := c["initial"].(map[string]any)
		if e := p.Init(context.Background(), initial, false); e != nil {
			t.Fatal(e)
		}
		for j, raw := range c["steps"].([]any) {
			s := raw.(map[string]any)
			key, _ := s["key"].(string)
			var result any
			var err error
			switch s["action"] {
			case "get":
				result = p.ComponentConfig(key)
			case "update":
				v, _ := s["value"].(map[string]any)
				result, err = p.UpdateComponent(context.Background(), key, v)
			case "form":
				var elements []any
				var data map[string]any
				elements, data, err = p.Form(context.Background())
				result = []any{elements, data}
			case "stop":
				p.Stop()
			}
			out := map[string]any{"return": result}
			if err != nil {
				out = map[string]any{"error": err.Error()}
			}
			prefix := fmt.Sprint(i, "/", j)
			equal(t, prefix+" outcome", out, s["outcome"])
			equal(t, prefix+" config", p.Config(), s["config"])
			equal(t, prefix+" stored", p.StoredConfig(), s["stored"])
			equal(t, prefix+" registered", registered(p), s["registered"])
		}
		equal(t, fmt.Sprint(i, " updates"), updates, c["updates"])
		equal(t, "page", p.Page(), c["page"])
		equal(t, "services", p.Services(), c["services"])
		equal(t, "commands", p.Commands(), c["commands"])
		equal(t, "API", p.API(), c["api"])
	})
}
func TestInstalledTestOnceOriginAndReset(t *testing.T) {
	cases(t, "testdata/installed-test_once.jsonl.gz", func(i int, c map[string]any) {
		updates := []any{}
		p := New(Host{SaveConfig: func(_ context.Context, cfg map[string]any) (bool, error) {
			updates = append(updates, cfg)
			return true, nil
		}})
		p.Init(context.Background(), nil, false)
		cfg := c["config"].(map[string]any)
		err := p.Init(context.Background(), cfg, c["save_request"].(bool))
		if err != nil {
			t.Fatal(err)
		}
		equal(t, fmt.Sprint(i, " config"), p.Config(), c["config_after"])
		equal(t, fmt.Sprint(i, " stored"), p.StoredConfig(), c["stored"])
		equal(t, fmt.Sprint(i, " updates"), updates, c["updates"])
		equal(t, fmt.Sprint(i, " registered"), registered(p), c["registered"])
	})
}

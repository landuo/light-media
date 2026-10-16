package mergemessagenotify

import (
	"context"
	"errors"
	"testing"
)

func TestActualMakoSubsetAndExplicitRemainingLanguage(t *testing.T) {
	supported, rejected := 0, 0
	cases(t, "testdata/installed-templates.jsonl.gz", func(i int, c map[string]any) {
		got, e := RenderBasic(context.Background(), c["text"], c["variables"].(map[string]any))
		if c["basic"] == true {
			if e != nil {
				t.Fatal(i, e)
			}
			equal(t, "actual Mako result", got, c["result"])
			supported++
		} else {
			if !errors.Is(e, ErrDependency) {
				t.Fatalf("unsupported source language silently accepted case %d, got=%v error=%v original=%v", i, got, e, c["result"])
			}
			rejected++
		}
	})
	if supported != 6 || rejected != 13 {
		t.Fatal("source limitation inventory changed", supported, rejected)
	}
}
func TestConfigAndFormCopyIsolation(t *testing.T) {
	ctx := context.Background()
	config := map[string]any{"unknown": map[string]any{"number": "9007199254740993", "array": []any{"value"}}}
	p := New(Host{})
	p.Init(ctx, config, false)
	config["unknown"].(map[string]any)["array"].([]any)[0] = "caller-mutated"
	cfg := p.Config()
	cfg["unknown"].(map[string]any)["array"].([]any)[0] = "copy-mutated"
	if p.Config()["unknown"].(map[string]any)["array"].([]any)[0] != "value" {
		t.Fatal("shared config reference")
	}
	elements, _, e := p.Form(ctx)
	if e != nil {
		t.Fatal(e)
	}
	elements[0].(map[string]any)["component"] = "mutated"
	again, _, _ := p.Form(ctx)
	if again[0].(map[string]any)["component"] != "VForm" {
		t.Fatal("shared form reference")
	}
	if p.Page() != nil || p.Services() != nil || p.Commands() != nil || p.API() != nil {
		t.Fatal("invented source entry")
	}
}

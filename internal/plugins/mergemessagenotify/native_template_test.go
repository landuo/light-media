package mergemessagenotify

import (
	"context"
	"errors"
	"testing"
)

func TestNativeTemplatePluginBoundaryAndURLValues(t *testing.T) {
	ctx := context.Background()
	p := New(Host{Render: RenderNative})
	got, err := p.render(ctx, "https://owned.invalid/${xs}", map[string]any{"xs": []any{"甲", "乙"}}, true)
	want := "https://owned.invalid/%5B%27%E7%94%B2%27%2C%20%27%E4%B9%99%27%5D"
	if err != nil || got != want {
		t.Fatal(got, err)
	}
	got, err = p.render(ctx, "${1/0}", map[string]any{"x": true}, false)
	if err != nil || got != "${1/0}" {
		t.Fatal(got, err)
	}
	_, err = p.render(ctx, "${hex(10)}", map[string]any{"x": true}, false)
	if !errors.Is(err, ErrDependency) {
		t.Fatal(err)
	}
	for _, v := range []any{nil, "", false, 0} {
		got, err = RenderNative(ctx, v, map[string]any{"v": 1})
		if err != nil || got != v {
			t.Fatal(v, got, err)
		}
	}
}

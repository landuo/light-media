package mako

import (
	"context"
	"errors"
	"testing"
)

func TestOrderedJSONCarriesActualSourceDictOrder(t *testing.T) {
	ctx := context.Background()
	got, err := RenderJSON(ctx, "${data}:${list(data.keys())}:${data['z']}", `{"data":{"z":1,"a":2,"z":3}}`)
	if err != nil || got != "{'z': 3, 'a': 2}:['z', 'a']:3" {
		t.Fatal(got, err)
	}
	got, err = Render(ctx, "${data['z']}:${len(data)}", map[string]any{"data": map[string]any{"z": 1, "a": 2}})
	if err != nil || got != "1:2" {
		t.Fatal(got, err)
	}
	for _, tmpl := range []string{"${data}", "% for k in data:\n${k}\n% endfor\n"} {
		_, err = Render(ctx, tmpl, map[string]any{"data": map[string]any{"z": 1, "a": 2}})
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("invented order for %s: %v", tmpl, err)
		}
	}
}
func TestUnportedValidBuiltinAndMethodsAreExplicit(t *testing.T) {
	for _, text := range []string{"${hex(10)}", "${'a'.encode()}", "${[1].append(2)}"} {
		_, err := Render(context.Background(), text, map[string]any{"v": 1})
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("valid language hidden as original-text exception %s: %v", text, err)
		}
	}
}

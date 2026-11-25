package torrenttransfer

import (
	_ "embed"
	"errors"
)

// Exact installed get_form AST values; the two downloader lists alone are
// populated by the current trusted inventory. No original application import.
//
//go:embed form.json
var formBytes []byte

//go:embed defaults.json
var defaultBytes []byte

func Form(downloaders []string) ([]any, map[string]any, error) {
	f, e := decode(formBytes)
	if e != nil {
		return nil, nil, e
	}
	v, e := decode(defaultBytes)
	if e != nil {
		return nil, nil, e
	}
	items := []any{}
	for _, n := range downloaders {
		items = append(items, map[string]any{"title": n, "value": n})
	}
	count := 0
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if p, ok := x["props"].(map[string]any); ok {
				if p["model"] == "fromdownloader" || p["model"] == "todownloader" {
					p["items"] = items
					count++
				}
			}
			for _, n := range x {
				visit(n)
			}
		case []any:
			for _, n := range x {
				visit(n)
			}
		}
	}
	visit(f)
	if count != 2 {
		return nil, nil, errors.New("transfer form inventory contract changed")
	}
	return f.([]any), v.(map[string]any), nil
}
func Commands() any { return nil }
func Routes() any   { return nil }
func Page() any     { return nil }

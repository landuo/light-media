package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"light-media/internal/plugins"
)

// Fixed recommend/source and discover/source both send an ordinary synchronous
// mutable event, then return only extra_sources (not the built-in source rows).
func (a *App) originalCatalogSources(ctx context.Context, kind string) ([]map[string]any, error) {
	event := &plugins.MutableEvent{Type: kind + ".source", Data: json.RawMessage(`{"extra_sources":[]}`)}
	if !a.sendOriginalChainEvent(ctx, event) {
		return []map[string]any{}, nil
	}
	value, err := decodeSourceJSON(event.Data)
	if err != nil {
		return nil, originalCatalogSourcesInvalid()
	}
	input, ok := value.(map[string]any)
	if !ok {
		return nil, originalCatalogSourcesInvalid()
	}
	sources := input["extra_sources"]
	if !sourceSubscriptionTruth(sources) {
		return []map[string]any{}, nil
	}
	rows, ok := sources.([]any)
	if !ok {
		return nil, originalCatalogSourcesInvalid()
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		raw, ok := row.(map[string]any)
		if !ok {
			return nil, originalCatalogSourcesInvalid()
		}
		item, err := originalCatalogSourceProjection(raw, kind)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}
func originalCatalogSourcesInvalid() error {
	return &APIError{"PLUGIN_CATALOG_SOURCE_INVALID", "插件媒体数据源返回格式无效", 500}
}
func originalCatalogSourceProjection(input map[string]any, kind string) (map[string]any, error) {
	out := map[string]any{}
	required := []string{"name", "api_path", "type"}
	if kind == "discover" {
		required = []string{"name", "mediaid_prefix", "api_path"}
		out["filter_params"] = nil
		out["filter_ui"] = []any{}
		out["depends"] = nil
	}
	for _, key := range required {
		text, ok := input[key].(string)
		if !ok {
			return nil, originalCatalogSourcesInvalid()
		}
		out[key] = text
	}
	if kind != "discover" {
		return out, nil
	}
	for _, key := range []string{"filter_params", "filter_ui", "depends"} {
		v, present := input[key]
		if !present {
			continue
		}
		if v == nil {
			out[key] = nil
			continue
		}
		if key == "filter_ui" {
			rows, ok := v.([]any)
			if !ok {
				return nil, originalCatalogSourcesInvalid()
			}
			for _, row := range rows {
				if _, ok := row.(map[string]any); !ok {
					return nil, originalCatalogSourcesInvalid()
				}
			}
		} else {
			values, ok := v.(map[string]any)
			if !ok {
				return nil, originalCatalogSourcesInvalid()
			}
			if key == "depends" {
				for _, value := range values {
					if _, ok := value.([]any); !ok {
						return nil, originalCatalogSourcesInvalid()
					}
				}
			}
		}
		out[key] = v
	}
	return out, nil
}
func (a *App) serveOriginalCatalogSources(w http.ResponseWriter, r *http.Request, p string) bool {
	if r.Method != http.MethodGet {
		return false
	}
	path := p
	if strings.HasPrefix(path, "v1/") || strings.HasPrefix(path, "v2/") {
		path = path[3:]
	}
	var kind string
	switch path {
	case "recommend/source":
		kind = "recommend"
	case "discover/source":
		kind = "discover"
	default:
		return false
	}
	value, err := a.originalCatalogSources(r.Context(), kind)
	if err != nil {
		a.writeError(w, r, err)
		return true
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
	return true
}

package uninstall

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// LocalPlugin is source-shaped metadata, supplied only by the trusted host.
// Maps distinguish missing attributes from present null values in get_form.
type LocalPlugin map[string]any

type Option struct {
	Title string `json:"title"`
	Value any    `json:"value"`
}

func attribute(plugin LocalPlugin, key string, fallback any) any {
	if v, exists := plugin[key]; exists {
		return v
	}
	return fallback
}

func numeric(value any) (float64, bool) {
	switch v := value.(type) {
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

func sourceString(value any) string {
	switch v := value.(type) {
	case nil:
		return "None"
	case string:
		return v
	case bool:
		if v {
			return "True"
		}
		return "False"
	case json.Number:
		if !strings.ContainsAny(string(v), ".eE") {
			return string(v)
		}
		if n, err := v.Float64(); err == nil {
			text := strconv.FormatFloat(n, 'g', -1, 64)
			if !strings.ContainsAny(text, ".eE") {
				text += ".0"
			}
			return text
		}
	}
	return fmt.Sprint(value)
}

// LocalOptions uses stable plugin_order sorting and only installed metadata.
// It never queries a market, compares versions, or excludes this plugin itself.
func LocalOptions(local []LocalPlugin) ([]Option, error) {
	installed := []LocalPlugin{}
	for _, plugin := range local {
		if truthy(attribute(plugin, "installed", false)) {
			installed = append(installed, plugin)
		}
	}
	var sortErr error
	sort.SliceStable(installed, func(i, j int) bool {
		a, b := attribute(installed[i], "plugin_order", 1000), attribute(installed[j], "plugin_order", 1000)
		if left, ok := a.(string); ok {
			if right, ok := b.(string); ok {
				return left < right
			}
		}
		left, lok := numeric(a)
		right, rok := numeric(b)
		if !lok || !rok {
			sortErr = errors.New("incomparable uninstall plugin order")
			return false
		}
		return left < right
	})
	if sortErr != nil {
		return nil, sortErr
	}
	options := []Option{}
	for _, plugin := range installed {
		id := attribute(plugin, "id", nil)
		if !truthy(id) {
			continue
		}
		name := attribute(plugin, "plugin_name", attribute(plugin, "id", "未知插件"))
		version := attribute(plugin, "plugin_version", "N/A")
		options = append(options, Option{Title: sourceString(name) + " v" + sourceString(version), Value: id})
	}
	return options, nil
}

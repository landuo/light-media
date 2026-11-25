package pluginuninstall

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
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

// Python compares integer order keys exactly, including int/float mixtures.
// A float64 projection would make adjacent values above2**53 falsely equal.
type orderNumber struct {
	value *big.Rat
	inf   int
}

func numeric(value any) (orderNumber, bool) {
	var f float64
	switch v := value.(type) {
	case json.Number:
		if !strings.ContainsAny(string(v), ".eE") {
			n, ok := new(big.Int).SetString(string(v), 10)
			if !ok {
				return orderNumber{}, false
			}
			return orderNumber{value: new(big.Rat).SetInt(n)}, true
		}
		var err error
		f, err = v.Float64()
		if err != nil && !math.IsInf(f, 0) {
			return orderNumber{}, false
		}
	case float64:
		f = v
	case int:
		return orderNumber{value: new(big.Rat).SetInt64(int64(v))}, true
	case int64:
		return orderNumber{value: new(big.Rat).SetInt64(v)}, true
	case bool:
		if v {
			return orderNumber{value: big.NewRat(1, 1)}, true
		}
		return orderNumber{value: new(big.Rat)}, true
	default:
		return orderNumber{}, false
	}
	if math.IsNaN(f) {
		return orderNumber{}, false
	}
	if math.IsInf(f, 1) {
		return orderNumber{inf: 1}, true
	}
	if math.IsInf(f, -1) {
		return orderNumber{inf: -1}, true
	}
	return orderNumber{value: new(big.Rat).SetFloat64(f)}, true
}

func sourceString(value any) string {
	switch v := value.(type) {
	case metadataContainer:
		parsed, _ := decodeMetadataValue(bytes.TrimSpace(v))
		return pythonMetadataRepr(parsed)
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
		n, _ := v.Float64()
		if math.IsInf(n, 1) {
			return "inf"
		}
		if math.IsInf(n, -1) {
			return "-inf"
		}
		scientific := strconv.FormatFloat(n, 'e', -1, 64)
		idx := strings.LastIndexByte(scientific, 'e')
		exponent, _ := strconv.Atoi(scientific[idx+1:])
		if exponent >= -4 && exponent < 16 {
			text := strconv.FormatFloat(n, 'f', -1, 64)
			if !strings.ContainsRune(text, '.') {
				text += ".0"
			}
			return text
		}
		return scientific
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
		if left.inf != 0 || right.inf != 0 {
			return left.inf < right.inf
		}
		return left.value.Cmp(right.value) < 0
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

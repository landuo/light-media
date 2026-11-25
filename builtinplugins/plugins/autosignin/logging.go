package autosignin

import (
	"encoding/json"
	"light-media/builtinplugins/plugins/autosignin/pyvalue"
	"strings"
)

// log is placed at the original logger expression. A host guard or revoked
// callback is terminal, rather than becoming a normal site/network exception.
func (e *Engine) log(level, message string) {
	if e.LogLive != nil {
		if err := e.LogLive(level, message); err != nil {
			panic(&boundaryFailure{err})
		}
	}
}
func pythonList(values []any) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			parts = append(parts, sourceRepr(text))
		} else {
			parts = append(parts, pythonScalar(value))
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
func pythonFormRepr(values map[string]string, order []string) string {
	parts := make([]string, 0, len(order))
	for _, key := range order {
		parts = append(parts, sourceRepr(key)+": "+sourceRepr(values[key]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
func siteLog(h SiteVisitHooks, level, message string) {
	if h.Log != nil {
		if err := h.Log(level, message); err != nil {
			panic(&boundaryFailure{err})
		}
	}
}
func pythonJSONRepr(text string) string {
	value, err := pyvalue.JSONString(json.RawMessage(text))
	if err != nil {
		panic(&sourceException{err})
	}
	return value
}

package autosignin

import (
	"encoding/json"
	"errors"
	"light-media/builtinplugins/plugins/autosignin/pyvalue"
	"strings"
)

// OCR is annotated str in Python but its JSON result has no type enforcement.
// Retain raw order/precision for the eventual Requests form and logger repr.
type sourceOCRValue struct {
	value    any
	raw      json.RawMessage
	fromHTTP bool
}

func rawOCRValue(value any) (json.RawMessage, error) {
	if v, ok := value.(*sourceOCRValue); ok {
		return append(json.RawMessage(nil), v.raw...), nil
	}
	return json.Marshal(value)
}
func ocrString(value *sourceOCRValue) string {
	if value.fromHTTP {
		return pythonScalar(value.value)
	}
	text, err := pyvalue.JSONString(value.raw)
	if err != nil {
		panic(&sourceException{err})
	}
	return text
}

// This is Requests' existing two-stage doseq behavior, now over the already
// parsed source JSON values instead of forcing NaN/Inf back through strict IPC
// JSON. It preserves dictionary insertion order and drops only outer None.
func sourceOCRFormValues(value any) ([]string, error) {
	iterable := func(value any) []any {
		switch x := value.(type) {
		case []any:
			return x
		case sourceHTTPMap:
			out := make([]any, 0, len(x.keys))
			for _, key := range x.keys {
				out = append(out, key)
			}
			return out
		}
		return []any{value}
	}
	out := []string{}
	for _, v := range iterable(value) {
		if v == nil {
			continue
		}
		for _, child := range iterable(v) {
			if !sourceOCRFormReprDepth(child) {
				return nil, &ValueError{"RecursionError", "maximum recursion depth exceeded while getting the repr of an object"}
			}
			text := pythonScalar(child)
			// Scalar strings are UTF8-encoded before urlencode. A nested container's
			// repr already escapes any lone codepoint, exactly as Python str does.
			if _, ok := child.(string); ok {
				if e := sourceWebSerializationFailure(text); e != nil {
					var detail *ValueError
					if errors.As(e, &detail) && detail.Kind == "PydanticSerializationError" {
						return nil, &ValueError{"UnicodeEncodeError", strings.TrimPrefix(detail.Message, "Error serializing to JSON: UnicodeEncodeError: ")}
					}
					return nil, e
				}
			}
			out = append(out, text)
		}
	}
	return out, nil
}

// The actual fixed RequestUtils form path accepts child repr container depth
// 9993 and fails at9994 for list/dict/mixed trees. This is separate from the
// existing HTTP JSON parser's9997 container bound, which remains unchanged.
func sourceOCRFormReprDepth(value any) bool {
	type item struct {
		value any
		depth int
	}
	pending := []item{{value, 1}}
	for len(pending) > 0 {
		last := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		var children []any
		switch x := last.value.(type) {
		case []any:
			children = x
		case sourceHTTPMap:
			for _, key := range x.keys {
				children = append(children, x.values[key])
			}
		default:
			continue
		}
		// CPython's empty list/dict repr returns before entering recursion.
		if len(children) == 0 {
			continue
		}
		if last.depth > 9993 {
			return false
		}
		for _, child := range children {
			pending = append(pending, item{child, last.depth + 1})
		}
	}
	return true
}

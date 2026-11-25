package autosignin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// sourceCacheJSON is only Tjupt's local answer-file json.dumps(indent=4).
// Its input is the JSON-decoded old cache plus the selected answer string.
// Python's file serializer permits NaN/Infinity and escapes all non-ASCII,
// including unpaired codepoints. Those bytes never enter a JSON IPC envelope
// as a raw JSON value. Request-body and strict saved-data serializers keep
// their separate contracts.
func sourceCacheJSON(cache Object, keys []string) ([]byte, error) {
	return sourceCacheJSONWithContext(context.Background(), cache, keys)
}
func sourceCacheJSONWithContext(ctx context.Context, cache Object, keys []string) ([]byte, error) {
	maxDepth := cacheDumpMaxDepth(ctx)
	var out bytes.Buffer
	var write func(any, int) error
	indent := func(level int) { out.WriteString(strings.Repeat("    ", level)) }
	write = func(value any, depth int) error {
		switch x := value.(type) {
		case nil:
			out.WriteString("null")
		case string:
			out.Write(sourceCacheJSONString(x))
		case bool:
			out.WriteString(strconv.FormatBool(x))
		case json.Number:
			text := string(x)
			if strings.ContainsAny(text, ".eE") {
				// json.loads constructs a Python binary64 float, not a token
				// preserving decimal. Overflow is a legal ±Infinity here.
				f, err := strconv.ParseFloat(text, 64)
				if err != nil && !math.IsInf(f, 0) {
					return err
				}
				out.WriteString(sourceCacheFloat(f))
			} else {
				if text == "-0" {
					text = "0"
				}
				out.WriteString(text)
			}
		case float64:
			out.WriteString(sourceCacheFloat(x))
		case int:
			out.WriteString(strconv.Itoa(x))
		case int64:
			out.WriteString(strconv.FormatInt(x, 10))
		case sourceHTTPMap:
			if len(x.keys) == 0 {
				out.WriteString("{}")
				return nil
			}
			if depth > maxDepth {
				return cacheDumpRecursionError()
			}
			out.WriteString("{\n")
			for i, key := range x.keys {
				if i > 0 {
					out.WriteString(",\n")
				}
				indent(depth + 1)
				out.Write(sourceCacheJSONString(key))
				out.WriteString(": ")
				if err := write(x.values[key], depth+1); err != nil {
					return err
				}
			}
			out.WriteByte('\n')
			indent(depth)
			out.WriteByte('}')
		case Object:
			// JSON-decoded maps carry their original order in sourceHTTPMap.
			// Retain the old deterministic order for ordinary Go-only objects;
			// do not invent source insertion metadata for a Go map.
			if x == nil {
				out.WriteString("null")
				return nil
			}
			order := make([]string, 0, len(x))
			for key := range x {
				order = append(order, key)
			}
			sort.Strings(order)
			return write(sourceHTTPMap{x, order}, depth)
		case []any:
			if x == nil {
				out.WriteString("null")
				return nil
			}
			if len(x) == 0 {
				out.WriteString("[]")
				return nil
			}
			if depth > maxDepth {
				return cacheDumpRecursionError()
			}
			out.WriteString("[\n")
			for i, item := range x {
				if i > 0 {
					out.WriteString(",\n")
				}
				indent(depth + 1)
				if err := write(item, depth+1); err != nil {
					return err
				}
			}
			out.WriteByte('\n')
			indent(depth)
			out.WriteByte(']')
		default:
			return sourceCacheJSONTypeError(value)
		}
		return nil
	}
	if err := write(sourceHTTPMap{cache, keys}, 0); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func sourceCacheJSONTypeError(value any) error {
	return &ValueError{"TypeError", fmt.Sprintf("Object of type %s is not JSON serializable", typeName(value))}
}

func sourceCacheFloat(value float64) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	default:
		return pythonFloatText(value)
	}
}

// CPython's ensure_ascii string spelling: JSON short control escapes, lower-
// case \uXXXX for other controls, DEL and every non-ASCII UTF16 code unit.
func sourceCacheJSONString(value string) []byte {
	var out bytes.Buffer
	out.WriteByte('"')
	for _, r := range sourceCodepoints(value) {
		switch r {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			if r < 0x20 || r >= 0x7f {
				if r <= 0xffff {
					fmt.Fprintf(&out, `\u%04x`, r)
				} else {
					x := r - 0x10000
					fmt.Fprintf(&out, `\u%04x\u%04x`, 0xd800+(x>>10), 0xdc00+(x&0x3ff))
				}
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return out.Bytes()
}

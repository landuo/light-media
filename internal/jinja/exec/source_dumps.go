package exec

import (
	"encoding/json"
	"math"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type JSONOptions struct {
	EnsureASCII, SortKeys bool
	Indent                *int
}

func PythonDumps(value any, options JSONOptions) (string, error) { return dumpValue(value, options, 0) }
func sourceQuote(s string, ascii bool) (string, error) {
	if !utf8.ValidString(s) {
		return "", SourceFailuref("native dependency: non-Unicode Go string")
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 32 || ascii && r >= 127 {
				if r > 0xffff {
					a, c := utf16.EncodeRune(r)
					b.WriteString(`\u` + hex4(uint32(a)) + `\u` + hex4(uint32(c)))
				} else {
					b.WriteString(`\u` + hex4(uint32(r)))
				}
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String(), nil
}
func hex4(n uint32) string {
	s := strconv.FormatUint(uint64(n), 16)
	return strings.Repeat("0", 4-len(s)) + s
}
func dumpValue(raw any, o JSONOptions, depth int) (string, error) {
	if v, ok := raw.(*Value); ok {
		if v.IsError() {
			return "", v
		}
		if v.IsUndefined() {
			return "", SourceFailuref("TypeError: Undefined is not JSON serializable")
		}
		return dumpValue(v.Interface(), o, depth)
	}
	switch v := raw.(type) {
	case nil:
		return "null", nil
	case string:
		return sourceQuote(v, o.EnsureASCII)
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case *big.Int:
		return v.String(), nil
	case json.Number:
		if n, ok := new(big.Int).SetString(string(v), 10); ok {
			return n.String(), nil
		}
		f, e := strconv.ParseFloat(string(v), 64)
		if e != nil && !math.IsInf(f, 0) {
			return "", SourceFailuref("TypeError: invalid JSON number")
		}
		return dumpFloat(f), nil
	case float64:
		return dumpFloat(v), nil
	case float32:
		return dumpFloat(float64(v)), nil
	}
	r := reflect.ValueOf(raw)
	switch r.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(r.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(r.Uint(), 10), nil
	}
	join := func(parts []string, open, close string) string {
		if len(parts) == 0 {
			return open + close
		}
		if o.Indent == nil {
			return open + strings.Join(parts, ", ") + close
		}
		n := *o.Indent
		if n < 0 {
			n = 0
		}
		return open + "\n" + strings.Repeat(" ", n*(depth+1)) + strings.Join(parts, ",\n"+strings.Repeat(" ", n*(depth+1))) + "\n" + strings.Repeat(" ", n*depth) + close
	}
	if r.Kind() == reflect.Slice || r.Kind() == reflect.Array {
		parts := make([]string, r.Len())
		for i := range parts {
			s, e := dumpValue(r.Index(i).Interface(), o, depth+1)
			if e != nil {
				return "", e
			}
			parts[i] = s
		}
		return join(parts, "[", "]"), nil
	}
	pairs := []*Pair{}
	switch v := raw.(type) {
	case *Dict:
		pairs = append(pairs, v.Pairs...)
	case Dict:
		pairs = append(pairs, v.Pairs...)
	default:
		if r.Kind() != reflect.Map {
			return "", SourceFailuref("TypeError: object is not JSON serializable")
		}
		if !o.SortKeys && r.Len() > 1 {
			return "", SourceFailuref("native dependency: unordered Go map JSON")
		}
		for _, k := range r.MapKeys() {
			pairs = append(pairs, &Pair{Key: ToValue(k), Value: ToValue(r.MapIndex(k))})
		}
	}
	if o.SortKeys && len(pairs) > 1 {
		kind := ""
		for _, p := range pairs {
			k := "other"
			if p.Key.IsString() {
				k = "str"
			} else if p.Key.IsNumber() || p.Key.IsBool() {
				k = "num"
			}
			if kind == "" {
				kind = k
			} else if kind != k || k == "other" {
				return "", SourceFailuref("TypeError: JSON keys cannot be ordered")
			}
		}
		sort.SliceStable(pairs, func(i, j int) bool {
			if kind == "str" {
				return pairs[i].Key.String() < pairs[j].Key.String()
			}
			return sourceFloat(pairs[i].Key) < sourceFloat(pairs[j].Key)
		})
	}
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		key, err := jsonObjectKey(p.Key)
		if err != nil {
			return "", err
		}
		key, err = sourceQuote(key, o.EnsureASCII)
		if err != nil {
			return "", err
		}
		value, err := dumpValue(p.Value, o, depth+1)
		if err != nil {
			return "", err
		}
		parts[i] = key + ": " + value
	}
	return join(parts, "{", "}"), nil
}
func dumpFloat(v float64) string {
	if math.IsNaN(v) {
		return "NaN"
	}
	if math.IsInf(v, 1) {
		return "Infinity"
	}
	if math.IsInf(v, -1) {
		return "-Infinity"
	}
	return formatFloatString(v)
}
func jsonObjectKey(v *Value) (string, error) {
	if v.IsString() {
		return v.String(), nil
	}
	if v.IsNil() && !v.IsUndefined() {
		return "null", nil
	}
	if v.IsBool() {
		if v.Bool() {
			return "true", nil
		}
		return "false", nil
	}
	if n, ok := v.Interface().(*big.Int); ok {
		return n.String(), nil
	}
	if v.IsInteger() {
		return v.String(), nil
	}
	if v.IsFloat() {
		return dumpFloat(v.Float()), nil
	}
	return "", SourceFailuref("TypeError: invalid JSON object key")
}

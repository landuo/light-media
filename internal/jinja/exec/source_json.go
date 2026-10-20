package exec

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

func jsonQuote(s string) string {
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
			if r < 0x20 || r > 0x7f {
				if r > 0xffff {
					a, c := utf16.EncodeRune(r)
					fmt.Fprintf(&b, `\u%04x\u%04x`, a, c)
				} else {
					fmt.Fprintf(&b, `\u%04x`, r)
				}
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
func PythonJSON(v *Value, indent *int) (string, error) { return pythonJSON(v, indent, 0) }
func pythonJSON(v *Value, indent *int, depth int) (string, error) {
	if v.IsError() {
		return "", v
	}
	if v.IsUndefined() {
		return "", SourceFailuref("TypeError: Undefined is not JSON serializable")
	}
	if v.IsNil() {
		return "null", nil
	}
	if v.IsString() {
		return jsonQuote(v.String()), nil
	}
	if v.IsBool() {
		if v.Bool() {
			return "true", nil
		}
		return "false", nil
	}
	if v.IsInteger() {
		return v.String(), nil
	}
	if v.IsFloat() {
		f := v.Float()
		if math.IsNaN(f) {
			return "NaN", nil
		}
		if math.IsInf(f, 1) {
			return "Infinity", nil
		}
		if math.IsInf(f, -1) {
			return "-Infinity", nil
		}
		return formatFloatString(f), nil
	}
	join := func(items []string, open, close string) string {
		if len(items) == 0 {
			return open + close
		}
		if indent == nil {
			return open + strings.Join(items, ", ") + close
		}
		n := *indent
		if n < 0 {
			n = 0
		}
		return open + "\n" + strings.Repeat(" ", n*(depth+1)) + strings.Join(items, ",\n"+strings.Repeat(" ", n*(depth+1))) + "\n" + strings.Repeat(" ", n*depth) + close
	}
	if v.IsList() {
		items := []string{}
		var err error
		v.Iterate(func(_, _ int, key, _ *Value) bool {
			var s string
			s, err = pythonJSON(key, indent, depth+1)
			items = append(items, s)
			return err == nil
		}, func() {})
		if err != nil {
			return "", err
		}
		return join(items, "[", "]"), nil
	}
	if v.IsDict() {
		items := map[string]*Value{}
		keys := []string{}
		var err error
		v.Iterate(func(_, _ int, key, val *Value) bool {
			if !key.IsString() {
				err = SourceFailuref("TypeError: unsupported JSON key")
				return false
			}
			s := key.String()
			if _, ok := items[s]; !ok {
				keys = append(keys, s)
			}
			items[s] = val
			return true
		}, func() {})
		if err != nil {
			return "", err
		}
		sort.Strings(keys)
		pairs := []string{}
		for _, k := range keys {
			s, e := pythonJSON(items[k], indent, depth+1)
			if e != nil {
				return "", e
			}
			pairs = append(pairs, jsonQuote(k)+": "+s)
		}
		return join(pairs, "{", "}"), nil
	}
	return "", SourceFailuref("TypeError: object %s is not JSON serializable", strconv.Quote(v.String()))
}

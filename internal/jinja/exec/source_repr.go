package exec

import (
	"fmt"
	"strings"
)

func PythonRepr(v *Value) string {
	if v.IsUndefined() {
		return "Undefined"
	}
	if !v.IsString() {
		return v.String()
	}
	s := v.String()
	quote := byte('\'')
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	var out strings.Builder
	out.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == rune(quote) || r == '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case r == '\t':
			out.WriteString(`\t`)
		case r == '\n':
			out.WriteString(`\n`)
		case r == '\r':
			out.WriteString(`\r`)
		case !pythonPrintable(r):
			if r <= 0xff {
				fmt.Fprintf(&out, `\x%02x`, r)
			} else if r <= 0xffff {
				fmt.Fprintf(&out, `\u%04x`, r)
			} else {
				fmt.Fprintf(&out, `\U%08x`, r)
			}
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte(quote)
	return out.String()
}

type Tuple []any

func (t Tuple) String() string {
	items := make([]string, len(t))
	for i, v := range t {
		items[i] = PythonRepr(ToValue(v))
	}
	s := strings.Join(items, ", ")
	if len(t) == 1 {
		s += ","
	}
	return "(" + s + ")"
}

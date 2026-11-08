package app

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Only the new source-interval service diagnostic uses these finite scalar
// renderers. Its callable identity is separately marked as a Go adaptation.
func originalSourceIntervalNameRepr(s string) string {
	quote := rune('\'')
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	var b strings.Builder
	b.WriteRune(quote)
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r == quote {
				b.WriteByte('\\')
				b.WriteRune(r)
			} else if unicode.IsPrint(r) {
				b.WriteRune(r)
			} else if r <= 255 {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else if r <= 65535 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				fmt.Fprintf(&b, `\U%08x`, r)
			}
		}
	}
	b.WriteRune(quote)
	return b.String()
}
func originalSourceIntervalFloatRepr(text string) string {
	v, e := strconv.ParseFloat(text, 64)
	if e != nil {
		return "<missing>"
	}
	switch {
	case math.IsNaN(v):
		return "nan"
	case math.IsInf(v, 1):
		return "inf"
	case math.IsInf(v, -1):
		return "-inf"
	}
	abs := math.Abs(v)
	if abs != 0 && (abs < 1e-4 || abs >= 1e16) {
		return strconv.FormatFloat(v, 'g', -1, 64)
	}
	out := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.ContainsRune(out, '.') {
		out += ".0"
	}
	return out
}

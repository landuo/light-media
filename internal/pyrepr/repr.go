package pyrepr

import (
	"fmt"
	"strconv"
	"strings"
)

// String renders a Python str repr: a single-quoted literal unless the value
// contains a quote but no double quote, matching CPython's quote selection.
func String(s string) string {
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
		case !Printable(r):
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

// Float renders a float64 the way Python's str() does: shortest round-tripping
// digits, positional notation for exponents in [-4, 16), and always a decimal
// point or exponent so the value reads as a float.
func Float(f float64) string {
	formatted := strconv.FormatFloat(f, 'g', -1, 64)
	if formatted == "NaN" || strings.HasSuffix(formatted, "Inf") {
		return formatted
	}
	if strings.ContainsAny(formatted, "eE") {
		mantissa, exponent, ok := splitScientificFloat(formatted)
		if ok && exponent >= -4 && exponent < 16 {
			return ensureFloatSuffix(expandScientificFloat(mantissa, exponent))
		}
		return formatted
	}
	return ensureFloatSuffix(formatted)
}

func splitScientificFloat(formatted string) (string, int, bool) {
	index := strings.IndexAny(formatted, "eE")
	if index < 0 {
		return "", 0, false
	}
	exponent, err := strconv.Atoi(formatted[index+1:])
	if err != nil {
		return "", 0, false
	}
	return formatted[:index], exponent, true
}

func expandScientificFloat(mantissa string, exponent int) string {
	sign := ""
	switch {
	case strings.HasPrefix(mantissa, "-"):
		sign = "-"
		mantissa = mantissa[1:]
	case strings.HasPrefix(mantissa, "+"):
		sign = "+"
		mantissa = mantissa[1:]
	}

	digits := strings.ReplaceAll(mantissa, ".", "")
	decimalPos := 1 + exponent

	var expanded string
	switch {
	case decimalPos <= 0:
		expanded = "0." + strings.Repeat("0", -decimalPos) + digits
	case decimalPos >= len(digits):
		expanded = digits + strings.Repeat("0", decimalPos-len(digits))
	default:
		expanded = digits[:decimalPos] + "." + digits[decimalPos:]
	}

	if strings.Contains(expanded, ".") {
		expanded = strings.TrimRight(expanded, "0")
		expanded = strings.TrimSuffix(expanded, ".")
	}
	return sign + expanded
}

func ensureFloatSuffix(formatted string) string {
	if formatted == "" || formatted == "-0" || formatted == "+0" {
		return formatted + ".0"
	}
	if strings.ContainsAny(formatted, ".eE") {
		return formatted
	}
	return formatted + ".0"
}

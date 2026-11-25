package mpserverstatus

import (
	"fmt"
	"regexp"
	"strings"
)

// Frozen from the installed Python 3.12.13 stdlib re/int/str Unicode 15.0.0
// tables. Do not inherit a future Go unicode.Nd or IsSpace table upgrade.
var pythonDecimalZeroes = []rune{0x30, 0x660, 0x6f0, 0x7c0, 0x966, 0x9e6, 0xa66, 0xae6, 0xb66, 0xbe6, 0xc66, 0xce6, 0xd66, 0xde6, 0xe50, 0xed0, 0xf20, 0x1040, 0x1090, 0x17e0, 0x1810, 0x1946, 0x19d0, 0x1a80, 0x1a90, 0x1b50, 0x1bb0, 0x1c40, 0x1c50, 0xa620, 0xa8d0, 0xa900, 0xa9d0, 0xa9f0, 0xaa50, 0xabf0, 0xff10, 0x104a0, 0x10d30, 0x11066, 0x110f0, 0x11136, 0x111d0, 0x112f0, 0x11450, 0x114d0, 0x11650, 0x116c0, 0x11730, 0x118e0, 0x11950, 0x11c50, 0x11d50, 0x11da0, 0x11f50, 0x16a60, 0x16ac0, 0x16b50, 0x1d7ce, 0x1d7d8, 0x1d7e2, 0x1d7ec, 0x1d7f6, 0x1e140, 0x1e2f0, 0x1e4f0, 0x1e950, 0x1fbf0}
var pythonWhitespace = []rune{0x9, 0xa, 0xb, 0xc, 0xd, 0x1c, 0x1d, 0x1e, 0x1f, 0x20, 0x85, 0xa0, 0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000}

var pySpace = pythonRuneClass(pythonWhitespace, false)
var pyDigit = pythonRuneClass(pythonDecimalZeroes, true)

func pythonRuneClass(values []rune, digits bool) string {
	var b strings.Builder
	b.WriteByte('[')
	for _, r := range values {
		fmt.Fprintf(&b, `\x{%X}`, r)
		if digits {
			fmt.Fprintf(&b, `-\x{%X}`, r+9)
		}
	}
	b.WriteByte(']')
	return b.String()
}
func pythonSpace(r rune) bool {
	for _, s := range pythonWhitespace {
		if s == r {
			return true
		}
	}
	return false
}

// CPython re.IGNORECASE adds dotted/dotless I, long S and Kelvin sign to the
// ASCII letter equivalence classes. RE2 (?i) omits the I variants.
func pythonCaseLiteral(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if r >= 'a' && r <= 'z' {
			b.WriteByte('[')
			b.WriteRune(r)
			b.WriteRune(r - ('a' - 'A'))
			switch r {
			case 'i':
				b.WriteString("İı")
			case 's':
				b.WriteRune('ſ')
			case 'k':
				b.WriteRune('K')
			}
			b.WriteByte(']')
		} else {
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	return b.String()
}

package downloadercommands

import "strings"

// These 68 decimal-zero entries are byte/value reuse of the already sealed
// Python Unicode15.0 table in serverstatus/unicode_table.go (680 Nd digits).
// That historical table was captured on Python3.12.13; the new numeric14
// method oracle ran on pinned Python3.12.14. See the draft provenance/erratum.
// Do not inherit a newer Go unicode.Nd table or map nondecimal isdigit values.
var trNumericDecimalZeroes = []rune{0x30, 0x660, 0x6f0, 0x7c0, 0x966, 0x9e6, 0xa66, 0xae6, 0xb66, 0xbe6, 0xc66, 0xce6, 0xd66, 0xde6, 0xe50, 0xed0, 0xf20, 0x1040, 0x1090, 0x17e0, 0x1810, 0x1946, 0x19d0, 0x1a80, 0x1a90, 0x1b50, 0x1bb0, 0x1c40, 0x1c50, 0xa620, 0xa8d0, 0xa900, 0xa9d0, 0xa9f0, 0xaa50, 0xabf0, 0xff10, 0x104a0, 0x10d30, 0x11066, 0x110f0, 0x11136, 0x111d0, 0x112f0, 0x11450, 0x114d0, 0x11650, 0x116c0, 0x11730, 0x118e0, 0x11950, 0x11c50, 0x11d50, 0x11da0, 0x11f50, 0x16a60, 0x16ac0, 0x16b50, 0x1d7ce, 0x1d7d8, 0x1d7e2, 0x1d7ec, 0x1d7f6, 0x1e140, 0x1e2f0, 0x1e4f0, 0x1e950, 0x1fbf0}

// Numeric conversion trims Python numeric whitespace, not all str/re spaces.
// In particular U+001C..U+001F must remain invalid instead of being stripped.
var trNumericWhitespace = []rune{0x9, 0xa, 0xb, 0xc, 0xd, 0x20, 0x85, 0xa0, 0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000}

func trNumericSpace(r rune) bool {
	for _, value := range trNumericWhitespace {
		if r == value {
			return true
		}
	}
	return false
}

func trNumericDecimal(r rune) (byte, bool) {
	for _, zero := range trNumericDecimalZeroes {
		if r >= zero && r <= zero+9 {
			return byte('0' + r - zero), true
		}
	}
	return 0, false
}

func trNumericASCIIDigit(value byte) bool { return value >= '0' && value <= '9' }

// trFinalNumericString normalizes only spelling. The callers keep the original
// value's truthiness and error repr, enforce their original int64/finite domain,
// and perform decimal parsing/rounding outside the dependency-call catch.
// Unknown characters are retained for the parser to reject; they are not
// silently omitted. Underscores are removed only after digit-neighbor checks.
func trFinalNumericString(value string) (string, bool) {
	value = strings.TrimFunc(value, trNumericSpace)
	var out strings.Builder
	out.Grow(len(value))
	for _, r := range value {
		if digit, ok := trNumericDecimal(r); ok {
			out.WriteByte(digit)
		} else {
			out.WriteRune(r)
		}
	}
	normalized := out.String()
	for i := 0; i < len(normalized); i++ {
		if normalized[i] == '_' && (i == 0 || i+1 == len(normalized) || !trNumericASCIIDigit(normalized[i-1]) || !trNumericASCIIDigit(normalized[i+1])) {
			return "", false
		}
	}
	return strings.ReplaceAll(normalized, "_", ""), true
}

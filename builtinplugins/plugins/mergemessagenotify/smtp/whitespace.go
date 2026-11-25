package mergemessagesmtp

import "strings"

// pythonSpace matches the pinned CPython whitespace set, including the four
// C0 separators accepted by str.strip.
func pythonSpace(r rune) bool {
	return r >= '\t' && r <= '\r' ||
		r >= 0x1c && r <= ' ' ||
		r == 0x85 || r == 0xa0 || r == 0x1680 ||
		r >= 0x2000 && r <= 0x200a ||
		r == 0x2028 || r == 0x2029 || r == 0x202f ||
		r == 0x205f || r == 0x3000
}

func pythonStrip(s string) string {
	return strings.TrimFunc(s, pythonSpace)
}

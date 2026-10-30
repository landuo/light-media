package app

import (
	"sort"
	"strings"
)

// These finite tables are fixed to Python 3.12's Unicode 15.0.0, the source
// witness runtime. They do not follow later Go Unicode table changes.
var originalPluginVersionDecimalStarts = [...]rune{
	0x30, 0x660, 0x6f0, 0x7c0, 0x966, 0x9e6, 0xa66, 0xae6,
	0xb66, 0xbe6, 0xc66, 0xce6, 0xd66, 0xde6, 0xe50, 0xed0,
	0xf20, 0x1040, 0x1090, 0x17e0, 0x1810, 0x1946, 0x19d0, 0x1a80,
	0x1a90, 0x1b50, 0x1bb0, 0x1c40, 0x1c50, 0xa620, 0xa8d0, 0xa900,
	0xa9d0, 0xa9f0, 0xaa50, 0xabf0, 0xff10, 0x104a0, 0x10d30, 0x11066,
	0x110f0, 0x11136, 0x111d0, 0x112f0, 0x11450, 0x114d0, 0x11650, 0x116c0,
	0x11730, 0x118e0, 0x11950, 0x11c50, 0x11d50, 0x11da0, 0x11f50, 0x16a60,
	0x16ac0, 0x16b50, 0x1d7ce, 0x1d7d8, 0x1d7e2, 0x1d7ec, 0x1d7f6, 0x1e140,
	0x1e2f0, 0x1e4f0, 0x1e950, 0x1fbf0,
}

// Python isdigit includes these Numeric_Type=Digit characters in addition to
// Nd. Python int cannot convert them, so an all-digit component containing one
// invalidates the comparison instead of receiving the ordinary unknown weight.
var originalPluginVersionNonDecimalDigits = [...][2]rune{
	{0xb2, 0xb3},
	{0xb9, 0xb9},
	{0x1369, 0x1371},
	{0x19da, 0x19da},
	{0x2070, 0x2070},
	{0x2074, 0x2079},
	{0x2080, 0x2089},
	{0x2460, 0x2468},
	{0x2474, 0x247c},
	{0x2488, 0x2490},
	{0x24ea, 0x24ea},
	{0x24f5, 0x24fd},
	{0x24ff, 0x24ff},
	{0x2776, 0x277e},
	{0x2780, 0x2788},
	{0x278a, 0x2792},
	{0x10a40, 0x10a43},
	{0x10e60, 0x10e68},
	{0x11052, 0x1105a},
	{0x1f100, 0x1f10a},
}

func originalPluginVersionDecimalValue(r rune) (byte, bool) {
	n := sort.Search(len(originalPluginVersionDecimalStarts), func(i int) bool {
		return originalPluginVersionDecimalStarts[i] > r
	}) - 1
	if n >= 0 && r < originalPluginVersionDecimalStarts[n]+10 {
		return byte(r - originalPluginVersionDecimalStarts[n]), true
	}
	return 0, false
}

// The third result distinguishes int conversion failure from a component that
// is not isdigit at all. Classify the whole component before deciding failure:
// "²" is invalid, whereas "²x" is the ordinary unknown component.
func originalPluginVersionDecimal(part string) (ascii string, digit, valid bool) {
	if part == "" {
		return "", false, true
	}
	var out strings.Builder
	nonDecimal := false
	for _, r := range part {
		if value, ok := originalPluginVersionDecimalValue(r); ok {
			out.WriteByte('0' + value)
			continue
		}
		extra := false
		for _, span := range originalPluginVersionNonDecimalDigits {
			if r >= span[0] && r <= span[1] {
				extra = true
				break
			}
		}
		if !extra {
			return "", false, true
		}
		nonDecimal = true
	}
	// ASCII output length is the number of Nd codepoints, not UTF-8 bytes.
	// Leading zeroes count toward Python's default 4300-digit conversion limit.
	if nonDecimal || out.Len() > 4300 {
		return "", true, false
	}
	return out.String(), true, true
}

func originalPluginVersionSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', 0x1c, 0x1d, 0x1e, 0x1f, ' ', 0x85, 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

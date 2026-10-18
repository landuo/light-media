package urlprep

import (
	"sort"
	"strings"
)

const punyDigits = "abcdefghijklmnopqrstuvwxyz0123456789"

func punyT(j, bias int64) int64 {
	t := 36*(j+1) - bias
	if t < 1 {
		return 1
	}
	if t > 26 {
		return 26
	}
	return t
}
func punyAdapt(delta int64, first bool, n int64) int64 {
	if first {
		delta /= 700
	} else {
		delta /= 2
	}
	delta += delta / n
	div := int64(0)
	for delta > 455 {
		delta /= 35
		div += 36
	}
	return div + 36*delta/(delta+38)
}
func punyEncode(text []rune) string {
	base := []byte{}
	set := map[rune]bool{}
	for _, c := range text {
		if c < 128 {
			base = append(base, byte(c))
		} else {
			set[c] = true
		}
	}
	extended := make([]rune, 0, len(set))
	for c := range set {
		extended = append(extended, c)
	}
	sort.Slice(extended, func(i, j int) bool { return extended[i] < extended[j] })
	deltas := []int64{}
	oldchar := rune(128)
	oldindex := int64(-1)
	for _, c := range extended {
		curlen := int64(0)
		for _, r := range text {
			if r < c {
				curlen++
			}
		}
		delta := (curlen + 1) * int64(c-oldchar)
		index := int64(-1)
		for _, r := range text {
			if r < c {
				index++
			} else if r == c {
				index++
				delta += index - oldindex
				deltas = append(deltas, delta-1)
				oldindex = index
				delta = 0
			}
		}
		oldchar = c
	}
	var out strings.Builder
	out.Write(base)
	if len(base) > 0 {
		out.WriteByte('-')
	}
	bias := int64(72)
	for points, delta := range deltas {
		n := delta
		for j := int64(0); ; j++ {
			t := punyT(j, bias)
			if n < t {
				out.WriteByte(punyDigits[n])
				break
			}
			out.WriteByte(punyDigits[t+(n-t)%(36-t)])
			n = (n - t) / (36 - t)
		}
		bias = punyAdapt(delta, points == 0, int64(len(base)+points+1))
	}
	return out.String()
}
func punyDecode(s string) ([]rune, error) {
	pos := strings.LastIndexByte(s, '-')
	base := []rune{}
	extended := s
	if pos >= 0 {
		for i := 0; i < pos; i++ {
			if s[i] >= 128 {
				return nil, fail("UnicodeError", "Invalid ASCII")
			}
			base = append(base, rune(s[i]))
		}
		extended = s[pos+1:]
	}
	char := int64(128)
	index := int64(-1)
	bias := int64(72)
	for extpos := 0; extpos < len(extended); {
		start := extpos
		delta, w := int64(0), int64(1)
		for j := int64(0); ; j++ {
			if extpos >= len(extended) {
				return nil, fail("UnicodeError", "incomplete punycode string")
			}
			c := extended[extpos]
			extpos++
			digit := int64(-1)
			if c >= 'a' && c <= 'z' {
				digit = int64(c - 'a')
			} else if c >= 'A' && c <= 'Z' {
				digit = int64(c - 'A')
			} else if c >= '0' && c <= '9' {
				digit = int64(c - '0' + 26)
			}
			if digit < 0 {
				return nil, fail("UnicodeError", "Invalid extended codepoint")
			}
			if w > 1<<50 || delta > 1<<50 {
				return nil, fail("UnicodeError", "Invalid codepoint")
			}
			delta += digit * w
			t := punyT(j, bias)
			if digit < t {
				break
			}
			w *= 36 - t
		}
		index += delta + 1
		char += index / int64(len(base)+1)
		if char > 0x10FFFF {
			return nil, fail("UnicodeError", "Invalid codepoint")
		}
		index %= int64(len(base) + 1)
		base = append(base, 0)
		copy(base[int(index)+1:], base[int(index):])
		base[index] = rune(char)
		bias = punyAdapt(delta, start == 0, int64(len(base)))
	}
	return base, nil
}

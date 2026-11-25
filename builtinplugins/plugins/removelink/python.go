package removelink

import (
	"errors"
	"math/big"
	"sort"
	"strings"
)

type sourceRange struct{ lo, hi rune }

func inRange(r rune, table []sourceRange) bool {
	i := sort.Search(len(table), func(i int) bool { return table[i].hi >= r })
	return i < len(table) && table[i].lo <= r
}
func sourceSpace(r rune) bool {
	return r >= 9 && r <= 13 || r >= 0x1c && r <= 0x20 || r == 0x85 || r == 0xa0 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000
}
func sourceStrip(s string) string { return strings.TrimFunc(s, sourceSpace) }
func sourceInteger(value string) (*big.Int, error) {
	value = strings.TrimFunc(value, func(r rune) bool { return sourceSpace(r) && !(r >= 0x1c && r <= 0x1f) })
	var normalized strings.Builder
	digits := 0
	for _, r := range value {
		if v, ok := pyDecimal[r]; ok {
			normalized.WriteByte(byte('0' + v))
			digits++
		} else {
			normalized.WriteRune(r)
		}
	}
	text := normalized.String()
	if text == "" || digits > 4300 {
		return nil, errors.New("invalid integer")
	}
	for i, r := range text {
		if r == '_' && (i == 0 || i == len(text)-1 || text[i-1] < '0' || text[i-1] > '9' || text[i+1] < '0' || text[i+1] > '9') {
			return nil, errors.New("invalid integer")
		}
	}
	n, ok := new(big.Int).SetString(strings.ReplaceAll(text, "_", ""), 10)
	if !ok {
		return nil, errors.New("invalid integer")
	}
	return n, nil
}
func sourceLower(value string) string {
	runes := []rune(value)
	var out strings.Builder
	for i, r := range runes {
		if r == 'Σ' {
			before, after := false, false
			for j := i - 1; j >= 0; j-- {
				if inRange(runes[j], pyCaseIgnorableRanges) {
					continue
				}
				before = inRange(runes[j], pyCasedRanges)
				break
			}
			for j := i + 1; j < len(runes); j++ {
				if inRange(runes[j], pyCaseIgnorableRanges) {
					continue
				}
				after = inRange(runes[j], pyCasedRanges)
				break
			}
			if before && !after {
				out.WriteRune('ς')
			} else {
				out.WriteRune('σ')
			}
		} else if v, ok := pyLowerMappings[r]; ok {
			out.WriteString(v)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

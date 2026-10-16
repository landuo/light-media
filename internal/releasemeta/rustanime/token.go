package rustanime

// Translation of anitomy-pure 0.1.0 src/split.rs and src/token/*.rs,
// licensed under MPL-2.0; no Go title heuristics are inserted here.

import (
	"strings"
	"unicode/utf8"
)

func splitTypeAndEpisode(value string) (string, string) {
	for at, r := range value {
		if r >= '0' && r <= '9' {
			return value[:at], value[at:]
		}
	}
	return value, ""
}
func newToken(raw, d string, inside, paren, corner bool) *token {
	t := &token{raw: raw, inside: inside, paren: paren, corner: corner}
	for _, v := range splitToken(raw, d) {
		t.subtokens = append(t.subtokens, &subtoken{value: v})
	}
	t.weak = paren && len(t.subtokens) == 1
	return t
}
func splitRawData(raw, d string) []*token {
	var result []*token
	start, end := 0, 0
	appendToken := func(inside, paren, corner bool) {
		if end > start {
			result = append(result, newToken(raw[start:end], d, inside, paren, corner))
		}
	}
	for at, r := range raw {
		if strings.ContainsRune("[({「『【（", r) {
			appendToken(false, false, false)
			start = at + utf8.RuneLen(r)
			end = start
		} else if strings.ContainsRune("])}」』】）", r) {
			appendToken(true, r == ')' || r == '）', r == '」' || r == '』')
			start = at + utf8.RuneLen(r)
			end = start
		} else {
			end = at + utf8.RuneLen(r)
		}
	}
	appendToken(false, false, false)
	return result
}
func splitToken(raw, d string) []string {
	trimmed := strings.Trim(raw, d)
	type character struct {
		at int
		r  rune
	}
	var chars []character
	for at, r := range trimmed {
		chars = append(chars, character{at, r})
	}
	var result []string
	start := 0
	for i := 0; i < len(chars); {
		cur := chars[i]
		if strings.ContainsRune(d, cur.r) {
			if cur.at > start {
				result = append(result, trimmed[start:cur.at])
			}
			if i+2 < len(chars) && strings.ContainsRune(d, chars[i+1].r) && strings.ContainsRune(d, chars[i+2].r) {
				m := chars[i+1]
				result = append(result, trimmed[m.at:m.at+utf8.RuneLen(m.r)])
				i += 2
			} else {
				i++
			}
			start = len(trimmed)
			if i < len(chars) {
				start = chars[i].at
			}
		} else {
			i++
		}
	}
	if start < len(trimmed) {
		result = append(result, trimmed[start:])
	}
	return result
}

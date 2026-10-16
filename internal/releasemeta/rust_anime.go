package releasemeta

// Token splitting and numeric resolution recognition are translated from
// anitomy-pure 0.1.0 (MPL-2.0), src/split.rs and src/parsing/number.rs.
// The accompanying license is retained in anitomy-pure.LICENSE.md.

import (
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type rustAnimeToken struct {
	raw                   string
	subtokens             []string
	inside, paren, corner bool
}

const rustAnimeDelimiters = " _.-&+,|"

var rustAnimeExtensionSet = func() map[string]bool {
	result := map[string]bool{}
	for _, extension := range strings.Split(rustAnimeExtensions, "|") {
		if extension != "" {
			result[extension] = true
		}
	}
	return result
}()

func rustAnimeRemoveExtension(title string) string {
	base := filepath.Base(title)
	extension := filepath.Ext(title)
	if extension == base || !rustAnimeExtensionSet[extension] {
		return title
	}
	return strings.TrimSuffix(title, extension)
}

func rustAnimeSplitRaw(title string) []rustAnimeToken {
	var tokens []rustAnimeToken
	start, end := 0, 0
	appendToken := func(inside, paren, corner bool) {
		if end > start {
			raw := title[start:end]
			tokens = append(tokens, rustAnimeToken{raw: raw, subtokens: rustAnimeSplitToken(raw), inside: inside, paren: paren, corner: corner})
		}
	}
	for at, r := range title {
		switch {
		case strings.ContainsRune("[({「『【（", r):
			appendToken(false, false, false)
			start = at + utf8.RuneLen(r)
			end = start
		case strings.ContainsRune("])}」』】）", r):
			appendToken(true, r == ')' || r == '）', r == '」' || r == '』')
			start = at + utf8.RuneLen(r)
			end = start
		default:
			end = at + utf8.RuneLen(r)
		}
	}
	appendToken(false, false, false)
	return tokens
}

func rustAnimeSplitToken(raw string) []string {
	trimmed := strings.Trim(raw, rustAnimeDelimiters)
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
		current := chars[i]
		if strings.ContainsRune(rustAnimeDelimiters, current.r) {
			if current.at > start {
				result = append(result, trimmed[start:current.at])
			}
			if i+2 < len(chars) && strings.ContainsRune(rustAnimeDelimiters, chars[i+1].r) && strings.ContainsRune(rustAnimeDelimiters, chars[i+2].r) {
				middle := chars[i+1]
				result = append(result, trimmed[middle.at:middle.at+utf8.RuneLen(middle.r)])
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

func rustAnimeIsResolution(value string) bool {
	n := len(value)
	if n == 2 && strings.ContainsRune("248", rune(value[0])) && (value[1] == 'K' || value[1] == 'k') {
		return true
	}
	if n > 0 && strings.ContainsRune("pPiI", rune(value[n-1])) {
		return (n == 4 || n == 5) && digits(value[:n-1])
	}
	if at := strings.IndexAny(value, "xX×"); at >= 0 {
		_, width := utf8.DecodeRuneInString(value[at:])
		left, right := value[:at], value[at+width:]
		return (len(left) == 3 || len(left) == 4) && (len(right) == 3 || len(right) == 4) && digits(left) && digits(right)
	}
	return false
}

func rustAnimeResolution(title string) string {
	title = rustAnimeRemoveExtension(title)
	if strings.Contains(title, "[") && !strings.Contains(title, "]") {
		return ""
	}
	for _, token := range rustAnimeSplitRaw(title) {
		// No preceding keyword consumes a complete resolution token: season,
		// episode and volume prefixes only consume numeric followers, while the
		// keyword dictionary has no VideoResolution entries. Consequently the
		// source's first VideoResolution is this first complete subtoken match.
		for _, value := range token.subtokens {
			if rustAnimeIsResolution(value) {
				return value
			}
		}
		// parsing_keywords performs this isolated-number pass after each raw
		// token, so [720] before a later [1080p] wins in the original as well.
		if len(token.subtokens) == 1 {
			value := token.subtokens[0]
			if value == "480" || value == "720" || value == "1080" {
				return value
			}
		}
	}
	return ""
}

package releasemeta

import (
	"github.com/dlclark/regexp2/v2"
	"strings"
)

// fullRustPattern expands pinned Rust classes in immutable source expressions.
// It is separate from user-config expressions and from Python's own tables.
func fullRustPattern(pattern string) string {
	return expandSourceClasses(pattern, rustRegexDigits, rustRegexSpace, fullRustWord)
}
func expandSourceClasses(pattern, digitClass, spaceClass, wordClass string) string {
	pattern = strings.ReplaceAll(pattern, `[\W\w]`, `(?s:.)`)
	var out strings.Builder
	inClass := false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == '\\' && i+1 < len(pattern) {
			i++
			e := pattern[i]
			value := ""
			switch e {
			case 'd', 'D':
				value = digitClass
			case 's', 'S':
				value = spaceClass
			case 'w', 'W':
				value = wordClass
			}
			if value != "" {
				if inClass {
					out.WriteString(value[1 : len(value)-1])
				} else if e == 'D' || e == 'S' || e == 'W' {
					out.WriteString("[^" + value[1:])
				} else {
					out.WriteString(value)
				}
			} else {
				out.WriteByte('\\')
				out.WriteByte(e)
			}
			continue
		}
		if c == '[' {
			inClass = true
		} else if c == ']' {
			inClass = false
		}
		out.WriteByte(c)
	}
	return out.String()
}
func fullPattern(p, mode string) string {
	switch mode {
	case "legacy":
		return expandSourceClasses(p, legacyAnimeDecimalClass, legacyAnimeWhitespaceClass, fullLegacyWord)
	case "legacy_metabase":
		return expandSourceClasses(p, fullLegacyMetaDigits, fullLegacyMetaSpace, fullLegacyWord)
	default:
		return fullRustPattern(p)
	}
}
func fullRXFirst(p, v string, ignore bool, mode string) *regexp2.Match {
	p = fullPattern(p, mode)
	if ignore {
		p = sourceFoldASCII(p, mode)
	}
	return rxFirst(p, v, false)
}
func fullRXHas(p, v string, ignore bool, mode string) bool {
	p = fullPattern(p, mode)
	if ignore {
		p = sourceFoldASCII(p, mode)
	}
	return rxHas(p, v, false)
}
func fullRXMatches(p, v string, ignore bool, mode string) []*regexp2.Match {
	p = fullPattern(p, mode)
	if ignore {
		p = sourceFoldASCII(p, mode)
	}
	return rxMatches(p, v, false)
}
func fullRXReplace(p, v, r string, ignore bool, mode string) string {
	p = fullPattern(p, mode)
	if ignore {
		p = sourceFoldASCII(p, mode)
	}
	return rxReplace(p, v, r, false)
}

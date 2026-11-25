package releasemeta

import (
	"strings"
	"sync"

	"github.com/dlclark/regexp2/v2"
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

// Only fixed source expressions belong here, never titles or user patterns.
// Bound retained entries even if the source pattern inventory grows. Overflow
// takes the original preparation path; warmed entries keep serving readers.
const maxPreparedSourcePatterns = 256

type sourcePatternKey struct {
	pattern, mode string
	ignore        bool
}

var preparedPatterns = struct {
	sync.RWMutex
	values map[sourcePatternKey]string
}{values: make(map[sourcePatternKey]string)}

func preparedSourcePattern(p, mode string, ignore bool) string {
	key := sourcePatternKey{p, mode, ignore}
	preparedPatterns.RLock()
	value, ok := preparedPatterns.values[key]
	preparedPatterns.RUnlock()
	if ok {
		return value
	}
	value = fullPattern(p, mode)
	if ignore {
		value = sourceFoldASCII(value, mode)
	}
	preparedPatterns.Lock()
	if len(preparedPatterns.values) < maxPreparedSourcePatterns {
		preparedPatterns.values[key] = value
	}
	preparedPatterns.Unlock()
	return value
}

func fullRXFirst(p, v string, ignore bool, mode string) *regexp2.Match {
	return rxFirst(preparedSourcePattern(p, mode, ignore), v, false)
}
func fullRXHas(p, v string, ignore bool, mode string) bool {
	return rxHas(preparedSourcePattern(p, mode, ignore), v, false)
}
func fullRXMatches(p, v string, ignore bool, mode string) []*regexp2.Match {
	return rxMatches(preparedSourcePattern(p, mode, ignore), v, false)
}
func fullRXReplace(p, v, r string, ignore bool, mode string) string {
	return rxReplace(preparedSourcePattern(p, mode, ignore), v, r, false)
}

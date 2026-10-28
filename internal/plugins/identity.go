package plugins

import (
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

// ValidInstanceID accepts a single Linux directory component. The old ASCII
// 96/128 character limits were host validation, not MoviePilot clone limits.
// Ordinary Unicode identifiers and import-failing dot/dash/space copies remain
// addressable; separators, controls and the filesystem's 255-byte limit do not.
func ValidInstanceID(value string) bool { return validPluginPhysicalComponent(value, false) }

func validPluginPhysicalComponent(value string, underscoreFirst bool) bool {
	if value == "" || len(value) > 255 || !utf8.ValidString(value) {
		return false
	}
	for index, r := range value {
		if index == 0 && !unicode.IsLetter(r) && !unicode.IsNumber(r) && !(underscoreFirst && r == '_') {
			return false
		}
		if r == '/' || r == '\\' || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return false
		}
	}
	return true
}

// OriginalPluginLower mirrors Python str.lower's full/contextual Unicode case
// mapping. Go strings.ToLower omits expansions such as capital dotted I.
func OriginalPluginLower(value string) string { return cases.Lower(language.Und).String(value) }

// OriginalPluginClassLoadable checks the class-name lookup that follows the
// fixed Python clone source edit. Python normalizes identifiers to NFKC during
// parsing; a normalized different __name__ fails the original exact-ID lookup.
// This gate is used only for a host-authenticated copied clone, never a primary
// Go package. The existing resolver failure path retains copied code/config.
func OriginalPluginClassLoadable(value string) bool {
	if value == "" || norm.NFKC.String(value) != value {
		return false
	}
	for index, r := range value {
		start := r == '_' || unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || unicode.Is(unicode.Other_ID_Start, r)
		if index == 0 {
			if !start {
				return false
			}
			continue
		}
		if !start && !unicode.Is(unicode.Mn, r) && !unicode.Is(unicode.Mc, r) && !unicode.Is(unicode.Nd, r) && !unicode.Is(unicode.Pc, r) && !unicode.Is(unicode.Other_ID_Continue, r) {
			return false
		}
	}
	return true
}

package rustanime

import "strings"

// TitleCase preserves MoviePilot-Rust 0.2.6 parser.rs::to_title_case's per-char
// casing and alphanumeric word boundaries, using the source Unicode tables.
func TitleCase(value string) string {
	var result strings.Builder
	newWord := true
	for _, r := range value {
		if sourceAlphabetic(r) || sourceNumeric(r) {
			mapped := sourceLowerMappings
			if newWord {
				mapped = sourceUpperMappings
			}
			if v, ok := mapped[r]; ok {
				result.WriteString(v)
			} else {
				result.WriteRune(r)
			}
			newWord = false
		} else {
			result.WriteRune(r)
			newWord = true
		}
	}
	return result.String()
}

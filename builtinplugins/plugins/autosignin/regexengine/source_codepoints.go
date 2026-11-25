package regexp2

import (
	"strings"
	"unicode/utf8"

	"light-media/builtinplugins/plugins/autosignin/regexengine/syntax"
)

// SourceCodepoints decodes the plugin-private Python string representation.
// Lone surrogate codepoints use WTF-8 only inside this private package; ordinary
// wire JSON must still use legal escapes. Invalid non-WTF-8 keeps Go's behavior.
func SourceCodepoints(s string) []rune {
	out := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		if i+2 < len(s) && s[i] == 0xed && s[i+1] >= 0xa0 && s[i+1] <= 0xbf && s[i+2] >= 0x80 && s[i+2] <= 0xbf {
			out = append(out, rune(s[i]&15)<<12|rune(s[i+1]&63)<<6|rune(s[i+2]&63))
			i += 3
		} else {
			r, n := utf8.DecodeRuneInString(s[i:])
			out = append(out, r)
			i += n
		}
	}
	return out
}
func SourceString(cp []rune) string {
	var out strings.Builder
	for _, r := range cp {
		if r >= 0xd800 && r <= 0xdfff {
			out.WriteByte(byte(0xe0 | r>>12))
			out.WriteByte(byte(0x80 | (r>>6)&63))
			out.WriteByte(byte(0x80 | r&63))
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}
func SourceHasSurrogates(s string) bool {
	for i := 0; i+2 < len(s); i++ {
		if s[i] == 0xed && s[i+1] >= 0xa0 && s[i+1] <= 0xbf && s[i+2] >= 0x80 && s[i+2] <= 0xbf {
			return true
		}
	}
	return false
}
func (re *Regexp) sourcePython() bool { return re.options&RegexOptions(syntax.SourcePython) != 0 }
func (re *Regexp) sourceMatchText(r []rune) *matchText {
	text := newMatchText(r)
	text.sourceCodepoints = re.sourcePython()
	return text
}

package words

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ErrUnicodeOutput is the title-only UTF-8 response boundary, not a rule parse
// error. Python can retain this result internally but cannot encode it to UTF-8.
var ErrUnicodeOutput = errors.New("recognition title cannot be encoded as Unicode")

// NewCodepointRules is for the source JSON bridge: Python strings may contain
// unpaired surrogates. The private WTF-8 storage never leaves this matcher via
// PrepareChecked. Ordinary Unicode rules use the original execution path.
func NewCodepointRules(rules [][]rune) (*Matcher, error) {
	encoded := make([]string, len(rules))
	special := false
	for i, rule := range rules {
		encoded[i] = encodeSourceRunes(rule)
		for _, r := range rule {
			special = special || sourceSurrogate(r)
		}
	}
	matcher, e := New(encoded)
	if e != nil {
		return nil, e
	}
	matcher.sourceUnicode = special
	return matcher, nil
}
func sourceSurrogate(r rune) bool { return r >= 0xd800 && r <= 0xdfff }
func encodeSourceRunes(value []rune) string {
	out := make([]byte, 0, len(value))
	for _, r := range value {
		if sourceSurrogate(r) {
			out = append(out, byte(0xe0|(r>>12)), byte(0x80|((r>>6)&0x3f)), byte(0x80|(r&0x3f)))
		} else {
			out = utf8.AppendRune(out, r)
		}
	}
	return string(out)
}
func decodeSourceRunes(value string) []rune {
	out := []rune{}
	for i := 0; i < len(value); {
		if i+2 < len(value) && value[i] == 0xed && value[i+1] >= 0xa0 && value[i+1] <= 0xbf && value[i+2] >= 0x80 && value[i+2] <= 0xbf {
			r := rune(value[i]&0xf)<<12 | rune(value[i+1]&0x3f)<<6 | rune(value[i+2]&0x3f)
			out = append(out, r)
			i += 3
			continue
		}
		r, n := utf8.DecodeRuneInString(value[i:])
		out = append(out, r)
		i += n
	}
	return out
}
func sourcePattern(pattern string) string {
	runes := decodeSourceRunes(pattern)
	var out strings.Builder
	write := func(r rune) {
		if sourceSurrogate(r) {
			out.WriteString(`\u` + strconv.FormatInt(int64(r), 16))
		} else {
			out.WriteRune(r)
		}
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '\\' && i+1 < len(runes) {
			next := runes[i+1]
			if !sourceSurrogate(next) {
				out.WriteRune(r)
			}
			write(next)
			i++
			continue
		}
		write(r)
	}
	return out.String()
}
func (m *Matcher) replaceCodepoints(title, pattern, value string) (string, bool, error) {
	re, e := m.compile(pattern)
	if e != nil {
		return title, false, e
	}
	parts, e := replacement(re, value)
	if e != nil {
		return title, false, e
	}
	input := decodeSourceRunes(title)
	match, e := re.FindRunesMatch(input)
	if e != nil {
		return title, false, errRecognitionLimit
	}
	if match == nil {
		return title, false, nil
	}
	result := []rune{}
	position, count := 0, 0
	for match != nil {
		result = append(result, input[position:match.RuneIndex]...)
		for _, part := range parts {
			if part.reference {
				if group := match.GroupByNumber(part.group); group != nil {
					result = append(result, group.Runes()...)
				}
			} else {
				result = append(result, decodeSourceRunes(part.literal)...)
			}
		}
		position = match.RuneIndex + match.RuneLength
		count++
		if len(result) > 64<<10 {
			return title, false, errRecognitionLimit
		}
		match, e = re.FindNextMatch(match)
		if e != nil {
			return title, false, errRecognitionLimit
		}
	}
	result = append(result, input[position:]...)
	encoded := encodeSourceRunes(result)
	if len(encoded) > 64<<10 {
		return title, false, errRecognitionLimit
	}
	return encoded, count > 0, nil
}

// PrepareChecked preserves intermediate code points, and refuses only a final
// title that cannot cross an ordinary UTF-8 string/JSON response boundary.
func (m *Matcher) PrepareChecked(title string) (Result, error) {
	result := m.Prepare(title)
	if m != nil && m.sourceUnicode && !utf8.ValidString(result.Title) {
		result.Title = ""
		return result, ErrUnicodeOutput
	}
	return result, nil
}

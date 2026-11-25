package pyregex

import (
	"encoding/json"
	"fmt"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"

	"light-media/builtinplugins/plugins/autosignin/regexengine"
)

func sourceRunes(s string) []rune { return regexp2.SourceCodepoints(s) }
func sourceText(cp []rune) string { return regexp2.SourceString(cp) }

// CompileCodepoints accepts Python str codepoints, including unpaired UTF16
// surrogates, without passing through Go's replacement-character conversion.
func CompileCodepoints(pattern []rune, ignoreCase bool) (*regexp2.Regexp, error) {
	for _, r := range pattern {
		if r < 0 || r > utf8.MaxRune {
			return nil, fmt.Errorf("invalid Python codepoint: %d", r)
		}
	}
	return Compile(sourceText(pattern), ignoreCase)
}

// DecodeSourceJSONString is a private string-value bridge. JSON validity and
// UTF8 stay strict; only escaped surrogates retain Python json.loads semantics.
// A valid adjacent escaped pair becomes one astral codepoint. Already-created
// Python objects with two surrogate codepoints use CompileCodepoints instead.
func DecodeSourceJSONString(raw []byte) (string, error) {
	if !utf8.Valid(raw) || !json.Valid(raw) || len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return "", fmt.Errorf("expected a strict JSON string")
	}
	cp := make([]rune, 0, len(raw))
	for i := 1; i < len(raw)-1; {
		if raw[i] != '\\' {
			r, n := utf8.DecodeRune(raw[i:])
			cp = append(cp, r)
			i += n
			continue
		}
		i++
		c := raw[i]
		i++
		switch c {
		case '"', '\\', '/':
			cp = append(cp, rune(c))
		case 'b':
			cp = append(cp, '\b')
		case 'f':
			cp = append(cp, '\f')
		case 'n':
			cp = append(cp, '\n')
		case 'r':
			cp = append(cp, '\r')
		case 't':
			cp = append(cp, '\t')
		case 'u':
			n, _ := strconv.ParseUint(string(raw[i:i+4]), 16, 16)
			i += 4
			r := rune(n)
			if r >= 0xd800 && r <= 0xdbff && i+6 <= len(raw)-1 && raw[i] == '\\' && raw[i+1] == 'u' {
				low, _ := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
				if low >= 0xdc00 && low <= 0xdfff {
					r = utf16.DecodeRune(r, rune(low))
					i += 6
				}
			}
			cp = append(cp, r)
		}
	}
	return sourceText(cp), nil
}

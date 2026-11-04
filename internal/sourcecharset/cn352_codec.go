package sourcecharset

import (
	"errors"
	"unicode/utf8"

	"light-media/internal/plugins/iyuuautoseed/compat/encodingdetector"
)

// Reuse only the existing fixed Python codec implementation. No Chardet model,
// language ranking, Detect result or guessed default encoding is requested.
func cnDecode(data []byte, name, mode string) (string, error) {
	return encodingdetector.DecodeErrors(data, name, mode)
}
func cnDecodeFailure(err error) bool { return encodingdetector.ErrorClass(err) == "UnicodeDecodeError" }

// Python strings can contain a surrogate from UTF-7. The reused exact codec
// preserves it as WTF-8; count one codepoint instead of three replacement runes.
func cnCodepoints(text string) ([]rune, error) {
	out := make([]rune, 0, len(text))
	for len(text) > 0 {
		if len(text) >= 3 && text[0] == 0xed && text[1] >= 0xa0 && text[1] <= 0xbf && text[2] >= 0x80 && text[2] <= 0xbf {
			out = append(out, rune(text[0]&15)<<12|rune(text[1]&63)<<6|rune(text[2]&63))
			text = text[3:]
			continue
		}
		r, n := utf8.DecodeRuneInString(text)
		if r == utf8.RuneError && n == 1 {
			return nil, errors.New("decoder returned invalid Python string encoding")
		}
		out = append(out, r)
		text = text[n:]
	}
	return out, nil
}

package iyuuautoseed

import (
	"errors"
	"math/big"
	"strings"
	"unicode/utf8"
)

var errTorrentBencode = errors.New("invalid torrentool bencoding")

type bencodeMarker struct{ dict bool }

// The installed torrentool parser is deliberately more permissive than the
// download-add validator: leading zero integers, unsorted/duplicate dictionary
// keys, truncated byte strings, and odd final dictionary pairs are accepted.
// Keep that parser separate so strict downloader validation cannot silently
// change TorrentHelper's cache and malformed-content behavior.
func torrentoolDecode(raw []byte) (any, error) {
	if len(raw) > 16<<20 {
		return nil, errTorrentBencode
	}
	stack := []any{}
	markers := []*bencodeMarker{}
	forward := func(data []byte, delim byte) (*big.Int, int, error) {
		end := 0
		for end < len(data) && data[end] != delim {
			end++
		}
		used := end + 1
		if end == len(data) {
			used = end
		}
		// The source builds the integer string with chr(byte). Python accepts
		// Latin-1 whitespace around digits and underscores only between digits.
		isSpace := func(b byte) bool {
			return b >= 9 && b <= 13 || b == 32 || b == 0x85 || b == 0xa0
		}
		digits := data[:end]
		for len(digits) > 0 && isSpace(digits[0]) {
			digits = digits[1:]
		}
		for len(digits) > 0 && isSpace(digits[len(digits)-1]) {
			digits = digits[:len(digits)-1]
		}
		s := string(digits)
		if s == "" {
			if end != 0 {
				return nil, 0, errTorrentBencode
			}
			s = "0"
		}
		for i := range len(s) {
			if s[i] == '_' && (i == 0 || i+1 == len(s) || !torrentDigit(s[i-1]) || !torrentDigit(s[i+1])) {
				return nil, 0, errTorrentBencode
			}
		}
		s = strings.ReplaceAll(s, "_", "")
		n, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return nil, 0, errTorrentBencode
		}
		return n, used, nil
	}
	for len(raw) > 0 {
		switch raw[0] {
		case 'd', 'l':
			m := &bencodeMarker{dict: raw[0] == 'd'}
			stack = append(stack, m)
			markers = append(markers, m)
			raw = raw[1:]
		case 'e':
			if len(markers) == 0 {
				return nil, errTorrentBencode
			}
			m := markers[len(markers)-1]
			markers = markers[:len(markers)-1]
			start := len(stack) - 1
			for start >= 0 {
				if marker, ok := stack[start].(*bencodeMarker); ok && marker == m {
					break
				}
				start--
			}
			if start < 0 {
				return nil, errTorrentBencode
			}
			values := stack[start+1:]
			var result any
			if m.dict {
				out := map[string]any{}
				for i := 0; i+1 < len(values); i += 2 {
					k, ok := values[i].(string)
					if !ok {
						return nil, errTorrentBencode
					}
					out[k] = values[i+1]
				}
				result = out
			} else {
				result = append([]any{}, values...)
			}
			stack = append(stack[:start], result)
			raw = raw[1:]
		case 'i':
			n, used, e := forward(raw[1:], 'e')
			if e != nil {
				return nil, e
			}
			stack = append(stack, n)
			raw = raw[1+used:]
		default:
			if !torrentDigit(raw[0]) {
				return nil, errTorrentBencode
			}
			n, used, e := forward(raw, ':')
			if e != nil || !n.IsInt64() || n.Sign() < 0 {
				return nil, errTorrentBencode
			}
			size := n.Int64()
			if size > int64(len(raw)-used) {
				size = int64(len(raw) - used)
			}
			piece := raw[used : used+int(size)]
			var value any = pythonUTF8Replace(piece)
			key := ""
			if len(stack) > 0 {
				key, _ = stack[len(stack)-1].(string)
			}
			if !utf8.Valid(piece) && key == "pieces" {
				value = append([]byte{}, piece...)
			}
			stack = append(stack, value)
			raw = raw[used+int(size):]
		}
	}
	// torrentool returns the remaining stack even when a container was never
	// closed. The confirmation form accepts a successful decode, and the
	// subsequent file projection decides whether the content can be cached.
	if len(stack) == 1 {
		return stack[0], nil
	}
	return stack, nil
}

func torrentDigit(b byte) bool { return b >= '0' && b <= '9' }

func pythonUTF8Replace(raw []byte) string {
	var b strings.Builder
	for len(raw) > 0 {
		r, n := utf8.DecodeRune(raw)
		if r != utf8.RuneError || n > 1 {
			b.WriteRune(r)
			raw = raw[n:]
			continue
		}
		// Python consumes the valid prefix of an incomplete sequence as one error;
		// invalid initial bytes and constrained second bytes each consume one.
		used := 1
		need := 0
		first := raw[0]
		if first >= 0xc2 && first <= 0xdf {
			need = 2
		} else if first >= 0xe0 && first <= 0xef {
			need = 3
		} else if first >= 0xf0 && first <= 0xf4 {
			need = 4
		}
		if need > 0 {
			for used < need && used < len(raw) {
				v := raw[used]
				if v < 0x80 || v > 0xbf {
					break
				}
				if used == 1 && ((first == 0xe0 && v < 0xa0) || (first == 0xed && v > 0x9f) || (first == 0xf0 && v < 0x90) || (first == 0xf4 && v > 0x8f)) {
					break
				}
				used++
			}
		}
		b.WriteRune(utf8.RuneError)
		raw = raw[used:]
	}
	return b.String()
}

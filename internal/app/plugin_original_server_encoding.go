package app

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"
)

var errOriginalCenterEncoding = errors.New("invalid plugin center JSON encoding")

// json.loads(bytes) auto-detects UTF-8/16/32 and decodes with surrogatepass.
// Do not confuse a malformed UTF-8 body with valid BOM-less UTF-16/32 JSON.
func originalCenterJSONBytes(raw []byte) ([]byte, error) {
	width, little, skip := 1, false, 0
	switch {
	case bytes.HasPrefix(raw, []byte{0, 0, 0xfe, 0xff}):
		width, skip = 4, 4
	case bytes.HasPrefix(raw, []byte{0xff, 0xfe, 0, 0}):
		width, little, skip = 4, true, 4
	case bytes.HasPrefix(raw, []byte{0xfe, 0xff}):
		width, skip = 2, 2
	case bytes.HasPrefix(raw, []byte{0xff, 0xfe}):
		width, little, skip = 2, true, 2
	case bytes.HasPrefix(raw, []byte{0xef, 0xbb, 0xbf}):
		skip = 3
	case len(raw) >= 4:
		if raw[0] == 0 {
			if raw[1] != 0 {
				width = 2
			} else {
				width = 4
			}
		} else if raw[1] == 0 {
			little = true
			if raw[2] != 0 || raw[3] != 0 {
				width = 2
			} else {
				width = 4
			}
		}
	case len(raw) == 2:
		if raw[0] == 0 {
			width = 2
		} else if raw[1] == 0 {
			width, little = 2, true
		}
	}
	raw = raw[skip:]
	if width == 1 {
		for at := 0; at < len(raw); {
			_, n := originalCenterDecodeRune(raw[at:])
			if n == 0 {
				return nil, errOriginalCenterEncoding
			}
			at += n
		}
		return raw, nil
	}
	if len(raw)%width != 0 {
		return nil, errOriginalCenterEncoding
	}
	var order binary.ByteOrder = binary.BigEndian
	if little {
		order = binary.LittleEndian
	}
	out := make([]byte, 0, len(raw))
	for at := 0; at < len(raw); at += width {
		var r rune
		if width == 4 {
			n := order.Uint32(raw[at:])
			if n > utf8.MaxRune {
				return nil, errOriginalCenterEncoding
			}
			r = rune(n)
		} else {
			r = rune(order.Uint16(raw[at:]))
			if r >= 0xd800 && r <= 0xdbff && at+4 <= len(raw) {
				low := rune(order.Uint16(raw[at+2:]))
				if low >= 0xdc00 && low <= 0xdfff {
					r = 0x10000 + (r-0xd800)*0x400 + (low - 0xdc00)
					at += 2
				}
			}
		}
		out = originalCenterAppendRune(out, r)
	}
	return out, nil
}
func originalCenterAppendRune(out []byte, r rune) []byte {
	if r >= 0xd800 && r <= 0xdfff {
		return append(out, byte(0xe0|(r>>12)), byte(0x80|((r>>6)&0x3f)), byte(0x80|(r&0x3f)))
	}
	return utf8.AppendRune(out, r)
}
func originalCenterDecodeRune(raw []byte) (rune, int) {
	if len(raw) >= 3 && raw[0] == 0xed && raw[1] >= 0xa0 && raw[1] <= 0xbf && raw[2] >= 0x80 && raw[2] <= 0xbf {
		return rune(raw[0]&15)<<12 | rune(raw[1]&63)<<6 | rune(raw[2]&63), 3
	}
	r, n := utf8.DecodeRune(raw)
	if r == utf8.RuneError && n == 1 {
		return 0, 0
	}
	return r, n
}
func originalCenterRuneCount(raw []byte) int {
	count := 0
	for len(raw) > 0 {
		_, n := originalCenterDecodeRune(raw)
		if n == 0 {
			n = 1
		}
		raw = raw[n:]
		count++
	}
	return count
}
func originalCenterQuotedJSON(raw []byte) []byte {
	out := make([]byte, 0, len(raw))
	for at := 0; at < len(raw); {
		r, n := originalCenterDecodeRune(raw[at:])
		if n == 0 {
			return raw
		}
		if r >= 0xd800 && r <= 0xdfff {
			out = append(out, fmt.Sprintf("\\u%04x", r)...)
		} else {
			out = append(out, raw[at:at+n]...)
		}
		at += n
	}
	return out
}

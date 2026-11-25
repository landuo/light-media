package autosignin

import (
	"strings"
	"unicode/utf8"
)

func siteUTF8(body []byte) string {
	var b strings.Builder
	for i := 0; i < len(body); {
		r, width := utf8.DecodeRune(body[i:])
		if r != utf8.RuneError || width > 1 {
			b.WriteRune(r)
			i += width
			continue
		}
		count := 1
		lead := body[i]
		length := 0
		switch {
		case lead >= 0xc2 && lead <= 0xdf:
			length = 2
		case lead >= 0xe0 && lead <= 0xef:
			length = 3
		case lead >= 0xf0 && lead <= 0xf4:
			length = 4
		}
		for count < length && i+count < len(body) {
			next := body[i+count]
			lo, hi := byte(0x80), byte(0xbf)
			if count == 1 {
				switch lead {
				case 0xe0:
					lo = 0xa0
				case 0xed:
					hi = 0x9f
				case 0xf0:
					lo = 0x90
				case 0xf4:
					hi = 0x8f
				}
			}
			if next < lo || next > hi {
				break
			}
			count++
		}
		b.WriteRune(utf8.RuneError)
		i += count
	}
	return b.String()
}

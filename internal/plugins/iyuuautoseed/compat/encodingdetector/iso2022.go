package encodingdetector

import (
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
	"sync"
)

// ISO-2022 state processing follows CPython v3.12.13 _codecs_iso2022.c.
// Character mappings and old ISO8859-7 G2 semantics come from exhaustive source calls.
type isoTableEntry struct {
	once sync.Once
	raw  []byte
	err  error
}

var isoCache sync.Map

func getISOTable(enc, key string) ([]byte, error) {
	v, _ := isoCache.LoadOrStore(enc+"-"+key, &isoTableEntry{})
	entry := v.(*isoTableEntry)
	entry.once.Do(func() {
		b, e := codecAssets.ReadFile("reference/generated/iso/" + enc + "-" + key + ".bin.z")
		if e != nil {
			entry.err = e
			return
		}
		raw, e := inflate(b)
		if e != nil {
			entry.err = e
			return
		}
		if len(raw) < 9 || string(raw[:5]) != "CISO1" || len(raw) != 9+int(binary.BigEndian.Uint32(raw[5:]))*8 {
			entry.err = errors.New("invalid ISO2022 map")
			return
		}
		entry.raw = raw[9:]
	})
	return entry.raw, entry.err
}
func isoText(enc, key string, index int) (uint64, error) {
	table, e := getISOTable(enc, key)
	if e != nil {
		return 0, e
	}
	if index < 0 || index >= len(table)/8 {
		return 0, errors.New("invalid ISO2022 map index")
	}
	return binary.BigEndian.Uint64(table[index*8:]), nil
}
func writeISO(b *strings.Builder, v uint64) {
	count := (v >> 42) & 3
	for i := uint64(0); i < count; i++ {
		appendScalar(b, uint32(v>>(21*i)&0x1fffff))
	}
}

var isoAllowed = map[string][]byte{"iso2022_jp": {'B' | 128, '@' | 128, 'J'}, "iso2022_jp_1": {'B' | 128, 'D' | 128, '@' | 128, 'J'}, "iso2022_jp_2": {'A' | 128, 'B' | 128, 'C' | 128, 'D' | 128, '@' | 128, 'J', 'A', 'F'}, "iso2022_jp_3": {'B' | 128, 'O' | 128, 'P' | 128}, "iso2022_jp_2004": {'B' | 128, 'Q' | 128, 'P' | 128}, "iso2022_jp_ext": {'B' | 128, 'D' | 128, 'J', 'I', '@' | 128}, "iso2022_kr": {'C' | 128}}

func decodeISO(d []byte, enc, mode string) (string, error) {
	if isoAllowed[enc] == nil {
		return "", errors.New("missing ISO2022 codec")
	}
	g := [3]byte{'B', 'B', 'B'}
	shifted, through := false, false
	noShift := enc != "iso2022_kr"
	useG2 := enc == "iso2022_jp_2"
	ext := enc != "iso2022_kr"
	allowed := func(c byte) bool {
		if c == 'B' {
			return true
		}
		if useG2 && (c == 'A' || c == 'F') {
			return true
		}
		for _, v := range isoAllowed[enc] {
			if c == v {
				return true
			}
		}
		return false
	}
	escend := func(c byte) bool { return c >= 'A' && c <= 'Z' || c == '@' }
	var b strings.Builder
	for i := 0; i < len(d); {
		c := d[i]
		if through {
			appendScalar(&b, uint32(c))
			i++
			if escend(c) {
				through = false
			}
			continue
		}
		consume := 0
		incomplete := false
		switch c {
		case 0x1b:
			if len(d)-i < 2 {
				incomplete = true
				break
			}
			c2 := d[i+1]
			if c2 == '(' || c2 == ')' || c2 == '$' || c2 == '.' || c2 == '&' {
				length := 0
				for j := 1; j < 16; j++ {
					if i+j >= len(d) {
						incomplete = true
						break
					}
					if escend(d[i+j]) {
						length = j + 1
						break
					} else if ext && i+j+1 < len(d) && d[i+j] == '&' && d[i+j+1] == '@' {
						j += 2
					}
				}
				if incomplete {
					break
				}
				if length == 0 {
					consume = 1
					break
				}
				charset := byte(0)
				designation := 0
				valid := true
				switch length {
				case 3:
					if c2 == '$' {
						charset = d[i+2] | 0x80
					} else {
						charset = d[i+2]
						switch c2 {
						case '(':
							designation = 0
						case ')':
							designation = 1
						case '.':
							if useG2 {
								designation = 2
							} else {
								valid = false
							}
						default:
							valid = false
						}
					}
				case 4:
					if c2 != '$' {
						valid = false
					} else {
						charset = d[i+3] | 0x80
						switch d[i+2] {
						case '(':
							designation = 0
						case ')':
							designation = 1
						default:
							valid = false
						}
					}
				case 6:
					if ext && d[i+3] == 0x1b && d[i+4] == '$' && d[i+5] == 'B' {
						charset = 'B' | 0x80
					} else {
						valid = false
					}
				default:
					valid = false
				}
				if !valid || !allowed(charset) {
					consume = length
					break
				}
				g[designation] = charset
				i += length
				continue
			} else if useG2 && c2 == 'N' {
				if len(d)-i < 3 {
					incomplete = true
					break
				}
				v := d[i+2]
				var text uint64
				switch g[2] {
				case 'A', 'F':
					var e error
					text, e = isoText(enc, "g2"+string(g[2]), int(v))
					if e != nil {
						return "", e
					}
				case 'B':
					if v < 128 {
						text = 1<<63 | 1<<42 | uint64(v)
					}
				default:
					return "", errors.New("invalid ISO2022 G2 internal state")
				}
				if text == 0 {
					consume = 3
					break
				}
				writeISO(&b, text)
				i += 3
				continue
			} else {
				b.WriteByte(0x1b)
				through = true
				i++
				continue
			}
		case 0x0e, 0x0f:
			if !noShift {
				shifted = c == 0x0e
				i++
				continue
			}
			b.WriteByte(c)
			i++
			continue
		case 0x0a:
			shifted = false
			b.WriteByte(c)
			i++
			continue
		default:
			if c < 0x20 {
				b.WriteByte(c)
				i++
				continue
			}
			if c >= 128 {
				consume = 1
				break
			}
			cs := g[0]
			if shifted {
				cs = g[1]
			}
			if cs == 'B' {
				b.WriteByte(c)
				i++
				continue
			}
			width := 1
			if cs&0x80 != 0 {
				width = 2
			}
			if len(d)-i < width {
				incomplete = true
				break
			}
			idx := int(c)
			if width == 2 {
				idx = idx*256 + int(d[i+1])
			}
			text := uint64(0)
			if cs != 'A' && cs != 'F' {
				var e error
				text, e = isoText(enc, strconv.Itoa(int(cs)), idx)
				if e != nil {
					return "", e
				}
			}
			if text == 0 {
				consume = width
				break
			}
			writeISO(&b, text)
			i += width
			continue
		}
		if e := bad(&b, mode); e != nil {
			return "", e
		}
		if incomplete {
			break
		}
		if consume <= 0 {
			return "", errors.New("invalid ISO2022 decoder state")
		}
		i += consume
	}
	return b.String(), nil
}

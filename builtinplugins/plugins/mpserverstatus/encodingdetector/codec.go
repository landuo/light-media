package encodingdetector

import (
	"embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

//go:embed reference/generated/codecs/*.bin.z reference/generated/special-codecs.json.z reference/generated/iso/*.bin.z reference/generated/unicode-names.bin.z
var codecAssets embed.FS
var machineCache sync.Map

type machineEntry struct {
	once sync.Once
	m    *machine
	e    error
}

type machine struct {
	raw            []byte
	outputs        []string
	offset, states int
}

func getMachine(enc, mode string) (*machine, error) {
	key := enc + "-" + mode
	value, _ := machineCache.LoadOrStore(key, &machineEntry{})
	entry := value.(*machineEntry)
	entry.once.Do(func() { entry.m, entry.e = parseMachine(enc, mode) })
	return entry.m, entry.e
}
func parseMachine(enc, mode string) (*machine, error) {
	key := enc + "-" + mode
	b, e := codecAssets.ReadFile("reference/generated/codecs/" + key + ".bin.z")
	if e != nil {
		return nil, e
	}
	raw, e := inflate(b)
	if e != nil {
		return nil, e
	}
	if len(raw) < 13 || string(raw[:5]) != "CDFA1" {
		return nil, fmt.Errorf("invalid codec table")
	}
	n := int(binary.BigEndian.Uint32(raw[5:]))
	out := int(binary.BigEndian.Uint32(raw[9:]))
	off := 13
	outputs := make([]string, out)
	for i := range outputs {
		if len(raw) < off+4 {
			return nil, errors.New("truncated codec")
		}
		ln := int(binary.BigEndian.Uint32(raw[off:]))
		off += 4
		if len(raw) < off+ln {
			return nil, errors.New("truncated codec output")
		}
		outputs[i] = string(raw[off : off+ln])
		off += ln
	}
	if len(raw)-off != n*(5+256*8) {
		return nil, errors.New("invalid codec transitions")
	}
	m := &machine{raw: raw, outputs: outputs, offset: off, states: n}
	return m, nil
}
func (m *machine) decode(d []byte) (string, error) {
	var b strings.Builder
	state := uint32(0)
	const row = 5 + 256*8
	for _, c := range d {
		off := m.offset + int(state)*row + 5 + int(c)*8
		next := binary.BigEndian.Uint32(m.raw[off:])
		oi := binary.BigEndian.Uint32(m.raw[off+4:])
		if next == 0xffffffff {
			return "", errors.New("invalid encoded byte sequence")
		}
		if next >= uint32(m.states) || oi >= uint32(len(m.outputs)) {
			return "", errors.New("corrupt codec transition")
		}
		b.WriteString(m.outputs[oi])
		state = next
	}
	off := m.offset + int(state)*row
	if m.raw[off] == 0 {
		return "", errors.New("incomplete encoded byte sequence")
	}
	oi := binary.BigEndian.Uint32(m.raw[off+1:])
	b.WriteString(m.outputs[oi])
	return b.String(), nil
}

var specialOnce sync.Once
var specialError error
var special struct {
	EUC    [][]int  `json:"euc_kr"`
	GB     [][]int  `json:"gb18030"`
	Ranges [][2]int `json:"gb18030_ranges"`
}

func loadSpecial() error {
	specialOnce.Do(func() {
		b, e := codecAssets.ReadFile("reference/generated/special-codecs.json.z")
		if e != nil {
			specialError = e
			return
		}
		b, e = inflate(b)
		if e != nil {
			specialError = e
			return
		}
		specialError = json.Unmarshal(b, &special)
	})
	return specialError
}
func appendScalar(b *strings.Builder, r uint32) {
	if r >= 0xd800 && r <= 0xdfff {
		b.WriteByte(byte(0xe0 | r>>12))
		b.WriteByte(byte(0x80 | r>>6&63))
		b.WriteByte(byte(0x80 | r&63))
		return
	}
	b.WriteRune(rune(r))
}
func bad(b *strings.Builder, mode string) error {
	if mode == "strict" {
		return errors.New("invalid encoded byte sequence")
	}
	if mode == "replace" {
		b.WriteRune('\ufffd')
	}
	return nil
}
func decodeCJK(d []byte, enc, mode string) (string, error) {
	if e := loadSpecial(); e != nil {
		return "", e
	}
	var b strings.Builder
	for i := 0; i < len(d); {
		c := d[i]
		if c < 128 {
			b.WriteByte(c)
			i++
			continue
		}
		need := 2
		if len(d)-i >= 2 {
			if enc == "gb18030" && d[i+1] >= 0x30 && d[i+1] <= 0x39 {
				need = 4
			}
			if enc == "euc_kr" && c == 0xa4 && d[i+1] == 0xd4 {
				need = 8
			}
		}
		if len(d)-i < need {
			if e := bad(&b, mode); e != nil {
				return "", e
			}
			break
		}
		accepted := false
		if need == 2 {
			table := special.GB
			if enc == "euc_kr" {
				table = special.EUC
			}
			v := table[(int(c)-128)*256+int(d[i+1])]
			if v != nil {
				for _, r := range v {
					appendScalar(&b, uint32(r))
				}
				accepted = true
			}
		}
		if need == 4 && c >= 0x81 && c <= 0xfe && d[i+2] >= 0x81 && d[i+2] <= 0xfe && d[i+3] >= 0x30 && d[i+3] <= 0x39 {
			n := ((int(c)-0x81)*10+int(d[i+1])-0x30)*1260 + (int(d[i+2])-0x81)*10 + int(d[i+3]) - 0x30
			scalar := -1
			if n < 39420 {
				for _, q := range special.Ranges {
					if q[0] > n {
						break
					}
					scalar = q[1] + n - q[0]
				}
			} else if c >= 0x90 {
				scalar = 0x10000 + n - 189000
				if scalar > 0x10ffff {
					scalar = -1
				}
			}
			if scalar >= 0 {
				appendScalar(&b, uint32(scalar))
				accepted = true
			}
		}
		if need == 8 && d[i+2] == 0xa4 && d[i+4] == 0xa4 && d[i+6] == 0xa4 {
			cho := []int{0, 1, -1, 2, -1, -1, 3, 4, 5, -1, -1, -1, -1, -1, -1, -1, 6, 7, 8, -1, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18}
			jong := []int{1, 2, 3, 4, 5, 6, 7, -1, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, -1, 18, 19, 20, 21, 22, -1, 23, 24, 25, 26, 27}
			a, j, k := -1, -1, -1
			if d[i+3] >= 0xa1 && d[i+3] <= 0xbe {
				a = cho[int(d[i+3])-0xa1]
			}
			if d[i+5] >= 0xbf && d[i+5] <= 0xd3 {
				j = int(d[i+5]) - 0xbf
			}
			if d[i+7] == 0xd4 {
				k = 0
			} else if d[i+7] >= 0xa1 && d[i+7] <= 0xbe {
				k = jong[int(d[i+7])-0xa1]
			}
			if a >= 0 && j >= 0 && k >= 0 {
				appendScalar(&b, uint32(0xac00+a*588+j*28+k))
				accepted = true
			}
		}
		if accepted {
			i += need
		} else {
			if e := bad(&b, mode); e != nil {
				return "", e
			}
			i++
		}
	}
	return b.String(), nil
}
func decodeUTF8(d []byte, mode string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(d); {
		c := d[i]
		if c < 128 {
			b.WriteByte(c)
			i++
			continue
		}
		n := 0
		switch {
		case c >= 0xc2 && c <= 0xdf:
			n = 2
		case c >= 0xe0 && c <= 0xef:
			n = 3
		case c >= 0xf0 && c <= 0xf4:
			n = 4
		}
		consumed := 1
		valid := n > 0
		for j := 1; valid && j < n; j++ {
			if i+j >= len(d) {
				valid = false
				break
			}
			v := d[i+j]
			if v < 0x80 || v > 0xbf || j == 1 && (c == 0xe0 && v < 0xa0 || c == 0xed && v > 0x9f || c == 0xf0 && v < 0x90 || c == 0xf4 && v > 0x8f) {
				valid = false
				break
			}
			consumed++
		}
		if !valid {
			if e := bad(&b, mode); e != nil {
				return "", e
			}
			i += consumed
			continue
		}
		b.Write(d[i : i+n])
		i += n
	}
	return b.String(), nil
}
func decodeUnicode(d []byte, enc, mode string) (string, error) {
	if enc == "utf-8" || enc == "utf-8-sig" {
		if enc == "utf-8-sig" && len(d) >= 3 && string(d[:3]) == "\xef\xbb\xbf" {
			d = d[3:]
		}
		return decodeUTF8(d, mode)
	}
	if enc == "utf-7" {
		return decodeUTF7(d, mode)
	}
	size := 2
	if strings.HasPrefix(enc, "utf-32") {
		size = 4
	}
	le := !strings.HasSuffix(enc, "be")
	if enc == "utf-16" && len(d) >= 2 {
		if d[0] == 0xff && d[1] == 0xfe {
			d = d[2:]
		} else if d[0] == 0xfe && d[1] == 0xff {
			le = false
			d = d[2:]
		}
	}
	if enc == "utf-32" && len(d) >= 4 {
		if string(d[:4]) == "\xff\xfe\x00\x00" {
			d = d[4:]
		} else if string(d[:4]) == "\x00\x00\xfe\xff" {
			le = false
			d = d[4:]
		}
	}
	order := binary.ByteOrder(binary.BigEndian)
	if le {
		order = binary.LittleEndian
	}
	var b strings.Builder
	for i := 0; i < len(d); {
		if len(d)-i < size {
			if e := bad(&b, mode); e != nil {
				return "", e
			}
			break
		}
		var r uint32
		if size == 4 {
			r = order.Uint32(d[i:])
			i += 4
			if r > utf8.MaxRune || r >= 0xd800 && r <= 0xdfff {
				if e := bad(&b, mode); e != nil {
					return "", e
				}
				continue
			}
		} else {
			r = uint32(order.Uint16(d[i:]))
			i += 2
			if r >= 0xd800 && r <= 0xdbff {
				if len(d)-i < 2 {
					if e := bad(&b, mode); e != nil {
						return "", e
					}
					break
				}
				v := uint32(order.Uint16(d[i:]))
				if v < 0xdc00 || v > 0xdfff {
					if e := bad(&b, mode); e != nil {
						return "", e
					}
					continue
				}
				r = 0x10000 + (r-0xd800)*1024 + v - 0xdc00
				i += 2
			} else if r >= 0xdc00 && r <= 0xdfff {
				if e := bad(&b, mode); e != nil {
					return "", e
				}
				continue
			}
		}
		appendScalar(&b, r)
	}
	return b.String(), nil
}
func decodeUTF7(d []byte, mode string) (string, error) {
	var b strings.Builder
	shift := false
	bits := 0
	var buffer, surrogate uint32
	for i := 0; i < len(d); {
		c := d[i]
		if shift {
			v := b64value(c)
			if v >= 0 {
				buffer = buffer<<6 | uint32(v)
				bits += 6
				i++
				if bits >= 16 {
					out := buffer >> uint(bits-16)
					bits -= 16
					buffer &= (1 << uint(bits)) - 1
					if surrogate != 0 {
						if out >= 0xdc00 && out <= 0xdfff {
							appendScalar(&b, 0x10000+(surrogate-0xd800)*1024+out-0xdc00)
							surrogate = 0
							continue
						}
						appendScalar(&b, surrogate)
						surrogate = 0
					}
					if out >= 0xd800 && out <= 0xdbff {
						surrogate = out
					} else {
						appendScalar(&b, out)
					}
				}
				continue
			}
			shift = false
			if bits > 0 && (bits >= 6 || buffer != 0) {
				i++
				if e := bad(&b, mode); e != nil {
					return "", e
				}
				continue
			}
			if surrogate != 0 && c <= 127 && c != '+' {
				appendScalar(&b, surrogate)
			}
			surrogate = 0
			if c == '-' {
				i++
			}
			continue
		}
		if c == '+' {
			i++
			if i < len(d) && d[i] == '-' {
				b.WriteByte('+')
				i++
			} else if i < len(d) && b64value(d[i]) < 0 {
				i++
				if e := bad(&b, mode); e != nil {
					return "", e
				}
			} else {
				shift = true
				surrogate = 0
				bits = 0
				buffer = 0
			}
			continue
		}
		if c <= 127 {
			b.WriteByte(c)
			i++
		} else {
			i++
			if e := bad(&b, mode); e != nil {
				return "", e
			}
		}
	}
	if shift && (surrogate != 0 || bits >= 6 || bits > 0 && buffer != 0) {
		if e := bad(&b, mode); e != nil {
			return "", e
		}
	}
	return b.String(), nil
}
func decode(d []byte, enc, mode string) (string, error) {
	switch enc {
	case "unicode-escape":
		return decodeEscape(d, false, mode)
	case "raw-unicode-escape":
		return decodeEscape(d, true, mode)
	case "punycode":
		return decodePunycode(d, mode)
	case "undefined":
		return "", codecError(enc, "UnicodeError", "undefined encoding")
	case "idna":
		return decodeIDNAConsumed(d, mode)
	}

	if strings.HasPrefix(enc, "utf-") {
		return decodeUnicode(d, enc, mode)
	}
	if enc == "euc_kr" || enc == "gb18030" {
		return decodeCJK(d, enc, mode)
	}
	if strings.HasPrefix(enc, "iso2022") {
		return decodeISO(d, enc, mode)
	}
	m, e := getMachine(enc, mode)
	if e != nil {
		return "", e
	}
	return m.decode(d)
}
func Decode(data []byte, encoding string, replace bool) (string, error) {
	mode := "strict"
	if replace {
		mode = "replace"
	}
	return DecodeErrors(data, encoding, mode)
}
func DecodeErrors(data []byte, encoding, mode string) (string, error) {
	if strings.IndexByte(encoding, 0) >= 0 {
		return "", codecError(encoding, "ValueError", "embedded null character")
	}
	if mode != "strict" && mode != "replace" && mode != "ignore" {
		return "", codecError(encoding, "LookupError", "unsupported error handling")
	}
	if len(data) == 0 {
		return "", nil
	}
	if e := loadData(); e != nil {
		return "", e
	}
	norm := codecNormalize(encoding)
	info, ok := pythonCodecLookup[norm]
	if !ok {
		module := pythonAliasModules[norm]
		if module == "" {
			module = pythonAliasModules[strings.ReplaceAll(norm, ".", "_")]
		}
		if module != "" {
			info, ok = pythonCodecLookup[module]
		}
	}
	if !ok || !info.Text {
		return "", codecError(encoding, "LookupError", "unknown or non-text encoding")
	}
	s, e := decode(data, info.Name, mode)
	if e != nil && ErrorClass(e) == "" {
		return "", &CodecError{Class: "UnicodeDecodeError", Encoding: info.Name, Reason: "invalid encoded byte sequence", Cause: e}
	}
	return s, e
}

func validate(d []byte, enc string) bool {
	if strings.HasPrefix(enc, "utf-") || strings.HasPrefix(enc, "iso2022") || enc == "gb18030" || enc == "euc_kr" {
		_, e := decode(d, enc, "strict")
		return e == nil
	}
	m, e := getMachine(enc, "strict")
	if e != nil {
		return false
	}
	state := uint32(0)
	const row = 5 + 256*8
	for _, c := range d {
		off := m.offset + int(state)*row + 5 + int(c)*8
		state = binary.BigEndian.Uint32(m.raw[off:])
		if state == 0xffffffff {
			return false
		}
	}
	return m.raw[m.offset+int(state)*row] != 0
}

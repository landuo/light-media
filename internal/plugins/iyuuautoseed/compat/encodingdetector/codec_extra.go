package encodingdetector

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/big"
	"strings"
	"sync"
)

// CodecError distinguishes the exception classes consumed by requests.text.
type CodecError struct {
	Class, Encoding, Reason string
	Cause                   error
}

func (e *CodecError) Error() string { return e.Class + ": " + e.Reason }
func (e *CodecError) Unwrap() error { return e.Cause }
func ErrorClass(e error) string {
	var c *CodecError
	if errors.As(e, &c) {
		return c.Class
	}
	return ""
}
func codecError(enc, class, reason string) error {
	return &CodecError{Class: class, Encoding: enc, Reason: reason}
}

var namesOnce sync.Once
var namesRaw []byte
var namesOffsets []int
var namesError error

func unicodeName(name []byte) (uint32, bool, error) {
	namesOnce.Do(func() {
		b, e := codecAssets.ReadFile("reference/generated/unicode-names.bin.z")
		if e != nil {
			namesError = e
			return
		}
		b, e = inflate(b)
		if e != nil {
			namesError = e
			return
		}
		if len(b) < 9 || string(b[:5]) != "CNAM1" {
			namesError = errors.New("invalid Unicode name table")
			return
		}
		n := int(binary.BigEndian.Uint32(b[5:]))
		off := 9
		for i := 0; i < n; i++ {
			if len(b) < off+2 {
				namesError = errors.New("truncated Unicode names")
				return
			}
			namesOffsets = append(namesOffsets, off)
			ln := int(binary.BigEndian.Uint16(b[off:]))
			off += 2 + ln + 4
			if off > len(b) {
				namesError = errors.New("truncated Unicode name")
				return
			}
		}
		namesRaw = b
	})
	if namesError != nil {
		return 0, false, namesError
	}
	key := append([]byte(nil), name...)
	for i, c := range key {
		if c >= 'a' && c <= 'z' {
			key[i] = c - 32
		}
	}
	lo, hi := 0, len(namesOffsets)
	for lo < hi {
		m := (lo + hi) / 2
		off := namesOffsets[m]
		ln := int(binary.BigEndian.Uint16(namesRaw[off:]))
		word := namesRaw[off+2 : off+2+ln]
		cmp := bytes.Compare(key, word)
		if cmp == 0 {
			return binary.BigEndian.Uint32(namesRaw[off+2+ln:]), true, nil
		}
		if cmp < 0 {
			hi = m
		} else {
			lo = m + 1
		}
	}
	return 0, false, nil
}
func hexDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}
func decodeEscape(d []byte, raw bool, mode string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(d); {
		c := d[i]
		i++
		if c != '\\' {
			appendScalar(&b, uint32(c))
			continue
		}
		if i == len(d) {
			if raw {
				b.WriteByte('\\')
				continue
			}
			if e := bad(&b, mode); e != nil {
				return "", e
			}
			break
		}
		c = d[i]
		i++
		count := 0
		if raw {
			if c == 'u' {
				count = 4
			} else if c == 'U' {
				count = 8
			} else {
				b.WriteByte('\\')
				appendScalar(&b, uint32(c))
				continue
			}
		} else {
			switch c {
			case '\n':
				continue
			case '\\', '\'', '"':
				b.WriteByte(c)
				continue
			case 'b':
				b.WriteByte(8)
				continue
			case 'f':
				b.WriteByte(12)
				continue
			case 't':
				b.WriteByte(9)
				continue
			case 'n':
				b.WriteByte(10)
				continue
			case 'r':
				b.WriteByte(13)
				continue
			case 'v':
				b.WriteByte(11)
				continue
			case 'a':
				b.WriteByte(7)
				continue
			case 'x':
				count = 2
			case 'u':
				count = 4
			case 'U':
				count = 8
			case 'N':
				if i == len(d) {
					if e := bad(&b, mode); e != nil {
						return "", e
					}
					continue
				}
				if d[i] == '{' {
					start := i + 1
					end := bytes.IndexByte(d[start:], '}')
					if end < 0 {
						if e := bad(&b, mode); e != nil {
							return "", e
						}
						i = len(d)
						continue
					}
					end += start
					if end == start {
						i = end
					}
					if end > start {
						value, ok, e := unicodeName(d[start:end])
						if e != nil {
							return "", e
						}
						i = end + 1
						if ok {
							appendScalar(&b, value)
							continue
						}
					}
				}
				if e := bad(&b, mode); e != nil {
					return "", e
				}
				continue
			default:
				if c >= '0' && c <= '7' {
					v := uint32(c - '0')
					for n := 0; n < 2 && i < len(d) && d[i] >= '0' && d[i] <= '7'; n++ {
						v = v<<3 | uint32(d[i]-'0')
						i++
					}
					appendScalar(&b, v)
					continue
				}
				b.WriteByte('\\')
				appendScalar(&b, uint32(c))
				continue
			}
		}
		var value uint32
		valid := true
		for n := 0; n < count; n++ {
			if i == len(d) {
				valid = false
				break
			}
			v := hexDigit(d[i])
			if v < 0 {
				valid = false
				break
			}
			value = value<<4 | uint32(v)
			i++
		}
		if !valid || value > 0x10ffff {
			if e := bad(&b, mode); e != nil {
				return "", e
			}
			continue
		}
		appendScalar(&b, value)
	}
	return b.String(), nil
}
func punyAdapt(delta *big.Int, first bool, num int) int {
	d := new(big.Int).Set(delta)
	if first {
		d.Quo(d, big.NewInt(700))
	} else {
		d.Quo(d, big.NewInt(2))
	}
	d.Add(d, new(big.Int).Quo(new(big.Int).Set(d), big.NewInt(int64(num))))
	divisions := 0
	for d.Cmp(big.NewInt(455)) > 0 {
		d.Quo(d, big.NewInt(35))
		divisions += 36
	}
	v := d.Int64()
	return divisions + int(36*v/(v+38))
}
func decodePunycode(d []byte, mode string) (string, error) {
	base := []rune{}
	pos := bytes.LastIndexByte(d, '-')
	ext := d
	if pos >= 0 {
		s, e := decode(d[:pos], "ascii", mode)
		if e != nil {
			return "", e
		}
		base = []rune(s)
		ext = d[pos+1:]
	}
	for _, v := range ext {
		if v >= 128 {
			return "", codecError("punycode", "UnicodeDecodeError", "non-ASCII punycode extension")
		}
	}
	char := int64(128)
	position := big.NewInt(-1)
	bias, extpos := 72, 0
	for extpos < len(ext) {
		delta := big.NewInt(0)
		w := big.NewInt(1)
		j := 0
		newpos := extpos
		valid := true
		for {
			if newpos >= len(ext) {
				valid = false
				break
			}
			c := ext[newpos]
			newpos++
			if c >= 'a' && c <= 'z' {
				c -= 32
			}
			digit := int64(-1)
			if c >= 'A' && c <= 'Z' {
				digit = int64(c - 'A')
			} else if c >= '0' && c <= '9' {
				digit = int64(c) - 22
			}
			if digit < 0 {
				valid = false
				break
			}
			threshold := 36*(j+1) - bias
			if threshold < 1 {
				threshold = 1
			}
			if threshold > 26 {
				threshold = 26
			}
			delta.Add(delta, new(big.Int).Mul(big.NewInt(digit), w))
			if digit < int64(threshold) {
				break
			}
			w.Mul(w, big.NewInt(int64(36-threshold)))
			j++
		}
		if !valid {
			if mode == "strict" {
				return "", codecError("punycode", "UnicodeError", "invalid or incomplete punycode integer")
			}
			break
		}
		position.Add(position, delta)
		position.Add(position, big.NewInt(1))
		q, rem := new(big.Int), new(big.Int)
		q.QuoRem(position, big.NewInt(int64(len(base)+1)), rem)
		q.Add(q, big.NewInt(char))
		if !q.IsInt64() || q.Int64() > 0x10ffff {
			if mode == "strict" {
				return "", codecError("punycode", "UnicodeError", "invalid punycode character")
			}
			char = '?'
		} else {
			char = q.Int64()
		}
		at := int(rem.Int64())
		base = append(base, 0)
		copy(base[at+1:], base[at:])
		base[at] = rune(char)
		position.Set(rem)
		bias = punyAdapt(delta, extpos == 0, len(base))
		extpos = newpos
	}
	var b strings.Builder
	for _, r := range base {
		appendScalar(&b, uint32(r))
	}
	return b.String(), nil
}
func decodeIDNAConsumed(d []byte, mode string) (string, error) {
	if mode != "strict" {
		return "", codecError("idna", "UnicodeError", "unsupported error handling "+mode)
	}
	if !bytes.Contains(d, []byte("xn--")) {
		if s, e := decode(d, "ascii", "strict"); e == nil {
			return s, nil
		}
	}
	return "", codecError("idna", "DependencyUnavailable", "strict IDNA2003 Nameprep/roundtrip is not consumed by requests and is not implemented")
}

package pythonre

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"sync"
	"unicode/utf8"
)

//go:embed internal/data/unicode.z
var unicodePacked []byte

//go:embed internal/data/names.z
var namesPacked []byte
var unicodeOnce, namesOnce sync.Once
var unicodeRows []unicodeRow
var unicodeLower, unicodeUpper map[rune]rune
var namesRaw []byte
var namesOffsets []int

type unicodeRow struct {
	start rune
	flags byte
}

func inflate(b []byte) []byte {
	z, e := zlib.NewReader(bytes.NewReader(b))
	if e != nil {
		panic(e)
	}
	out, e := io.ReadAll(z)
	z.Close()
	if e != nil {
		panic(e)
	}
	return out
}
func loadUnicode() {
	unicodeOnce.Do(func() {
		b := inflate(unicodePacked)
		if fmt.Sprintf("%x", dataDigest(b)) != "44b6d572499abe7dd11187b3a83fd136a8160bd01ad0992a62175592ef44f921" {
			panic("pythonre Unicode digest")
		}
		if len(b) < 11 || string(b[:7]) != "CPRE001" {
			panic("pythonre Unicode data")
		}
		off := 7
		u32 := func() uint32 { v := binary.LittleEndian.Uint32(b[off:]); off += 4; return v }
		unicodeRows = make([]unicodeRow, u32())
		for i := range unicodeRows {
			unicodeRows[i] = unicodeRow{rune(u32()), b[off]}
			off++
		}
		m := func() map[rune]rune {
			out := map[rune]rune{}
			for n := u32(); n > 0; n-- {
				c, v := u32(), u32()
				out[rune(c)] = rune(v)
			}
			return out
		}
		unicodeLower = m()
		unicodeUpper = m()
		if off != len(b) {
			panic("pythonre trailing Unicode data")
		}
	})
}
func unicodeFlags(c rune) byte {
	loadUnicode()
	i := sort.Search(len(unicodeRows), func(i int) bool { return unicodeRows[i].start > c }) - 1
	if i < 0 {
		return 0
	}
	return unicodeRows[i].flags
}
func simpleLower(c rune, ascii bool) rune {
	if ascii {
		if c >= 'A' && c <= 'Z' {
			return c + 32
		}
		return c
	}
	loadUnicode()
	if v, ok := unicodeLower[c]; ok {
		return v
	}
	return c
}
func simpleUpper(c rune) rune {
	loadUnicode()
	if v, ok := unicodeUpper[c]; ok {
		return v
	}
	return c
}
func isCased(c rune, ascii bool) bool {
	if ascii {
		return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
	}
	return unicodeFlags(c)&32 != 0
}
func isIdentifier(rs []rune) bool {
	if len(rs) == 0 {
		return false
	}
	if rs[0] != '_' && unicodeFlags(rs[0])&8 == 0 {
		return false
	}
	for _, r := range rs[1:] {
		if unicodeFlags(r)&16 == 0 {
			return false
		}
	}
	return true
}
func namedRune(name string) (rune, bool) {
	namesOnce.Do(func() {
		if fmt.Sprintf("%x", dataDigest(namesPacked)) != "d375bd0caff3e7763dafdb689ac86edc97862c5a3675867c28e7557965e46f93" {
			panic("pythonre names digest")
		}
		b := inflate(namesPacked)
		if len(b) < 9 || string(b[:5]) != "CNAM1" {
			panic("pythonre name data")
		}
		n := int(binary.BigEndian.Uint32(b[5:]))
		off := 9
		namesOffsets = make([]int, n)
		for i := range namesOffsets {
			namesOffsets[i] = off
			ln := int(binary.BigEndian.Uint16(b[off:]))
			off += 2 + ln + 4
			if off > len(b) {
				panic("pythonre truncated names")
			}
		}
		if off != len(b) {
			panic("pythonre trailing names")
		}
		namesRaw = b
	})
	key := []byte(name)
	for i, c := range key {
		if c >= 'a' && c <= 'z' {
			key[i] = c - 32
		}
	}
	i := sort.Search(len(namesOffsets), func(i int) bool {
		p := namesOffsets[i]
		n := int(binary.BigEndian.Uint16(namesRaw[p:]))
		return bytes.Compare(namesRaw[p+2:p+2+n], key) >= 0
	})
	if i == len(namesOffsets) {
		return 0, false
	}
	p := namesOffsets[i]
	n := int(binary.BigEndian.Uint16(namesRaw[p:]))
	if !bytes.Equal(namesRaw[p+2:p+2+n], key) {
		return 0, false
	}
	return rune(binary.BigEndian.Uint32(namesRaw[p+2+n:])), true
}
func decodeString(s string) ([]rune, error) {
	out := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		if s[i] < 128 {
			out = append(out, rune(s[i]))
			i++
			continue
		}
		if i+2 < len(s) && s[i] == 0xED && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2] >= 0x80 && s[i+2] <= 0xBF {
			out = append(out, rune(s[i]&15)<<12|rune(s[i+1]&63)<<6|rune(s[i+2]&63))
			i += 3
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			return nil, newError("UnicodeError", fmt.Sprintf("invalid WTF8 at %d", i))
		}
		out = append(out, r)
		i += n
	}
	return out, nil
}
func encodeString(rs []rune) string {
	b := []byte{}
	for _, r := range rs {
		if r >= 0xD800 && r <= 0xDFFF {
			b = append(b, byte(0xE0|r>>12), byte(0x80|r>>6&63), byte(0x80|r&63))
		} else {
			b = utf8.AppendRune(b, r)
		}
	}
	return string(b)
}
func dataDigest(b []byte) [32]byte { return sha256.Sum256(b) }

package urlprep

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	hexencoding "encoding/hex"
	"fmt"
	"io"
	"sort"
	"sync"
	"unicode/utf8"
)

//go:embed internal/data/tables.z
var packedTables []byte
var tableOnce sync.Once
var tables unicodeTables

type unicodeProperty struct {
	Start                                       rune
	Combining                                   uint8
	Mark, Named, Ignorable, Cased, Space, Alnum bool
	Bidi                                        string
}
type utsRow struct {
	Start          rune
	Status         byte
	Replacement    []rune
	HasReplacement bool
}
type unicodeTables struct {
	properties                []unicodeProperty
	lower, decomposition      map[rune][]rune
	composition               map[uint64]rune
	decimal                   map[rune]int
	scripts, joining, classes map[string][]uint64
	uts                       []utsRow
}
type tableReader struct {
	b []byte
	i int
}

func (r *tableReader) take(n int) []byte {
	if n < 0 || r.i+n > len(r.b) {
		panic("urlprep corrupt tables")
	}
	v := r.b[r.i : r.i+n]
	r.i += n
	return v
}
func (r *tableReader) u8() byte     { return r.take(1)[0] }
func (r *tableReader) u32() uint32  { return binary.LittleEndian.Uint32(r.take(4)) }
func (r *tableReader) u64() uint64  { return binary.LittleEndian.Uint64(r.take(8)) }
func (r *tableReader) text() string { return string(r.take(int(r.u32()))) }
func loadTables() {
	tableOnce.Do(func() {
		z, e := zlib.NewReader(bytes.NewReader(packedTables))
		if e != nil {
			panic(e)
		}
		raw, e := io.ReadAll(z)
		z.Close()
		if e != nil {
			panic(e)
		}
		sum := sha256.Sum256(raw)
		if hexencoding.EncodeToString(sum[:]) != "1ae2896fca7f3e7cd2cf82896e6e505095809a735b54e56a830aa720d08135f8" {
			panic("urlprep embedded data digest")
		}
		r := &tableReader{b: raw}
		if string(r.take(8)) != "URLPREP1" {
			panic("urlprep table version")
		}
		bidis := make([]string, int(r.u32()))
		for i := range bidis {
			bidis[i] = r.text()
		}
		tables.decimal = map[rune]int{}
		for n := r.u32(); n > 0; n-- {
			tables.decimal[rune(r.u32())] = int(r.u8())
		}
		tables.properties = make([]unicodeProperty, int(r.u32()))
		for i := range tables.properties {
			p := unicodeProperty{Start: rune(r.u32()), Combining: r.u8()}
			f := r.u8()
			p.Mark = f&1 != 0
			p.Named = f&2 != 0
			p.Ignorable = f&4 != 0
			p.Cased = f&8 != 0
			p.Space = f&16 != 0
			p.Alnum = f&32 != 0
			p.Bidi = bidis[r.u8()]
			tables.properties[i] = p
		}
		seqMap := func() map[rune][]rune {
			m := map[rune][]rune{}
			for n := r.u32(); n > 0; n-- {
				cp := rune(r.u32())
				s := make([]rune, int(r.u8()))
				for i := range s {
					s[i] = rune(r.u32())
				}
				m[cp] = s
			}
			return m
		}
		tables.lower = seqMap()
		tables.decomposition = seqMap()
		tables.composition = map[uint64]rune{}
		for n := r.u32(); n > 0; n-- {
			a, b, c := r.u32(), r.u32(), r.u32()
			tables.composition[uint64(a)<<32|uint64(b)] = rune(c)
		}
		rangeMap := func() map[string][]uint64 {
			m := map[string][]uint64{}
			for n := r.u32(); n > 0; n-- {
				name := r.text()
				rs := make([]uint64, int(r.u32()))
				for i := range rs {
					rs[i] = r.u64()
				}
				m[name] = rs
			}
			return m
		}
		tables.scripts = rangeMap()
		tables.joining = rangeMap()
		tables.classes = rangeMap()
		tables.uts = make([]utsRow, int(r.u32()))
		for i := range tables.uts {
			p := utsRow{Start: rune(r.u32()), Status: r.u8()}
			n := r.u8()
			if n != 255 {
				p.HasReplacement = true
				p.Replacement = make([]rune, int(n))
				for j := range p.Replacement {
					p.Replacement[j] = rune(r.u32())
				}
			}
			tables.uts[i] = p
		}
		if r.i != len(raw) {
			panic("urlprep trailing table data")
		}
	})
}
func property(cp rune) unicodeProperty {
	loadTables()
	i := sort.Search(len(tables.properties), func(i int) bool { return tables.properties[i].Start > cp }) - 1
	if i < 0 {
		return unicodeProperty{}
	}
	return tables.properties[i]
}
func inRanges(cp rune, rs []uint64) bool {
	i := sort.Search(len(rs), func(i int) bool { return uint32(rs[i]>>32) > uint32(cp) })
	return i > 0 && uint32(cp) < uint32(rs[i-1])
}
func pythonRunes(s string) ([]rune, error) {
	out := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c < 128 {
			out = append(out, rune(c))
			i++
			continue
		}
		if i+2 < len(s) && c == 0xED && s[i+1] >= 0xA0 && s[i+1] <= 0xBF && s[i+2] >= 0x80 && s[i+2] <= 0xBF {
			out = append(out, rune(c&15)<<12|rune(s[i+1]&63)<<6|rune(s[i+2]&63))
			i += 3
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			return nil, fail("UnicodeError", fmt.Sprintf("invalid WTF-8 byte at %d", i))
		}
		out = append(out, r)
		i += n
	}
	return out, nil
}
func pythonString(rs []rune) string {
	b := make([]byte, 0, len(rs))
	for _, r := range rs {
		if r >= 0xD800 && r <= 0xDFFF {
			b = append(b, byte(0xE0|r>>12), byte(0x80|(r>>6)&63), byte(0x80|r&63))
		} else {
			b = utf8.AppendRune(b, r)
		}
	}
	return string(b)
}
func pythonLower(s string) (string, error) {
	rs, e := pythonRunes(s)
	if e != nil {
		return "", e
	}
	loadTables()
	out := make([]rune, 0, len(rs))
	for i, r := range rs {
		if r == 0x3A3 {
			j := i - 1
			for j >= 0 && property(rs[j]).Ignorable {
				j--
			}
			final := j >= 0 && property(rs[j]).Cased
			if final {
				j = i + 1
				for j < len(rs) && property(rs[j]).Ignorable {
					j++
				}
				final = j == len(rs) || !property(rs[j]).Cased
			}
			if final {
				out = append(out, 0x3C2)
			} else {
				out = append(out, 0x3C3)
			}
		} else if l, ok := tables.lower[r]; ok {
			out = append(out, l...)
		} else {
			out = append(out, r)
		}
	}
	return pythonString(out), nil
}
func nfc(rs []rune) []rune {
	loadTables()
	out := make([]rune, 0, len(rs))
	var decompose func(rune)
	decompose = func(c rune) {
		if c >= 0xAC00 && c < 0xD7A4 {
			s := c - 0xAC00
			out = append(out, 0x1100+s/588, 0x1161+s%588/28)
			if s%28 != 0 {
				out = append(out, 0x11A7+s%28)
			}
			return
		}
		if ds, ok := tables.decomposition[c]; ok {
			for _, d := range ds {
				decompose(d)
			}
			return
		}
		out = append(out, c)
	}
	for _, r := range rs {
		decompose(r)
	}
	for i := 1; i < len(out); i++ {
		cc := property(out[i]).Combining
		if cc == 0 {
			continue
		}
		for j := i; j > 0 && property(out[j-1]).Combining > cc; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if len(out) == 0 {
		return out
	}
	composed := make([]rune, 1, len(out))
	composed[0] = out[0]
	starterPos := 0
	starter := out[0]
	lastClass := property(out[0]).Combining
	for _, c := range out[1:] {
		cc := property(c).Combining
		var composite rune
		ok := false
		if lastClass < cc || lastClass == 0 {
			if starter >= 0x1100 && starter <= 0x1112 && c >= 0x1161 && c <= 0x1175 {
				composite = 0xAC00 + (starter-0x1100)*588 + (c-0x1161)*28
				ok = true
			} else if starter >= 0xAC00 && starter < 0xD7A4 && (starter-0xAC00)%28 == 0 && c > 0x11A7 && c <= 0x11C2 {
				composite = starter + c - 0x11A7
				ok = true
			} else {
				composite, ok = tables.composition[uint64(starter)<<32|uint64(c)]
			}
		}
		if ok {
			composed[starterPos] = composite
			starter = composite
			continue
		}
		if cc == 0 {
			starterPos = len(composed)
			starter = c
		}
		lastClass = cc
		composed = append(composed, c)
	}
	return composed
}

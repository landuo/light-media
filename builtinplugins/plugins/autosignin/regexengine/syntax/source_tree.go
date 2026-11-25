package syntax

import (
	"bytes"
	"encoding/binary"
)

// Only the specialized Python frontend constructs these trusted private trees.
// These flags are not exposed through the plugin/host protocol or old parser.
const SourcePython RegexOptions = 0x1000
const SourceLookbehind RegexOptions = 0x2000

// SourceRuneSet receives normalized source Unicode15 ranges, never .NET class
// syntax. The returned set owns its immutable range backing array.
func SourceRuneSet(ranges []SingleRange, negate bool) *CharSet {
	set := &CharSet{ranges: append([]SingleRange(nil), ranges...), negate: negate}
	for _, r := range ranges {
		if r.First < 0 || r.Last > 0x10ffff || r.First > r.Last {
			panic("invalid source rune range")
		}
	}
	set.canonicalize()
	return set
}

// Source Python strings include surrogate codepoints. WriteRune would replace
// those values with U+FFFD, merging distinct source character sets. This hash
// is source-only; the existing .NET parser and its cache format stay unchanged.
func (c CharSet) sourceMapHashFill(buf *bytes.Buffer) {
	buf.WriteByte(b2i(c.negate) + b2i(c.anything)*2)
	_ = binary.Write(buf, binary.LittleEndian, uint32(len(c.ranges)))
	_ = binary.Write(buf, binary.LittleEndian, uint32(len(c.categories)))
	for _, r := range c.ranges {
		_ = binary.Write(buf, binary.LittleEndian, int32(r.First))
		_ = binary.Write(buf, binary.LittleEndian, int32(r.Last))
	}
	for _, ct := range c.categories {
		buf.WriteByte(b2i(ct.Negate))
		_ = binary.Write(buf, binary.LittleEndian, uint32(len(ct.Cat)))
		buf.WriteString(ct.Cat)
	}
	if c.sub != nil {
		buf.WriteByte(1)
		c.sub.sourceMapHashFill(buf)
	} else {
		buf.WriteByte(0)
	}
}

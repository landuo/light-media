package pythonre

// CapturedMatch is the selected CPython VM match. Offsets are Unicode str
// indices. Nil groups are unmatched, distinct from matching the empty string.
// BrushFlow consumes this for its fixed, nonempty source promotion patterns.
type CapturedMatch struct {
	Start, End int
	Before     string
	Groups     []*string
}

func (p *Pattern) SearchGroups(subject string, from int) (*CapturedMatch, error) {
	rs, e := DecodeSubject(subject)
	if e != nil {
		return nil, e
	}
	return rs.SearchGroups(p, from)
}

func (rs Subject) SearchGroups(p *Pattern, from int) (*CapturedMatch, error) {
	if p == nil {
		return nil, newError("TypeError", "nil pattern")
	}
	if from < 0 {
		from = 0
	}
	if from > len(rs) {
		return nil, nil
	}
	start, s, ok := p.search(rs, from)
	if !ok {
		return nil, nil
	}
	m := &CapturedMatch{Start: start, End: s.pos, Before: encodeString(rs[:start]), Groups: make([]*string, p.groups+1)}
	v := encodeString(rs[start:s.pos])
	m.Groups[0] = &v
	for g := 1; g <= p.groups; g++ {
		if groupExists(s.marks, g) {
			v := encodeString(rs[s.marks[(g-1)*2]:s.marks[(g-1)*2+1]])
			m.Groups[g] = &v
		}
	}
	return m, nil
}

// DecimalValue uses the same fixed CPython Unicode15 category table as \d.
// Contiguous decimal sets have ten digits; adjacent mathematical sets repeat.
func DecimalValue(c rune) (int, bool) {
	if !categoryMatches("d", c, false) {
		return 0, false
	}
	start := c
	for start > 0 && categoryMatches("d", start-1, false) {
		start--
	}
	return int(c-start) % 10, true
}

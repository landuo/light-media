package pythonre

func Compile(pattern string) (*Pattern, error) {
	rs, e := decodeString(pattern)
	if e != nil {
		return nil, e
	}
	p := &parser{depth: parserEntryFrames, s: rs, names: map[string]int{}, widths: []*width{nil}, lookbehind: -1, forward: map[int]bool{}}
	seq := p.parseAlt(0, false)
	if p.err != nil {
		return nil, p.err
	}
	if p.flags&flagA == 0 {
		p.flags |= flagU
	} else if p.flags&flagU != 0 {
		return nil, newError("ValueError", "ASCII and UNICODE flags are incompatible")
	}
	if p.peek() != "" {
		p.error("unbalanced parenthesis")
	}
	for g := range p.forward {
		if g >= len(p.widths) {
			p.error("invalid group reference")
		}
	}
	if p.err != nil {
		return nil, p.err
	}
	w := p.getwidth(seq)
	if e = compileNodes(p, seq, p.flags); e != nil {
		return nil, e
	}
	return &Pattern{code: seq, flags: p.flags, groups: len(p.widths) - 1, minimum: w.lo}, nil
}
func combinedFlags(parent, add, del int) int {
	if add&typeFlags != 0 {
		parent &^= typeFlags
	}
	return (parent | add) &^ del
}
func compileNodes(p *parser, seq []*node, flags int) error {
	for _, n := range seq {
		n.flags = flags
		switch n.op {
		case opClass:
			compileClass(n)
		case opGroup:
			if e := compileNodes(p, n.sub, combinedFlags(flags, n.add, n.del)); e != nil {
				return e
			}
		case opAtomic:
			if e := compileNodes(p, n.sub, flags); e != nil {
				return e
			}
		case opRepeat:
			if flags&flagT != 0 {
				return regexError("internal: unsupported template operator")
			}
			if e := compileNodes(p, n.sub, flags); e != nil {
				return e
			}
		case opAssert:
			if n.behind {
				w := p.getwidth(n.sub)
				if w.lo > 0xFFFFFFFF {
					return regexError("looks too much behind")
				}
				if w.lo != w.hi {
					return regexError("look-behind requires fixed-width pattern")
				}
				n.width = w.lo
			}
			if e := compileNodes(p, n.sub, flags); e != nil {
				return e
			}
		case opBranch:
			for _, s := range n.alts {
				if e := compileNodes(p, s, flags); e != nil {
					return e
				}
			}
		case opConditional:
			if e := compileNodes(p, n.sub, flags); e != nil {
				return e
			}
			if len(n.alts) > 0 {
				if e := compileNodes(p, n.alts[0], flags); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func compileClass(n *node) {
	if n.flags&flagI == 0 {
		n.tail = append([]classItem(nil), n.items...)
		return
	}
	ascii := n.flags&flagA != 0
	bits := make([]byte, 65536)
	hascased := false
	put := func(c rune) {
		c = simpleLower(c, ascii)
		if c < 65536 {
			bits[c] = 1
			if !ascii {
				for _, v := range extraCases[c] {
					if v < 65536 {
						bits[v] = 1
					}
				}
			}
		}
	}
	for _, item := range n.items {
		switch item.kind {
		case "literal":
			c := simpleLower(item.lo, ascii)
			if c < 65536 {
				put(item.lo)
			} else {
				n.tail = append(n.tail, classItem{kind: "literal", lo: c})
			}
			if isCased(c, ascii) {
				hascased = true
			}
		case "range":
			end := item.hi
			if end >= 65536 {
				end = 65535
			}
			for c := item.lo; c <= end; c++ {
				put(c)
				if isCased(c, ascii) {
					hascased = true
				}
			}
			if item.hi >= 65536 {
				kind := "range"
				if !ascii {
					kind = "rangeUniIgnore"
				}
				n.tail = append(n.tail, classItem{kind: kind, lo: item.lo, hi: item.hi})
				hascased = true
			}
		default:
			n.tail = append(n.tail, item)
		}
	}
	for i := 0; i < len(bits); {
		if bits[i] == 0 {
			i++
			continue
		}
		start := i
		for i < len(bits) && bits[i] != 0 {
			i++
		}
		n.compiled = append(n.compiled, codeRange{rune(start), rune(i - 1)})
	}
	n.fold = hascased
}
func literalMatches(c, target rune, flags int) bool {
	if flags&flagI == 0 || !isCased(target, flags&flagA != 0) {
		return c == target
	}
	ascii := flags&flagA != 0
	c = simpleLower(c, ascii)
	lo := simpleLower(target, ascii)
	if c == lo {
		return true
	}
	if !ascii {
		for _, v := range extraCases[lo] {
			if c == v {
				return true
			}
		}
	}
	return false
}
func categoryMatches(kind string, c rune, ascii bool) bool {
	negative := kind == "D" || kind == "S" || kind == "W"
	positive := false
	switch kind {
	case "d", "D":
		if ascii {
			positive = c >= '0' && c <= '9'
		} else {
			positive = unicodeFlags(c)&1 != 0
		}
	case "s", "S":
		if ascii {
			positive = c == ' ' || c >= '\t' && c <= '\r'
		} else {
			positive = unicodeFlags(c)&2 != 0
		}
	case "w", "W":
		if ascii {
			positive = c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		} else {
			positive = unicodeFlags(c)&4 != 0
		}
	}
	if negative {
		return !positive
	}
	return positive
}
func classMatches(n *node, c rune) bool {
	if n.fold {
		c = simpleLower(c, n.flags&flagA != 0)
	}
	ok := false
	for _, r := range n.compiled {
		if c >= r.lo && c <= r.hi {
			ok = true
			break
		}
	}
	if !ok {
		for _, item := range n.tail {
			switch item.kind {
			case "literal":
				ok = c == item.lo
			case "range":
				ok = c >= item.lo && c <= item.hi
			case "rangeUniIgnore":
				u := simpleUpper(c)
				ok = c >= item.lo && c <= item.hi || u >= item.lo && u <= item.hi
			default:
				ok = categoryMatches(item.kind, c, n.flags&flagA != 0)
			}
			if ok {
				break
			}
		}
	}
	if n.neg {
		return !ok
	}
	return ok
}

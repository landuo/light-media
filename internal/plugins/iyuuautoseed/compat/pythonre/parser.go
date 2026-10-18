package pythonre

import (
	"strconv"
	"strings"
)

type parser struct {
	depth      int
	s          []rune
	i          int
	err        error
	flags      int
	names      map[string]int
	widths     []*width
	lookbehind int
	forward    map[int]bool
}

func (p *parser) error(msg string) {
	if !p.pulse(2) {
		return
	}
	if p.err == nil {
		p.err = regexError(msg)
	}
}
func (p *parser) peek() string {
	if p.i >= len(p.s) {
		return ""
	}
	if p.s[p.i] == '\\' {
		if p.i+1 == len(p.s) {
			p.error("bad escape (end of pattern)")
			return ""
		}
		return encodeString(p.s[p.i : p.i+2])
	}
	return encodeString(p.s[p.i : p.i+1])
}
func (p *parser) pulse(n int) bool {
	if p.err != nil {
		return false
	}
	if p.depth+n > cpythonRecursionLimit {
		p.err = newError("RecursionError", "maximum recursion depth exceeded")
		return false
	}
	return true
}
func (p *parser) rawGet() string {
	if !p.pulse(1) {
		return ""
	}
	s := p.peek()
	if s != "" {
		if p.s[p.i] == '\\' {
			p.i += 2
		} else {
			p.i++
		}
		p.peek()
	}
	return s
}
func (p *parser) get() string {
	p.depth++
	defer func() { p.depth-- }()
	if !p.pulse(0) {
		return ""
	}
	return p.rawGet()
}
func (p *parser) match(s string) bool {
	p.depth++
	defer func() { p.depth-- }()
	if !p.pulse(0) {
		return false
	}
	if p.peek() == s {
		p.rawGet()
		return p.err == nil
	}
	return false
}
func (p *parser) whileChars(n int, set string) string {
	p.depth++
	defer func() { p.depth-- }()
	if !p.pulse(0) {
		return ""
	}

	var b strings.Builder
	for j := 0; j < n; j++ {
		s := p.peek()
		if len(s) != 1 || !strings.Contains(set, s) {
			break
		}
		b.WriteString(p.rawGet())
	}
	return b.String()
}
func (p *parser) until(end string) string {
	p.depth++
	defer func() { p.depth-- }()
	if !p.pulse(0) {
		return ""
	}

	var b strings.Builder
	for p.err == nil {
		t := p.rawGet()
		if t == "" {
			p.error("unterminated name")
			return ""
		}
		if t == end {
			if b.Len() == 0 {
				p.error("missing name")
			}
			return b.String()
		}
		b.WriteString(t)
	}
	return ""
}
func oneRune(s string) rune {
	r, _ := decodeString(s)
	if len(r) == 0 {
		return 0
	}
	return r[0]
}
func asciiDigit(s string) bool { return len(s) == 1 && s[0] >= '0' && s[0] <= '9' }
func octDigit(s string) bool   { return len(s) == 1 && s[0] >= '0' && s[0] <= '7' }
func asciiLetter(s string) bool {
	return len(s) == 1 && (s[0] >= 'a' && s[0] <= 'z' || s[0] >= 'A' && s[0] <= 'Z')
}
func (p *parser) validName(s string) bool {
	p.depth++
	defer func() { p.depth-- }()
	if !p.pulse(0) {
		return false
	}

	rs, e := decodeString(s)
	if e != nil || !isIdentifier(rs) {
		p.error("bad character in group name")
		return false
	}
	return true
}
func (p *parser) checkGroup(g int) bool {
	p.depth++
	defer func() { p.depth-- }()
	if !p.pulse(0) {
		return false
	}
	if !p.pulse(1) {
		return false
	}
	return g > 0 && g < len(p.widths) && p.widths[g] != nil
}
func (p *parser) checkBehind(g int) {
	p.depth++
	defer func() { p.depth-- }()
	if !p.pulse(0) {
		return
	}

	if p.lookbehind >= 0 {
		if !p.checkGroup(g) {
			p.error("cannot refer to an open group")
		} else if g >= p.lookbehind {
			p.error("cannot refer to group defined in same lookbehind")
		}
	}
}
func (p *parser) escape(t string, inClass bool) *node {
	p.depth++
	defer func() { p.depth-- }()
	if !p.pulse(0) {
		return nil
	}

	c := oneRune(t[1:])
	if !inClass {
		switch c {
		case 'A':
			return &node{op: opAnchor, anchor: "beginString"}
		case 'Z':
			return &node{op: opAnchor, anchor: "endString"}
		case 'b':
			return &node{op: opAnchor, anchor: "boundary"}
		case 'B':
			return &node{op: opAnchor, anchor: "nonBoundary"}
		}
	}
	switch c {
	case 'd', 'D', 's', 'S', 'w', 'W':
		return &node{op: opClass, items: []classItem{{kind: string(c)}}}
	case 'a':
		return &node{op: opLiteral, c: 7}
	case 'b':
		return &node{op: opLiteral, c: 8}
	case 'f':
		return &node{op: opLiteral, c: 12}
	case 'n':
		return &node{op: opLiteral, c: 10}
	case 'r':
		return &node{op: opLiteral, c: 13}
	case 't':
		return &node{op: opLiteral, c: 9}
	case 'v':
		return &node{op: opLiteral, c: 11}
	case '\\':
		return &node{op: opLiteral, c: '\\'}
	case 'x', 'u', 'U':
		n := 2
		if c == 'u' {
			n = 4
		} else if c == 'U' {
			n = 8
		}
		s := p.whileChars(n, "0123456789abcdefABCDEF")
		if len(s) != n {
			p.error("incomplete escape")
			return nil
		}
		v, _ := strconv.ParseUint(s, 16, 32)
		if v > 0x7FFFFFFF {
			p.err = newError("OverflowError", "Python int too large to convert to C int")
			return nil
		}
		if v > 0x10FFFF {
			p.error("bad escape codepoint")
			return nil
		}
		return &node{op: opLiteral, c: rune(v)}
	case 'N':
		if !p.match("{") {
			p.error("missing {")
			return nil
		}
		name := p.until("}")
		r, ok := namedRune(name)
		if !ok {
			p.error("undefined character name")
		}
		return &node{op: opLiteral, c: r}
	}
	if c >= '0' && c <= '9' {
		s := string(c)
		if inClass {
			if c > '7' {
				p.error("bad escape")
				return nil
			}
			s += p.whileChars(2, "01234567")
			v, _ := strconv.ParseUint(s, 8, 32)
			if v > 255 {
				p.error("octal escape out of range")
			}
			return &node{op: opLiteral, c: rune(v)}
		}
		if c == '0' {
			s += p.whileChars(2, "01234567")
			v, _ := strconv.ParseUint(s, 8, 32)
			return &node{op: opLiteral, c: rune(v)}
		}
		if asciiDigit(p.peek()) {
			s += p.get()
			if octDigit(s[:1]) && octDigit(s[1:]) && octDigit(p.peek()) {
				s += p.get()
				v, _ := strconv.ParseUint(s, 8, 32)
				if v > 255 {
					p.error("octal escape out of range")
				}
				return &node{op: opLiteral, c: rune(v)}
			}
		}
		g, _ := strconv.Atoi(s)
		if !p.checkGroup(g) {
			p.error("invalid or open group reference")
			return nil
		}
		p.checkBehind(g)
		return &node{op: opRef, group: g}
	}
	if asciiLetter(string(c)) {
		p.error("bad escape")
		return nil
	}
	return &node{op: opLiteral, c: c}
}
func (p *parser) class() *node {
	neg := p.match("^")
	items := []classItem{}
	for p.err == nil {
		t := p.get()
		if t == "" {
			p.error("unterminated character set")
			break
		}
		if t == "]" && len(items) > 0 {
			break
		}
		var a *node
		if strings.HasPrefix(t, "\\") {
			a = p.escape(t, true)
		} else {
			a = &node{op: opLiteral, c: oneRune(t)}
		}
		if p.err != nil || a == nil {
			break
		}
		if p.match("-") {
			u := p.get()
			if u == "" {
				p.error("unterminated character set")
				break
			}
			if u == "]" {
				if a.op == opClass {
					items = append(items, a.items...)
				} else {
					items = append(items, classItem{kind: "literal", lo: a.c})
				}
				items = append(items, classItem{kind: "literal", lo: '-'})
				break
			}
			var b *node
			if strings.HasPrefix(u, "\\") {
				b = p.escape(u, true)
			} else {
				b = &node{op: opLiteral, c: oneRune(u)}
			}
			if b == nil || a.op != opLiteral || b.op != opLiteral || b.c < a.c {
				p.error("bad character range")
				break
			}
			items = append(items, classItem{kind: "range", lo: a.c, hi: b.c})
		} else if a.op == opClass {
			items = append(items, a.items...)
		} else {
			items = append(items, classItem{kind: "literal", lo: a.c})
		}
	}
	if len(items) == 1 && items[0].kind == "literal" {
		kind := opLiteral
		if neg {
			kind = opNotLiteral
		}
		return &node{op: kind, c: items[0].lo}
	}
	return &node{op: opClass, items: items, neg: neg}
}
func (p *parser) count(s string) (uint64, bool) {
	if len(s) > 4300 {
		p.err = newError("ValueError", "integer string conversion limit")
		return 0, false
	}
	trim := strings.TrimLeft(s, "0")
	if trim == "" {
		return 0, true
	}
	if len(trim) > 10 {
		p.err = newError("OverflowError", "the repetition number is too large")
		return 0, false
	}
	v, _ := strconv.ParseUint(trim, 10, 64)
	if v >= maxRepeat {
		p.err = newError("OverflowError", "the repetition number is too large")
		return 0, false
	}
	return v, true
}
func (p *parser) quantify(out []*node, t string) []*node {
	lo, hi := uint64(0), maxRepeat
	saved := p.i
	switch t {
	case "?":
		hi = 1
	case "+":
		lo = 1
	case "{":
		if p.peek() == "}" {
			return append(out, &node{op: opLiteral, c: '{'})
		}
		a := p.whileChars(len(p.s), "0123456789")
		b := a
		if p.match(",") {
			b = p.whileChars(len(p.s), "0123456789")
		}
		if !p.match("}") {
			p.i = saved
			return append(out, &node{op: opLiteral, c: '{'})
		}
		if a != "" {
			var ok bool
			lo, ok = p.count(a)
			if !ok {
				return out
			}
		}
		if b != "" {
			var ok bool
			hi, ok = p.count(b)
			if !ok {
				return out
			}
			if hi < lo {
				p.error("min repeat greater than max repeat")
				return out
			}
		}
	}
	if len(out) == 0 || out[len(out)-1].op == opAnchor {
		p.error("nothing to repeat")
		return out
	}
	if !p.pulse(2) {
		return out
	}
	last := out[len(out)-1]
	if last.op == opRepeat {
		p.error("multiple repeat")
		return out
	}
	sub := []*node{last}
	if last.op == opGroup && last.group == 0 && last.add == 0 && last.del == 0 {
		sub = last.sub
	}
	n := &node{op: opRepeat, sub: sub, min: lo, max: hi}
	if p.match("?") {
		n.lazy = true
	} else if p.match("+") {
		n.possessive = true
	}
	out[len(out)-1] = n
	return out
}
func flag(c string) int {
	switch c {
	case "i":
		return flagI
	case "L":
		return flagL
	case "m":
		return flagM
	case "s":
		return flagS
	case "x":
		return flagX
	case "a":
		return flagA
	case "t":
		return flagT
	case "u":
		return flagU
	}
	return 0
}
func (p *parser) parseFlags(c string) (add, del int, global bool) {
	p.depth++
	defer func() { p.depth-- }()
	if !p.pulse(0) {
		return
	}

	if c != "-" {
		for {
			f := flag(c)
			if c == "L" {
				p.error("cannot use L flag with str")
				return
			}
			add |= f
			if f&typeFlags != 0 && add&typeFlags != f {
				p.error("incompatible type flags")
				return
			}
			c = p.get()
			if c == "" {
				p.error("missing -, : or )")
				return
			}
			if c == ")" || c == "-" || c == ":" {
				break
			}
			if flag(c) == 0 {
				p.error("unknown flag")
				return
			}
		}
	}
	if c == ")" {
		p.flags |= add
		return add, 0, true
	}
	if add&flagT != 0 {
		p.error("cannot turn on global flag")
		return
	}
	if c == "-" {
		c = p.get()
		if c == "" || flag(c) == 0 {
			p.error("missing flag")
			return
		}
		for {
			f := flag(c)
			if f&typeFlags != 0 {
				p.error("cannot turn off type flags")
				return
			}
			del |= f
			c = p.get()
			if c == ":" {
				break
			}
			if c == "" || flag(c) == 0 {
				p.error("missing :")
				return
			}
		}
	}
	if c != ":" || del&flagT != 0 || add&del != 0 {
		p.error("bad inline flags")
	}
	return
}
func (p *parser) parseAlt(nested int, verbose bool) []*node {
	p.depth++
	defer func() { p.depth-- }()
	if !p.pulse(0) {
		return nil
	}

	alts := [][]*node{}
	for p.err == nil {
		alts = append(alts, p.parseSeq(nested+1, verbose, nested == 0 && len(alts) == 0))
		if !p.match("|") {
			break
		}
		if nested == 0 {
			verbose = p.flags&flagX != 0
		}
	}
	if len(alts) == 1 {
		return alts[0]
	}
	return []*node{{op: opBranch, alts: alts}}
}
func (p *parser) parseSeq(nested int, verbose, first bool) []*node {
	p.depth++
	defer func() { p.depth-- }()
	if !p.pulse(0) {
		return nil
	}

	if !p.pulse(1) {
		return nil
	}
	out := []*node{}
	for p.err == nil {
		t := p.peek()
		if t == "" || t == "|" || t == ")" {
			break
		}
		p.get()
		if verbose {
			if len(t) == 1 && strings.Contains(" \t\n\r\v\f", t) {
				continue
			}
			if t == "#" {
				for {
					c := p.get()
					if c == "" || c == "\n" {
						break
					}
				}
				continue
			}
		}
		if strings.HasPrefix(t, "\\") {
			n := p.escape(t, false)
			if n != nil {
				out = append(out, n)
			}
			continue
		}
		switch t {
		case "[":
			out = append(out, p.class())
		case "?", "*", "+", "{":
			out = p.quantify(out, t)
		case ".":
			out = append(out, &node{op: opAny})
		case "^":
			out = append(out, &node{op: opAnchor, anchor: "begin"})
		case "$":
			out = append(out, &node{op: opAnchor, anchor: "end"})
		case "(":
			capture, atomic := true, false
			name := ""
			add, del := 0, 0
			if p.match("?") {
				c := p.get()
				switch c {
				case "P":
					if p.match("<") {
						name = p.until(">")
						p.validName(name)
					} else if p.match("=") {
						name = p.until(")")
						p.validName(name)
						g, ok := p.names[name]
						if !ok || !p.checkGroup(g) {
							p.error("unknown or open group name")
						}
						p.checkBehind(g)
						out = append(out, &node{op: opRef, group: g})
						continue
					} else {
						p.error("unknown extension ?P")
					}
				case ":":
					capture = false
				case "#":
					for p.err == nil && !p.match(")") {
						if p.get() == "" {
							p.error("unterminated comment")
						}
					}
					continue
				case "=", "!", "<":
					behind := false
					if c == "<" {
						behind = true
						c = p.get()
						if c != "=" && c != "!" {
							p.error("unknown extension ?<")
						}
					}
					old := p.lookbehind
					if behind && old < 0 {
						p.lookbehind = len(p.widths)
					}
					sub := p.parseAlt(nested+1, verbose)
					if behind {
						p.lookbehind = old
					}
					if !p.match(")") {
						p.error("unterminated subpattern")
					}
					out = append(out, &node{op: opAssert, sub: sub, behind: behind, neg: c == "!"})
					continue
				case "(":
					condition := p.until(")")
					g := 0
					allDec := len(condition) > 0
					for _, r := range condition {
						if r < '0' || r > '9' {
							allDec = false
						}
					}
					if allDec {
						if len(condition) > 4300 {
							p.err = newError("ValueError", "integer string conversion limit")
							break
						}
						v, e := strconv.ParseUint(strings.TrimLeft(condition, "0"), 10, 64)
						if e != nil && strings.TrimLeft(condition, "0") != "" || v == 0 || v >= maxGroups {
							p.error("bad group reference")
						} else {
							g = int(v)
							p.forward[g] = true
						}
					} else {
						p.validName(condition)
						var ok bool
						g, ok = p.names[condition]
						if !ok {
							p.error("unknown group name")
						}
					}
					p.checkBehind(g)
					yes := p.parseSeq(nested+1, verbose, false)
					var no []*node
					if p.match("|") {
						no = p.parseSeq(nested+1, verbose, false)
						if p.peek() == "|" {
							p.error("conditional more than two branches")
						}
					}
					if !p.match(")") {
						p.error("unterminated conditional")
					}
					out = append(out, &node{op: opConditional, group: g, sub: yes, alts: [][]*node{no}})
					continue
				case ">":
					capture = false
					atomic = true
				default:
					if flag(c) != 0 || c == "-" {
						var global bool
						add, del, global = p.parseFlags(c)
						if global {
							if !first || len(out) > 0 {
								p.error("global flags not at start")
							}
							verbose = p.flags&flagX != 0
							continue
						}
						capture = false
					} else {
						p.error("unknown extension")
					}
				}
			}
			g := 0
			if capture && p.err == nil {
				if !p.pulse(2) {
					return out
				}
				g = len(p.widths)
				p.widths = append(p.widths, nil)
				if g+1 > maxGroups {
					p.error("too many groups")
				}
				if name != "" {
					if _, ok := p.names[name]; ok {
						p.error("redefinition of group name")
					}
					p.names[name] = g
				}
			}
			subverbose := (verbose || add&flagX != 0) && del&flagX == 0
			sub := p.parseAlt(nested+1, subverbose)
			if !p.match(")") {
				p.error("unterminated subpattern")
			}
			if g > 0 {
				if !p.pulse(2) {
					return out
				}
				w := p.getwidth(sub)
				p.widths[g] = &w
			}
			kind := opGroup
			if atomic {
				kind = opAtomic
			}
			out = append(out, &node{op: kind, sub: sub, group: g, add: add, del: del})
		default:
			out = append(out, &node{op: opLiteral, c: oneRune(t)})
		}
	}
	flattened := []*node{}
	for _, n := range out {
		if n != nil && n.op == opGroup && n.group == 0 && n.add == 0 && n.del == 0 {
			flattened = append(flattened, n.sub...)
		} else if n != nil {
			flattened = append(flattened, n)
		}
	}
	return flattened
}
func addWidth(a, b uint64) uint64 {
	if maxWidth-a < b {
		return maxWidth
	}
	return a + b
}
func mulWidth(a, b uint64) uint64 {
	if b != 0 && a > maxWidth/b {
		return maxWidth
	}
	return a * b
}
func (p *parser) getwidth(seq []*node) width {
	w := width{}
	for _, n := range seq {
		v := width{}
		switch n.op {
		case opLiteral, opNotLiteral, opClass, opAny:
			v = width{1, 1}
		case opGroup, opAtomic:
			v = p.getwidth(n.sub)
		case opBranch:
			v.lo = maxWidth
			for _, s := range n.alts {
				x := p.getwidth(s)
				if x.lo < v.lo {
					v.lo = x.lo
				}
				if x.hi > v.hi {
					v.hi = x.hi
				}
			}
		case opRepeat:
			x := p.getwidth(n.sub)
			v.lo = mulWidth(x.lo, n.min)
			if n.max == maxRepeat && x.hi > 0 {
				v.hi = maxWidth
			} else {
				v.hi = mulWidth(x.hi, n.max)
			}
		case opRef:
			if n.group < len(p.widths) && p.widths[n.group] != nil {
				v = *p.widths[n.group]
			}
		case opConditional:
			v = p.getwidth(n.sub)
			if len(n.alts) > 0 && n.alts[0] != nil {
				x := p.getwidth(n.alts[0])
				if x.lo < v.lo {
					v.lo = x.lo
				}
				if x.hi > v.hi {
					v.hi = x.hi
				}
			} else {
				v.lo = 0
			}
		}
		w.lo = addWidth(w.lo, v.lo)
		w.hi = addWidth(w.hi, v.hi)
	}
	return w
}

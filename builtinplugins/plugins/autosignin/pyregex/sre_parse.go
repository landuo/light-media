package pyregex

import (
	"fmt"
	"strings"
)

func parse(t *tokenizer, s *State, verbose bool, nested int, first bool) *Pattern {
	p := &Pattern{State: s}
	for {
		this := t.next
		if this == "" || this == "|" || this == ")" {
			break
		}
		t.get()
		if verbose {
			if singleIn(this, whitespace) {
				continue
			}
			if this == "#" {
				for {
					c := t.get()
					if c == "" || c == "\n" {
						break
					}
				}
				continue
			}
		}
		if strings.HasPrefix(this, `\`) {
			p.Nodes = append(p.Nodes, escape(t, this, s, false))
			continue
		}
		if !singleIn(this, ".\\[{()*+?^$|") {
			p.Nodes = append(p.Nodes, literal(sourceRunes(this)[0]))
			continue
		}
		switch this {
		case "[":
			p.Nodes = append(p.Nodes, parseClass(t, s))
		case "?", "*", "+", "{":
			here := t.tell()
			var lo, hi uint64
			switch this {
			case "?":
				lo, hi = 0, 1
			case "*":
				lo, hi = 0, MaxRepeat
			case "+":
				lo, hi = 1, MaxRepeat
			case "{":
				if t.next == "}" {
					p.Nodes = append(p.Nodes, literal('{'))
					continue
				}
				lo, hi = 0, MaxRepeat
				low, high := "", ""
				for singleIn(t.next, digits) {
					low += t.get()
				}
				if t.match(",") {
					for singleIn(t.next, digits) {
						high += t.get()
					}
				} else {
					high = low
				}
				if !t.match("}") {
					p.Nodes = append(p.Nodes, literal('{'))
					t.seek(here)
					continue
				}
				if low != "" {
					lo = parseUint(low, 10)
					if lo >= MaxRepeat {
						raise("OverflowError", "the repetition number is too large", s.Pattern, -1)
					}
				}
				if high != "" {
					hi = parseUint(high, 10)
					if hi >= MaxRepeat {
						raise("OverflowError", "the repetition number is too large", s.Pattern, -1)
					}
					if hi < lo {
						t.fail("min repeat greater than max repeat", t.tell()-here)
					}
				}
			}
			if len(p.Nodes) == 0 || p.Nodes[len(p.Nodes)-1].Op == At {
				t.fail("nothing to repeat", t.tell()-here+len(sourceRunes(this)))
			}
			prev := p.Nodes[len(p.Nodes)-1]
			if prev.Op == MinRep || prev.Op == MaxRep || prev.Op == PossessiveRep {
				t.fail("multiple repeat", t.tell()-here+len(sourceRunes(this)))
			}
			item := &Pattern{State: s, Nodes: []Node{prev}}
			if prev.Op == Subpattern && prev.Group < 0 && prev.AddFlags == 0 && prev.DelFlags == 0 {
				item = prev.Children[0]
			}
			op := MaxRep
			if t.match("?") {
				op = MinRep
			} else if t.match("+") {
				op = PossessiveRep
			}
			p.Nodes[len(p.Nodes)-1] = Node{Op: op, Min: lo, Max: hi, Children: []*Pattern{item}}
		case ".":
			p.Nodes = append(p.Nodes, Node{Op: Any})
		case "(":
			start := t.tell() - 1
			capture, atomic := true, false
			name := ""
			add, del := 0, 0
			if t.match("?") {
				char := t.get()
				if char == "" {
					t.fail("unexpected end of pattern", 0)
				}
				switch char {
				case "P":
					if t.match("<") {
						name = t.until(">", "group name")
						t.checkName(name, 1)
					} else if t.match("=") {
						name = t.until(")", "group name")
						t.checkName(name, 1)
						id, exists := s.GroupDict[name]
						if !exists {
							t.fail("unknown group name "+repr(name), len(sourceRunes(name))+1)
						}
						if !s.checkGroup(id) {
							t.fail("cannot refer to an open group", len(sourceRunes(name))+1)
						}
						s.checkLookbehind(id, t)
						p.Nodes = append(p.Nodes, Node{Op: GroupRef, Group: id})
						continue
					} else {
						char = t.get()
						if char == "" {
							t.fail("unexpected end of pattern", 0)
						}
						t.fail("unknown extension ?P"+char, len(sourceRunes(char))+2)
					}
				case ":":
					capture = false
				case "#":
					for {
						if t.next == "" {
							t.fail("missing ), unterminated comment", t.tell()-start)
						}
						if t.get() == ")" {
							break
						}
					}
					continue
				case "=", "!", "<":
					direction := 1
					old := s.LookbehindGroups
					if char == "<" {
						char = t.get()
						if char == "" {
							t.fail("unexpected end of pattern", 0)
						}
						if char != "=" && char != "!" {
							t.fail("unknown extension ?<"+char, len(sourceRunes(char))+2)
						}
						direction = -1
						if old == nil {
							n := len(s.GroupWidths)
							s.LookbehindGroups = &n
						}
					}
					body := parseSub(t, s, verbose, nested+1)
					if direction < 0 && old == nil {
						s.LookbehindGroups = nil
					}
					if !t.match(")") {
						t.fail("missing ), unterminated subpattern", t.tell()-start)
					}
					op := Assert
					if char == "!" {
						op = AssertNot
					}
					p.Nodes = append(p.Nodes, Node{Op: op, Direction: direction, Children: []*Pattern{body}})
					continue
				case "(":
					condname := t.until(")", "group name")
					numeric := condname != ""
					for _, c := range condname {
						numeric = numeric && c >= '0' && c <= '9'
					}
					group := 0
					if !numeric {
						t.checkName(condname, 1)
						var exists bool
						group, exists = s.GroupDict[condname]
						if !exists {
							t.fail("unknown group name "+repr(condname), len(sourceRunes(condname))+1)
						}
					} else {
						value := parseUint(condname, 10)
						if value == 0 {
							t.fail("bad group number", len(sourceRunes(condname))+1)
						}
						if value >= MaxGroups {
							normalized := strings.TrimLeft(condname, "0")
							t.fail("invalid group reference "+normalized, len(sourceRunes(condname))+1)
						}
						group = int(value)
						if _, exists := s.GroupRefPos[group]; !exists {
							s.GroupRefPos[group] = t.tell() - len(sourceRunes(condname)) - 1
							s.GroupRefOrder = append(s.GroupRefOrder, group)
						}
					}
					s.checkLookbehind(group, t)
					yes := parse(t, s, verbose, nested+1, false)
					var no *Pattern
					if t.match("|") {
						no = parse(t, s, verbose, nested+1, false)
						if t.next == "|" {
							t.fail("conditional backref with more than two branches", 0)
						}
					}
					if !t.match(")") {
						t.fail("missing ), unterminated subpattern", t.tell()-start)
					}
					p.Nodes = append(p.Nodes, Node{Op: GroupRefExists, Group: group, Children: []*Pattern{yes, no}})
					continue
				case ">":
					capture, atomic = false, true
				default:
					if _, ok := flags[char]; ok || char == "-" {
						var global bool
						add, del, global = parseFlags(t, s, char)
						if global {
							if !first || len(p.Nodes) > 0 {
								t.fail("global flags not at the start of the expression", t.tell()-start)
							}
							verbose = s.Flags&Verbose != 0
							continue
						}
						capture = false
					} else {
						t.fail("unknown extension ?"+char, len(sourceRunes(char))+1)
					}
				}
			}
			group := -1
			if capture {
				group = s.openGroup(name, t)
			}
			subVerbose := (verbose || add&Verbose != 0) && del&Verbose == 0
			body := parseSub(t, s, subVerbose, nested+1)
			if !t.match(")") {
				t.fail("missing ), unterminated subpattern", t.tell()-start)
			}
			if group >= 0 {
				w := body.Width()
				s.GroupWidths[group] = &w
			}
			if atomic {
				p.Nodes = append(p.Nodes, Node{Op: AtomicGroup, Children: []*Pattern{body}})
			} else {
				p.Nodes = append(p.Nodes, Node{Op: Subpattern, Group: group, AddFlags: add, DelFlags: del, Children: []*Pattern{body}})
			}
		case "^":
			p.Nodes = append(p.Nodes, Node{Op: At, Anchor: "AT_BEGINNING"})
		case "$":
			p.Nodes = append(p.Nodes, Node{Op: At, Anchor: "AT_END"})
		default:
			panic(fmt.Sprintf("unhandled source special character %s", this))
		}
	}
	for i := len(p.Nodes) - 1; i >= 0; i-- {
		n := p.Nodes[i]
		if n.Op == Subpattern && n.Group < 0 && n.AddFlags == 0 && n.DelFlags == 0 {
			expanded := append([]Node(nil), p.Nodes[:i]...)
			expanded = append(expanded, n.Children[0].Nodes...)
			expanded = append(expanded, p.Nodes[i+1:]...)
			p.Nodes = expanded
		}
	}
	return p
}

// CheckCompile preserves the original compiler stage, after parsing/width
// assignment. No backend parser supplies source legality or error metadata.
func CheckCompile(p *Pattern) (err error) {
	defer func() {
		if v := recover(); v != nil {
			if e, ok := v.(*Error); ok {
				err = e
			} else {
				panic(v)
			}
		}
	}()
	var visit func(*Pattern, int)
	visit = func(part *Pattern, flags int) {
		for _, n := range part.Nodes {
			next := flags
			if n.Op == Subpattern {
				if n.AddFlags&typeFlags != 0 {
					next &^= typeFlags
				}
				next = (next | n.AddFlags) &^ n.DelFlags
			}
			if (n.Op == MaxRep || n.Op == MinRep || n.Op == PossessiveRep) && flags&Template != 0 {
				raise("error", "internal: unsupported template operator "+string(n.Op), p.State.Pattern, -1)
			}
			if (n.Op == Assert || n.Op == AssertNot) && n.Direction < 0 {
				w := n.Children[0].Width()
				if w.Lo.Saturated || w.Lo.N > MaxCode {
					raise("error", "looks too much behind", p.State.Pattern, -1)
				}
				if w.Lo != w.Hi {
					raise("error", "look-behind requires fixed-width pattern", p.State.Pattern, -1)
				}
			}
			for _, child := range n.Children {
				if child != nil {
					visit(child, next)
				}
			}
		}
	}
	visit(p, p.State.Flags)
	return nil
}

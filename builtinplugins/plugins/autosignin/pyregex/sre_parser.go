package pyregex

import (
	"fmt"
	"reflect"
	"strings"
)

var flags = map[string]int{"i": IgnoreCase, "L": Locale, "m": Multiline, "s": DotAll, "x": Verbose, "a": ASCII, "t": Template, "u": Unicode}

func Parse(pattern string, initialFlags int) (*Pattern, error) {
	return parseSourceWithWarnings(pattern, initialFlags, nil)
}

func parseSourceWithWarnings(pattern string, initialFlags int, sink func(Warning)) (result *Pattern, err error) {
	defer func() {
		if p := recover(); p != nil {
			if e, ok := p.(*Error); ok {
				err = e
				result = nil
			} else {
				panic(p)
			}
		}
	}()
	source := newTokenizer(pattern)
	state := &State{Flags: initialFlags, GroupDict: map[string]int{}, GroupWidths: []*Width{nil}, GroupNames: []string{""}, GroupRefPos: map[int]int{}, Pattern: pattern, warningSink: sink}
	p := parseSub(source, state, initialFlags&Verbose != 0, 0)
	if state.Flags&Locale != 0 {
		raise("ValueError", "cannot use LOCALE flag with a str pattern", pattern, -1)
	}
	if state.Flags&ASCII == 0 {
		state.Flags |= Unicode
	} else if state.Flags&Unicode != 0 {
		raise("ValueError", "ASCII and UNICODE flags are incompatible", pattern, -1)
	}
	if source.next != "" {
		source.fail("unbalanced parenthesis", 0)
	}
	for _, g := range state.GroupRefOrder {
		if g >= len(state.GroupWidths) {
			raise("error", fmt.Sprintf("invalid group reference %d", g), pattern, state.GroupRefPos[g])
		}
	}
	return p, nil
}
func parseSub(t *tokenizer, s *State, verbose bool, nested int) *Pattern {
	items := []*Pattern{}
	for {
		items = append(items, parse(t, s, verbose, nested+1, nested == 0 && len(items) == 0))
		if !t.match("|") {
			break
		}
		if nested == 0 {
			verbose = s.Flags&Verbose != 0
		}
	}
	if len(items) == 1 {
		return items[0]
	}
	p := &Pattern{State: s}
	for {
		var first *Node
		equal := true
		for _, item := range items {
			if len(item.Nodes) == 0 {
				equal = false
				break
			}
			if first == nil {
				x := item.Nodes[0]
				first = &x
			} else if !nodeEqual(item.Nodes[0], *first) {
				equal = false
				break
			}
		}
		if !equal {
			break
		}
		for _, item := range items {
			item.Nodes = item.Nodes[1:]
		}
		p.Nodes = append(p.Nodes, *first)
	}
	set := []ClassItem{}
	canSet := true
	for _, item := range items {
		if len(item.Nodes) != 1 {
			canSet = false
			break
		}
		n := item.Nodes[0]
		if n.Op == Literal {
			set = append(set, classItem(n))
		} else if n.Op == In && (len(n.Set) == 0 || n.Set[0].Op != Negate) {
			set = append(set, n.Set...)
		} else {
			canSet = false
			break
		}
	}
	if canSet {
		p.Nodes = append(p.Nodes, Node{Op: In, Set: uniqueSet(set)})
	} else {
		p.Nodes = append(p.Nodes, Node{Op: Branch, Children: items})
	}
	return p
}
func nodeEqual(a, b Node) bool {
	// Source tuple equality descends into lists, but SubPattern has no __eq__.
	// Its child patterns therefore compare by object identity, not their AST.
	if a.Op != b.Op || a.Rune != b.Rune || a.Anchor != b.Anchor || a.Group != b.Group || a.AddFlags != b.AddFlags || a.DelFlags != b.DelFlags || a.Min != b.Min || a.Max != b.Max || a.Direction != b.Direction || !reflect.DeepEqual(a.Set, b.Set) || len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Children {
		if a.Children[i] != b.Children[i] {
			return false
		}
	}
	return true
}
func uniqueSet(input []ClassItem) []ClassItem {
	seen := map[ClassItem]bool{}
	out := []ClassItem{}
	for _, item := range input {
		if !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}
func parseClass(t *tokenizer, s *State) Node {
	here := t.tell() - 1
	set := []ClassItem{}
	if t.next == "[" {
		s.warn(fmt.Sprintf("Possible nested set at position %d", t.tell()))
	}
	negate := t.match("^")
	for {
		this := t.get()
		if this == "" {
			t.fail("unterminated character set", t.tell()-here)
		}
		if this == "]" && len(set) > 0 {
			break
		}
		var one Node
		if strings.HasPrefix(this, `\`) {
			one = escape(t, this, s, true)
		} else {
			if len(set) > 0 && singleIn(this, "-&~|") && t.next == this {
				what := map[string]string{"-": "difference", "&": "intersection", "~": "symmetric difference", "|": "union"}[this]
				s.warn(fmt.Sprintf("Possible set %s at position %d", what, t.tell()-1))
			}
			one = literal(sourceRunes(this)[0])
		}
		if t.match("-") {
			that := t.get()
			if that == "" {
				t.fail("unterminated character set", t.tell()-here)
			}
			if that == "]" {
				set = append(set, classItem(one), ClassItem{Op: Literal, Rune: '-'})
				break
			}
			var two Node
			if strings.HasPrefix(that, `\`) {
				two = escape(t, that, s, true)
			} else {
				if that == "-" {
					s.warn(fmt.Sprintf("Possible set difference at position %d", t.tell()-2))
				}
				two = literal(sourceRunes(that)[0])
			}
			if one.Op != Literal || two.Op != Literal || two.Rune < one.Rune {
				t.fail("bad character range "+this+"-"+that, len(sourceRunes(this))+1+len(sourceRunes(that)))
			}
			set = append(set, ClassItem{Op: Range, Rune: one.Rune, High: two.Rune})
		} else {
			set = append(set, classItem(one))
		}
	}
	set = uniqueSet(set)
	if len(set) == 1 && set[0].Op == Literal {
		n := literal(set[0].Rune)
		if negate {
			n.Op = NotLiteral
		}
		return n
	}
	if negate {
		set = append([]ClassItem{{Op: Negate}}, set...)
	}
	return Node{Op: In, Set: set}
}
func parseFlags(t *tokenizer, s *State, char string) (add, del int, global bool) {
	if char != "-" {
		for {
			flag := flags[char]
			if char == "L" {
				t.fail("bad inline flags: cannot use 'L' flag with a str pattern", 0)
			}
			add |= flag
			if flag&typeFlags != 0 && add&typeFlags != flag {
				t.fail("bad inline flags: flags 'a', 'u' and 'L' are incompatible", 0)
			}
			char = t.get()
			if char == "" {
				t.fail("missing -, : or )", 0)
			}
			if singleIn(char, ")-:") {
				break
			}
			if _, ok := flags[char]; !ok {
				message := "missing -, : or )"
				if inRanges(sourceRunes(char)[0], tables.Alpha) {
					message = "unknown flag"
				}
				t.fail(message, len(sourceRunes(char)))
			}
		}
	}
	if char == ")" {
		s.Flags |= add
		return add, del, true
	}
	if add&globalFlags != 0 {
		t.fail("bad inline flags: cannot turn on global flag", 1)
	}
	if char == "-" {
		char = t.get()
		if char == "" {
			t.fail("missing flag", 0)
		}
		if _, ok := flags[char]; !ok {
			message := "missing flag"
			if inRanges(sourceRunes(char)[0], tables.Alpha) {
				message = "unknown flag"
			}
			t.fail(message, len(sourceRunes(char)))
		}
		for {
			flag := flags[char]
			if flag&typeFlags != 0 {
				t.fail("bad inline flags: cannot turn off flags 'a', 'u' and 'L'", 0)
			}
			del |= flag
			char = t.get()
			if char == "" {
				t.fail("missing :", 0)
			}
			if char == ":" {
				break
			}
			if _, ok := flags[char]; !ok {
				message := "missing :"
				if inRanges(sourceRunes(char)[0], tables.Alpha) {
					message = "unknown flag"
				}
				t.fail(message, len(sourceRunes(char)))
			}
		}
	}
	if del&globalFlags != 0 {
		t.fail("bad inline flags: cannot turn off global flag", 1)
	}
	if add&del != 0 {
		t.fail("bad inline flags: flag turned on and off", 1)
	}
	return
}

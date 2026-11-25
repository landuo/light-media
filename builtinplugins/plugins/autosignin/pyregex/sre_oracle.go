package pyregex

import "strconv"

// Oracle exposes the original parser's pure intermediate representation for
// source differential tests. It is not a plugin/host protocol.
func (p *Pattern) AST() []any {
	out := []any{}
	for _, n := range p.Nodes {
		var arg any
		switch n.Op {
		case Literal, NotLiteral:
			arg = n.Rune
		case In:
			items := []any{}
			for _, item := range n.Set {
				var v any
				switch item.Op {
				case Literal:
					v = item.Rune
				case Range:
					v = []rune{item.Rune, item.High}
				case Category:
					v = item.Category
				}
				items = append(items, []any{item.Op, v})
			}
			arg = items
		case At:
			arg = n.Anchor
		case Branch:
			children := []any{}
			for _, child := range n.Children {
				children = append(children, child.AST())
			}
			arg = []any{nil, children}
		case Subpattern:
			var group any
			if n.Group >= 0 {
				group = n.Group
			}
			arg = []any{group, n.AddFlags, n.DelFlags, n.Children[0].AST()}
		case Assert, AssertNot:
			arg = []any{n.Direction, n.Children[0].AST()}
		case AtomicGroup:
			arg = n.Children[0].AST()
		case GroupRef:
			arg = n.Group
		case GroupRefExists:
			var no any
			if len(n.Children) > 1 && n.Children[1] != nil {
				no = n.Children[1].AST()
			}
			arg = []any{n.Group, n.Children[0].AST(), no}
		case MaxRep, MinRep, PossessiveRep:
			var maximum any = n.Max
			if n.Max == MaxRepeat {
				maximum = "MAXREPEAT"
			}
			arg = []any{n.Min, maximum, n.Children[0].AST()}
		}
		out = append(out, []any{n.Op, arg})
	}
	return out
}
func (p *Pattern) Oracle() map[string]any {
	widths := []any{}
	for _, w := range p.State.GroupWidths {
		if w == nil {
			widths = append(widths, nil)
		} else {
			widths = append(widths, []any{w.Lo.JSON(), w.Hi.JSON()})
		}
	}
	refs := map[string]int{}
	for k, v := range p.State.GroupRefPos {
		refs[strconv.Itoa(k)] = v
	}
	width := p.Width()
	return map[string]any{"ast": p.AST(), "flags": p.State.Flags, "groups": len(p.State.GroupWidths), "groupdict": p.State.GroupDict, "groupwidths": widths, "width": []any{width.Lo.JSON(), width.Hi.JSON()}, "lookbehindgroups": p.State.LookbehindGroups, "grouprefpos": refs}
}

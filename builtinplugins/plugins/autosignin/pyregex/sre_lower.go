package pyregex

import (
	"math"
	"strconv"

	"light-media/builtinplugins/plugins/autosignin/regexengine"
	"light-media/builtinplugins/plugins/autosignin/regexengine/syntax"
)

func treeNode(op syntax.NodeType, children ...*syntax.RegexNode) *syntax.RegexNode {
	node := &syntax.RegexNode{T: op, Children: children}
	for _, child := range children {
		child.Parent = node
	}
	return node
}
func treePattern(p *Pattern, flags int) *syntax.RegexNode {
	children := []*syntax.RegexNode{}
	for _, n := range p.Nodes {
		children = append(children, treeOperation(n, flags))
	}
	if len(children) == 0 {
		return treeNode(syntax.NtEmpty)
	}
	if len(children) == 1 {
		return children[0]
	}
	return treeNode(syntax.NtConcatenate, children...)
}
func treeSet(items []ClassItem, flags int) *syntax.RegexNode {
	ranges, negate := classRanges(items, flags)
	spans := make([]syntax.SingleRange, len(ranges))
	for i, r := range ranges {
		spans[i] = syntax.SingleRange{First: r.lo, Last: r.hi}
	}
	n := treeNode(syntax.NtSet)
	n.Set = syntax.SourceRuneSet(spans, negate)
	return n
}
func look(child *syntax.RegexNode, positive bool, width int) *syntax.RegexNode {
	op := syntax.NtPosLook
	if !positive {
		op = syntax.NtNegLook
	}
	n := treeNode(op, child)
	if width >= 0 {
		n.Options |= syntax.SourceLookbehind
		n.M = width
	}
	return n
}
func boundary(flags int, positive bool) *syntax.RegexNode {
	word := func() *syntax.RegexNode {
		return treeSet([]ClassItem{{Op: Category, Category: "CATEGORY_WORD"}}, flags&^IgnoreCase)
	}
	// Each typed branch owns its own child nodes and parent pointers.
	yes := treeNode(syntax.NtAlternate, treeNode(syntax.NtConcatenate, look(word(), false, 1), look(word(), true, -1)), treeNode(syntax.NtConcatenate, look(word(), true, 1), look(word(), false, -1)))
	if positive {
		return yes
	}
	emptyWhole := treeNode(syntax.NtConcatenate, treeNode(syntax.NtBeginning), treeNode(syntax.NtEnd))
	return treeNode(syntax.NtConcatenate, look(emptyWhole, false, -1), look(yes, false, -1))
}
func treeOperation(n Node, flags int) *syntax.RegexNode {
	switch n.Op {
	case Literal, NotLiteral:
		if flags&IgnoreCase != 0 && (flags&ASCII != 0 && n.Rune >= 'A' && n.Rune <= 'Z' || flags&ASCII != 0 && n.Rune >= 'a' && n.Rune <= 'z' || flags&ASCII == 0 && inRanges(n.Rune, tables.Cased)) {
			items := []ClassItem{{Op: Literal, Rune: n.Rune}}
			if n.Op == NotLiteral {
				items = append([]ClassItem{{Op: Negate}}, items...)
			}
			return treeSet(items, flags)
		}
		op := syntax.NtOne
		if n.Op == NotLiteral {
			op = syntax.NtNotone
		}
		node := treeNode(op)
		node.Ch = n.Rune
		return node
	case In:
		return treeSet(n.Set, flags)
	case Any:
		if flags&DotAll != 0 {
			return treeSet([]ClassItem{{Op: Range, Rune: 0, High: 0x10ffff}}, 0)
		}
		return treeSet([]ClassItem{{Op: Negate}, {Op: Literal, Rune: '\n'}}, 0)
	case At:
		switch n.Anchor {
		case "AT_BEGINNING_STRING":
			return treeNode(syntax.NtBeginning)
		case "AT_END_STRING":
			return treeNode(syntax.NtEnd)
		case "AT_BEGINNING":
			if flags&Multiline != 0 {
				return treeNode(syntax.NtBol)
			}
			return treeNode(syntax.NtBeginning)
		case "AT_END":
			if flags&Multiline != 0 {
				return treeNode(syntax.NtEol)
			}
			return treeNode(syntax.NtEndZ)
		case "AT_BOUNDARY":
			return boundary(flags, true)
		case "AT_NON_BOUNDARY":
			return boundary(flags, false)
		}
	case Branch:
		children := []*syntax.RegexNode{}
		for _, child := range n.Children {
			children = append(children, treePattern(child, flags))
		}
		return treeNode(syntax.NtAlternate, children...)
	case Subpattern:
		next := flags
		if n.AddFlags&typeFlags != 0 {
			next &^= typeFlags
		}
		next = (next | n.AddFlags) &^ n.DelFlags
		body := treePattern(n.Children[0], next)
		if n.Group < 0 {
			return body
		}
		capture := treeNode(syntax.NtCapture, body)
		capture.M = n.Group
		capture.N = -1
		return capture
	case Assert, AssertNot:
		width := -1
		if n.Direction < 0 {
			width = int(n.Children[0].Width().Lo.N)
		}
		return look(treePattern(n.Children[0], flags), n.Op == Assert, width)
	case AtomicGroup:
		return treeNode(syntax.NtAtomic, treePattern(n.Children[0], flags))
	case GroupRef:
		ref := treeNode(syntax.NtRef)
		ref.M = n.Group
		if flags&IgnoreCase != 0 {
			ref.Options |= syntax.IgnoreCase
			if flags&ASCII != 0 {
				ref.Options |= syntax.PythonASCII
			}
		}
		return ref
	case GroupRefExists:
		children := []*syntax.RegexNode{treePattern(n.Children[0], flags)}
		if len(n.Children) > 1 && n.Children[1] != nil {
			children = append(children, treePattern(n.Children[1], flags))
		} else {
			// Source absence is an empty false branch; retaining it in the
			// typed backend tree also makes first-character nullability exact.
			children = append(children, treeNode(syntax.NtEmpty))
		}
		branch := treeNode(syntax.NtBackRefCond, children...)
		branch.M = n.Group
		return branch
	case MaxRep, MinRep, PossessiveRep:
		op := syntax.NtLoop
		if n.Op == MinRep {
			op = syntax.NtLazyloop
		}
		loop := treeNode(op, treePattern(n.Children[0], flags))
		loop.M = int(n.Min)
		if n.Max == MaxRepeat {
			loop.N = math.MaxInt64
		} else {
			loop.N = int(n.Max)
		}
		if n.Op == PossessiveRep {
			return treeNode(syntax.NtAtomic, loop)
		}
		return loop
	case Success:
		return treeNode(syntax.NtEmpty)
	}
	panic("unhandled source AST operation")
}
func compileSource(pattern string, flags int) (*regexp2.Regexp, error) {
	p, err := Parse(pattern, flags)
	if err != nil {
		return nil, err
	}
	if err = CheckCompile(p); err != nil {
		return nil, err
	}
	return regexp2.CompileSourceTree(pattern, lowerTree(p))
}

func lowerTree(p *Pattern) *syntax.RegexTree {
	root := treeNode(syntax.NtCapture, treePattern(p, p.State.Flags))
	root.M = 0
	root.N = -1
	names := map[string]int{}
	list := make([]string, len(p.State.GroupNames))
	for i, name := range p.State.GroupNames {
		list[i] = name
		if name == "" {
			list[i] = strconv.Itoa(i)
		}
		names[list[i]] = i
	}
	tree := &syntax.RegexTree{Root: root, Captop: len(list), Capnames: names, Caplist: list, Options: syntax.SourcePython}
	return tree
}

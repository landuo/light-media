package pythonre

type vmTask struct {
	kind  uint8
	n     *node
	group int
	count uint64
	last  int
}
type vmState struct {
	pos   int
	marks []int
	tasks []vmTask
}

func (s vmState) clone() vmState {
	return vmState{pos: s.pos, marks: append([]int(nil), s.marks...), tasks: append([]vmTask(nil), s.tasks...)}
}
func (s *vmState) push(seq []*node) {
	for i := len(seq) - 1; i >= 0; i-- {
		s.tasks = append(s.tasks, vmTask{n: seq[i]})
	}
}
func groupExists(m []int, g int) bool {
	i := (g - 1) * 2
	return i >= 0 && i+1 < len(m) && m[i] >= 0 && m[i+1] >= m[i]
}
func anchorMatches(n *node, pos int, subject []rune) bool {
	size := len(subject)
	switch n.anchor {
	case "beginString":
		return pos == 0
	case "endString":
		return pos == size
	case "begin":
		return pos == 0 || n.flags&flagM != 0 && pos > 0 && subject[pos-1] == '\n'
	case "end":
		return pos == size || pos < size && subject[pos] == '\n' && (n.flags&flagM != 0 || pos == size-1)
	case "boundary", "nonBoundary":
		if size == 0 {
			return false
		}
		left, right := false, false
		if pos > 0 {
			left = categoryMatches("w", subject[pos-1], n.flags&flagA != 0)
		}
		if pos < size {
			right = categoryMatches("w", subject[pos], n.flags&flagA != 0)
		}
		if n.anchor == "boundary" {
			return left != right
		}
		return left == right
	}
	return false
}

// Reject a backtracking point only when the next required assertion or
// consuming atom already fails at this position. Capture-end bookkeeping
// consumes no input. All other VM instructions keep their usual fallback.
func continuationPossible(tasks []vmTask, pos int, subject []rune) bool {
	for i := len(tasks) - 1; i >= 0; i-- {
		task := tasks[i]
		if task.kind == 1 {
			continue
		}
		if task.kind != 0 {
			return true
		}
		n := task.n
		switch n.op {
		case opAnchor:
			if !anchorMatches(n, pos, subject) {
				return false
			}
		case opLiteral:
			return pos < len(subject) && literalMatches(subject[pos], n.c, n.flags)
		case opNotLiteral:
			return pos < len(subject) && !literalMatches(subject[pos], n.c, n.flags)
		case opClass:
			return pos < len(subject) && classMatches(n, subject[pos])
		default:
			return true
		}
	}
	return true
}
func runVM(seq []*node, subject []rune, pos int, marks []int) (vmState, bool) {
	initial := vmState{pos: pos, marks: append([]int(nil), marks...)}
	initial.push(seq)
	return runState(initial, subject)
}

func runState(initial vmState, subject []rune) (vmState, bool) {
	stack := []vmState{initial}
	for len(stack) > 0 {
		s := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		alive := true
		for alive && len(s.tasks) > 0 {
			task := s.tasks[len(s.tasks)-1]
			s.tasks = s.tasks[:len(s.tasks)-1]
			n := task.n
			switch task.kind {
			case 1:
				s.marks[(task.group-1)*2+1] = s.pos
				continue
			case 3:
				s.tasks = append(s.tasks, vmTask{kind: 2, n: n, count: task.count + 1, last: task.last})
				continue
			case 2:
				if task.count >= n.max || task.count >= n.min && s.pos == task.last {
					if task.count < n.min {
						alive = false
					}
					continue
				}
				canStop := task.count >= n.min && continuationPossible(s.tasks, s.pos, subject)
				body := func(v vmState) vmState {
					v.tasks = append(v.tasks, vmTask{kind: 3, n: n, count: task.count, last: v.pos})
					v.push(n.sub)
					return v
				}
				if n.lazy && canStop {
					stack = append(stack, body(s.clone()))
					continue
				}
				if canStop {
					stack = append(stack, s.clone())
				}
				s = body(s)
				continue
			}
			switch n.op {
			case opLiteral, opNotLiteral:
				if s.pos >= len(subject) {
					alive = false
					break
				}
				ok := literalMatches(subject[s.pos], n.c, n.flags)
				if n.op == opNotLiteral {
					ok = !ok
				}
				if !ok {
					alive = false
					break
				}
				s.pos++
			case opClass:
				if s.pos >= len(subject) || !classMatches(n, subject[s.pos]) {
					alive = false
				} else {
					s.pos++
				}
			case opAny:
				if s.pos >= len(subject) || n.flags&flagS == 0 && subject[s.pos] == '\n' {
					alive = false
				} else {
					s.pos++
				}
			case opAnchor:
				alive = anchorMatches(n, s.pos, subject)
			case opGroup:
				if n.group > 0 {
					s.marks[(n.group-1)*2] = s.pos
					s.tasks = append(s.tasks, vmTask{kind: 1, group: n.group})
				}
				s.push(n.sub)
			case opBranch:
				for i := len(n.alts) - 1; i >= 0; i-- {
					v := s.clone()
					v.push(n.alts[i])
					stack = append(stack, v)
				}
				alive = false
			case opRepeat:
				if n.possessive {
					count := uint64(0)
					for count < n.min {
						v, ok := runVM(n.sub, subject, s.pos, s.marks)
						if !ok {
							alive = false
							break
						}
						s.pos = v.pos
						s.marks = v.marks
						count++
					}
					if !alive {
						break
					}
					last := -1
					for count < n.max && s.pos != last {
						last = s.pos
						v, ok := runVM(n.sub, subject, s.pos, s.marks)
						if !ok {
							break
						}
						s.pos = v.pos
						s.marks = v.marks
						count++
					}
				} else {
					s.tasks = append(s.tasks, vmTask{kind: 2, n: n, last: -1})
				}
			case opAtomic:
				v, ok := runVM(n.sub, subject, s.pos, s.marks)
				if !ok {
					alive = false
				} else {
					s.pos = v.pos
					s.marks = v.marks
				}
			case opAssert:
				from := s.pos
				if n.behind {
					if n.width > uint64(s.pos) {
						if !n.neg {
							alive = false
						}
						break
					}
					from -= int(n.width)
				}
				v, ok := runVM(n.sub, subject, from, s.marks)
				if n.neg {
					alive = !ok
				} else if ok {
					s.marks = v.marks
				} else {
					alive = false
				}
			case opRef:
				if !groupExists(s.marks, n.group) {
					alive = false
					break
				}
				i := (n.group - 1) * 2
				start, end := s.marks[i], s.marks[i+1]
				size := end - start
				if size > len(subject)-s.pos {
					alive = false
					break
				}
				for j := 0; j < size; j++ {
					a, b := subject[start+j], subject[s.pos+j]
					if n.flags&flagI != 0 {
						a = simpleLower(a, n.flags&flagA != 0)
						b = simpleLower(b, n.flags&flagA != 0)
					}
					if a != b {
						alive = false
						break
					}
				}
				if alive {
					s.pos += size
				}
			case opConditional:
				if groupExists(s.marks, n.group) {
					s.push(n.sub)
				} else if len(n.alts) > 0 {
					s.push(n.alts[0])
				}
			}
		}
		if alive && len(s.tasks) == 0 {
			return s, true
		}
	}
	return vmState{}, false
}

// Subject holds one decoded Python string. Multiple page patterns can share
// this read-only representation, including strings containing lone surrogates.
type Subject []rune

func DecodeSubject(s string) (Subject, error) { return decodeString(s) }

func (p *Pattern) Search(subject string) (bool, error) {
	rs, e := DecodeSubject(subject)
	if e != nil {
		return false, e
	}
	return rs.Matches(p)
}

func (rs Subject) Matches(p *Pattern) (bool, error) {
	if p == nil {
		return false, newError("TypeError", "nil pattern")
	}
	_, _, found := p.search(rs, 0)
	return found, nil
}

// A top-level run of literals must occur in every match, even when other
// instructions backtrack. Never infer requirements from optional branches.
func longestRequiredLiteral(seq []*node) []*node {
	var longest []*node
	start := 0
	for i, n := range seq {
		if n.op != opLiteral {
			start = i + 1
			continue
		}
		if i-start+1 >= 3 && i-start+1 > len(longest) {
			longest = seq[start : i+1]
		}
	}
	if len(longest) > 16 {
		longest = longest[:16]
	}
	return longest
}

func containsRequiredLiteral(rs []rune, literal []*node) bool {
	for i := 0; i+len(literal) <= len(rs); i++ {
		found := true
		for j, n := range literal {
			if !literalMatches(rs[i+j], n.c, n.flags) {
				found = false
				break
			}
		}
		if found {
			return true
		}
	}
	return false
}

// A failed leading literal rules out this start without touching VM state.
// Groups begin at the same position; all alternatives must fail to rule out
// a branch. Unknown or possibly empty heads always go through the full VM.
func possibleStart(seq []*node, rs []rune) bool {
	if len(seq) != 0 {
		n := seq[0]
		switch n.op {
		case opLiteral:
			return len(rs) != 0 && literalMatches(rs[0], n.c, n.flags)
		case opGroup:
			return possibleStart(n.sub, rs)
		case opBranch:
			for _, alt := range n.alts {
				if possibleStart(alt, rs) {
					return true
				}
			}
			return false
		}
	}
	return true
}

func (p *Pattern) search(rs []rune, from int) (int, vmState, bool) {
	if from > len(rs) || (len(p.requiredLiteral) > 0 && !containsRequiredLiteral(rs[from:], p.requiredLiteral)) {
		return 0, vmState{}, false
	}
	var initial vmState
	for start := from; start <= len(rs) && p.minimum <= uint64(len(rs)-start); start++ {
		if !possibleStart(p.code, rs[start:]) {
			continue
		}
		// Only reuse the initial storage of a *finished* attempt. Branches
		// and nested VM calls still own independent marks and continuations.
		if initial.tasks == nil {
			initial.tasks = make([]vmTask, 0, len(p.code))
			initial.marks = make([]int, p.groups*2)
		}
		initial.pos = start
		for i := range initial.marks {
			initial.marks[i] = -1
		}
		initial.tasks = initial.tasks[:0]
		initial.push(p.code)
		if s, ok := runState(initial, rs); ok {
			return start, s, true
		}
	}
	return 0, vmState{}, false
}

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
func runVM(seq []*node, subject []rune, pos int, marks []int) (vmState, bool) {
	initial := vmState{pos: pos, marks: append([]int(nil), marks...)}
	initial.push(seq)
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
				canStop := task.count >= n.min
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
func (p *Pattern) Search(subject string) (bool, error) {
	rs, e := decodeString(subject)
	if e != nil {
		return false, e
	}
	if p == nil {
		return false, newError("TypeError", "nil pattern")
	}
	if p.minimum > uint64(len(rs)) {
		return false, nil
	}
	marks := make([]int, p.groups*2)
	for i := range marks {
		marks[i] = -1
	}
	for start := 0; start <= len(rs) && p.minimum <= uint64(len(rs)-start); start++ {
		if _, ok := runVM(p.code, rs, start, marks); ok {
			return true, nil
		}
	}
	return false, nil
}

package sourcecharset

import "errors"

// Fixed CPython3.12.14 count_run/binarysort/gallop/merge paths. The current
// detector has98 unique codec candidates, so minrun produces at most two runs.
// This is an algorithm invariant, NOT a caller-input byte budget. No arbitrary
// Python list-sort API is exposed and no Go-sort substitution hides comparison
// scheduling under the original tolerance-based, possibly non-transitive order.
type cnSortState struct {
	err       error
	minGallop int
}

func (s *cnSortState) less(a, b *cnMatch) bool {
	if s.err != nil {
		return false
	}
	var result bool
	result, s.err = a.less(b)
	return result
}
func (s *cnSortState) run(data []*cnMatch) (int, bool) {
	if len(data) == 1 {
		return 1, false
	}
	descending := s.less(data[1], data[0])
	if s.err != nil {
		return 0, false
	}
	n := 2
	for n < len(data) {
		less := s.less(data[n], data[n-1])
		if s.err != nil {
			return 0, false
		}
		if less != descending {
			break
		}
		n++
	}
	return n, descending
}
func (s *cnSortState) binary(data []*cnMatch, start int) {
	if start == 0 {
		start++
	}
	for i := start; i < len(data); i++ {
		pivot := data[i]
		left, right := 0, i
		for left < right {
			mid := left + (right-left)/2
			if s.less(pivot, data[mid]) {
				right = mid
			} else {
				left = mid + 1
			}
			if s.err != nil {
				return
			}
		}
		copy(data[left+1:i+1], data[left:i])
		data[left] = pivot
	}
}
func (s *cnSortState) gallopLeft(key *cnMatch, a []*cnMatch, hint int) int {
	last, off := 0, 1
	n := len(a)
	if s.less(a[hint], key) {
		maximum := n - hint
		for off < maximum {
			if !s.less(a[hint+off], key) {
				break
			}
			last = off
			off = off*2 + 1
		}
		off = min(off, maximum)
		last += hint
		off += hint
	} else {
		maximum := hint + 1
		for off < maximum {
			if s.less(a[hint-off], key) {
				break
			}
			last = off
			off = off*2 + 1
		}
		off = min(off, maximum)
		saved := last
		last = hint - off
		off = hint - saved
	}
	if s.err != nil {
		return 0
	}
	last++
	for last < off {
		mid := last + (off-last)/2
		if s.less(a[mid], key) {
			last = mid + 1
		} else {
			off = mid
		}
		if s.err != nil {
			return 0
		}
	}
	return off
}
func (s *cnSortState) gallopRight(key *cnMatch, a []*cnMatch, hint int) int {
	last, off := 0, 1
	n := len(a)
	if s.less(key, a[hint]) {
		maximum := hint + 1
		for off < maximum {
			if !s.less(key, a[hint-off]) {
				break
			}
			last = off
			off = off*2 + 1
		}
		off = min(off, maximum)
		saved := last
		last = hint - off
		off = hint - saved
	} else {
		maximum := n - hint
		for off < maximum {
			if s.less(key, a[hint+off]) {
				break
			}
			last = off
			off = off*2 + 1
		}
		off = min(off, maximum)
		last += hint
		off += hint
	}
	if s.err != nil {
		return 0
	}
	last++
	for last < off {
		mid := last + (off-last)/2
		if s.less(key, a[mid]) {
			off = mid
		} else {
			last = mid + 1
		}
		if s.err != nil {
			return 0
		}
	}
	return off
}
func (s *cnSortState) mergeLow(data []*cnMatch, start, na, mid, nb int) {
	temp := append([]*cnMatch(nil), data[start:start+na]...)
	a, b, dest := 0, mid, start
	finish := func() {
		if na > 0 {
			copy(data[dest:dest+na], temp[a:a+na])
		}
	}
	copyB := func() { copy(data[dest:dest+nb], data[b:b+nb]); data[dest+nb] = temp[a] }
	data[dest] = data[b]
	dest++
	b++
	nb--
	if nb == 0 {
		finish()
		return
	}
	if na == 1 {
		copyB()
		return
	}
	minGallop := s.minGallop
	for {
		acount, bcount := 0, 0
		for {
			if s.less(data[b], temp[a]) {
				if s.err != nil {
					finish()
					return
				}
				data[dest] = data[b]
				dest++
				b++
				bcount++
				acount = 0
				nb--
				if nb == 0 {
					finish()
					return
				}
				if bcount >= minGallop {
					break
				}
			} else {
				if s.err != nil {
					finish()
					return
				}
				data[dest] = temp[a]
				dest++
				a++
				acount++
				bcount = 0
				na--
				if na == 1 {
					copyB()
					return
				}
				if acount >= minGallop {
					break
				}
			}
		}
		minGallop++
		for {
			if minGallop > 1 {
				minGallop--
			}
			s.minGallop = minGallop
			k := s.gallopRight(data[b], temp[a:a+na], 0)
			if s.err != nil {
				finish()
				return
			}
			acount = k
			if k > 0 {
				copy(data[dest:dest+k], temp[a:a+k])
				dest += k
				a += k
				na -= k
				if na == 1 {
					copyB()
					return
				}
				if na == 0 {
					finish()
					return
				}
			}
			data[dest] = data[b]
			dest++
			b++
			nb--
			if nb == 0 {
				finish()
				return
			}
			k = s.gallopLeft(temp[a], data[b:b+nb], 0)
			if s.err != nil {
				finish()
				return
			}
			bcount = k
			if k > 0 {
				copy(data[dest:dest+k], data[b:b+k])
				dest += k
				b += k
				nb -= k
				if nb == 0 {
					finish()
					return
				}
			}
			data[dest] = temp[a]
			dest++
			a++
			na--
			if na == 1 {
				copyB()
				return
			}
			if acount < 7 && bcount < 7 {
				break
			}
		}
		minGallop++
		s.minGallop = minGallop
	}
}
func (s *cnSortState) mergeHigh(data []*cnMatch, start, na, mid, nb int) {
	temp := append([]*cnMatch(nil), data[mid:mid+nb]...)
	a, b, dest := mid-1, nb-1, mid+nb-1
	finish := func() {
		if nb > 0 {
			copy(data[dest-nb+1:dest+1], temp[:nb])
		}
	}
	copyA := func() { copy(data[dest+1-na:dest+1], data[a+1-na:a+1]); data[dest-na] = temp[b] }
	data[dest] = data[a]
	dest--
	a--
	na--
	if na == 0 {
		finish()
		return
	}
	if nb == 1 {
		copyA()
		return
	}
	minGallop := s.minGallop
	for {
		acount, bcount := 0, 0
		for {
			if s.less(temp[b], data[a]) {
				if s.err != nil {
					finish()
					return
				}
				data[dest] = data[a]
				dest--
				a--
				acount++
				bcount = 0
				na--
				if na == 0 {
					finish()
					return
				}
				if acount >= minGallop {
					break
				}
			} else {
				if s.err != nil {
					finish()
					return
				}
				data[dest] = temp[b]
				dest--
				b--
				bcount++
				acount = 0
				nb--
				if nb == 1 {
					copyA()
					return
				}
				if bcount >= minGallop {
					break
				}
			}
		}
		minGallop++
		for {
			if minGallop > 1 {
				minGallop--
			}
			s.minGallop = minGallop
			k := s.gallopRight(temp[b], data[start:start+na], na-1)
			if s.err != nil {
				finish()
				return
			}
			k = na - k
			acount = k
			if k > 0 {
				copy(data[dest-k+1:dest+1], data[a-k+1:a+1])
				dest -= k
				a -= k
				na -= k
				if na == 0 {
					finish()
					return
				}
			}
			data[dest] = temp[b]
			dest--
			b--
			nb--
			if nb == 1 {
				copyA()
				return
			}
			k = s.gallopLeft(data[a], temp[:nb], nb-1)
			if s.err != nil {
				finish()
				return
			}
			k = nb - k
			bcount = k
			if k > 0 {
				copy(data[dest-k+1:dest+1], temp[b-k+1:b+1])
				dest -= k
				b -= k
				nb -= k
				if nb == 1 {
					copyA()
					return
				}
				if nb == 0 {
					finish()
					return
				}
			}
			data[dest] = data[a]
			dest--
			a--
			na--
			if na == 0 {
				finish()
				return
			}
			if acount < 7 && bcount < 7 {
				break
			}
		}
		minGallop++
		s.minGallop = minGallop
	}
}
func (s *cnSortState) merge(data []*cnMatch, mid int) {
	na, nb := mid, len(data)-mid
	k := s.gallopRight(data[mid], data[:mid], 0)
	if s.err != nil {
		return
	}
	start := k
	na -= k
	if na == 0 {
		return
	}
	nb = s.gallopLeft(data[mid-1], data[mid:mid+nb], nb-1)
	if s.err != nil || nb == 0 {
		return
	}
	if na <= nb {
		s.mergeLow(data, start, na, mid, nb)
	} else {
		s.mergeHigh(data, start, na, mid, nb)
	}
}
func cnSortMatches(data []*cnMatch) error {
	if len(data) < 2 {
		return nil
	}
	if len(data) > 98 {
		return errors.New("fixed CN candidate-count invariant violated")
	}
	minRun, lost := len(data), 0
	for minRun >= 64 {
		lost |= minRun & 1
		minRun >>= 1
	}
	minRun += lost
	state := cnSortState{minGallop: 7}
	runs := []int{}
	start := 0
	for start < len(data) {
		n, descending := state.run(data[start:])
		if state.err != nil {
			return state.err
		}
		if descending {
			for i, j := start, start+n-1; i < j; i, j = i+1, j-1 {
				data[i], data[j] = data[j], data[i]
			}
		}
		if n < minRun {
			force := min(minRun, len(data)-start)
			state.binary(data[start:start+force], n)
			if state.err != nil {
				return state.err
			}
			n = force
		}
		runs = append(runs, n)
		start += n
	}
	if len(runs) > 2 {
		return errors.New("fixed CPython two-run invariant violated")
	}
	if len(runs) == 2 {
		state.merge(data, runs[0])
	}
	return state.err
}

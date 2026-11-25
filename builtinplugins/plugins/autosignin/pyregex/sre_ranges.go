package pyregex

import (
	"sort"
	"strconv"
)

type span struct{ lo, hi rune }
type runeSet []span

func normalize(input runeSet) runeSet {
	out := append(runeSet(nil), input...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].lo != out[j].lo {
			return out[i].lo < out[j].lo
		}
		return out[i].hi < out[j].hi
	})
	result := runeSet{}
	for _, r := range out {
		if len(result) > 0 && r.lo <= result[len(result)-1].hi+1 {
			if r.hi > result[len(result)-1].hi {
				result[len(result)-1].hi = r.hi
			}
		} else {
			result = append(result, r)
		}
	}
	return result
}
func (s runeSet) contains(r rune) bool {
	i := sort.Search(len(s), func(i int) bool { return s[i].hi >= r })
	return i < len(s) && s[i].lo <= r
}
func (s runeSet) inverse() runeSet {
	out := runeSet{}
	at := rune(0)
	for _, r := range s {
		if at < r.lo {
			out = append(out, span{at, r.lo - 1})
		}
		at = r.hi + 1
	}
	if at <= 0x10ffff {
		out = append(out, span{at, 0x10ffff})
	}
	return out
}
func (s runeSet) without(r rune) runeSet {
	out := runeSet{}
	for _, x := range s {
		if r < x.lo || r > x.hi {
			out = append(out, x)
		} else {
			if x.lo < r {
				out = append(out, span{x.lo, r - 1})
			}
			if r < x.hi {
				out = append(out, span{r + 1, x.hi})
			}
		}
	}
	return out
}
func spans(input [][2]rune) runeSet {
	out := make(runeSet, len(input))
	for i, r := range input {
		out[i] = span{r[0], r[1]}
	}
	return out
}
func categorySet(category string, ascii bool) runeSet {
	var s runeSet
	switch category {
	case "CATEGORY_DIGIT", "CATEGORY_NOT_DIGIT":
		if ascii {
			s = runeSet{{'0', '9'}}
		} else {
			s = spans(tables.Digit)
		}
	case "CATEGORY_WORD", "CATEGORY_NOT_WORD":
		if ascii {
			s = runeSet{{'0', '9'}, {'A', 'Z'}, {'_', '_'}, {'a', 'z'}}
		} else {
			s = spans(tables.Word)
		}
	case "CATEGORY_SPACE", "CATEGORY_NOT_SPACE":
		if ascii {
			s = runeSet{{9, 13}, {32, 32}}
		} else {
			s = spans(tables.Space)
		}
	default:
		panic("unsupported source category")
	}
	if category == "CATEGORY_NOT_DIGIT" || category == "CATEGORY_NOT_WORD" || category == "CATEGORY_NOT_SPACE" {
		return s.inverse()
	}
	return s
}

var lowers = func() map[rune]rune {
	out := map[rune]rune{}
	for _, p := range tables.Lower {
		out[p[0]] = p[1]
	}
	return out
}()

func lower(r rune, ascii bool) rune {
	if ascii {
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return r
	}
	if l, ok := lowers[r]; ok {
		return l
	}
	return r
}

var unicodeCaseGroups = func() [][]rune {
	parents := map[rune]rune{}
	var root func(rune) rune
	root = func(r rune) rune {
		if p, ok := parents[r]; ok && p != r {
			parents[r] = root(p)
			return parents[r]
		}
		return r
	}
	for key, other := range tables.Extra {
		n, _ := strconv.Atoi(key)
		a := rune(n)
		for _, b := range other {
			ra, rb := root(a), root(b)
			parents[rb] = ra
		}
	}
	groups := map[rune][]rune{}
	for _, s := range tables.Cased {
		for r := s[0]; r <= s[1]; r++ {
			key := root(lower(r, false))
			groups[key] = append(groups[key], r)
		}
	}
	keys := make([]rune, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := [][]rune{}
	for _, key := range keys {
		out = append(out, groups[key])
	}
	return out
}()

func classRanges(items []ClassItem, flags int) (runeSet, bool) {
	literals, categories := runeSet{}, runeSet{}
	negate := false
	ascii := flags&ASCII != 0
	for _, item := range items {
		switch item.Op {
		case Literal:
			literals = append(literals, span{item.Rune, item.Rune})
		case Range:
			literals = append(literals, span{item.Rune, item.High})
		case Category:
			categories = append(categories, categorySet(item.Category, ascii)...)
		case Negate:
			negate = true
		}
	}
	literals = normalize(literals)
	categories = normalize(categories)
	if flags&IgnoreCase != 0 {
		hasCased := false
		extra := runeSet{}
		if ascii {
			for r := rune('A'); r <= 'Z'; r++ {
				lo := r + 32
				if literals.contains(r) || literals.contains(lo) {
					hasCased = true
					extra = append(extra, span{r, r}, span{lo, lo})
				}
			}
		} else {
			for _, group := range unicodeCaseGroups {
				selected := false
				for _, r := range group {
					if literals.contains(r) {
						selected = true
						hasCased = true
						break
					}
				}
				if selected {
					for _, r := range group {
						extra = append(extra, span{r, r})
					}
				}
			}
		}
		literals = normalize(append(literals, extra...))
		// _compile_charset evaluates categories against lowered input only when
		// an explicit cased literal/range selected IN_*_IGNORE. Never fold a
		// category through the _EXTRA_CASES equivalence relation.
		if hasCased {
			original := categories
			for _, pair := range tables.Lower {
				r, l := pair[0], pair[1]
				if ascii {
					if r < 'A' || r > 'Z' {
						continue
					}
					l = r + 32
				}
				wanted := original.contains(l)
				was := original.contains(r)
				if wanted && !was {
					categories = normalize(append(categories, span{r, r}))
				} else if !wanted && was {
					categories = categories.without(r)
				}
			}
		}
	}
	return normalize(append(literals, categories...)), negate
}

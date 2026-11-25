// Package pyregex ports the actual Python3.12.13 string-pattern parser
// for AutoSignIn. It lowers validated source operations into a private Go VM.
package pyregex

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"math/bits"
	"sort"
	"strconv"
	"strings"
)

const (
	Template           = 1
	IgnoreCase         = 2
	Locale             = 4
	Multiline          = 8
	DotAll             = 16
	Unicode            = 32
	Verbose            = 64
	Debug              = 128
	ASCII              = 256
	typeFlags          = ASCII | Locale | Unicode
	globalFlags        = Debug | Template
	MaxRepeat   uint64 = 4294967295
	MaxGroups          = 1073741823
	MaxCode     uint64 = 4294967295
)

type Op string

const (
	Literal        Op = "LITERAL"
	NotLiteral     Op = "NOT_LITERAL"
	In             Op = "IN"
	Category       Op = "CATEGORY"
	Range          Op = "RANGE"
	Negate         Op = "NEGATE"
	Any            Op = "ANY"
	At             Op = "AT"
	Branch         Op = "BRANCH"
	Subpattern     Op = "SUBPATTERN"
	Assert         Op = "ASSERT"
	AssertNot      Op = "ASSERT_NOT"
	GroupRef       Op = "GROUPREF"
	GroupRefExists Op = "GROUPREF_EXISTS"
	MaxRep         Op = "MAX_REPEAT"
	MinRep         Op = "MIN_REPEAT"
	PossessiveRep  Op = "POSSESSIVE_REPEAT"
	AtomicGroup    Op = "ATOMIC_GROUP"
	Success        Op = "SUCCESS"
)

type ClassItem struct {
	Op         Op
	Rune, High rune
	Category   string
}
type Node struct {
	Op                        Op
	Rune                      rune
	Set                       []ClassItem
	Anchor                    string
	Group, AddFlags, DelFlags int
	Min, Max                  uint64
	Direction                 int
	Children                  []*Pattern
}
type Pattern struct {
	Nodes  []Node
	State  *State
	cached *Width
}
type State struct {
	Flags            int
	GroupDict        map[string]int
	GroupNames       []string
	GroupWidths      []*Width
	LookbehindGroups *int
	GroupRefPos      map[int]int
	GroupRefOrder    []int
	Pattern          string
	Warnings         []Warning
	warningSink      func(Warning)
}
type Warning struct {
	Category string `json:"category"`
	Message  string `json:"message"`
}

// Size represents exact widths below2^64 and the original2^64 saturation.
type Size struct {
	N         uint64
	Saturated bool
}
type Width struct{ Lo, Hi Size }

func (s Size) JSON() any {
	if s.Saturated {
		return json.RawMessage(`18446744073709551616`)
	}
	return s.N
}
func add(a, b Size) Size {
	if a.Saturated || b.Saturated {
		return Size{Saturated: true}
	}
	sum, c := bits.Add64(a.N, b.N, 0)
	return Size{sum, c != 0}
}
func multiply(a Size, n uint64) Size {
	if n == 0 || a.N == 0 && !a.Saturated {
		return Size{}
	}
	if a.Saturated {
		return a
	}
	hi, lo := bits.Mul64(a.N, n)
	return Size{lo, hi != 0}
}
func less(a, b Size) bool { return !a.Saturated && (b.Saturated || a.N < b.N) }
func (p *Pattern) Width() Width {
	if p.cached != nil {
		return *p.cached
	}
	var w Width
	for _, node := range p.Nodes {
		var v Width
		switch node.Op {
		case Branch:
			v.Lo = Size{Saturated: true}
			for _, part := range node.Children {
				x := part.Width()
				if less(x.Lo, v.Lo) {
					v.Lo = x.Lo
				}
				if less(v.Hi, x.Hi) {
					v.Hi = x.Hi
				}
			}
		case AtomicGroup, Subpattern:
			v = node.Children[0].Width()
		case MaxRep, MinRep, PossessiveRep:
			x := node.Children[0].Width()
			v.Lo = multiply(x.Lo, node.Min)
			if node.Max == MaxRepeat && (x.Hi.N > 0 || x.Hi.Saturated) {
				v.Hi = Size{Saturated: true}
			} else {
				v.Hi = multiply(x.Hi, node.Max)
			}
		case Any, Range, In, Literal, NotLiteral, Category:
			v = Width{Size{N: 1}, Size{N: 1}}
		case GroupRef:
			v = *p.State.GroupWidths[node.Group]
		case GroupRefExists:
			v = node.Children[0].Width()
			if len(node.Children) > 1 && node.Children[1] != nil {
				x := node.Children[1].Width()
				if less(x.Lo, v.Lo) {
					v.Lo = x.Lo
				}
				if less(v.Hi, x.Hi) {
					v.Hi = x.Hi
				}
			} else {
				v.Lo = Size{}
			}
		case Success:
			p.cached = &w
			return w
		}
		w.Lo = add(w.Lo, v.Lo)
		w.Hi = add(w.Hi, v.Hi)
	}
	p.cached = &w
	return w
}
func (s *State) openGroup(name string, t *tokenizer) int {
	id := len(s.GroupWidths)
	s.GroupWidths = append(s.GroupWidths, nil)
	s.GroupNames = append(s.GroupNames, name)
	if len(s.GroupWidths) > MaxGroups {
		raise("error", "too many groups", s.Pattern, -1)
	}
	if name != "" {
		if old, ok := s.GroupDict[name]; ok {
			t.fail(fmt.Sprintf("redefinition of group name %s as group %d; was group %d", repr(name), id, old), len(sourceRunes(name))+1)
		}
		s.GroupDict[name] = id
	}
	return id
}
func (s *State) checkGroup(id int) bool { return id < len(s.GroupWidths) && s.GroupWidths[id] != nil }
func (s *State) checkLookbehind(id int, t *tokenizer) {
	if s.LookbehindGroups != nil {
		if !s.checkGroup(id) {
			t.fail("cannot refer to an open group", 0)
		}
		if id >= *s.LookbehindGroups {
			t.fail("cannot refer to group defined in the same lookbehind subpattern", 0)
		}
	}
}
func (s *State) warn(message string) {
	w := Warning{"FutureWarning", message}
	s.Warnings = append(s.Warnings, w)
	if s.warningSink != nil {
		s.warningSink(w)
	}
}

type Error struct {
	Class   string `json:"class"`
	Message string `json:"message"`
	Msg     any    `json:"msg"`
	Pos     any    `json:"pos"`
	Line    any    `json:"lineno"`
	Column  any    `json:"colno"`
}

func (e *Error) Error() string { return e.Message }
func raise(class, msg, pattern string, pos int) {
	e := &Error{Class: class, Message: msg}
	if class == "error" {
		e.Msg = msg
		if pos >= 0 {
			e.Pos = pos
			r := sourceRunes(pattern)
			if pos > len(r) {
				pos = len(r)
			}
			line := 1
			last := -1
			for i, c := range r[:pos] {
				if c == '\n' {
					line++
					last = i
				}
			}
			e.Line = line
			e.Column = pos - last
			e.Message += fmt.Sprintf(" at position %d", e.Pos)
			if strings.ContainsRune(pattern, '\n') {
				e.Message += fmt.Sprintf(" (line %d, column %d)", line, pos-last)
			}
		}
	}
	panic(e)
}

//go:embed sre_characters.json
var characterJSON []byte
var tables = func() struct {
	Start, Continue, Alpha, Printable, Cased, Word, Digit, Space [][2]rune
	Lower                                                        [][2]rune
	Extra                                                        map[string][]rune
} {
	var raw struct {
		Word      [][2]rune         `json:"word"`
		Digit     [][2]rune         `json:"digit"`
		Space     [][2]rune         `json:"space"`
		Start     [][2]rune         `json:"identifier_start"`
		Continue  [][2]rune         `json:"identifier_continue"`
		Alpha     [][2]rune         `json:"alpha"`
		Printable [][2]rune         `json:"printable"`
		Cased     [][2]rune         `json:"cased"`
		Lower     [][2]rune         `json:"simple_lower"`
		Extra     map[string][]rune `json:"extra_cases"`
	}
	if err := json.Unmarshal(characterJSON, &raw); err != nil {
		panic(err)
	}
	return struct {
		Start, Continue, Alpha, Printable, Cased, Word, Digit, Space [][2]rune
		Lower                                                        [][2]rune
		Extra                                                        map[string][]rune
	}{raw.Start, raw.Continue, raw.Alpha, raw.Printable, raw.Cased, raw.Word, raw.Digit, raw.Space, raw.Lower, raw.Extra}
}()

func inRanges(r rune, ranges [][2]rune) bool {
	i := sort.Search(len(ranges), func(i int) bool { return ranges[i][1] >= r })
	return i < len(ranges) && r >= ranges[i][0]
}
func identifier(s string) bool {
	r := sourceRunes(s)
	if len(r) == 0 || !inRanges(r[0], tables.Start) {
		return false
	}
	for _, c := range r[1:] {
		if !inRanges(c, tables.Continue) {
			return false
		}
	}
	return true
}
func repr(value string) string {
	quote := rune('\'')
	if strings.ContainsRune(value, '\'') && !strings.ContainsRune(value, '"') {
		quote = '"'
	}
	var b strings.Builder
	b.WriteRune(quote)
	for _, c := range sourceRunes(value) {
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if c == quote {
				b.WriteRune('\\')
				b.WriteRune(c)
			} else if inRanges(c, tables.Printable) {
				b.WriteRune(c)
			} else if c < 256 {
				fmt.Fprintf(&b, `\x%02x`, c)
			} else if c < 65536 {
				fmt.Fprintf(&b, `\u%04x`, c)
			} else {
				fmt.Fprintf(&b, `\U%08x`, c)
			}
		}
	}
	b.WriteRune(quote)
	return b.String()
}
func parseUint(s string, base int) uint64 {
	n, err := strconv.ParseUint(s, base, 64)
	if err != nil {
		return math.MaxUint64
	}
	return n
}

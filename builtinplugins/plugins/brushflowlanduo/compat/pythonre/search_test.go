package pythonre

import (
	"slices"
	"strings"
	"testing"
)

// Reference the previous exhaustive search: fresh tasks and capture marks for
// every start, without any leading-literal rejection.
func exhaustiveSearch(p *Pattern, rs []rune, from int) (int, vmState, bool) {
	marks := make([]int, 2*p.groups)
	for i := range marks {
		marks[i] = -1
	}
	for start := from; start <= len(rs); start++ {
		if s, ok := runVM(p.code, rs, start, marks); ok {
			return start, s, true
		}
	}
	return 0, vmState{}, false
}

func TestSearchAgainstExhaustiveVM(t *testing.T) {
	for _, tc := range []struct{ pattern, subject string }{
		{`(?i:(k|s))(.)`, "文🎞Kſ剩"},
		{`(?ai:(k|s))(.)`, "文KſSK"},
		{`(剩余时间|剩余|)(:|$)`, "🙂剩余时间x剩余:剩余时间:"},
		{`((?:ab|a)+?)b`, "文axbabab"},
		{`(a)?b(?(1)c|d)`, "abxbd"},
		{`(a|ab)\1`, "abaXabab"},
		{`(?<=前)(?=(.))\1`, "🙂前后"},
		{`(?m)^((?:)|x)$`, "\nx\n"},
		{`(a*+)ab|(?>ab|a)b`, "aabaaabb"},
		{`(?:a|(?=))`, ""},
		{"(\xed\xa0\x80)", "文\xed\xa0\x80🙂"}, // Lone surrogate, encoded as WTF-8.
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			p, err := Compile(tc.pattern)
			if err != nil {
				t.Fatal(err)
			}
			rs, err := decodeString(tc.subject)
			if err != nil {
				t.Fatal(err)
			}
			for from := 0; from <= len(rs)+1; from++ {
				wantStart, want, ok := exhaustiveSearch(p, rs, from)
				if from == 0 {
					got, err := p.Search(tc.subject)
					if err != nil || got != ok {
						t.Fatalf("Search = %t, %v; want %t", got, err, ok)
					}
				}
				start, got, found := p.search(rs, from)
				if found != ok || start != wantStart || got.pos != want.pos || !slices.Equal(got.marks, want.marks) {
					t.Fatalf("from %d: got (%t, %d:%d, %v), want (%t, %d:%d, %v)", from, found, start, got.pos, got.marks, ok, wantStart, want.pos, want.marks)
				}
				m, err := p.SearchGroups(tc.subject, from)
				if err != nil || (m != nil) != ok {
					t.Fatalf("SearchGroups from %d: match=%t, err=%v", from, m != nil, err)
				}
				if m != nil && (m.Start != wantStart || m.End != want.pos || m.Before != encodeString(rs[:wantStart]) || *m.Groups[0] != encodeString(rs[wantStart:want.pos])) {
					t.Fatalf("SearchGroups from %d: wrong match span or text", from)
				}
			}
		})
	}
}

func TestFailedStartsReuseInitialTasks(t *testing.T) {
	p, err := Compile(`ab`)
	if err != nil {
		t.Fatal(err)
	}
	allocs := func(size int) float64 {
		s := strings.Repeat("a", size)
		return testing.AllocsPerRun(3, func() {
			if ok, err := p.Search(s); ok || err != nil {
				t.Fatal(ok, err)
			}
		})
	}
	// Every start passes the head filter and fails on the second rune.
	if small, large := allocs(64), allocs(8192); large > small+1 {
		t.Fatalf("failed-start allocations grew with subject length: %.0f -> %.0f", small, large)
	}
}

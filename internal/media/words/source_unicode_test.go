package words

import (
	"errors"
	"reflect"
	"testing"
)

func TestSourceUnicodeCodepointRules(t *testing.T) {
	surrogate := rune(0xd800)
	cases := []struct {
		name          string
		rules         [][]rune
		title, want   string
		encodingError bool
	}{
		{"not_replacement", [][]rune{{surrogate}}, "�A", "�A", false},
		{"intermediate", [][]rune{append([]rune("A => "), surrogate), append([]rune{surrogate}, []rune(" => B")...)}, "A", "B", false},
		{"unicode_category", [][]rune{append([]rune("A => "), surrogate), []rune(`\p{Cs} => C`)}, "A", "C", false},
		{"group", [][]rune{append([]rune(`(A) => \1`), surrogate), append([]rune{surrogate}, []rune(" => B")...)}, "A", "AB", false},
		{"final_encoding", [][]rune{append([]rune("A => "), surrogate)}, "A", "", true},
		{"comment", [][]rune{{'#', surrogate}, []rune("A => B")}, "A", "B", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, e := NewCodepointRules(tc.rules)
			if e != nil {
				t.Fatal(e)
			}
			result, e := m.PrepareChecked(tc.title)
			if errors.Is(e, ErrUnicodeOutput) != tc.encodingError {
				t.Fatal(result, e)
			}
			if result.Title != tc.want {
				t.Fatalf("got %q want %q", result.Title, tc.want)
			}
		})
	}
}
func TestSourceUnicodeOrdinaryPathUnchanged(t *testing.T) {
	rules := []string{"Raw => Native", `S01E <>  >> 2`, "# comment"}
	normal, e := New(rules)
	if e != nil {
		t.Fatal(e)
	}
	codepoints := make([][]rune, len(rules))
	for i, r := range rules {
		codepoints[i] = []rune(r)
	}
	source, e := NewCodepointRules(codepoints)
	if e != nil {
		t.Fatal(e)
	}
	if source.sourceUnicode {
		t.Fatal("ordinary Unicode took special path")
	}
	got, e := source.PrepareChecked("Raw S01E01")
	if e != nil || !reflect.DeepEqual(got, normal.Prepare("Raw S01E01")) {
		t.Fatal(got, e)
	}
}

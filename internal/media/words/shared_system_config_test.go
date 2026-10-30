package words

import (
	"strings"
	"testing"
)

func TestSharedSystemConfigIgnoredRulesDoNotConsumeRegexBudget(t *testing.T) {
	rules := []string{"#" + strings.Repeat("x", 65<<10), strings.Repeat(" ", 65537), strings.Repeat("\u001c", 65537), "", "A => B"}
	matcher, e := New(rules)
	if e != nil {
		t.Fatal(e)
	}
	got := matcher.Prepare("A")
	if got.Title != "B" || got.Limited || len(got.Applied) != 1 || got.Applied[0] != 4 {
		t.Fatal(got)
	}
	if _, e = New([]string{strings.Repeat("x", 65<<10)}); e == nil {
		t.Fatal("executable regex bound removed")
	}
	many := make([]string, 100001)
	many = append(many, "A => B")
	matcher, e = New(many)
	if e != nil {
		t.Fatal(e)
	}
	got = matcher.Prepare("A")
	if got.Title != "B" || len(got.Applied) != 1 || got.Applied[0] != 100001 {
		t.Fatal(got)
	}
}

func TestSourceCombinedRulePythonStrip(t *testing.T) {
	// Original normally imported WordsMatcher observation, also replayed through
	// authenticated UI -> real typed Remote producer -> host -> SQLite -> cold.
	matcher, err := New([]string{"\u001cA => B && S <> E >> EP+1"})
	if err != nil {
		t.Fatal(err)
	}
	result := matcher.Prepare("A S1 E")
	if result.Title != "B S2 E" || len(result.Applied) != 1 || result.Failed != 0 {
		t.Fatal(result)
	}
	// The other exact source .strip call only decides whether a block rule is
	// empty; Python's separators must not become destructive regex removals.
	matcher, err = New([]string{"\u001c\u001d\u001e\u001f"})
	if err != nil {
		t.Fatal(err)
	}
	result = matcher.Prepare("A\u001c\u001d\u001e\u001fB")
	if result.Title != "A\u001c\u001d\u001e\u001fB" || len(result.Applied) != 0 {
		t.Fatal(result)
	}
}

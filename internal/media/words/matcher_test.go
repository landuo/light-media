package words

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestOriginalWordsOracle(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/plugins/words-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Title   string
			Words   []string
			Result  string
			Applied []string
		}
	}
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Title+"/"+sample.Words[0], func(t *testing.T) {
			m, err := New(sample.Words)
			if err != nil {
				t.Fatal(err)
			}
			got := m.Prepare(sample.Title)
			applied := []string{}
			for _, i := range got.Applied {
				applied = append(applied, sample.Words[i])
			}
			if got.Title != sample.Result || !reflect.DeepEqual(applied, sample.Applied) {
				t.Fatalf("got %q applied %v failed %d; want %q applied %v", got.Title, applied, got.Failed, sample.Result, sample.Applied)
			}
		})
	}
}

func TestWordsConcurrentCacheAndBoundedPathologicalRule(t *testing.T) {
	m, _ := New([]string{`(a+)+$ => X`, `Show => 测试`})
	t.Run("timeout", func(t *testing.T) {
		result := m.Prepare("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa!")
		if result.Failed != 1 || !result.Limited {
			t.Fatal("pathological match did not fail explicitly")
		}
	})
	for n := 0; n < 8; n++ {
		t.Run("concurrent", func(t *testing.T) {
			t.Parallel()
			for i := 0; i < 100; i++ {
				if m.Prepare("Show").Title != "测试" {
					t.Fatal("concurrent matcher failed")
				}
			}
		})
	}
}

func TestLiteralReplacementsKeepPythonSemantics(t *testing.T) {
	m, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		title, pattern, value, want string
		applied                     bool
	}{
		{"电影电影", "电影", "$1", "$1$1", true},
		{"aa", "a", "a", "aa", true},
		{"a", "A", "x", "a", false},
		{"ab", "a.b", "x", "ab", false},
		{"x", "x", `\g<0>!`, "x!", true},
	} {
		got, applied, err := m.replace(tc.title, tc.pattern, tc.value)
		if err != nil || got != tc.want || applied != tc.applied {
			t.Fatalf("%q: %q, %t, %v", tc.pattern, got, applied, err)
		}
	}
	if _, _, err = m.replace("absent", "word", `\g<9>`); err == nil {
		t.Fatal("invalid group reference suppressed on miss")
	}
}

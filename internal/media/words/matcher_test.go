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

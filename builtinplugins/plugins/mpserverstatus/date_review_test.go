package mpserverstatus

import (
	"encoding/json"
	"os"
	"testing"
)

func TestIndependentActual312DateLexicalCases(t *testing.T) {
	raw, e := os.ReadFile("reference/source-date-review.json")
	if e != nil {
		t.Fatal(e)
	}
	var source struct {
		Cases []struct {
			Input  string
			Return string `json:"return"`
		}
	}
	if e = json.Unmarshal(raw, &source); e != nil {
		t.Fatal(e)
	}
	for _, c := range source.Cases {
		t.Run(c.Input, func(t *testing.T) {
			if got := ResponseDate(c.Input); got != c.Return {
				t.Fatalf("date got=%q source=%q", got, c.Return)
			}
		})
	}
}

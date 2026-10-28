package app

import (
	"encoding/json"
	"os"
	"testing"
)

// The expected nullable fields, coercions and failures come from the actual
// fixed MediaPerson Pydantic class, not from the new response projection.
func TestOriginalCatalogPersonProjectionFixedSourceDTO(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-catalog-modules/model-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Model  string
			Input  json.RawMessage
			State  string
			Output json.RawMessage
		}
	}
	if json.Unmarshal(raw, &oracle) != nil {
		t.Fatal("fixed DTO oracle invalid")
	}
	count := 0
	for _, row := range oracle.Cases {
		if row.Model != "MediaPerson" {
			continue
		}
		count++
		t.Run(string(row.Input), func(t *testing.T) {
			actual, err := originalCatalogPersonProjection(row.Input)
			if row.State == "invalid" {
				if err == nil {
					t.Fatal("fixed source rejects value")
				}
				return
			}
			if err != nil {
				t.Fatal("fixed source accepts value", err)
			}
			got, e := json.Marshal(actual)
			if e != nil {
				t.Fatal(e)
			}
			// JSON number lexemes 0 and 0.0 express the same value; compare
			// without float64 rounding of the source's 400-digit integer.
			if !originalBaseJSONEqual(got, row.Output) {
				t.Fatalf("source DTO differs\nactual %s\nsource %s", got, row.Output)
			}
		})
	}
	if count != 18 {
		t.Fatal("unexpected fixed DTO cases", count)
	}
}

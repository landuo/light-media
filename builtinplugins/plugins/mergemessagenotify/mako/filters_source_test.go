package mako

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSourceTemplateFilters141(t *testing.T) {
	file, err := os.Open("testdata/source-filters-1.4.1.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var fixture struct {
		Cases []struct {
			Name, Template, Expected string
			Vars                     map[string]any
		}
	}
	dec := json.NewDecoder(file)
	dec.UseNumber()
	if err := dec.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			got, err := Render(t.Context(), row.Template, row.Vars)
			if err != nil || got != row.Expected {
				t.Fatalf("Render = %q, %v; want %q", got, err, row.Expected)
			}
		})
	}
}

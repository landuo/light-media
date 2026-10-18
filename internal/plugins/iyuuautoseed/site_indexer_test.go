package iyuuautoseed

import (
	"os"
	"testing"
)

func TestInstalledCompiledSiteIndexerProjectionOracle(t *testing.T) {
	total := 0
	for _, filename := range []string{"reference/sites-probe-round9.json", "reference/sites-probe-round10.json"} {
		raw, e := os.ReadFile(filename)
		if e != nil {
			t.Fatal(e)
		}
		v, e := decodeJSON(raw)
		if e != nil {
			t.Fatal(e)
		}
		for _, raw := range v.(map[string]any)["result"].(map[string]any)["calls"].([]any) {
			row := raw.(map[string]any)
			if row["case"] != "indexer-row" {
				continue
			}
			total++
			definition, _ := row["template"].(map[string]any)
			db, present := row["input"].(map[string]any)
			got, e := ProjectIndexer(definition, db, present, "synthetic-default-UA")
			want := row["result"].(map[string]any)
			if want["exception"] != nil {
				if e == nil {
					t.Fatal("source error accepted")
				}
				continue
			}
			if e != nil {
				t.Fatal(e)
			}
			var value any = got
			if got == nil {
				value = nil
			}
			assertOracle(t, map[string]any{"value": value}, want)
		}
	}
	if total != 35 {
		t.Fatalf("projection count %d", total)
	}
}
func TestSiteIndexerProjectionOwnsNestedData(t *testing.T) {
	template := map[string]any{"category": map[string]any{"movie": "one"}, "url": "https://source.invalid/"}
	row := map[string]any{"extra": map[string]any{"a": "before"}}
	out, e := ProjectIndexer(template, row, true, "UA")
	if e != nil {
		t.Fatal(e)
	}
	out["category"].(map[string]any)["movie"] = "changed"
	out["extra"].(map[string]any)["a"] = "after"
	if template["category"].(map[string]any)["movie"] != "one" || row["extra"].(map[string]any)["a"] != "before" {
		t.Fatal("projection aliases input")
	}
}

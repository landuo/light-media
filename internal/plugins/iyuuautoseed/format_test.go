package iyuuautoseed

import (
	"errors"
	"os"
	"testing"
)

func TestInstalledPythonFormatOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/format-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	doc, e := decodeJSON(raw)
	if e != nil {
		t.Fatal(e)
	}
	m := doc.(map[string]any)
	failures := 0
	total := 0
	compare := func(name string, got string, err error, want map[string]any) {
		t.Helper()
		total++
		if exception, ok := want["exception"].(string); ok {
			var source *SourceFault
			if !errors.As(err, &source) || source.Class != exception {
				if failures < 30 {
					t.Errorf("%s goterr %v want%s", name, err, exception)
				}
				failures++
			}
			return
		}
		if err != nil || got != want["value"] {
			if failures < 30 {
				t.Errorf("%s got%q/%v want%q", name, got, err, want["value"])
			}
			failures++
		}
	}
	for _, raw := range m["scalar"].([]any) {
		c := raw.(map[string]any)
		got, e := formatScalar(c["input"], c["spec"].(string))
		compare(c["name"].(string)+" spec="+c["spec"].(string), got, e, c["result"].(map[string]any))
	}
	for _, raw := range m["fields"].([]any) {
		c := raw.(map[string]any)
		got, e := sourceFormat(c["template"].(string), c["fields"].(map[string]any))
		compare(c["name"].(string)+" template="+c["template"].(string), got, e, c["result"].(map[string]any))
	}
	if failures > 0 {
		t.Fatalf("%d/%d format cases differed", failures, total)
	}
}

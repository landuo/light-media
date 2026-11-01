package zhconv

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

// This separate actual source capture adds the correctly delimited adjacent
// rules and the long replacement-removal witnesses. It does not rewrite any
// of the original 4,789 inputs, expected values, tests or recorded results.
func TestHansSourceDelta03AdjacentAndRemoval(t *testing.T) {
	b, err := os.ReadFile("testdata/source-delta03.json")
	if err != nil {
		t.Fatal(err)
	}
	if hashText(string(b)) != "f70aba727c8f4ab6f49f50206933d0355bda26dfb77e2c8443da3157d5c46575" {
		t.Fatal("source delta bytes changed")
	}
	var doc struct {
		Count int               `json:"case_count"`
		Cases []sourceCase      `json:"cases"`
		Drift []json.RawMessage `json:"input_drift"`
	}
	if err = json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Count != 5 || len(doc.Cases) != 5 || len(doc.Drift) != 0 {
		t.Fatal("source delta count/drift")
	}
	for _, c := range doc.Cases {
		if hashText(c.Input.Text) != c.InputSHA || c.Outcome.Kind != "return" || hashText(c.Outcome.Value) != c.Outcome.SHA || len(c.Outcome.Value) != c.Outcome.Bytes || base64.StdEncoding.EncodeToString([]byte(c.Outcome.Value)) != c.Outcome.Base64 {
			t.Fatalf("source delta encoding %s", c.Input.ID)
		}
		got, err := Hans(c.Input.Text)
		if err != nil || got != c.Outcome.Value {
			t.Errorf("%s: got %q err=%v want %q", c.Input.ID, got, err, c.Outcome.Value)
		}
	}
	t.Log("exact byte comparison: 5 independent source-delta03 returns; original source4789 unchanged")
}

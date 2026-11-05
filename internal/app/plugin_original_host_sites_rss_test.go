package app

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestOriginalHostBusinessRSSRecoverFixedLXML611Source(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-business-20261031/rss-fallback-source-probe.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Commit      string `json:"source_commit"`
		Environment struct {
			LXML []int `json:"lxml"`
		}
		Cases []struct {
			Name, Input string
			Returned    json.RawMessage
		}
	}
	if err = json.Unmarshal(raw, &oracle); err != nil || oracle.Commit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Cases) < 11 || oracle.Environment.LXML[0] != 6 || oracle.Environment.LXML[1] != 1 {
		t.Fatal("fixed recovery source result missing", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			got, err := originalRSSRecoverRows(sample.Input, time.Date(2026, 10, 31, 12, 0, 0, 0, shanghai()))
			if string(sample.Returned) == "false" {
				if err == nil {
					t.Fatal("source rejected non-XML input was accepted", got)
				}
				return
			}
			if err != nil {
				t.Fatal("source XML recovery changed", err)
			}
			encoded, _ := json.Marshal(got)
			if !originalBaseJSONEqual(encoded, sample.Returned) {
				t.Fatalf("lxml recovery mismatch got=%s expected=%s", encoded, sample.Returned)
			}
		})
	}
}

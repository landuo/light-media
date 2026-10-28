package app

import (
	"encoding/json"
	"os"
	"testing"
)

func TestOriginalTransferSubtitleExactSourceNames(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-subtitle-source-oracle.json")
	var oracle struct {
		Cases []struct {
			DefaultSub             any `json:"default_sub"`
			Source, Target, Result string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 648 {
		t.Fatal("fixed subtitle source cases", err)
	}
	for _, sample := range oracle.Cases {
		actual, err := originalTransferSubtitleName(sample.Source, sample.Target, sample.DefaultSub)
		if err != nil || actual != sample.Result {
			t.Fatalf("source=%q target=%q default=%v actual=%q want=%q error=%v", sample.Source, sample.Target, sample.DefaultSub, actual, sample.Result, err)
		}
	}
}

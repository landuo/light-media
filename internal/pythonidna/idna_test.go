package pythonidna

import (
	"encoding/json"
	"os"
	"testing"
)

func TestInstalledBuiltinIDNAOracle(t *testing.T) {
	var in struct {
		Python string `json:"python"`
		Cases  []struct {
			Input  string  `json:"input"`
			Output *string `json:"output"`
			Error  *string `json:"error"`
		} `json:"cases"`
	}
	raw, e := os.ReadFile("testdata/original-idna.json")
	if e != nil || json.Unmarshal(raw, &in) != nil || in.Python != "3.12.13" || len(in.Cases) != 5674 {
		t.Fatal("invalid installed oracle", e)
	}
	for _, c := range in.Cases {
		got, e := ToASCII(c.Input)
		if c.Error != nil {
			if e == nil {
				t.Fatalf("source UnicodeError became success for %q: %q", c.Input, got)
			}
			continue
		}
		if e != nil || got != *c.Output {
			t.Fatalf("%q got %q error=%v want %q", c.Input, got, e, *c.Output)
		}
	}
}

func TestInstalledUnicode32NormalizationWithRuntimeCombining(t *testing.T) {
	var in struct {
		Cases []struct {
			Input  string `json:"input"`
			Output string `json:"output"`
		} `json:"cases"`
	}
	raw, e := os.ReadFile("testdata/original-normalization.json")
	if e != nil || json.Unmarshal(raw, &in) != nil || len(in.Cases) < 12000 {
		t.Fatal("invalid installed normalization oracle", e)
	}
	for _, c := range in.Cases {
		if got := normalizeUnicode32(c.Input); got != c.Output {
			t.Fatalf("%q got %q want %q", c.Input, got, c.Output)
		}
	}
}

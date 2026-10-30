package app

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"
)

func TestOriginalMarketVersionUnicodeSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/market-version-unicode/source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Unicode string
		Cases   []struct {
			Name, Left, Right string
			Comparison        int
		}
		DecimalMap  [][2]int `json:"decimal_map"`
		DigitOnly   []int    `json:"digit_only"`
		StripSpaces []int    `json:"strip_spaces"`
	}
	if err := json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	if oracle.Unicode != "15.0.0" || len(oracle.DecimalMap) != 680 || len(oracle.DigitOnly) != 128 || len(oracle.StripSpaces) != 29 {
		t.Fatal("unexpected actual-source Unicode input")
	}
	for _, example := range oracle.Cases {
		t.Run(example.Name, func(t *testing.T) {
			if actual := compareOriginalPluginVersion(example.Left, example.Right); actual != example.Comparison {
				t.Fatalf("actual %d, source %d", actual, example.Comparison)
			}
		})
	}
	wantDecimal := map[rune]byte{}
	wantDigitOnly := map[rune]bool{}
	wantSpace := map[rune]bool{}
	for _, item := range oracle.DecimalMap {
		wantDecimal[rune(item[0])] = byte(item[1])
	}
	for _, cp := range oracle.DigitOnly {
		wantDigitOnly[rune(cp)] = true
	}
	for _, cp := range oracle.StripSpaces {
		wantSpace[rune(cp)] = true
	}
	// Exhaustive codepoint classification tests the fixed tables, including gaps
	// and later Unicode additions. It is not a claim of all string combinations.
	for r := rune(0); r <= 0x10ffff; r++ {
		value, ok := originalPluginVersionDecimalValue(r)
		want, wanted := wantDecimal[r]
		if ok != wanted || ok && value != want {
			t.Fatalf("decimal U+%04X", r)
		}
		if originalPluginVersionSpace(r) != wantSpace[r] {
			t.Fatalf("space U+%04X", r)
		}
		if r >= 0xd800 && r <= 0xdfff {
			continue
		} // Go string cannot encode a lone surrogate.
		ascii, digit, valid := originalPluginVersionDecimal(string(r))
		if wanted {
			if !digit || !valid || ascii != strconv.Itoa(int(want)) {
				t.Fatalf("digit U+%04X", r)
			}
		} else if wantDigitOnly[r] {
			if !digit || valid {
				t.Fatalf("digit-only U+%04X", r)
			}
		} else if digit || !valid {
			t.Fatalf("nondigit U+%04X", r)
		}
	}
}

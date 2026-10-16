package larkmessager

import (
	"encoding/json"
	"os"
	"testing"
)

func TestInstalledScalarPredicateTables(t *testing.T) {
	raw, e := os.ReadFile(originalFixture("reference/python-printable15.json"))
	if e != nil {
		t.Fatal(e)
	}
	var p struct {
		Unicode string    `json:"unicode"`
		Ranges  [][2]rune `json:"ranges"`
	}
	if e = json.Unmarshal(raw, &p); e != nil {
		t.Fatal(e)
	}
	if p.Unicode != "15.0.0" {
		t.Fatal(p.Unicode)
	}
	raw, e = os.ReadFile(originalFixture("reference/pydantic-int-whitespace.json"))
	if e != nil {
		t.Fatal(e)
	}
	var m struct {
		Version  string `json:"version"`
		Accepted []rune `json:"accepted"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Version != "2.46.4" {
		t.Fatal(m.Version)
	}
	spaces := map[rune]bool{}
	for _, r := range m.Accepted {
		spaces[r] = true
	}
	index := 0
	for r := rune(0); r <= 0x10ffff; r++ {
		for index < len(p.Ranges) && r > p.Ranges[index][1] {
			index++
		}
		expected := index < len(p.Ranges) && p.Ranges[index][0] <= r
		if pythonPrintable15(r) != expected {
			t.Fatalf("printable15 mismatch U+%04X", r)
		}
		if modelSpace(r) != spaces[r] {
			t.Fatalf("core trim mismatch U+%04X", r)
		}
	}
}

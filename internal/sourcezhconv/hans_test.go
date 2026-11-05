package sourcezhconv

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

func TestPinnedWheelOracle(t *testing.T) {
	var data struct {
		Version   string
		CaseCount int `json:"case_count"`
		Cases     []struct {
			Type, Input, Output, Panic string
			Unstable                   bool `json:"description_order_unstable"`
		}
	}
	b, err := os.ReadFile("testdata/oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &data); err != nil {
		t.Fatal(err)
	}
	if data.Version != "0.4.1" || len(data.Cases) != data.CaseCount || data.CaseCount < 6000 {
		t.Fatalf("oracle binding: %s %d/%d", data.Version, len(data.Cases), data.CaseCount)
	}
	normalize := func(s string) string {
		// Syntax corpus has known prefix/suffix; only description clause order
		// is normalized. All text and clause multiplicities remain compared.
		const edge = "繁体电脑"
		if !strings.HasPrefix(s, edge) || !strings.HasSuffix(s, edge) {
			return s
		}
		body := strings.TrimSuffix(strings.TrimPrefix(s, edge), edge)
		p := strings.Split(body, "；")
		sort.Strings(p)
		return strings.Join(p, "；")
	}
	for i, c := range data.Cases {
		var got string
		var err error
		var panicked any
		func() { defer func() { panicked = recover() }(); got, err = Hans(c.Input) }()
		if c.Panic != "" {
			if err == nil || panicked != nil {
				t.Errorf("case %d expected source panic %s input=%q got=%q", i, c.Panic, c.Input, got)
			}
			continue
		}
		if panicked != nil || err != nil {
			t.Errorf("case %d unexpected panic %v / error %v", i, panicked, err)
			continue
		}
		want := c.Output
		if c.Unstable {
			got, want = normalize(got), normalize(want)
		}
		if got != want {
			t.Errorf("case %d %s input=%q got=%q want=%q", i, c.Type, c.Input, got, want)
		}
	}
}

func BenchmarkHans(b *testing.B) {
	text := strings.Repeat("慶餘年 乾隆 電腦髮型𠮷🍵", 4096)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		Hans(text)
	}
}

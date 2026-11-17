package pyrepr

import (
	"encoding/json"
	"os"
	"testing"
)

// repr-golden.json was captured from the template engine's formatter before the
// source-shaped JSON writer stopped depending on it. It is the contract: extras
// move to a leaf package only when every recorded output still matches.
func TestGoldenReprParity(t *testing.T) {
	raw, err := os.ReadFile("testdata/repr-golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		Strings []struct {
			In  string `json:"in"`
			Out string `json:"out"`
		} `json:"strings"`
		Floats []struct {
			In  float64 `json:"in"`
			Out string  `json:"out"`
		} `json:"floats"`
	}
	if err = json.Unmarshal(raw, &golden); err != nil {
		t.Fatal(err)
	}
	if len(golden.Strings) < 20 || len(golden.Floats) < 15 {
		t.Fatalf("golden corpus is incomplete: %d strings, %d floats", len(golden.Strings), len(golden.Floats))
	}
	for _, c := range golden.Strings {
		if got := String(c.In); got != c.Out {
			t.Errorf("String(%q) = %q, want %q", c.In, got, c.Out)
		}
	}
	for _, c := range golden.Floats {
		if got := Float(c.In); got != c.Out {
			t.Errorf("Float(%v) = %q, want %q", c.In, got, c.Out)
		}
	}
}

func TestPrintableMatchesCPythonsShadowClass(t *testing.T) {
	// Ordinary text, spaces and CJK print literally.
	for _, r := range []rune{'a', 'Z', '0', ' ', '中', '文', '😀'} {
		if !Printable(r) {
			t.Errorf("%q must print literally", r)
		}
	}
	// Control characters, and separators Python escapes in a repr.
	for _, r := range []rune{0, 1, 0x1f, 0x7f, 0x85, 0x2028, 0x2029} {
		if Printable(r) {
			t.Errorf("%U must be escaped", r)
		}
	}
	// A non-breaking space is not printable to CPython's repr either.
	if Printable('\u00a0') {
		t.Error("U+00A0 must be escaped")
	}
}

func TestFloatKeepsFloatIdentity(t *testing.T) {
	cases := map[float64]string{
		0:          "0.0",
		1:          "1.0",
		-1:         "-1.0",
		1e16:       "1e+16",
		1e15:       "1000000000000000.0",
		0.0001:     "0.0001",
		0.00001:    "1e-05",
		1234567.5:  "1234567.5",
		-0.0001234: "-0.0001234",
	}
	for in, want := range cases {
		if got := Float(in); got != want {
			t.Errorf("Float(%v) = %q, want %q", in, got, want)
		}
	}
}

// The formatter runs once per JSON field, so its cost must stay far below the
// I/O it frames. Guard against regressions that would make a memory fix a
// latency fix too.
func BenchmarkString(b *testing.B) {
	value := `字幕组发布 1080p 简繁内嵌 [S01E02][it's 100% done]`
	for i := 0; i < b.N; i++ {
		_ = String(value)
	}
}

func BenchmarkFloat(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = Float(1234.5678)
	}
}

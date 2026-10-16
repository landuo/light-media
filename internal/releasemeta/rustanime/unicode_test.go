package rustanime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// The digest was produced from the precise compiler commit found in the actual
// installed wheel, not inferred from the local compiler's Unicode version.
func TestInstalledCompilerUnicodeScalarProperties(t *testing.T) {
	data, err := os.ReadFile("../../../testdata/plugins/release-meta-rust-unicode-provenance.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Unicode struct {
			Version     string `json:"version"`
			ScalarCount int    `json:"scalar_count"`
			RowsSHA256  string `json:"reference_rows_sha256"`
		} `json:"unicode"`
	}
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	toHex := func(value string) string {
		var out strings.Builder
		for i, r := range value {
			if i != 0 {
				out.WriteByte(',')
			}
			fmt.Fprintf(&out, "%x", r)
		}
		return out.String()
	}
	digest := sha256.New()
	fmt.Fprintf(digest, "version\t%s\n", reference.Unicode.Version)
	count := 0
	for r := rune(0); r <= 0x10ffff; r++ {
		if r >= 0xd800 && r <= 0xdfff {
			continue // Surrogates cannot be Rust char or Unicode scalar values.
		}
		flags := 0
		if sourceAlphabetic(r) {
			flags |= 1
		}
		if sourceNumeric(r) {
			flags |= 2
		}
		if sourceUppercase(r) {
			flags |= 4
		}
		lowercase := string(r)
		if mapped, ok := sourceLowerMappings[r]; ok {
			lowercase = mapped
		}
		fmt.Fprintf(digest, "%x\t%d\t%s\t%s\n", r, flags, toHex(upper(string(r))), toHex(lowercase))
		count++
	}
	if got := hex.EncodeToString(digest.Sum(nil)); got != reference.Unicode.RowsSHA256 || count != reference.Unicode.ScalarCount {
		t.Fatalf("installed compiler scalar properties differ: count=%d digest=%s; want count=%d digest=%s", count, got, reference.Unicode.ScalarCount, reference.Unicode.RowsSHA256)
	}
}

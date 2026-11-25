package mpserverstatus

import (
	"encoding/hex"
	"os"
	"testing"
)

func TestActual312CompressionBoundaries(t *testing.T) {
	raw, e := os.ReadFile("reference/source-compression-reference.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct{ Cases []Object }
	if e = decode(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	for _, c := range fixture.Cases {
		t.Run(c["name"].(string), func(t *testing.T) {
			input, e := hex.DecodeString(c["input_hex"].(string))
			if e != nil {
				t.Fatal(e)
			}
			out, e := decodeContent(input, c["encoding"].(string))
			if c["error_kind"] != nil {
				if e == nil {
					t.Fatal("source rejects this body")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if hex.EncodeToString(out) != c["output_hex"] {
				t.Fatalf("source output mismatch: %x", out)
			}
		})
	}
}

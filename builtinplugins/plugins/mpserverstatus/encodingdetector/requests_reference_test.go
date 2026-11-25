package encodingdetector

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// Reused pure library checked independently against actual image Requests
// Response.text. Its output text, not Go-derived expected values, is compared.
func TestActual312RequestsTextReference(t *testing.T) {
	raw, e := os.ReadFile("../reference/source-encoding-reference.json")
	if e != nil {
		t.Fatal(e)
	}
	var reference struct {
		Cases []struct {
			Name, Text string
			InputHex   string  `json:"input_hex"`
			Declared   *string `json:"declared_encoding"`
		}
	}
	if e = json.Unmarshal(raw, &reference); e != nil {
		t.Fatal(e)
	}
	for _, c := range reference.Cases {
		t.Run(c.Name, func(t *testing.T) {
			data, e := hex.DecodeString(c.InputHex)
			if e != nil {
				t.Fatal(e)
			}
			encoding := ""
			if c.Declared != nil {
				encoding = *c.Declared
			} else {
				encoding, _, e = Detect(data)
				if e != nil {
					t.Fatal(e)
				}
			}
			if encoding == "" {
				encoding = "utf-8"
			}
			got, e := Decode(data, encoding, true)
			if e != nil {
				t.Fatal(e)
			}
			if got != c.Text {
				t.Fatalf("source Requests.text mismatch encoding=%s got=%q want=%q", encoding, got, c.Text)
			}
		})
	}
}

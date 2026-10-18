package torrenttransfer

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

func TestActualFastBencodeOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/bencode-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture struct {
		Dependency string
		Cases      []struct {
			Name            string
			Raw             string `json:"raw_b64"`
			Roundtrip       string `json:"roundtrip_b64"`
			Exception       string
			NativeCrash     bool `json:"source_native_crash"`
			NativeUndefined bool `json:"source_native_undefined"`
		}
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	if fixture.Dependency != "fast-bencode1.1.8" {
		t.Fatal("dependency source")
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			b, e := base64.StdEncoding.DecodeString(tc.Raw)
			if e != nil {
				t.Fatal(e)
			}
			v, e := bdecode(b)
			if tc.Exception != "" || tc.NativeCrash || tc.NativeUndefined {
				if e == nil {
					t.Fatalf("source rejects %q but accepted %#v", b, v)
				}
				return
			}
			if e != nil {
				t.Fatalf("source accepts %q: %v", b, e)
			}
			b, e = bencode(v)
			if e != nil {
				t.Fatal(e)
			}
			if base64.StdEncoding.EncodeToString(b) != tc.Roundtrip {
				t.Fatalf("source roundtrip differs %q", b)
			}
		})
	}
}

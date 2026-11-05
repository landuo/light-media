package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

func TestOriginalHostNetworkVendorDecoderEdges(t *testing.T) {
	var oracle struct {
		Cases []struct {
			Name, Encoding string
			Wire           string  `json:"wire_b64"`
			Content        *string `json:"content_b64"`
			Exception      *string
		}
	}
	raw, err := os.ReadFile("../../testdata/plugin-host-network-20261031/vendor-edges-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 15 {
		t.Fatal("actual vendor edge oracle unavailable", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			wire, _ := base64.StdEncoding.DecodeString(sample.Wire)
			value, err := originalHostNetworkDecode(wire, sample.Encoding)
			if sample.Exception != nil {
				if err == nil {
					t.Fatal("source content decoding failure accepted")
				}
				return
			}
			want, _ := base64.StdEncoding.DecodeString(*sample.Content)
			if err != nil || !bytes.Equal(value, want) {
				t.Fatalf("vendor content mismatch: got=%x want=%x err=%v", value, want, err)
			}
		})
	}
}

package torrenttransfer

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestInstalledVendorSkipWireOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/vendor-skip-wire.json")
	if e != nil {
		t.Fatal(e)
	}
	var oracle struct {
		Version string `json:"vendor_version"`
		Cases   []struct {
			InputJSON string   `json:"input_json"`
			Wire      []string `json:"wire_values"`
		}
	}
	if e = json.Unmarshal(raw, &oracle); e != nil {
		t.Fatal(e)
	}
	if oracle.Version != "2026.6.0" || len(oracle.Cases) < 33 {
		t.Fatal("wrong installed-vendor capture")
	}
	for _, c := range oracle.Cases {
		t.Run(c.InputJSON, func(t *testing.T) {
			got, e := sourceSkipWire(json.RawMessage(c.InputJSON), nil)
			if e != nil {
				t.Fatal(e)
			}
			if len(c.Wire) == 0 {
				if got != nil {
					t.Fatal("None was not omitted", *got)
				}
				return
			}
			if got == nil || *got != c.Wire[0] {
				t.Fatalf("source multipart %q, got %v", c.Wire, got)
			}
		})
	}
}
func TestRawSkipOrderSurvivesObjectAndConfigSave(t *testing.T) {
	s := newSDKFixture(t, "qbittorrent", "qbittorrent")
	p := New()
	config := s.config()
	raw, _ := json.Marshal(config)
	raw = json.RawMessage(strings.Replace(string(raw), `"skipverify":false`, `"skipverify":{"z":1,"a":["quote'",true]}`, 1))
	if _, e := p.Handle(context.Background(), "init", raw, s.host); e != nil {
		t.Fatal(e)
	}
	c := p.currentConfig()
	wire, e := sourceSkipWire(c.skipRaw, c.values["skipverify"])
	if e != nil || wire == nil || *wire != `{'z': 1, 'a': ["quote'", True]}` {
		t.Fatal("input dictionary order lost", wire, e)
	}
	saved, _ := json.Marshal(c.saveConfigValue(map[string]any{"skipverify": c.values["skipverify"]}))
	if !strings.Contains(string(saved), `"z":1,"a"`) {
		t.Fatal("update_config reordered raw skip value", string(saved))
	}
}
func TestWireSurrogatesMatchStringVersusContainerEncoding(t *testing.T) {
	if _, e := sourceSkipWire(json.RawMessage(`"\ud800"`), nil); e == nil {
		t.Fatal("unpaired top-level surrogate encoded to replacement")
	}
	for _, c := range []struct{ raw, want string }{{`["\ud800"]`, `['\ud800']`}, {`"\ud83d\ude00"`, "😀"}} {
		got, e := sourceSkipWire(json.RawMessage(c.raw), nil)
		if e != nil || got == nil || *got != c.want {
			t.Fatal(c.raw, got, e)
		}
	}
}

package subscribegroup

import (
	"encoding/json"
	"os"
	"testing"
)

func TestOriginalUnicodeParserContract(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/plugins/subscribe-group-parser-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SHA   string `json:"source_sha256"`
		Cases []struct {
			Input      string `json:"input"`
			Resolution string `json:"resolution"`
			Quality    string `json:"quality"`
			Effect     string `json:"effect"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.SHA != "06870f20e39237e4a69c9615ce005411feaa5e1a97cad50fd3b7b0645bead383" || len(fixture.Cases) != 51 {
		t.Fatal("incomplete pinned parser oracle")
	}
	for _, c := range fixture.Cases {
		t.Run(c.Input, func(t *testing.T) {
			for name, pair := range map[string][2]string{
				"resolution": {ParseResolution(c.Input), c.Resolution},
				"quality":    {ParseQuality(c.Input), c.Quality},
				"effect":     {ParseEffect(c.Input), c.Effect},
			} {
				if pair[0] != pair[1] {
					t.Errorf("%s got %q; original %q", name, pair[0], pair[1])
				}
			}
		})
	}
}

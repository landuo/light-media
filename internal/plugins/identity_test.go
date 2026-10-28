package plugins

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCloneIdentityFixedPythonHTTPOracle(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/standard-clone-suffix/source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var oracle struct {
		Cases []struct {
			Suffix          string
			Source          string `json:"source_id"`
			Target          string `json:"target_id"`
			Success, Loaded bool
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 28 {
		t.Fatal("fixed source cases")
	}
	for _, row := range oracle.Cases {
		if row.Suffix == "" {
			continue
		}
		t.Run(row.Suffix, func(t *testing.T) {
			if got := row.Source + OriginalPluginLower(row.Suffix); got != row.Target {
				t.Fatalf("Python full lowercase mismatch %q %q", got, row.Target)
			}
			if got := ValidInstanceID(row.Target); got != row.Success {
				t.Fatalf("actual Linux component boundary differs %v %v", got, row.Success)
			}
			if row.Success {
				m := fixtureManifest()
				m.PluginID = row.Target
				if err := m.Validate(); err != nil {
					t.Fatal("source-safe identity manifest rejected", err)
				}
			}
			if row.Success && OriginalPluginClassLoadable(row.Target) != row.Loaded {
				t.Fatal("fixed compiler/exact class lookup differs")
			}
		})
	}
	for _, id := range []string{"../escape", "A/escape", "A\\escape", "A\x00", "A\n", string([]byte{'A', 255}), strings.Repeat("A", 256)} {
		if ValidInstanceID(id) {
			t.Fatalf("unsafe component accepted %q", id)
		}
	}
}

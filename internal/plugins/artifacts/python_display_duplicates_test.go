package artifacts

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestPythonDisplayDuplicateLastValueFullSourceWire(t *testing.T) {
	var input struct {
		Cases []struct {
			Name        string
			PackageJSON string `json:"package_json"`
		}
	}
	var source struct {
		Observations []struct {
			Name, Band string
			Rows       []map[string]json.RawMessage
		}
	}
	raw, err := os.ReadFile("../../../testdata/market-selection-collision/half-package/duplicate-wire-inputs.json")
	if err != nil || json.Unmarshal(raw, &input) != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("../../../testdata/market-selection-collision/half-package/duplicate-wire-oracle.json")
	if err != nil || json.Unmarshal(raw, &source) != nil {
		t.Fatal(err)
	}
	for _, example := range input.Cases {
		for _, band := range []string{"base", "higher"} {
			t.Run(example.Name+band, func(t *testing.T) {
				values, err := discoverPythonMetadataForVersion([]byte(example.PackageJSON), "https://github.com/Owned/Duplicate", band == "base")
				if err != nil || len(values) != 2 {
					t.Fatal(err)
				}
				for index, value := range values {
					display := value.SourceDisplay
					if err = display.WireError(); err != nil {
						t.Fatal("overwritten source value leaked its error", err)
					}
					var want map[string]json.RawMessage
					for _, o := range source.Observations {
						if o.Name == example.Name && o.Band == band {
							want = o.Rows[index]
						}
					}
					if want == nil {
						t.Fatal("source oracle missing")
					}
					decode := func(raw json.RawMessage) any {
						d := json.NewDecoder(bytes.NewReader(raw))
						d.UseNumber()
						var v any
						if err := d.Decode(&v); err != nil {
							t.Fatal(err)
						}
						return v
					}
					for key, raw := range display.Values() {
						if !reflect.DeepEqual(decode(raw), decode(want[key])) {
							t.Fatalf("%s mismatch", key)
						}
					}
				}
			})
		}
	}
	// Negative unit guards: the last value, and every surviving bad key/array
	// member, still prevents rendering. These are not additional real-source runs.
	for _, value := range []string{`{"x":"ok","x":"A\ud800"}`, `{"\ud800":1,"\ud800":2}`, `["A\ud800"]`} {
		d := &PythonDisplay{Name: json.RawMessage(value), Version: json.RawMessage(`"1"`)}
		if err := d.initialize(); err != nil || d.WireError() == nil {
			t.Fatal("surviving invalid source value was discarded", value, err)
		}
	}
}

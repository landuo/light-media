package artifacts

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPythonDisplayCompleteSourceWireSixFields(t *testing.T) {
	var input struct {
		Cases []struct {
			Name    string
			Package json.RawMessage
		}
	}
	var source struct {
		Observations []struct {
			Name, Band string
			Rows       []map[string]any
		}
	}
	raw, err := os.ReadFile("../../../testdata/market-selection-collision/half-package/mixed-entry-inputs.json")
	if err != nil || json.Unmarshal(raw, &input) != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("../../../testdata/market-selection-collision/half-package/display-wire-oracle.json")
	if err != nil || json.Unmarshal(raw, &source) != nil {
		t.Fatal(err)
	}
	for _, example := range input.Cases {
		for _, band := range []string{"base", "higher"} {
			t.Run(example.Name+"/"+band, func(t *testing.T) {
				values, err := discoverPythonMetadataForVersion(example.Package, "https://github.com/Owned/Wire", band == "base")
				if err != nil {
					t.Fatal(err)
				}
				actual := []map[string]any{}
				for _, value := range values {
					if value.SourceDisplay == nil || value.Current.Manifest.Runtime != "python" || value.UnavailableReason == "" || len(value.Releases) != 0 || value.Current.HostContract != "" {
						t.Fatal("display became executable metadata", value)
					}
					row := map[string]any{"id": value.PluginID, "add_time": float64(value.AddTime)}
					for key, raw := range value.SourceDisplay.Values() {
						var decoded any
						if err := json.Unmarshal(raw, &decoded); err != nil {
							t.Fatal(err)
						}
						row[key] = decoded
					}
					actual = append(actual, row)
				}
				var want []map[string]any
				found := false
				for _, v := range source.Observations {
					if v.Name == example.Name && v.Band == band {
						want = v.Rows
						found = true
						break
					}
				}
				if !found || !reflect.DeepEqual(actual, want) {
					t.Fatalf("source wire mismatch actual=%#v want=%#v", actual, want)
				}
			})
		}
	}
}

func TestPythonDisplayNeverAcquiresExecutable(t *testing.T) {
	req, client, _, hits := fixture(t, []byte("owned-inert-never-fetched"), "executable")
	raw := []byte(`{"Owned":{"name":["Owned"],"version":2,"history":["note"],"v2":true}}`)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(raw) }))
	defer peer.Close()
	req.RepositoryURL = peer.URL
	rows, err := DiscoverRepository(context.Background(), peer.URL, req.Platform, req.Validate, client)
	if err != nil || len(rows) != 1 || rows[0].UnavailableReason == "" {
		t.Fatal("display metadata unavailable", err)
	}
	if _, err = Acquire(context.Background(), req, client); err == nil {
		t.Fatal("Python display acquired as a Go artifact")
	}
	if *hits != 0 {
		t.Fatal("display triggered artifact download")
	}
	if _, err = os.Stat(filepath.Join(req.Root, req.PluginID)); !os.IsNotExist(err) {
		t.Fatal("display preflight wrote executable", err)
	}
}

func TestPythonDisplaySourceJSONNumbers(t *testing.T) {
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
	raw, err := os.ReadFile("../../../testdata/market-selection-collision/half-package/number-wire-inputs.json")
	if err != nil || json.Unmarshal(raw, &input) != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("../../../testdata/market-selection-collision/half-package/number-wire-oracle.json")
	if err != nil || json.Unmarshal(raw, &source) != nil {
		t.Fatal(err)
	}
	for _, example := range input.Cases {
		for _, band := range []string{"base", "higher"} {
			t.Run(example.Name+band, func(t *testing.T) {
				values, err := discoverPythonMetadataForVersion([]byte(example.PackageJSON), "https://github.com/Owned/Numbers", band == "base")
				if err != nil || len(values) != 1 {
					t.Fatal(err)
				}
				var want map[string]json.RawMessage
				for _, o := range source.Observations {
					if o.Name == example.Name && o.Band == band {
						want = o.Rows[0]
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
				for key, raw := range values[0].SourceDisplay.Values() {
					if !reflect.DeepEqual(decode(raw), decode(want[key])) {
						t.Fatalf("%s wire number mismatch: %s != %s", key, raw, want[key])
					}
				}
				if example.Name == "overflow-exponent" && values[0].SourceDisplay.VersionKey != "inf" {
					t.Fatal("wire null corrupted source infinity key")
				}
			})
		}
	}
}

func TestPythonDisplaySurrogateKeysAndWireFailure(t *testing.T) {
	var input struct {
		Cases []struct {
			Name     string
			Packages map[string]json.RawMessage
		}
	}
	var source struct {
		Observations []struct {
			Name string
			Keys []struct {
				Band, ID string
				Key      string `json:"key_surrogatepass_hex"`
			}
		}
	}
	raw, err := os.ReadFile("../../../testdata/market-selection-collision/half-package/surrogate-selector-inputs.json")
	if err != nil || json.Unmarshal(raw, &input) != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("../../../testdata/market-selection-collision/half-package/surrogate-selector-oracle.json")
	if err != nil || json.Unmarshal(raw, &source) != nil {
		t.Fatal(err)
	}
	for _, example := range input.Cases {
		for _, band := range []string{"higher", "base"} {
			values, err := discoverPythonMetadataForVersion(example.Packages[band], "https://github.com/Owned/Surrogate", band == "base")
			if err != nil || len(values) != 1 {
				t.Fatal(err)
			}
			want := ""
			for _, o := range source.Observations {
				if o.Name == example.Name {
					for _, k := range o.Keys {
						if k.Band == band {
							want = k.Key
						}
					}
				}
			}
			if got := hex.EncodeToString([]byte(values[0].PluginID + values[0].SourceDisplay.VersionKey)); got != want {
				t.Fatalf("%s/%s key %s != %s", example.Name, band, got, want)
			}
			if (values[0].SourceDisplay.WireError() != nil) != (band == "base") {
				t.Fatal("source rendering failure not retained separately from successful parsing")
			}
		}
	}
	for _, value := range []string{`"A\\ud800"`, `"\ud83d\ude00"`, `"中文"`} {
		display := &PythonDisplay{Name: json.RawMessage(value), Version: json.RawMessage(`"1"`)}
		if err := display.initialize(); err != nil || display.WireError() != nil {
			t.Fatal("valid escaped/pair/unicode rejected", err)
		}
	}
}

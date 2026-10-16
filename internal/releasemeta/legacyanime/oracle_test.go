package legacyanime

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// Decode one case at a time: the reference includes every source stage, but
// testing it must not require retaining all expanded token graphs in memory.
func TestInstalledCompletePipeline(t *testing.T) {
	file, err := os.Open("../../../testdata/plugins/release-meta-legacy-anitopy-oracle.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	decoder := json.NewDecoder(reader)
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	count := 0
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			t.Fatal(err)
		}
		if key != "cases" {
			var discard json.RawMessage
			if err := decoder.Decode(&discard); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if _, err := decoder.Token(); err != nil {
			t.Fatal(err)
		}
		for decoder.More() {
			var sample struct {
				Name    string         `json:"name"`
				Title   string         `json:"title"`
				Options Options        `json:"options"`
				Error   string         `json:"error"`
				Stages  []Stage        `json:"stages"`
				Result  map[string]any `json:"result"`
				Final   Stage          `json:"final_state"`
			}
			if err := decoder.Decode(&sample); err != nil {
				t.Fatal(err)
			}
			count++
			t.Run(sample.Name, func(t *testing.T) {
				sample.Options.Trace = true
				actual, err := Parse(sample.Title, sample.Options)
				message := ""
				if err != nil {
					message = err.Error()
				}
				if message != sample.Error {
					t.Fatalf("error=%q want %q; title=%q", message, sample.Error, sample.Title)
				}
				if len(actual.Stages) != len(sample.Stages) {
					t.Fatalf("stage count %d != %d", len(actual.Stages), len(sample.Stages))
				}
				for i := range sample.Stages {
					if !reflect.DeepEqual(actual.Stages[i], sample.Stages[i]) {
						t.Fatalf("source stages differ; title=%q\ngot:%+v\nwant:%+v", sample.Title, actual.Stages, sample.Stages)
					}
				}
				if !reflect.DeepEqual(actual.FinalState, sample.Final) {
					t.Fatalf("source final state differs; title=%q", sample.Title)
				}
				actualJSON, _ := json.Marshal(actual.Dictionary)
				expectedJSON, _ := json.Marshal(sample.Result)
				if !bytes.Equal(actualJSON, expectedJSON) {
					t.Fatalf("source dictionary differs; title=%q\ngot:%+v\nwant:%+v", sample.Title, actual.Dictionary, sample.Result)
				}
			})
		}
		if _, err := decoder.Token(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := decoder.Token(); err != nil {
		t.Fatal(err)
	}
	if count != 10271 {
		t.Fatalf("reference count=%d want10271", count)
	}
}

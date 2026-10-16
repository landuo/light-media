package rustanime

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"testing"
)

// The expectations are actual pinned Rust execution, not duplicated Go rules.
func TestOriginalSourcePipeline(t *testing.T) {
	compressed, err := os.ReadFile("../../../testdata/plugins/release-meta-rust-anitomy-oracle.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name    string  `json:"name"`
			Title   string  `json:"title"`
			Options Options `json:"options"`
			Result  Result  `json:"result"`
			Error   string  `json:"error"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, test := range oracle.Cases {
		t.Run(test.Name, func(t *testing.T) {
			test.Options.Trace = len(test.Result.Stages) > 0
			actual, err := Parse(test.Title, test.Options)
			message := ""
			if err != nil {
				message = err.Error()
			}
			if message != test.Error {
				t.Fatalf("error: got %q, want %q; title=%q", message, test.Error, test.Title)
			}
			if message != "" {
				return
			}
			if len(actual.Stages) != len(test.Result.Stages) {
				t.Fatalf("stages: got %d, want %d; title=%q", len(actual.Stages), len(test.Result.Stages), test.Title)
			}
			for i, want := range test.Result.Stages {
				got := actual.Stages[i]
				if !reflect.DeepEqual(got, want) {
					gotJSON, _ := json.Marshal(got)
					wantJSON, _ := json.Marshal(want)
					t.Fatalf("first differing stage %s; title=%q\ngot: %s\nwant:%s", want.Name, test.Title, gotJSON, wantJSON)
				}
			}
			if !reflect.DeepEqual(actual.Elements, test.Result.Elements) {
				t.Fatalf("final: got %#v, want %#v; title=%q", actual.Elements, test.Result.Elements, test.Title)
			}
		})
	}
}

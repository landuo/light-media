package priorityrules

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestInstalledPythonAndRustReference(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugins/priority-rule-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SHA     map[string]string `json:"source_sha256"`
		Version string            `json:"rust_version"`
		Builtin json.RawMessage   `json:"builtin"`
		Cases   []struct {
			Name     string                `json:"name"`
			Groups   []Group               `json:"groups"`
			Torrents []Torrent             `json:"torrents"`
			Custom   map[string]Definition `json:"custom"`
			Media    map[string]any        `json:"media"`
			Selected []string              `json:"selected"`
			Results  map[string]struct {
				Matches []Match `json:"matches"`
				Error   string  `json:"error"`
			} `json:"results"`
		} `json:"cases"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&fixture) != nil || fixture.Version != "0.2.6" || len(fixture.Cases) != 202 || fixture.SHA["module"] != "78f7741b8782ed23a8ef94a18afc14038366b832d9a0907cce9274664052abe8" || fixture.SHA["helper"] != "271909d15a4576a67310485066d655d68f946615c30add8de02b662cdf442c7d" {
		t.Fatal("invalid installed source reference")
	}
	var expectedBuiltin map[string]Definition
	d := json.NewDecoder(strings.NewReader(string(fixture.Builtin)))
	d.UseNumber()
	if d.Decode(&expectedBuiltin) != nil || !reflect.DeepEqual(Builtins(), expectedBuiltin) {
		t.Fatal("builtin rules diverged")
	}
	for _, sample := range fixture.Cases {
		for _, path := range []struct {
			Name string
			Mode Mode
		}{{"python", Python}, {"rust", Rust}} {
			t.Run(sample.Name+"/"+path.Name, func(t *testing.T) {
				var media *Media
				if sample.Media != nil {
					media = &Media{Type: "电视剧", Attributes: sample.Media}
					if kind, ok := sample.Media["type"].(string); ok {
						media.Type = kind
					}
					if category, ok := sample.Media["category"].(string); ok {
						media.Category = &category
					}
				}
				engine, err := NewWithDefinitions(sample.Groups, sample.Custom, path.Mode)
				if err != nil {
					t.Fatal(err)
				}
				result, err := engine.Filter(context.Background(), sample.Selected, sample.Torrents, media)
				expected := sample.Results[path.Name]
				if expected.Error != "" {
					if err == nil || err.Error() != expected.Error {
						t.Fatalf("error %v != source %s", err, expected.Error)
					}
					return
				}
				if err != nil || !reflect.DeepEqual(result, expected.Matches) {
					t.Fatalf("matches %+v, %v != source %+v", result, err, expected.Matches)
				}
			})
		}
	}
}
func TestRuleSnapshotConcurrencyAndCancellation(t *testing.T) {
	text := "WEBDL"
	groups := []Group{{Name: "main", RuleString: &text}}
	rules := map[string]Definition{"WEBDL": {"include": "WEB-DL"}}
	e, err := NewWithDefinitions(groups, rules, Rust)
	if err != nil {
		t.Fatal(err)
	}
	text = "MISSING"
	rules["WEBDL"]["include"] = "NEVER"
	var workers sync.WaitGroup
	for i := 0; i < 20; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			matched, err := e.Filter(context.Background(), []string{"main"}, []Torrent{{Title: "WEB-DL"}}, nil)
			if err != nil || len(matched) != 1 || matched[0].Priority != 100 {
				t.Error("shared filter snapshot changed", matched, err)
			}
		}()
	}
	workers.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = e.Filter(ctx, []string{"main"}, []Torrent{{Title: "WEB-DL"}}, nil); err != context.Canceled {
		t.Fatal("cancelled filtering continued", err)
	}
}

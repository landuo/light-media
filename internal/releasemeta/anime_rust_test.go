package releasemeta

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// This supplemental installed-wrapper oracle also asserts the name/type/episode
// dependencies consumed by Rust's default-season decision. It does not turn the
// ordinary-video identity parser into a proven full port.
func TestInstalledRustAnimeFullPipelineWrapper(t *testing.T) {
	data, err := os.ReadFile("../../testdata/plugins/release-meta-rust-anime-wrapper-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Cases []struct {
			Name     string  `json:"name"`
			Title    string  `json:"title"`
			Subtitle *string `json:"subtitle"`
			Options  Options `json:"options"`
			Rust     struct {
				Error  string   `json:"error"`
				Result Metadata `json:"result"`
			} `json:"rust"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			sample.Options.SourceMode = "rust"
			got, err := Parse(sample.Title, sample.Subtitle, sample.Options)
			if sample.Rust.Error != "" {
				if err == nil {
					t.Fatal("source rejected input but Go accepted it")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, sample.Rust.Result) {
				t.Fatalf("title=%q\ngot: %+v\nwant:%+v", sample.Title, got, sample.Rust.Result)
			}
		})
	}
}

func TestInstalledRustRoutingAndChineseNumberBoundaries(t *testing.T) {
	data, err := os.ReadFile("../../testdata/plugins/releasemeta-rust-reference/routing-numbers.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Cases []struct {
			Name, Title string
			Subtitle    *string
			Rust        struct {
				Error  string
				Result Metadata
			}
		}
		Routes []struct {
			Title string
			Anime bool `json:"is_anime"`
		}
	}
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	if len(reference.Cases) != 36 || len(reference.Routes) != 1531 {
		t.Fatalf("incomplete installed reference: wrappers=%d routes=%d", len(reference.Cases), len(reference.Routes))
	}
	for _, sample := range reference.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			got, err := Parse(sample.Title, sample.Subtitle, Options{SourceMode: "rust"})
			if sample.Rust.Error != "" {
				if err == nil {
					t.Fatal("source rejected input but Go accepted it")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, sample.Rust.Result) {
				t.Fatalf("title=%q\ngot: %+v\nwant:%+v", sample.Title, got, sample.Rust.Result)
			}
		})
	}
	for _, sample := range reference.Routes {
		if got := isAnime(sample.Title); got != sample.Anime {
			t.Fatalf("installed Rust route mismatch; title=%q got%t want%t", sample.Title, got, sample.Anime)
		}
	}
}

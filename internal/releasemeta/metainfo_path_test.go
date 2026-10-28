package releasemeta

import (
	"os"
	"testing"
)

func TestMetaInfoPathFixedSourceAndPublicRustABI(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-event-target/metainfo-path-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		MediaExts []string `json:"media_exts"`
		Cases     []struct {
			Name, Path, Mode string
			Words            []string
			Local            *[]string `json:"local_words"`
			Expected         map[string]any
		}
	}
	decodeMetaInfoReference(t, raw, &reference)
	for _, sample := range reference.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			parser, err := NewMetaInfoParser(MetaInfoEnvironment{Options: Options{SourceMode: sample.Mode, CustomWords: sample.Words, MediaExts: reference.MediaExts}, StreamingPlatforms: map[string]string{}})
			if err != nil {
				t.Fatal(err)
			}
			object, err := parser.ParsePath(sample.Path, sample.Local)
			if err != nil {
				t.Fatal(err)
			}
			actual := make(map[string]any, len(sample.Expected))
			for key := range sample.Expected {
				actual[key] = object.Get(key)
			}
			checkMetaInfoReference(t, "fixed path fields", actual, sample.Expected)
		})
	}
}

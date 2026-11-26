package releasemeta

import (
	"bytes"
	"math/big"
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

func TestSiblingPathParserKeepsEpisodeOverridesIndependent(t *testing.T) {
	for _, mode := range []string{"legacy", "rust"} {
		t.Run(mode, func(t *testing.T) {
			p, err := NewMetaInfoParser(MetaInfoEnvironment{Options: Options{SourceMode: mode, MediaExts: []string{"mkv", "srt"}}})
			if err != nil {
				t.Fatal(err)
			}
			parse := p.SiblingPathParser()
			for _, path := range []string{"/Fallout.2024.2160p.BluRay.DoVi/Season.2/Fallout.S02E01.Part1.mkv", "/Fallout.2024.2160p.BluRay.DoVi/Season.2/Fallout.S02E02E03.Part2.mkv", "/Fallout.2024.2160p.BluRay.DoVi/Season.3/Fallout.S03E01.HDR.mkv", "/Other.TV.1080p/Season.2/双语.srt", "/Fallout.2024.2160p.BluRay.DoVi/Season.2/Fallout.S02E08.mkv"} {
				want, err := p.ParsePath(path, nil)
				if err != nil {
					t.Fatal(err)
				}
				got, err := parse(path)
				if err != nil {
					t.Fatal(err)
				}
				w, _ := want.EncodeState()
				g, _ := got.EncodeState()
				if !bytes.Equal(w, g) {
					t.Fatalf("%s differs\n%s\n%s", path, w, g)
				}
				got.Set("part", "changed")
				if n, ok := got.Get("begin_season").(*big.Int); ok {
					n.SetInt64(123)
				}
			}
		})
	}
}

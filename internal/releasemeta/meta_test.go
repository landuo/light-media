package releasemeta

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestInstalledOriginalResourceMetadataOracle(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugins/release-meta-original-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name     string
			Title    string
			Subtitle *string
			Options  struct {
				CustomWords         []string `json:"custom_words"`
				CustomReleaseGroups []string `json:"release_groups"`
				Customization       []string `json:"customization"`
			}
			Legacy              Metadata
			Rust                Metadata
			LegacyError         string  `json:"legacy_error"`
			RustError           string  `json:"rust_error"`
			LegacyPreparedTitle *string `json:"legacy_prepared_title"`
		}
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, sample := range fixture.Cases {
		for _, mode := range []string{"rust", "legacy"} {
			t.Run(sample.Name+"/"+mode, func(t *testing.T) {
				if mode == "legacy" && sample.LegacyPreparedTitle != nil {
					if got := prepareAnimeTitle(sample.Title, true); got != *sample.LegacyPreparedTitle {
						t.Errorf("source category title preparation %q != %q", got, *sample.LegacyPreparedTitle)
					}
				}
				got, err := Parse(sample.Title, sample.Subtitle, Options{SourceMode: mode, CustomWords: sample.Options.CustomWords, ReleaseGroups: sample.Options.CustomReleaseGroups, Customization: sample.Options.Customization})
				wantError := sample.RustError
				if mode == "legacy" {
					wantError = sample.LegacyError
				}
				if wantError != "" {
					if err == nil {
						t.Fatal("source rejects this configuration; Go accepted it")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				want := sample.Rust
				if mode == "legacy" {
					want = sample.Legacy
				}
				// Name, kind, type and episode details are diagnostic context, not
				// part of the downstream SubscribeGroup metadata projection.
				got.Name, got.Kind, got.Type, got.BeginEpisode, got.EndEpisode = "", "", "", nil, nil
				want.Name, want.Kind, want.Type, want.BeginEpisode, want.EndEpisode = "", "", "", nil, nil
				if !reflect.DeepEqual(got, want) {
					t.Errorf("title=%q\n got: %+v\nwant: %+v", sample.Title, got, want)
				}
			})
		}
	}
}

func TestSharedSourceExpressionsAreSafeForConcurrentPluginEvents(t *testing.T) {
	options := Options{SourceMode: "rust", ReleaseGroups: []string{"MySynthetic"}, Customization: []string{"CustomA", "CustomB"}}
	var group sync.WaitGroup
	for i := 0; i < 24; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for repeat := 0; repeat < 4; repeat++ {
				got, err := Parse("Synthetic Show S04E02 2160p UHD BluRay REMUX HDR10 CustomB CustomA-MySynthetic.mkv", nil, options)
				if err != nil || got.Season != "S04" || got.Resolution != "2160p" || got.Quality != "UHD BluRay REMUX" || got.Team != "MySynthetic" || got.Customization != "CustomA@CustomB" {
					t.Errorf("concurrent metadata=%+v error=%v", got, err)
				}
			}
		}()
	}
	group.Wait()
}

func TestMetadataRejectsInvalidInputsWithoutReturningGuessesOrPatterns(t *testing.T) {
	for _, options := range []Options{{SourceMode: "future-mode"}, {SourceMode: "legacy", ReleaseGroups: []string{"private-source-pattern["}}, {SourceMode: "legacy", Customization: []string{"private-source-pattern["}}, {Customization: []string{strings.Repeat("x", 64<<10+1)}}} {
		got, err := Parse("Synthetic Film 2024 1080p WEB-DL", nil, options)
		if err == nil || !reflect.DeepEqual(got, Metadata{}) || strings.Contains(err.Error(), "private-source-pattern") {
			t.Fatalf("invalid configuration must return only a safe error: %+v %v", got, err)
		}
	}
	if got, err := Parse(strings.Repeat("x", 64<<10+1), nil, Options{}); err == nil || !reflect.DeepEqual(got, Metadata{}) {
		t.Fatalf("oversized remote title accepted: %+v %v", got, err)
	}
}

func TestConfiguredExtensionsPreserveSourceFileRangeRules(t *testing.T) {
	for _, sample := range []struct {
		extensions []string
		want       string
	}{{nil, "S01-S03"}, {[]string{".synthetic"}, "S01"}, {[]string{}, "S01-S03"}} {
		got, err := Parse("Synthetic Show S01-S03.synthetic", nil, Options{SourceMode: "rust", MediaExts: sample.extensions})
		if err != nil || got.Season != sample.want {
			t.Fatalf("configured extension lost original file/range distinction: %+v %v", got, err)
		}
	}
}

func TestFrozenOptionsRetainExplicitEmptyExtensionOverride(t *testing.T) {
	for _, sample := range []struct {
		extensions []string
		want       string
	}{{nil, "S01"}, {[]string{}, "S01-S03"}} {
		encoded, err := json.Marshal(Options{SourceMode: "rust", MediaExts: sample.extensions})
		if err != nil {
			t.Fatal(err)
		}
		var restored Options
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		got, err := Parse("Synthetic Show S01-S03.mkv", nil, restored)
		if err != nil || got.Season != sample.want {
			t.Fatalf("frozen extension override changed source season semantics: %+v %v", got, err)
		}
	}
}

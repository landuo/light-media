package media

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestTMDBCompatibilityIDUsesSameValidatedValue(t *testing.T) {
	parentID, episodeID := "1418", "972676"
	for _, tc := range []struct {
		name string
		meta NFOMetadata
		want string
	}{
		{"movie", NFOMetadata{Kind: "movie", Title: "合成电影", UniqueIDs: []UniqueID{{Type: "tmdb", Default: true, Value: "123"}}}, "123"},
		{"parent-show", NFOMetadata{Kind: "tvshow", Title: "合成父剧", UniqueIDs: []UniqueID{{Type: "tmdb", Default: true, Value: parentID}}}, parentID},
		{"episode-own-id", NFOMetadata{Kind: "episode", Title: "合成单集", ShowTitle: "合成父剧", Season: 7, Episode: 17, UniqueIDs: []UniqueID{{Type: "tmdb", Default: true, Value: episodeID}}}, episodeID},
		{"tmdb-not-default", NFOMetadata{Kind: "movie", Title: "合成电影", UniqueIDs: []UniqueID{{Type: "synthetic-other", Default: true, Value: "other-id"}, {Type: "tmdb", Value: "456"}}}, "456"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := EncodeNFO(tc.meta)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				TMDBIDs   []string   `xml:"tmdbid"`
				UniqueIDs []UniqueID `xml:"uniqueid"`
			}
			if err := xml.Unmarshal(b, &doc); err != nil || len(doc.TMDBIDs) != 1 || doc.TMDBIDs[0] != tc.want {
				t.Fatalf("legacy ID differs from confirmed input: %s, %v", b, err)
			}
			found := false
			for _, id := range doc.UniqueIDs {
				if id.Type == "tmdb" {
					found = true
					if id.Value != doc.TMDBIDs[0] {
						t.Fatalf("TMDB assertions conflict: %s", b)
					}
				}
			}
			if !found || (tc.meta.Kind == "episode" && doc.TMDBIDs[0] == parentID) {
				t.Fatal("episode's own source identity was replaced with parent identity")
			}
		})
	}
}

func TestCustomOnlyNFOBytesUnchangedAndNoTMDBID(t *testing.T) {
	for _, kind := range []string{"movie", "tvshow", "episode"} {
		m := NFOMetadata{Kind: kind, Title: "合成电影", Year: 2024, UniqueIDs: []UniqueID{{Type: "lightmedia-m0", Default: true, Value: "synthetic:900000101"}}}
		if kind == "episode" {
			m.Season, m.Episode = 1, 2
		}
		b, err := EncodeNFO(m)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "<tmdbid>") {
			t.Fatal("custom identifier was promoted to TMDB")
		}
		if kind == "movie" {
			// Exact previous serialization: adding an omitted compatibility
			// field must not change custom-only fixture XML bytes/hashes.
			want := xml.Header + "<movie>\n  <title>合成电影</title>\n  <year>2024</year>\n  <uniqueid type=\"lightmedia-m0\" default=\"true\">synthetic:900000101</uniqueid>\n</movie>\n"
			if string(b) != want {
				t.Fatalf("custom-only bytes changed: %s", b)
			}
		}
	}
}

func TestInvalidTMDBStillRejectsEntireNFO(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "1.5", "invalid", " 123", "18446744073709551616"} {
		t.Run(value, func(t *testing.T) {
			b, err := EncodeNFO(NFOMetadata{Kind: "movie", Title: "合成电影", UniqueIDs: []UniqueID{{Type: "tmdb", Default: true, Value: value}}})
			if err == nil || b != nil {
				t.Fatalf("invalid TMDB value produced XML: %q", value)
			}
		})
	}
	if b, err := EncodeNFO(NFOMetadata{Kind: "movie", Title: "合成电影", UniqueIDs: []UniqueID{{Type: "tmdb", Default: true, Value: "123"}, {Type: "tmdb", Value: "456"}}}); err == nil || b != nil {
		t.Fatal("conflicting duplicate TMDB input produced XML")
	}
}

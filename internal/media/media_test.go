package media

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestConservativeFilenameParsing(t *testing.T) {
	cases := []struct {
		name     string
		status   Status
		kind     string
		episodes []int
	}{
		{"合成电影 (2024).mkv", Parsed, "movie", nil},
		{"Synthetic.Movie.2024.1080p.WEB-DL.mkv", Parsed, "movie", nil},
		{"合成剧 S01E02.mkv", Parsed, "episode", []int{2}},
		{"夜王 (2026) - 1080p.mkv", Parsed, "movie", nil},
		{"诛仙 - S04E05 - 第 5 集.mkv", Parsed, "episode", []int{5}},
		{"生活大爆炸 - S07E17 - 第 17 集.mkv", Parsed, "episode", []int{17}},
		{"诛仙 - S04E05 - 第 6 集.mkv", NeedsReview, "", nil},
		{"Show S04E05 EP05.mkv", Parsed, "episode", []int{5}},
		{"Show S04E05 EP06.mkv", NeedsReview, "", nil},
		{"Show S04E05 第六集.mkv", NeedsReview, "", nil},
		{"Show S04E05 第5集 第6集.mkv", NeedsReview, "", nil},
		{"Show S04E05 S04E05.mkv", NeedsReview, "", nil},
		{"Synthetic.Show.S01E01-E03.1080p.mkv", Parsed, "episode", []int{1, 2, 3}},
		{"Synthetic.Show.S01E01E03.mkv", Parsed, "episode", []int{1, 3}},
		{"合成国漫 EP101.mkv", Parsed, "episode", []int{101}},
		{"合成国漫 第101集.mkv", Parsed, "episode", []int{101}},
		{"合成国漫 101.mkv", NeedsReview, "", nil},
		{"Show S01E01 S02E02.mkv", NeedsReview, "", nil},
		{"Show S01E01-E00.mkv", NeedsReview, "", nil},
		{"Show S01E01-E.mkv", NeedsReview, "", nil},
		{"Show S01E01-E02-E03.mkv", NeedsReview, "", nil},
		{"Show S00E01.mkv", NeedsReview, "", nil},
		{"Show S01E00.mkv", NeedsReview, "", nil},
		{"电影 (2024) Episode 3.mkv", NeedsReview, "", nil},
		{"Title (1999) (2024).mkv", NeedsReview, "", nil},
		{"../Show S01E01.mkv", NeedsReview, "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Parse(tc.name)
			if r.Status != tc.status || r.Type != tc.kind {
				t.Fatalf("result %+v", r)
			}
			if tc.episodes != nil {
				if r.Original == nil || len(r.Original.Episodes) != len(tc.episodes) {
					t.Fatalf("numbering %+v", r.Original)
				}
				for i, n := range tc.episodes {
					if r.Original.Episodes[i] != n {
						t.Fatalf("numbering %+v", r.Original)
					}
				}
			}
		})
	}
}

func TestMappingScopeAndNoGuessing(t *testing.T) {
	offset := -100
	rule := MappingRule{ID: "synthetic-range", Revision: 1, MediaID: "synthetic:show", TargetSeason: 2, From: 101, To: 103, Source: Source{Site: "synthetic-site", Form: "absolute_episode"}, Offset: &offset, Confirmed: true}
	original := Numbering{Episodes: []int{101}, Form: "absolute_episode"}
	known := []Episode{{2, 1}, {2, 2}, {2, 3}}
	source := Source{Site: "synthetic-site"}
	r := Resolve(rule.MediaID, original, source, []MappingRule{rule}, known, nil)
	if r.Status != Parsed || r.Standard[0] != (Episode{2, 1}) || r.Original.Episodes[0] != 101 || r.Revision != 1 {
		t.Fatalf("%+v", r)
	}
	cases := []struct {
		name       string
		orig       Numbering
		src        Source
		rules      []MappingRule
		targets    []Episode
		correction []Episode
		reason     string
	}{
		{"range", Numbering{Episodes: []int{104}, Form: original.Form}, source, []MappingRule{rule}, known, nil, "outside_mapping_range"},
		{"partial", Numbering{Episodes: []int{103, 104}, Form: original.Form}, source, []MappingRule{rule}, known, nil, "range_crosses_mapping_scope"},
		{"source", original, Source{Site: "other"}, []MappingRule{rule}, known, nil, "mapping_required_for_absolute_numbering"},
		{"conflict", original, source, []MappingRule{rule, rule}, known, nil, "multiple_matching_rules"},
		{"unknown", original, source, []MappingRule{rule}, []Episode{{2, 2}}, nil, "unknown_target_episode"},
		{"correct-count", original, source, nil, known, []Episode{{2, 1}, {2, 2}}, "correction_count_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(rule.MediaID, tc.orig, tc.src, tc.rules, tc.targets, tc.correction)
			if got.Status != NeedsReview || got.Reason != tc.reason {
				t.Fatalf("%+v", got)
			}
		})
	}
	correction := Resolve(rule.MediaID, original, source, []MappingRule{rule, rule}, known, []Episode{{2, 3}})
	if correction.Status != Parsed || correction.Standard[0].Episode != 3 {
		t.Fatalf("explicit correction %+v", correction)
	}
	if Resolve("", original, source, nil, known, nil).Status != NeedsReview {
		t.Fatal("unconfirmed identity accepted")
	}
	pairs := MappingRule{ID: "pairs", Revision: 2, MediaID: rule.MediaID, TargetSeason: 2, From: 101, To: 103, Pairs: map[int]Episode{101: {2, 3}, 102: {2, 1}}, Confirmed: true}
	if got := Resolve(rule.MediaID, original, source, []MappingRule{pairs}, known, nil); got.Status != Parsed || got.Standard[0].Episode != 3 {
		t.Fatalf("pairs %+v", got)
	}
	if got := Resolve(rule.MediaID, Numbering{Episodes: []int{103}, Form: original.Form}, source, []MappingRule{pairs}, known, nil); got.Reason != "unmapped_original_episode" {
		t.Fatalf("pairs missing %+v", got)
	}
	pairs.Pairs[102] = Episode{2, 3}
	if pairs.Validate() == nil {
		t.Fatal("duplicate target accepted")
	}
}

func TestNFOXMLEscapingAndKind(t *testing.T) {
	for _, kind := range []string{"movie", "tvshow", "episode"} {
		t.Run(kind, func(t *testing.T) {
			m := NFOMetadata{Kind: kind, Title: "合成 <标题> & 测试", Year: 2024, UniqueIDs: []UniqueID{{Type: "tmdb", Default: true, Value: "123"}}, Actors: []Actor{{Name: "合成 & 演员", Role: "A < B", Order: 0}}}
			if kind == "episode" {
				m.Season, m.Episode = 2, 1
			}
			b, err := EncodeNFO(m)
			if err != nil {
				t.Fatal(err)
			}
			var doc nfoDocument
			if err := xml.Unmarshal(b, &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Title != m.Title || doc.Actors[0].Name != m.Actors[0].Name || len(doc.UniqueIDs) != 1 || !doc.UniqueIDs[0].Default {
				t.Fatalf("roundtrip %s", b)
			}
			if !strings.HasPrefix(string(b), xml.Header) {
				t.Fatal("missing UTF-8 header")
			}
		})
	}
	if _, err := EncodeNFO(NFOMetadata{Kind: "episode", Title: "x", UniqueIDs: []UniqueID{{"tmdb", true, "123"}}}); err == nil {
		t.Fatal("unknown episode accepted")
	}
}

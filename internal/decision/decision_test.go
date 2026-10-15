package decision

import (
	"strings"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/media"
	"light-media/internal/sites"
)

func movieTarget(auto bool) Target {
	return Target{Media: catalog.Media{ID: 1602727, MediaType: "movie", Title: "夜王", OriginalTitle: "Night King", Year: 2026}, Auto: auto}
}
func tvTarget(auto bool) Target {
	return Target{Media: catalog.Media{ID: 206484, MediaType: "tv", Title: "诛仙", OriginalTitle: "Jade Dynasty", Year: 2022}, Season: 4, Episodes: []int{5}, KnownEpisodes: []int{1, 2, 3, 4, 5, 6}, AiredEpisodes: []int{1, 2, 3, 4, 5}, Auto: auto}
}
func resource(title string) sites.Resource {
	return sites.Resource{ID: "12", SiteID: "mteam", Title: title, Seeders: 3, Size: 2 << 30, DownloadFactor: 0, Free: true}
}
func hasReason(d Decision, reason string) bool {
	for _, r := range d.Reasons {
		if r == reason {
			return true
		}
	}
	return false
}
func TestMovieIdentityAndYearConfirmation(t *testing.T) {
	for _, title := range []string{"夜王 (2026) 1080p WEB-DL H.265", "Night.King.2026.2160p.HEVC-Group", "夜王 Night King 2026 1080p", "[TEAM] Night.King.2026.1080p"} {
		d := Evaluate(resource(title), movieTarget(true), Policy{})
		if !d.Accepted || d.NeedsConfirmation {
			t.Errorf("%s: %+v", title, d)
		}
	}
	for _, tc := range []struct{ title, reason string }{{"夜王 1080p WEB-DL", "movie_year_unconfirmed"}, {"夜王 2025 1080p", "movie_year_conflict"}} {
		manual := Evaluate(resource(tc.title), movieTarget(false), Policy{})
		auto := Evaluate(resource(tc.title), movieTarget(true), Policy{})
		if !manual.Accepted || !manual.NeedsConfirmation || auto.Accepted || !hasReason(auto, tc.reason) {
			t.Errorf("manual=%+v auto=%+v", manual, auto)
		}
	}
	for _, title := range []string{"夜王2 2026 1080p", "Another.Night.King.2026.1080p", "夜王 S01E01 1080p"} {
		d := Evaluate(resource(title), movieTarget(false), Policy{})
		if d.Accepted {
			t.Errorf("wrong movie accepted %q %+v", title, d)
		}
	}
}
func TestYearInMovieTitleIsNotItsReleaseYear(t *testing.T) {
	target := Target{Media: catalog.Media{ID: 1, MediaType: "movie", Title: "2012", Year: 2009}, Auto: true}
	d := Evaluate(resource("2012.2009.1080p.BluRay"), target, Policy{})
	if !d.Accepted || d.NeedsConfirmation {
		t.Fatalf("title year lost: %+v", d)
	}
	d = Evaluate(resource("2012.1080p.BluRay"), target, Policy{})
	if d.Accepted || !hasReason(d, "movie_year_unconfirmed") {
		t.Fatalf("title year used as release year: %+v", d)
	}
}
func TestExplicitEpisodeAndAuthoritativeSets(t *testing.T) {
	d := Evaluate(resource("诛仙 S04E05 2160p WEB-DL H.265"), tvTarget(true), Policy{})
	if !d.Accepted || d.Season != 4 || len(d.Episodes) != 1 || d.Episodes[0] != 5 || d.NeedsConfirmation {
		t.Fatalf("valid episode: %+v", d)
	}
	d = Evaluate(resource("Jade.Dynasty.2022.S04E05.1080p"), tvTarget(true), Policy{})
	if !d.Accepted {
		t.Fatalf("original title failed: %+v", d)
	}
	for _, tc := range []struct{ title, reason string }{{"诛仙 S04E05-E06 1080p", "resource_episode_outside_target"}, {"诛仙 S04E06 1080p", "episode_not_confirmed_aired"}, {"诛仙 S03E05 1080p", "numbering_unknown_target_episode"}, {"诛仙 EP260 1080p", "numbering_mapping_required_for_absolute_numbering"}, {"诛仙 S04 2160p", "season_pack_requires_confirmation"}, {"诛仙 2022 1080p Complete", "episode_numbering_unresolved"}} {
		d = Evaluate(resource(tc.title), tvTarget(true), Policy{})
		if d.Accepted || !d.NeedsConfirmation || !hasReason(d, tc.reason) {
			t.Errorf("%s: %+v", tc.title, d)
		}
		target := tvTarget(false)
		d = Evaluate(resource(tc.title), target, Policy{})
		if !d.Accepted || !d.NeedsConfirmation {
			t.Errorf("manual not previewable %s: %+v", tc.title, d)
		}
	}
	wrong := Evaluate(resource("诛仙2 S04E05 1080p"), tvTarget(false), Policy{})
	if wrong.Accepted || !hasReason(wrong, "title_mismatch") {
		t.Fatalf("sequel accepted: %+v", wrong)
	}
}
func TestMappingRequiresConfirmedMatchingRuleAndKnownTarget(t *testing.T) {
	offset := -255
	target := tvTarget(true)
	target.Mappings = []media.MappingRule{{ID: "animation-absolute", Revision: 1, MediaID: "tv:206484", TargetSeason: 4, From: 256, To: 261, Offset: &offset, Confirmed: true, Source: media.Source{Site: "mteam", Form: "absolute_episode"}}}
	d := Evaluate(resource("诛仙 EP260 1080p"), target, Policy{})
	if !d.Accepted || len(d.Episodes) != 1 || d.Episodes[0] != 5 {
		t.Fatalf("mapping: %+v", d)
	}
	target.Mappings[0].Confirmed = false
	d = Evaluate(resource("诛仙 EP260 1080p"), target, Policy{})
	if d.Accepted || len(d.Episodes) != 0 {
		t.Fatalf("unconfirmed rule applied: %+v", d)
	}
	target.Mappings[0].Confirmed = true
	target.Mappings[0].MediaID = "tv:123"
	d = Evaluate(resource("诛仙 EP260 1080p"), target, Policy{})
	if d.Accepted {
		t.Fatalf("other work mapping applied: %+v", d)
	}
}
func TestEarlyReleaseCannotCreateMetadataEpisode(t *testing.T) {
	target := tvTarget(true)
	target.Episodes = []int{6}
	target.AllowEarly = []int{6}
	d := Evaluate(resource("诛仙 S04E06 1080p"), target, Policy{})
	if !d.Accepted {
		t.Fatalf("explicit known early permission ignored: %+v", d)
	}
	target.Episodes = []int{7}
	target.AllowEarly = []int{7}
	d = Evaluate(resource("诛仙 S04E07 1080p"), target, Policy{})
	if d.Accepted || !hasReason(d, "episode_not_in_authoritative_metadata") {
		t.Fatalf("calendar/override invented episode: %+v", d)
	}
	target = tvTarget(true)
	target.AiredEpisodes = nil
	d = Evaluate(resource("诛仙 S04E05 1080p"), target, Policy{})
	if d.Accepted || !hasReason(d, "episode_not_confirmed_aired") {
		t.Fatalf("missing air evidence accepted: %+v", d)
	}
	target = tvTarget(true)
	target.Episodes = nil
	d = Evaluate(resource("诛仙 S04E05 1080p"), target, Policy{})
	if d.Accepted || !hasReason(d, "automatic_target_episodes_required") {
		t.Fatalf("unset automatic targets accepted: %+v", d)
	}
}
func TestMinSeedersZeroAndPromotionUnknown(t *testing.T) {
	r := resource("夜王 2026 1080p")
	r.Seeders = 0
	if d := Evaluate(r, movieTarget(true), Policy{MinSeeders: 0}); !d.Accepted {
		t.Fatalf("explicit zero lost: %+v", d)
	}
	if d := Evaluate(r, movieTarget(true), Policy{MinSeeders: 1}); d.Accepted || !hasReason(d, "insufficient_seeders") {
		t.Fatalf("minimum ignored: %+v", d)
	}
	r.Seeders = -1
	if d := Evaluate(r, movieTarget(true), Policy{}); d.Accepted || !hasReason(d, "seeders_unknown") {
		t.Fatalf("unknown seeders accepted: %+v", d)
	}
	r = resource("夜王 2026 1080p")
	r.Free = false
	r.DownloadFactor = 0
	if d := Evaluate(r, movieTarget(true), Policy{FreeOnly: true}); d.Accepted || !hasReason(d, "free_promotion_not_confirmed") {
		t.Fatalf("default zero treated as free: %+v", d)
	}
	r.Free = true
	r.PromotionEnd = "2000-01-01T00:00:00Z"
	if d := Evaluate(r, movieTarget(true), Policy{FreeOnly: true}); d.Accepted || !hasReason(d, "free_promotion_expired") {
		t.Fatalf("expired free accepted: %+v", d)
	}
	r.PromotionEnd = "not-a-date"
	if d := Evaluate(r, movieTarget(true), Policy{FreeOnly: true}); d.Accepted || !hasReason(d, "promotion_expiry_unknown") {
		t.Fatalf("unknown expiry accepted: %+v", d)
	}
}
func TestQualitySizeAndRegexPolicies(t *testing.T) {
	r := resource("夜王 2026 1080p WEB-DL H.265")
	policy := Policy{MinSeeders: 1, Resolution: "1080p", Codec: "HEVC", MinBytes: 1 << 30, MaxBytes: 3 << 30, Include: `(?i)WEB[ ._-]?DL`, Exclude: `CAM|TS-Rip`, FreeOnly: true}
	if d := Evaluate(r, movieTarget(true), policy); !d.Accepted {
		t.Fatalf("valid quality denied: %+v", d)
	}
	for _, tc := range []struct {
		name   string
		change func(*Policy)
		reason string
	}{{"resolution", func(p *Policy) { p.Resolution = "4k" }, "resolution_mismatch"}, {"codec", func(p *Policy) { p.Codec = "AVC" }, "codec_mismatch"}, {"minimum size", func(p *Policy) { p.MinBytes = 3 << 30 }, "below_minimum_size"}, {"maximum size", func(p *Policy) { p.MaxBytes = 1 << 30 }, "above_maximum_size"}, {"bad include", func(p *Policy) { p.Include = "(" }, "invalid_include_regex"}, {"bad exclude", func(p *Policy) { p.Exclude = strings.Repeat("a", 513) }, "invalid_exclude_regex"}, {"include", func(p *Policy) { p.Include = "REMUX" }, "include_not_matched"}, {"exclude", func(p *Policy) { p.Exclude = "WEB" }, "excluded_by_pattern"}} {
		t.Run(tc.name, func(t *testing.T) {
			p := policy
			tc.change(&p)
			d := Evaluate(r, movieTarget(false), p)
			if d.Accepted || !hasReason(d, tc.reason) {
				t.Fatalf("policy ignored: %+v", d)
			}
		})
	}
	r.Resolution = "2160p"
	if d := Evaluate(r, movieTarget(true), policy); d.Accepted || !hasReason(d, "resolution_conflict") {
		t.Fatalf("provider/title conflict ignored: %+v", d)
	}
	r = resource("夜王 2026 1080p 2160p HEVC")
	if d := Evaluate(r, movieTarget(true), Policy{Resolution: "1080p"}); d.Accepted || !hasReason(d, "resolution_conflict") {
		t.Fatalf("multiple qualities ignored: %+v", d)
	}
}

func TestProviderAliasesPreserveIdentityBoundaries(t *testing.T) {
	target := movieTarget(true)
	target.Media.Aliases = []string{"夜・王者", "A Different Official Title"}
	for _, title := range []string{"夜 王者 2026 1080p WEB-DL AVC", "A.Different.Official.Title.2026.1080p.WEB-DL.AVC"} {
		if d := Evaluate(resource(title), target, Policy{}); !d.Accepted {
			t.Fatalf("confirmed provider alias rejected: %+v", d)
		}
	}
	for _, title := range []string{"夜 王者 2025 1080p WEB-DL AVC", "夜 王者 2 2026 1080p WEB-DL AVC", "A Different Official Title 1080p WEB-DL AVC"} {
		if d := Evaluate(resource(title), target, Policy{}); d.Accepted {
			t.Fatalf("alias bypassed year or exact-title identity: %+v", d)
		}
	}
}

func TestSeriesIdentityAllowsConfirmedSeasonYear(t *testing.T) {
	target := tvTarget(true)
	target.SeasonYear = 2026
	for _, year := range []string{"2022", "2026"} {
		if d := Evaluate(resource("诛仙 "+year+" S04E05 1080p WEB-DL"), target, Policy{}); !d.Accepted || d.NeedsConfirmation {
			t.Fatalf("known series/season year rejected: %+v", d)
		}
	}
	if d := Evaluate(resource("诛仙 2025 S04E05 1080p WEB-DL"), target, Policy{}); d.Accepted || !hasReason(d, "series_year_conflict") {
		t.Fatalf("unrelated year allowed: %+v", d)
	}
	target.SeasonYear = 0
	if d := Evaluate(resource("诛仙 2026 S04E05 1080p WEB-DL"), target, Policy{}); d.Accepted {
		t.Fatalf("guessed season year: %+v", d)
	}
}

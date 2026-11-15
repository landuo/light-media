package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"light-media/internal/releasemeta"
)

type episodeAutoOracleCase struct {
	Name          string
	Files         []string
	Template      string
	ErrorCode     string `json:"error_code"`
	SizeMB        int64  `json:"size_mb"`
	SampleCount   int    `json:"sample_count"`
	MajorityCount int    `json:"majority_count"`
	Confidence    string
	Relaxed       bool
	Reason        string
}

func loadEpisodeAutoOracle(t *testing.T) []episodeAutoOracleCase {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/history-mp-alignment-20261115/episode-format-auto-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct{ Cases []episodeAutoOracleCase }
	if err = json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	return oracle.Cases
}
func episodeAutoFixture(names []string, size int64) []organizeEpisodeSample {
	samples := make([]organizeEpisodeSample, 0, len(names))
	for _, name := range names {
		kind := "audio"
		media := strings.HasSuffix(name, ".mkv") || strings.HasSuffix(name, ".mp4")
		if media {
			kind = "media"
		} else if strings.HasSuffix(name, ".ass") || strings.HasSuffix(name, ".srt") {
			kind = "subtitle"
		}
		samples = append(samples, organizeEpisodeSample{path: "/downloads/" + name, name: name, size: size, media: media, kind: kind})
	}
	return samples
}
func checkEpisodeAutoOracle(t *testing.T, tc episodeAutoOracleCase, out organizeEpisodeRecommendResult, err error) {
	t.Helper()
	if tc.ErrorCode != "" {
		episodeTestError(t, err, tc.ErrorCode)
		return
	}
	if err != nil || out.EpisodeFormat != tc.Template || out.SampleCount != tc.SampleCount || out.MajorityCount != tc.MajorityCount || out.Confidence != tc.Confidence || out.SizeFilterRelaxed != tc.Relaxed || out.RuleName != "智能分析" || out.RuleIndex != -1 {
		t.Fatalf("source oracle %+v: got %+v, error=%v", tc, out, err)
	}
	if tc.Reason != "" && !slices.Contains(out.Reasons, tc.Reason) {
		t.Fatalf("missing reason %s: %+v", tc.Reason, out)
	}
}
func TestEpisodeRecommendAutomaticSourceOracle(t *testing.T) {
	// Both parser implementations execute; neither episode recognition nor native
	// metadata is stubbed for these source-derived filenames.
	native, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: releasemeta.Options{SourceMode: "legacy", MediaExts: strings.Fields(".mkv .mp4 .ass .srt .mka .flac")}})
	if err != nil {
		t.Fatal(err)
	}
	a, source, _ := importTestApp(t)
	for _, tc := range loadEpisodeAutoOracle(t) {
		t.Run(tc.Name, func(t *testing.T) {
			size := int64(150 * 1024 * 1024)
			if tc.SizeMB != 0 {
				size = tc.SizeMB * 1024 * 1024
			}
			samples := episodeAutoFixture(tc.Files, size)
			out, err := recommendEpisodeAutomatically(context.Background(), samples, native)
			checkEpisodeAutoOracle(t, tc, out, err)
			// Materialize the same names as real sparse fixtures, then exercise
			// the complete app path: authorization, configured classifier, stats,
			// global recognition options, sampling and the default fallback.
			dir := filepath.Join(source, tc.Name)
			for _, name := range tc.Files {
				path := filepath.Join(dir, name)
				importWrite(t, path, []byte("synthetic oracle fixture"))
				if e := os.Truncate(path, size); e != nil {
					t.Fatal(e)
				}
			}
			physical, e := a.recommendEpisodeFormat(context.Background(), organizeEpisodeRecommendRequest{Paths: []string{dir}})
			checkEpisodeAutoOracle(t, tc, physical, e)
			if err != nil {
				return
			}
			slices.Reverse(samples)
			reversed, e := recommendEpisodeAutomatically(context.Background(), samples, native)
			if e != nil || !reflect.DeepEqual(out, reversed) {
				t.Fatalf("input-order-dependent: %+v / %+v, %v", out, reversed, e)
			}
		})
	}
}
func TestEpisodeRecommendAutomaticRealFilesAndRulePriority(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := context.Background()
	first := filepath.Join(source, "Show.S01E01.mkv")
	second := filepath.Join(source, "Show.S01E02.mkv")
	for _, path := range []string{first, second} {
		importWrite(t, path, []byte("synthetic sparse media"))
		if err := os.Truncate(path, 150*1024*1024); err != nil {
			t.Fatal(err)
		}
	}
	request := organizeEpisodeRecommendRequest{Paths: []string{source}}
	assertAuto := func() organizeEpisodeRecommendResult {
		t.Helper()
		out, err := a.recommendEpisodeFormat(ctx, request)
		if err != nil || out.RuleName != "智能分析" || out.EpisodeFormat != "Show.S01E{ep}.mkv" || out.Confidence != "high" || out.NativeVerifiedCount != 2 || out.MajorityCount != 2 {
			t.Fatalf("actual files and native parser: %+v %v", out, err)
		}
		return out
	}
	assertAuto()
	// Native validation uses the independently configured recognition words,
	// while automatic spans remain anchored in physical filenames.
	if err := a.saveRecognitionWords(ctx, 0, []string{"S01E01 => S01E09"}); err != nil {
		t.Fatal(err)
	}
	_, wordErr := a.recommendEpisodeFormat(ctx, request)
	episodeTestError(t, wordErr, "EPISODE_AUTO_NATIVE_CONFLICT")
	if err := a.saveRecognitionWords(ctx, 1, nil); err != nil {
		t.Fatal(err)
	}
	assertAuto()
	rule := organizeEpisodeRule{Name: "never matched", Enabled: true, Pattern: `Other(?P<ep>\d+)`, Order: 0}
	episodeTestSave(t, a, rule)
	assertAuto()
	rule.Enabled = false
	episodeTestSave(t, a, rule)
	assertAuto()
	rule.Enabled = true
	rule.Pattern = `(?P<title>Show)\.S01E(?P<ep>\d+)\.mkv`
	episodeTestSave(t, a, rule)
	out, err := a.recommendEpisodeFormat(ctx, request)
	if err != nil || out.RuleName != rule.Name || out.EpisodeFormat != "{title}.S01E{ep}.mkv" || out.Confidence != "" {
		t.Fatalf("configured rule must precede auto: %+v %v", out, err)
	}
	// A single selected file samples the parent; a directory gets the same set.
	episodeTestSave(t, a)
	request.Paths = []string{first}
	assertAuto()
	request.Paths = []string{second, first}
	assertAuto()
	// History batch follows the existing current-source resolution, not display filenames.
	var jobsBefore int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM jobs`).Scan(&jobsBefore); err != nil || jobsBefore != 0 {
		t.Fatalf("automatic recommendation wrote jobs: %d %v", jobsBefore, err)
	}
	plan, _ := historyParityPlan(t, a, source, source, "auto-history", "Show", "failed", "error", "Show.S01E01.mkv", "Show.S01E02.mkv")
	plan.Files[0].Source = first
	plan.Files[1].Source = second
	if err = a.Store.Put(ctx, "import_plan", plan.ID, plan); err != nil {
		t.Fatal(err)
	}
	out, err = a.recommendEpisodeFormat(ctx, organizeEpisodeRecommendRequest{HistoryIDs: []string{plan.Files[0].ID, plan.Files[1].ID}})
	if err != nil || out.EpisodeFormat != "Show.S01E{ep}.mkv" || out.MajorityCount != 2 {
		t.Fatalf("history automatic recommendation: %+v %v", out, err)
	}
	var jobs int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM jobs`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("read-only recommendation: %d %v", jobs, err)
	}
}

func TestEpisodeRecommendAutomaticFiltersAndConflicts(t *testing.T) {
	ctx := context.Background()
	native, err := releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: releasemeta.Options{SourceMode: "legacy", MediaExts: strings.Fields(".mkv .mp4 .ass .srt .mka .flac")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("auxiliary_prevents_small_video_relaxation", func(t *testing.T) {
		samples := episodeAutoFixture([]string{"Show.S01E01.mkv", "Show.S01E02.mkv", "Credits.srt"}, 42*1024*1024)
		_, err := recommendEpisodeAutomatically(ctx, samples, native)
		episodeTestError(t, err, "EPISODE_AUTO_NOT_DETECTED")
	})
	t.Run("sixty_percent_boundary", func(t *testing.T) {
		samples := episodeAutoFixture([]string{"Show - 01.mkv", "Show - 02.mkv", "Show - 03.mkv", "Show bonus.mkv", "Show extra.mkv"}, 150*1024*1024)
		out, err := recommendEpisodeAutomatically(ctx, samples, native)
		if err != nil || out.SampleCount != 3 || out.MajorityCount != 3 {
			t.Fatalf("exact 60%% must pass: %+v %v", out, err)
		}
	})
	t.Run("native_fallback", func(t *testing.T) {
		samples := episodeAutoFixture([]string{"Show - 01.mkv", "Show - 02.mkv"}, 150*1024*1024)
		out, err := recommendEpisodeAutomaticallyWithExtractor(ctx, samples, func(s organizeEpisodeSample) episodeExtraction {
			value := "01"
			if strings.Contains(s.name, "02") {
				value = "02"
			}
			return resolveRecommendedEpisode(s.name, "", "", value)
		})
		if err != nil || out.NativeFallbackCount != 2 || out.NativeVerifiedCount != 0 || out.Reason != "native_meta_fallback" || out.EpisodeFormat != "Show - {ep}.mkv" {
			t.Fatalf("source native fallback: %+v %v", out, err)
		}
	})
	t.Run("native_conflict_veto", func(t *testing.T) {
		samples := episodeAutoFixture([]string{"Show - 01.mkv", "Show - 02.mkv"}, 150*1024*1024)
		_, err := recommendEpisodeAutomaticallyWithExtractor(ctx, samples, func(s organizeEpisodeSample) episodeExtraction {
			value := "01"
			if strings.Contains(s.name, "02") {
				value = "02"
			}
			return resolveRecommendedEpisode(s.name, value, "", "02")
		})
		episodeTestError(t, err, "EPISODE_AUTO_NATIVE_CONFLICT")
	})
	t.Run("title_number_degradation_and_fallback_preference", func(t *testing.T) {
		for _, episode := range []string{"01", "04"} {
			name := "[VCB-Studio] Kono Subarashii Sekai ni Shukufuku wo! 3 [" + episode + "][Ma10p_1080p][x265_flac_aac].mkv"
			e := resolveRecommendedEpisode(name, "3", episode, "3")
			if e.value != episode || e.native != "" || e.nativeVerified || e.usedNativeFallback {
				t.Fatalf("title sequence conflict: %+v", e)
			}
		}
		e := resolveRecommendedEpisode("Show 3 [01][02].mkv", "01-02", "02", "")
		if e.value != "01-02" {
			t.Fatalf("range replaced by one fallback token: %+v", e)
		}
	})
	t.Run("special_only_and_unrecognized", func(t *testing.T) {
		for _, tc := range []struct{ name, code string }{{"Show [NCOP].mkv", "EPISODE_AUTO_FAILED"}, {"Show - 00.mkv", "EPISODE_AUTO_FAILED"}, {"Movie.2026.1080p.12345.mkv", "EPISODE_AUTO_NOT_DETECTED"}} {
			_, err := recommendEpisodeAutomatically(ctx, episodeAutoFixture([]string{tc.name}, 150*1024*1024), native)
			episodeTestError(t, err, tc.code)
		}
	})
}
func TestEpisodeRecommendAutomaticDiffSpans(t *testing.T) {
	t.Run("existing_placeholder_and_literal_braces", func(t *testing.T) {
		names := []string{"Show - 01 [x265_flac][1080p].mkv", "Show - 02 [x265_flac][720p].mkv"}
		samples := []episodeDetectedSample{{episodeCompatibilitySample: episodeCompatibilitySample{name: names[0]}}, {episodeCompatibilitySample: episodeCompatibilitySample{name: names[1]}}}
		updated := insertEpisodeVariable("Show - {ep} [x265{a}][1080p].mkv", []string{names[1]}, []string{" [x265_flac][1080p].mkv", " [x265_flac][720p].mkv"}, samples, "b")
		// Source asserts preservation of both names: adjacent lazy probe capture
		// may absorb the bracket separator; do not invent a narrower gap policy.
		if updated != "Show - {ep} [x265{a}{b}0p].mkv" {
			t.Fatalf("existing a was lost: %s", updated)
		}
		spans := collectEpisodeVariableSpans(" 中文{{v{a}}}suffix", " 中文{v一}suffix")
		if len(spans) != 1 || spans[0].start != 5 || spans[0].end != 6 {
			t.Fatalf("Unicode capture offsets: %+v", spans)
		}
	})
	t.Run("optional_and_common_parts", func(t *testing.T) {
		for _, tc := range []struct {
			base, other string
			start, end  int
			ok          bool
		}{{" Fin ", "", 0, 5, true}, {" [1080p].mkv", " Fin [1080p].mkv", 1, 1, true}, {"same", "same", 0, 0, false}, {"中文甲尾", "中文乙尾", 2, 3, true}} {
			start, end, ok := calculateEpisodeVariableSpan(tc.base, []string{tc.other})
			if start != tc.start || end != tc.end || ok != tc.ok {
				t.Fatalf("%q/%q: %d,%d,%v", tc.base, tc.other, start, end, ok)
			}
		}
	})
	t.Run("whole_tail_capture_and_empty_variable", func(t *testing.T) {
		re := compileEpisodeSuffix(" {a}[1080p].mkv")
		if re == nil {
			t.Fatal("no suffix parser")
		}
		match := re.FindStringSubmatch(" [1080p].mkv")
		if match == nil || match[re.SubexpIndex("a")] != "" {
			t.Fatalf("empty capture: %v", match)
		}
		if compileEpisodeSuffix("{a}{a}") != nil || compileEpisodeSuffix("{a") != nil {
			t.Fatal("invalid suffix template accepted")
		}
	})
}

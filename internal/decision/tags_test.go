package decision

import (
	"testing"
)

func TestSourceAndHDRRequireExplicitUnambiguousTags(t *testing.T) {
	for _, tc := range []struct {
		title    string
		policy   Policy
		accepted bool
		reason   string
	}{
		{"夜王 2026 2160p BluRay REMUX DV HDR10 HEVC", Policy{Source: "BluRay", HDR: "dolby_vision"}, true, ""},
		{"夜王 2026 2160p BluRay REMUX DV HDR10 HEVC", Policy{Source: "REMUX", HDR: "hdr10"}, true, ""},
		{"夜王 2026 2160p WEB-DL HDR10+ HEVC", Policy{Source: "WEB-DL", HDR: "hdr10plus"}, true, ""},
		{"夜王 2026 2160p WEB-DL 杜比视界 HEVC", Policy{HDR: "dolby_vision"}, true, ""},
		{"夜王 2026 1080p WEB-DL SDR AVC", Policy{HDR: "sdr"}, true, ""},
		{"夜王 2026 1080p AVC", Policy{HDR: "sdr"}, false, "hdr_unknown"},
		{"夜王 2026 2160p HDR HEVC", Policy{HDR: "hdr10"}, false, "hdr_unknown"},
		{"夜王 2026 2160p SDR HDR10 HEVC", Policy{HDR: "hdr10"}, false, "hdr_conflict"},
		{"夜王 2026 1080p WEB-DL BluRay AVC", Policy{Source: "WEB-DL"}, false, "source_conflict"},
		{"夜王 2026 1080p AVC", Policy{Source: "BluRay"}, false, "source_unknown"},
		{"夜王 2026 1080p WEB-DL AVC", Policy{Source: "BluRay"}, false, "source_mismatch"},
	} {
		t.Run(tc.title+tc.policy.Source+tc.policy.HDR, func(t *testing.T) {
			d := Evaluate(resource(tc.title), movieTarget(true), tc.policy)
			if d.Accepted != tc.accepted || (tc.reason != "" && !hasReason(d, tc.reason)) {
				t.Fatalf("%+v", d)
			}
		})
	}
	r := resource("夜王 2026 1080p BluRay AVC")
	r.Source = "WEBDL"
	d := Evaluate(r, movieTarget(true), Policy{Source: "WEB-DL"})
	if d.Accepted || !hasReason(d, "source_conflict") {
		t.Fatalf("supplied source conflicted with title: %+v", d)
	}
}
func TestRequiredAudioAndSubtitlesAreSeparateAndAllRequired(t *testing.T) {
	for _, tc := range []struct {
		title    string
		policy   Policy
		accepted bool
		reason   string
	}{
		{"夜王 2026 1080p WEB-DL 国语中字 AVC", Policy{Languages: []string{"zh"}, Subtitles: []string{"zh"}}, true, ""},
		{"夜王 2026 1080p WEB-DL 国配 粤配 中字 AVC", Policy{Languages: []string{"zh", "yue"}, Subtitles: []string{"zh"}}, true, ""},
		{"夜王 2026 1080p WEB-DL 无国配 AVC", Policy{Languages: []string{"zh"}}, false, "audio_language_not_confirmed"},
		{"夜王 2026 1080p WEB-DL Mandarin CHS English.Audio ENG.SUB AVC", Policy{Languages: []string{"zh", "en"}, Subtitles: []string{"zh-hans", "en"}}, true, ""},
		{"夜王 2026 1080p WEB-DL 国粤双语 简繁中字 AVC", Policy{Languages: []string{"zh", "yue"}, Subtitles: []string{"zh-hans", "zh-hant"}}, true, ""},
		{"夜王 2026 1080p WEB-DL 中文字幕 AVC", Policy{Languages: []string{"zh"}}, false, "audio_language_not_confirmed"},
		{"夜王 2026 1080p WEB-DL English.Subtitles AVC", Policy{Languages: []string{"en"}}, false, "audio_language_not_confirmed"},
		{"夜王 2026 1080p WEB-DL 国语 AVC", Policy{Subtitles: []string{"zh"}}, false, "subtitle_language_not_confirmed"},
		{"夜王 2026 1080p WEB-DL 国语 CHS AVC", Policy{Languages: []string{"zh", "en"}}, false, "audio_language_not_confirmed"},
		{"夜王 2026 1080p WEB-DL 国语 无字幕 CHS AVC", Policy{Subtitles: []string{"zh"}}, false, "subtitle_conflict"},
		{"夜王 2026 1080p WEB-DL 无中文字幕 AVC", Policy{Subtitles: []string{"zh"}}, false, "subtitle_language_not_confirmed"},
		{"夜王 2026 1080p WEB-DL 无国语 AVC", Policy{Languages: []string{"zh"}}, false, "audio_language_not_confirmed"},
	} {
		t.Run(tc.title, func(t *testing.T) {
			d := Evaluate(resource(tc.title), movieTarget(true), tc.policy)
			if d.Accepted != tc.accepted || (tc.reason != "" && !hasReason(d, tc.reason)) {
				t.Fatalf("%+v", d)
			}
		})
	}
}
func TestExplicitPreferenceRankDoesNotBypassHardFilters(t *testing.T) {
	p := Policy{PreferredGroups: []string{"Favorite-Team", "Second"}, PreferredResolutions: []string{"2160p", "1080p"}, MinSeeders: 2}
	preferred := resource("夜王 2026 2160p WEB-DL HEVC-Favorite-Team")
	ordinary := resource("夜王 2026 1080p WEB-DL AVC-Other")
	if Rank(preferred, p) <= Rank(ordinary, p) || Rank(preferred, Policy{}) != 0 {
		t.Fatal("explicit preferences not ranked independently")
	}
	preferred.Seeders = 0
	if d := Evaluate(preferred, movieTarget(true), p); d.Accepted || !hasReason(d, "insufficient_seeders") {
		t.Fatalf("preference bypassed hard seeder threshold: %+v", d)
	}
	for _, invalid := range []Policy{{HDR: "anything"}, {Source: "unknown"}, {Languages: []string{"unknown"}}, {Subtitles: []string{"unknown"}}, {PreferredGroups: []string{"group,other"}}, {PreferredResolutions: []string{"unknown"}}} {
		if d := Evaluate(ordinary, movieTarget(true), invalid); d.Accepted {
			t.Fatalf("invalid explicit policy accepted: %+v", invalid)
		}
	}
}

func TestRepeatedTranslatedTitleIsNotAudioEvidence(t *testing.T) {
	target := movieTarget(true)
	target.Media.Title = "英语老师"
	target.Media.OriginalTitle = "English Teacher"
	target.Media.Year = 2024
	r := resource("English.Teacher.2024.1080p.WEB-DL")
	r.Subtitle = "英语老师 / English Teacher"
	d := Evaluate(r, target, Policy{Languages: []string{"en"}})
	if d.Accepted || !hasReason(d, "audio_language_not_confirmed") {
		t.Fatalf("title word used as audio tag: %+v", d)
	}
	r.Subtitle = "English Audio / 英语音轨"
	if d = Evaluate(r, target, Policy{Languages: []string{"en"}}); !d.Accepted {
		t.Fatalf("explicit audio label ignored: %+v", d)
	}
}

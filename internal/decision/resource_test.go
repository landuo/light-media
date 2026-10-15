package decision

import (
	"reflect"
	"testing"
)

func TestResourcePolicyUsesIdentityAwareFiltersWithoutInventingIdentity(t *testing.T) {
	r := resource("夜王 2026 2160p WEB-DL HDR10 HEVC 国语 CHS-Preferred")
	for _, policy := range []Policy{
		{}, {Include: "WEB-DL"}, {Include: "BluRay"}, {Exclude: "WEB"}, {Resolution: "1080p"},
		{Codec: "AVC"}, {FreeOnly: true}, {MinSeeders: 4}, {MinBytes: 3 << 30}, {MaxBytes: 1 << 30},
		{Source: "BluRay"}, {HDR: "sdr"}, {Languages: []string{"en"}}, {Subtitles: []string{"en"}},
		{Include: "("}, {Resolution: "this-invalid-quality-value-is-far-too-long"},
	} {
		raw := EvaluateResource(r, policy)
		identified := Evaluate(r, movieTarget(true), policy)
		if raw.Accepted != identified.Accepted || !reflect.DeepEqual(raw.Reasons, identified.Reasons) {
			t.Fatalf("policy divergence for %+v: raw=%+v identified=%+v", policy, raw, identified)
		}
		if raw.Season != 0 || len(raw.Episodes) != 0 || raw.NeedsConfirmation {
			t.Fatal("resource policy invented media/episode confirmation", raw)
		}
	}
	if got := Evaluate(r, Target{}, Policy{}); got.Accepted || !hasReason(got, "invalid_media_identity") {
		t.Fatal("resource extraction bypassed identity validation", got)
	}
}

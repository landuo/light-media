package sites

import (
	"bytes"
	"strings"
	"testing"
)

// Keep the original implementation independent of the optimized detector: it
// is both a differential oracle and the baseline in the paired benchmarks.
func baselineLoginChallenge(b []byte) bool {
	s := strings.ToLower(string(b))
	s = strings.ReplaceAll(s, "/cdn-cgi/challenge-platform/scripts/jsd/main.js", "")
	for _, marker := range []string{"cf-chl-", "challenge-platform", "just a moment...", "g-recaptcha", "h-captcha", "turnstile"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

func TestLoginChallengeScan(t *testing.T) {
	p := passiveChallengePath
	for _, tc := range []struct {
		input string
		want  bool
	}{
		{"", false}, {strings.ToUpper(p) + p, false},
		{strings.ReplaceAll(p, "i", "İ"), false},
		{"见证TuRnStİLe", true}, {"turnſtile", false}, // SimpleFold is not ToLower.
		{"turn\xffstile", false}, {"\xffTURNSTILE\xc0", true},
		{p[:len(p)-1], true}, // Partial path still contains the interactive marker.
		{p + "cf-" + p + p + "CHL-", true},
		{p[:3] + p + p[3:], true}, // Removing the inner path must not remove the newly joined outer path.
	} {
		if got := hasLoginChallenge([]byte(tc.input)); got != tc.want || got != baselineLoginChallenge([]byte(tc.input)) {
			t.Errorf("detector(%q) = %t, want %t", tc.input, got, tc.want)
		}
	}
	for _, marker := range loginChallengeMarkers {
		for split := 0; split <= len(marker); split++ {
			// Put both the deletion and a marker split at the scan block boundary.
			input := strings.Repeat("x", 4096-split) + strings.ToUpper(marker[:split]) + p + p + marker[split:]
			if !hasLoginChallenge([]byte(input)) {
				t.Errorf("missed %q with split at %d", marker, split)
			}
		}
	}
}

func TestLoginChallengeLargePageAllocations(t *testing.T) {
	page := bytes.Repeat([]byte("<p>电影 Title</p>"+passiveJSD), 8192)
	if allocs := testing.AllocsPerRun(5, func() {
		if hasLoginChallenge(page) {
			t.Fatal("passive page reported as a challenge")
		}
	}); allocs != 0 {
		t.Errorf("large-page scan allocated %v times; want 0", allocs)
	}
}

func FuzzLoginChallengeScan(f *testing.F) {
	for _, s := range []string{"", passiveJSD, "turnstİle", "turn\xffstile", "cf-" + passiveChallengePath + "chl-", strings.Repeat("x", 4093) + "h-captcha"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		if got, want := hasLoginChallenge(input), baselineLoginChallenge(input); got != want {
			t.Fatalf("detector = %t, original = %t", got, want)
		}
	})
}

func BenchmarkLoginChallengeScan(b *testing.B) {
	const size = 2 << 20
	for _, tc := range []struct {
		name, row, prefix, suffix string
		want                      bool
	}{
		{"ASCII", `<tr><td><a href="details.php?id=1">Sample Title</a></td></tr>`, "", "", false},
		{"UnicodePassive", `<tr><td><a href="details.php?id=1">电影 Sample Épisode</a></td></tr>`, "", passiveJSD, false},
		{"UnicodeLate", `<tr><td><a href="details.php?id=1">电影 Sample Épisode</a></td></tr>`, "", "turn" + passiveChallengePath + "stİle", true},
		{"UnicodeEarly", `<tr><td><a href="details.php?id=1">电影 Sample Épisode</a></td></tr>`, "JUST A MOMENT...", "", true},
	} {
		page := []byte(tc.prefix)
		page = append(page, bytes.Repeat([]byte(tc.row), size/len(tc.row))...)
		page = append(page, tc.suffix...)
		for _, impl := range []struct {
			name   string
			detect func([]byte) bool
		}{{"Baseline", baselineLoginChallenge}, {"Bounded", hasLoginChallenge}} {
			b.Run(tc.name+"/"+impl.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if got := impl.detect(page); got != tc.want {
						b.Fatalf("got %t, want %t", got, tc.want)
					}
				}
			})
		}
	}
}

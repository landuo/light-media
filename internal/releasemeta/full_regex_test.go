package releasemeta

import "testing"

func TestPreparedSourceUnicodeCaptures(t *testing.T) {
	const pattern = `(?P<label>KiS)\s(\d+)`
	const text = "Kİſ\u00a0１２/Kıſ\u00a0３/KiS 4"
	for _, tc := range []struct {
		mode   string
		ignore bool
		labels []string
	}{
		{"rust", true, []string{"KiS"}},
		{"legacy", true, []string{"Kİſ", "Kıſ", "KiS"}},
		{"legacy_metabase", true, []string{"Kİſ", "KiS"}},
		{"legacy", false, []string{"KiS"}},
	} {
		matches := fullRXMatches(pattern, text, tc.ignore, tc.mode)
		if len(matches) != len(tc.labels) {
			t.Fatalf("mode=%s ignore=%v: %d matches, want %d", tc.mode, tc.ignore, len(matches), len(tc.labels))
		}
		for i, label := range tc.labels {
			m := matches[i]
			if g := m.GroupByName("label"); g == nil || g.String() != label {
				t.Errorf("mode=%s ignore=%v: label = %v, want %q", tc.mode, tc.ignore, g, label)
			}
		}
	}
}

// Each iteration parses the same two ordinary (non-anime) release titles.
func BenchmarkFullRegexOrdinaryTitles(b *testing.B) {
	for _, mode := range []string{"rust", "legacy"} {
		b.Run(mode, func(b *testing.B) {
			p, err := NewMetaInfoParser(MetaInfoEnvironment{Options: Options{SourceMode: mode}})
			if err != nil {
				b.Fatal(err)
			}
			parse := func() {
				for _, title := range []string{"Example.Movie.2024.2160p.UHD.BluRay.HDR10.HEVC-GROUP", "Example.Series.S02E03.1080p.WEB-DL.DDP5.1.H.264-GROUP"} {
					m, err := p.ParseTitle(title, nil, nil)
					if err != nil || m == nil || m.Text("resource_pix") == "" {
						b.Fatalf("parse %q: %v, %v", title, m, err)
					}
				}
			}
			parse() // Warm the compiled source expressions before timing.
			b.ReportAllocs()
			for b.Loop() {
				parse()
			}
		})
	}
}

package releasemeta

import "testing"

// The source build collapses a split UHD BluRay source to "UHD BluRay" and
// keeps REMUX only for the non-UHD BluRay source. Every expectation below was
// read from the installed MoviePilot build (app.core.metainfo.MetaInfo), not
// copied from this package's own output.
func TestUHDBluRaySourceMatchesInstalledBuild(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		{"Show.2160p.UHD.BluRay.REMUX.HEVC", "UHD BluRay"},
		{"Show.2160p.UHD.BluRay.REMUX.HEVC.TrueHD-GRP", "UHD BluRay"},
		{"Show.2160p.UHD REMUX BluRay HEVC", "UHD BluRay"},
		{"Show.2160p.UHD.BluRay.HEVC", "UHD BluRay"},
		{"Show.2160p.UHD.BluRay.x265", "UHD BluRay"},
		{"Show.2160p.BluRay.REMUX.HEVC", "BluRay REMUX"},
		{"Show.1080p.BluRay.REMUX.AVC", "BluRay REMUX"},
		{"Show.2160p.BluRay.x265", "BluRay"},
	}
	for _, mode := range []string{"rust", "legacy"} {
		for _, c := range cases {
			got, err := Parse(c.title, nil, Options{SourceMode: mode})
			if err != nil {
				t.Fatalf("%s: %v", c.title, err)
			}
			if got.Quality != c.want {
				t.Errorf("%s (%s): quality = %q, want %q", c.title, mode, got.Quality, c.want)
			}
		}
	}
}

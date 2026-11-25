package app

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/catalog"
)

const spiderManDiscName = "Spider-Man.2.2004.2160p.EUR.UHD.BluRay.HEVC.Atmos.TrueHD.7.1-DiY@HDHome"

func TestBlurayDiscPreviewRecognizesReleaseWithoutExplicitIdentity(t *testing.T) {
	a, source, library := importTestApp(t)
	ctx := t.Context()
	movie := catalog.Media{ID: 558, MediaType: "movie", Title: "蜘蛛侠2", OriginalTitle: "Spider-Man 2", Year: 2004}
	distractor := catalog.Media{ID: 557, MediaType: "movie", Title: "蜘蛛侠", OriginalTitle: "Spider-Man", Year: 2002}
	other := catalog.Media{ID: 902, MediaType: "movie", Title: "另一部电影", OriginalTitle: "Another Film", Year: 2004}
	importCache(t, a, "directory-identify:movie:Spider Man 2", catalog.Page{Page: 1, TotalPages: 1, TotalResults: 3, Results: []catalog.Media{distractor, other, movie}})
	importCache(t, a, "media:movie:558", movie)
	installMetadataFixture(t, a, "disc-preview", []string{"/3/movie/558", "/3/movie/558/images"}, func(r *http.Request) (*http.Response, error) {
		body := `{"id":558,"title":"蜘蛛侠2","original_title":"Spider-Man 2","release_date":"2004-06-25","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`
		if strings.HasSuffix(r.URL.Path, "/images") {
			body = `{"id":558,"backdrops":[],"posters":[],"logos":[]}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})

	disc := filepath.Join(source, "电影", spiderManDiscName)
	src := filepath.Join(disc, "BDMV", "STREAM", "00001.m2ts")
	importWrite(t, src, []byte("synthetic disc member"))
	existingDir := filepath.Join(library, "蜘蛛侠2 (2004)")
	dst := filepath.Join(existingDir, "BDMV", "STREAM", "00001.m2ts")
	if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(src, dst); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}

	req := manualOrganizeRequest{Paths: []string{disc}, TargetPath: library, Mode: "hardlink", Preview: true, Scraping: importFlag(false)}
	for _, provider := range []string{"", "themoviedb"} {
		if provider != "" {
			// This collection-level NFO belongs to another movie. It must
			// not override the disc identity on the source recognition path.
			importWrite(t, filepath.Join(filepath.Dir(disc), "movie.nfo"), []byte(`<movie><title>蜘蛛侠</title><uniqueid type="tmdb" default="true">557</uniqueid></movie>`))
		}
		req.MediaSource = provider
		out, err := a.manualOrganize(ctx, req)
		if err != nil || len(out.Items) != 1 || out.Submitted != 0 || out.Failed != 0 {
			t.Fatalf("provider %q: expected one disc preview without submission: %+v, %v", provider, out, err)
		}
		item := out.Items[0]
		if item.Source != disc || item.Status != "ready" || item.Media.ID != movie.ID || item.Media.MediaType != movie.MediaType || item.Media.Title != movie.Title || item.Media.Year != movie.Year || item.Target != existingDir {
			t.Fatalf("provider %q: incorrect disc identity or preview path: %+v", provider, item)
		}
	}
	var jobs int
	if err := a.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs").Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("preview created a job: %d, %v", jobs, err)
	}
	after, err := os.Stat(dst)
	if err != nil || !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("preview modified the existing hardlink: %v", err)
	}
	original, err := os.Stat(src)
	if err != nil || !os.SameFile(original, after) {
		t.Fatalf("preview altered source membership: %v", err)
	}
	entries, err := os.ReadDir(library)
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(existingDir) {
		t.Fatalf("preview created an additional destination: %v, %v", entries, err)
	}

	classifier, err := a.transferMediaClassifier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observed, accepted, err := a.automaticDiscCandidate(ctx, disc, classifier, nil)
	if err != nil || !accepted || observed == nil {
		t.Fatalf("automatic monitor did not group the disc: %t, %v", accepted, err)
	}
	request, err := a.directoryFileIdentity(ctx, DirectoryRule{MediaType: "mixed"}, *observed)
	if err != nil || request.TMDBID != movie.ID || request.MediaType != movie.MediaType {
		t.Fatalf("automatic monitor lost the disc identity: %+v, %v", request, err)
	}
	ambiguous := movie
	ambiguous.ID = 9999
	importCache(t, a, "directory-identify:movie:Spider Man 2", catalog.Page{Page: 1, TotalPages: 1, TotalResults: 2, Results: []catalog.Media{movie, ambiguous}})
	request, err = a.directoryFileIdentity(ctx, DirectoryRule{MediaType: "mixed"}, *observed)
	if err == nil || request.TMDBID != 0 {
		t.Fatalf("ambiguous disc matched automatically: %+v, %v", request, err)
	}
}

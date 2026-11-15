package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestManualPreparationFailureKeepsConfirmedMediaWithoutExtraLookup(t *testing.T) {
	a, source, target := importTestApp(t)
	media := importFixture(t, a, "movie")
	media.CategoryMetadataKnown = false
	media.OriginalLanguage = ""
	importCache(t, a, "media:movie:123", media)
	calls := useCategoryDetailResponse(t, a, 503, map[string]any{"error": "synthetic unavailable category metadata"})
	path := filepath.Join(source, "Filename.2024.mkv")
	importWrite(t, path, []byte("synthetic incomplete category source"))
	input := manualOrganizeRequest{Paths: []string{path}, MediaType: "movie", TMDBID: 123, Mode: "hardlink", TargetPath: target, LibraryTypeFolder: importFlag(true), Scraping: importFlag(false), Preview: true}
	for _, preview := range []bool{true, false} {
		input.Preview = preview
		out, err := a.manualOrganize(context.Background(), input)
		if err != nil || out.Failed != 1 || len(out.Items) != 1 || out.Items[0].Media.ID != media.ID || out.Items[0].Media.Title != media.Title || out.Items[0].Mode != "hardlink" || out.Items[0].Target != "" {
			t.Fatalf("partial facts lost or invented: %+v %v", out, err)
		}
		page := historyParityReadPage(t, a, "capabilities=deferred")
		if preview {
			if page.Total != 0 || *calls != 1 {
				t.Fatalf("preview wrote history or made extra requests: %+v calls=%d", page, *calls)
			}
		} else {
			if page.Total != 1 || *calls != 2 {
				t.Fatalf("actual failure history/lookup count: %+v calls=%d", page, *calls)
			}
			h := page.Items[0]
			if h.Media.ID != media.ID || h.Media.Title != media.Title || h.Category != "" || h.Mode != "hardlink" || h.Status != "failed" || h.Target != "" || h.Error == "" {
				t.Fatalf("confirmed identity lost or unconfirmed target/category invented: %+v", h)
			}
		}
	}
	bytes, err := os.ReadFile(path)
	if err != nil || string(bytes) != "synthetic incomplete category source" {
		t.Fatalf("source changed: %q %v", bytes, err)
	}
}

func TestManualPreparationFailureDoesNotInventSelectedIdentity(t *testing.T) {
	a, source, target := importTestApp(t)
	calls := useCategoryDetailResponse(t, a, 503, map[string]any{"error": "no metadata request expected"})
	path := filepath.Join(source, "Unconfirmed Film.2024.mkv")
	importWrite(t, path, []byte("synthetic unknown identity source"))
	broken := filepath.Join(target, "not-a-directory")
	importWrite(t, broken, []byte("synthetic destination conflict"))
	out, err := a.manualOrganize(context.Background(), manualOrganizeRequest{Paths: []string{path}, MediaType: "movie", TMDBID: 999, Mode: "hardlink", TargetPath: broken, Scraping: importFlag(false)})
	if err != nil || out.Failed != 1 {
		t.Fatalf("failure=%+v %v", out, err)
	}
	page := historyParityReadPage(t, a, "capabilities=deferred")
	if page.Total != 1 {
		t.Fatalf("missing failure history: %+v", page)
	}
	h := page.Items[0]
	if h.Media.ID != 0 || h.Media.Title != "Unconfirmed Film" || h.Media.Year != 2024 || h.Mode != "hardlink" || h.Target != "" || h.Status != "failed" || *calls != 0 {
		t.Fatalf("failure guessed identity/target or discarded parsed facts: %+v calls=%d", h, *calls)
	}
}

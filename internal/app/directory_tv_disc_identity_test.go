package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/media"
)

func TestDirectoryTVDiscPreservesVerifiedSeasonWithoutInventingEpisodes(t *testing.T) {
	a, source, _ := importTestApp(t)
	ctx := t.Context()
	show := importFixture(t, a, "tv")
	importCache(t, a, "directory-identify:tv:Test Film", catalog.Page{Page: 1, TotalPages: 1, TotalResults: 1, Results: []catalog.Media{show}})
	rule := DirectoryRule{RootID: "root", MediaType: "tv", Mode: "hardlink", Scraping: importFlag(false)}
	classifier, err := a.transferMediaClassifier(ctx)
	if err != nil {
		t.Fatal(err)
	}
	disc := func(path string) importScanFile {
		t.Helper()
		importWrite(t, filepath.Join(path, "BDMV", "STREAM", "00001.m2ts"), []byte("complete TV disc stream"))
		file, recognized, err := a.automaticDiscCandidate(ctx, path, classifier, nil)
		if err != nil || !recognized || file == nil {
			t.Fatalf("scan %s: %v %v", path, recognized, err)
		}
		return *file
	}
	known := disc(filepath.Join(source, "Test.Film.2024.S01.Disc03"))
	req, err := a.directoryFileIdentity(ctx, rule, known)
	if err != nil || req.TMDBID != show.ID || !req.Files[0].SeasonExplicit || req.Files[0].Season != 1 || len(req.Files[0].Episodes) != 0 {
		t.Fatalf("named TV disc: %+v, %v", req, err)
	}
	plan, err := a.previewImport(ctx, req)
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" || len(plan.Files[0].Episodes) != 0 || !strings.HasSuffix(plan.Files[0].Target, "/Season 1/Disc 3") {
		t.Fatalf("whole disc preview: %+v %v", plan, err)
	}
	job := overwriteStart(t, a, plan)
	if err := a.runImport(ctx, job, plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	src, err := os.Stat(filepath.Join(known.Path, "BDMV/STREAM/00001.m2ts"))
	dst, destErr := os.Stat(filepath.Join(plan.Files[0].Target, "BDMV/STREAM/00001.m2ts"))
	if err != nil || destErr != nil || !os.SameFile(src, dst) {
		t.Fatalf("whole disc hardlink: %v %v", err, destErr)
	}
	var slots int
	if err := a.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM asset_slots WHERE media_key=? AND episode>0", mediaKey(show)).Scan(&slots); err != nil || slots != 0 {
		t.Fatalf("disc invented %d episode slots: %v", slots, err)
	}
	known.InferredMedia = &importInferredMedia{Type: "tv", ID: show.ID}
	if r, err := a.directoryFileIdentity(ctx, rule, known); err != nil || r.Files[0].Season != 1 || len(r.Files[0].Episodes) != 0 {
		t.Fatalf("verified download identity and named disc: %+v, %v", r, err)
	}

	nested := disc(filepath.Join(source, "电视剧", "Test.Film.2024.S01E01-E08.2160p.UHD.Blu-ray", "Disc 2"))
	if r, err := a.directoryFileIdentity(ctx, rule, nested); err != nil || r.TMDBID != show.ID || !r.Files[0].SeasonExplicit || r.Files[0].Season != 1 || len(r.Files[0].Episodes) != 0 {
		t.Fatalf("season pack episode range leaked into the disc: %+v %v", r, err)
	}
	unknown := disc(filepath.Join(source, "电视剧", "Season 1", "Disc 3"))
	if r, err := a.directoryFileIdentity(ctx, rule, unknown); err == nil || !strings.Contains(err.Error(), "季号") || r.TMDBID != 0 {
		t.Fatalf("anonymous disc inherited unrelated title: %+v %v", r, err)
	}
	unknown.InferredMedia = known.InferredMedia
	if r, err := a.directoryFileIdentity(ctx, rule, unknown); err == nil || !strings.Contains(err.Error(), "集号可留空") || r.Files[0].SeasonExplicit {
		t.Fatalf("anonymous disc guessed a season: %+v %v", r, err)
	}
	season := 1
	regular := importScanFile{Path: filepath.Join(source, "Disc 3.mkv"), InferredMedia: known.InferredMedia, Numbering: &media.Numbering{Season: &season}}
	if _, err := a.directoryFileIdentity(ctx, rule, regular); err == nil {
		t.Fatal("ordinary episode file admitted without episode number")
	}
}

package app

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"light-media/internal/catalog"
)

func TestManualOrganizeJokerReleaseActuallyImportsWithoutNFO(t *testing.T) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	m := catalog.Media{ID: 889737, MediaType: "movie", Title: "小丑2：双重妄想", OriginalTitle: "Joker: Folie à Deux", Year: 2024}
	importCache(t, a, "directory-identify:movie:Joker Folie a Deux", catalog.Page{Results: []catalog.Media{m}, TotalPages: 1})
	importCache(t, a, "media:movie:889737", m)
	name := filepath.Join(source, "Joker.Folie.a.Deux.2024.2160p.AMZN.WEB-DL.H265.HDR.DDP5.1-ADWeb.mkv")
	importWrite(t, name, []byte("synthetic-joker-video"))
	scrape := false
	out, e := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{source}, TargetPath: target, Mode: "hardlink", Scraping: &scrape})
	if e != nil || out.Failed != 0 || out.Submitted != 1 || out.Items[0].Media.ID != 889737 {
		t.Fatalf("%+v %v", out, e)
	}
	job, e := a.claim(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.safeRun(ctx, job); e != nil {
		t.Fatal(e)
	}
	first, e := os.Stat(name)
	if e != nil {
		t.Fatal(e)
	}
	second, e := os.Stat(out.Items[0].Target)
	if e != nil {
		t.Fatal(e)
	}
	if !os.SameFile(first, second) {
		t.Fatal("hardlink/source preservation failed")
	}
}

func TestManualOrganizeFailureIsDurableHistoryButPreviewIsNot(t *testing.T) {
	a, source, _ := importTestApp(t)
	path := filepath.Join(source, "unknown.mkv")
	importWrite(t, path, []byte("synthetic"))
	ctx := context.Background()
	for _, preview := range []bool{true, false} {
		out, e := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{path}, Preview: preview})
		if e != nil || out.Failed != 1 || out.Items[0].Error == "" {
			t.Fatalf("missing visible failure: %+v %v", out, e)
		}
		r := httptest.NewRequest("GET", "/history/transfers?q=失败&page_size=1000", nil)
		history, e := a.transferHistory(ctx, r)
		if e != nil {
			t.Fatal(e)
		}
		expected := 0
		if !preview {
			expected = 1
		}
		if history.(map[string]any)["total"] != expected {
			t.Fatalf("failed transfer history missing: %+v", history)
		}
	}
}

func TestOriginalOrganizeNumberingOptions(t *testing.T) {
	for value, want := range map[string][]int{"3": {3}, "03": {3}, "1,3": {1, 2, 3}, "E03": {3}, "EP03-EP04": {3, 4}} {
		got, e := organizeEpisodeRange(value)
		if e != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s %v %v", value, got, e)
		}
	}
	for _, value := range []string{"-1", "1-", "1,,2", "0", "2,1", "1;9"} {
		if _, e := organizeEpisodeRange(value); e == nil {
			t.Fatalf("invalid range accepted: %s", value)
		}
	}
	for value, want := range map[string]int{"-10": 3, "EP*2": 26, "(EP+1)/2": 7} {
		got, e := organizeEpisodeOffset(value, 13)
		if e != nil || got != want {
			t.Fatalf("%s %d %v", value, got, e)
		}
	}
	for _, value := range []string{"EP/0", "EP/2", "f(EP)", "EP*9999999"} {
		if _, e := organizeEpisodeOffset(value, 13); e == nil {
			t.Fatalf("invalid expression accepted %s", value)
		}
	}
	p, e := organizeEpisodeTemplate("{title} - {ep} [{quality}].mkv")
	if e != nil || p.FindStringSubmatch("Show - E03 [1080p].mkv") == nil {
		t.Fatal("original named template rejected", e)
	}
}

func TestManualOrganizeAbsoluteEpisodeUsesExplicitSeason(t *testing.T) {
	a, source, target := importTestApp(t)
	identity := importFixture(t, a, "tv")
	identity.OriginalTitle = "Original Show"
	importCache(t, a, "directory-identify:tv:Original Show", catalog.Page{Results: []catalog.Media{identity}, TotalPages: 1})
	importCache(t, a, "season:123:1", catalog.Season{ID: 901, SeriesID: identity.ID, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 7103, SeasonNumber: 1, EpisodeNumber: 3, Name: "第三集"}}})
	input := filepath.Join(source, "Original.Show.EP03.WEB-DL.mkv")
	importWrite(t, input, []byte("synthetic absolute-numbered episode"))
	season := 1
	scrape := false
	out, e := a.manualOrganize(context.Background(), manualOrganizeRequest{Paths: []string{input}, TargetPath: target, Mode: "copy", Season: &season, Scraping: &scrape, Preview: true})
	if e != nil || out.Failed != 0 || len(out.Items) != 1 || out.Items[0].Media.ID != identity.ID || out.Items[0].Status != "ready" {
		t.Fatalf("explicit season did not resolve EP-only numbering: %+v %v", out, e)
	}
}

func TestManualOrganizeManagedLibraryFileCanMoveWithoutHistoryID(t *testing.T) {
	a, source, old := reorgFixture(t)
	target := filepath.Join(filepath.Dir(source), "file-manager-new-target")
	if e := os.MkdirAll(target, 0750); e != nil {
		t.Fatal(e)
	}
	scrape := false
	out, e := a.manualOrganize(context.Background(), manualOrganizeRequest{Paths: []string{old.Path}, TargetPath: target, Mode: "copy", Scraping: &scrape})
	if e != nil || out.Failed != 0 || out.Submitted != 1 {
		t.Fatalf("managed video selected in file manager cannot reorganize: %+v %v", out, e)
	}
	reorgRunJob(t, a, reorgJob(t, a, "import", out.Items[0].PlanID))
	reorgRunJob(t, a, reorgJob(t, a, "reorganize_cleanup", out.Items[0].PlanID))
	if _, e = os.Stat(source); e != nil {
		t.Fatal("download source removed", e)
	}
	if _, e = os.Stat(old.Path); !os.IsNotExist(e) {
		t.Fatal("old managed target not cleared after replacement", e)
	}
	if _, e = os.Stat(out.Items[0].Target); e != nil {
		t.Fatal("replacement missing", e)
	}
}

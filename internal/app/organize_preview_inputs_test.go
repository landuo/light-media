package app

import (
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/catalog"
)

func TestManualPreviewRecognizesSeasonFilesAndRereadsChangedEvidence(t *testing.T) {
	a, source, target := importTestApp(t)
	identity := importFixture(t, a, "tv")
	identity.OriginalTitle = "Fallout"
	importCache(t, a, "directory-identify:tv:Fallout", catalog.Page{Page: 1, TotalPages: 1, TotalResults: 1, Results: []catalog.Media{identity}})
	importCache(t, a, "season:123:2", catalog.Season{SeriesID: 123, SeasonNumber: 2, Episodes: []catalog.Episode{
		{ID: 201, SeasonNumber: 2, EpisodeNumber: 1, Name: "One"},
		{ID: 202, SeasonNumber: 2, EpisodeNumber: 2, Name: "Two"},
	}})
	pack := filepath.Join(source, "Fallout")
	first := filepath.Join(pack, "Fallout.S02E01.2160p.BluRay-WiKi.mkv")
	second := filepath.Join(pack, "Fallout.S02E02.2160p.BluRay-WiKi.mkv")
	importWrite(t, first, []byte("first"))
	importWrite(t, second, []byte("second"))
	request := manualOrganizeRequest{Interactive: true, Preview: true, Paths: []string{pack}, TargetPath: target, Mode: "hardlink", Scraping: importFlag(false)}
	run := func() map[string]manualOrganizeItem {
		t.Helper()
		out, err := a.manualOrganize(t.Context(), request)
		if err != nil || len(out.Items) != 2 {
			t.Fatal(out, err)
		}
		items := make(map[string]manualOrganizeItem)
		for _, item := range out.Items {
			items[item.Source] = item
		}
		return items
	}
	items := run()
	for path, ep := range map[string]string{first: "S02E01", second: "S02E02"} {
		if item := items[path]; item.Status != "ready" || item.Media.ID != 123 || !strings.Contains(item.Target, ep) {
			t.Fatalf("unexpected episode target: %+v", item)
		}
	}
	importWrite(t, strings.TrimSuffix(first, ".mkv")+".nfo", []byte("<invalid document"))
	items = run()
	if items[first].Status != "failed" || items[second].Status != "ready" {
		t.Fatalf("new preview reused prior NFO or sibling numbering: %+v", items)
	}
}

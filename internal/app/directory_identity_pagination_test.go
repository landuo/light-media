package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/media"
)

func TestDirectoryFileIdentitySearchPagination(t *testing.T) {
	moana := catalog.Media{ID: 1108427, MediaType: "movie", Title: "海洋奇缘：启航", OriginalTitle: "Moana", Year: 2026}
	other := catalog.Media{ID: 568124, MediaType: "movie", Title: "魔法满屋", OriginalTitle: "Encanto", Year: 2021}
	for _, test := range []struct {
		name   string
		change func(*catalog.Page, *catalog.Page)
		valid  bool
		omit   bool
		stale  bool
	}{
		{name: "unique across two pages", valid: true},
		{name: "unique match only on second page", valid: true, change: func(first, second *catalog.Page) { first.Results, second.Results = second.Results, first.Results }},
		{name: "same title and year with different ID", change: func(_, second *catalog.Page) {
			duplicate := moana
			duplicate.ID = 1108428
			second.Results = []catalog.Media{duplicate}
		}},
		{name: "same ID repeated across pages", valid: true, change: func(_, second *catalog.Page) { second.Results = []catalog.Media{moana} }},
		{name: "same title different year", valid: true, change: func(_, second *catalog.Page) {
			differentYear := moana
			differentYear.ID, differentYear.Year = 277834, 2016
			second.Results = []catalog.Media{differentYear}
		}},
		{name: "same title wrong year everywhere", change: func(first, _ *catalog.Page) { first.Results[0].Year = 2016 }},
		{name: "too many pages", change: func(first, _ *catalog.Page) { first.TotalPages = 6 }},
		{name: "missing first page number", change: func(first, _ *catalog.Page) { first.Page = 0 }},
		{name: "wrong first page", change: func(first, _ *catalog.Page) { first.Page = 2 }},
		{name: "wrong second page", change: func(_, second *catalog.Page) { second.Page = 1 }},
		{name: "total pages changed", change: func(_, second *catalog.Page) { second.TotalPages = 3 }},
		{name: "total results changed", change: func(_, second *catalog.Page) { second.TotalResults = 3 }},
		{name: "empty second page", change: func(_, second *catalog.Page) { second.Results = []catalog.Media{} }},
		{name: "second page unavailable", omit: true},
		{name: "second page fetch fails with stale cache", stale: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, source, _ := importTestApp(t)
			first := catalog.Page{Page: 1, TotalPages: 2, TotalResults: 2, Results: []catalog.Media{moana}}
			second := catalog.Page{Page: 2, TotalPages: 2, TotalResults: 2, Results: []catalog.Media{other}}
			if test.change != nil {
				test.change(&first, &second)
			}
			importCache(t, a, "directory-identify:movie:Moana", first)
			if !test.omit {
				importCache(t, a, "directory-identify:movie:Moana:page:2", second)
			}
			if test.stale {
				if err := a.Store.Put(context.Background(), "cache", "directory-identify:movie:Moana:page:2", map[string]any{"at": time.Now().Add(-25 * time.Hour), "data": second}); err != nil {
					t.Fatal(err)
				}
			}
			name := "Moana.2026.2160p.iT.WEB-DL.DDP5.1.Atmos.H.265-DepWeb.mkv"
			request, err := a.directoryFileIdentity(context.Background(), DirectoryRule{MediaType: "mixed"}, importScanFile{Path: filepath.Join(source, name), Parsed: media.Parse(name)})
			if test.valid {
				if err != nil || request.MediaType != "movie" || request.TMDBID != moana.ID {
					t.Fatalf("unique exact identity not selected: %+v %v", request, err)
				}
			} else if err == nil || request.TMDBID != 0 {
				t.Fatalf("incomplete or ambiguous search selected an identity: %+v %v", request, err)
			}
		})
	}
}

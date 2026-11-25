package app

import (
	"context"
	"fmt"
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

func TestDirectoryFileIdentityUsesTVSeasonReleaseYear(t *testing.T) {
	friends := catalog.Media{ID: 1668, MediaType: "tv", Title: "老友记", OriginalTitle: "Friends", Year: 1994,
		Seasons: []catalog.SeasonSummary{{SeasonNumber: 4, AirDate: "1997-09-25"}}}
	namesake := catalog.Media{ID: 900001, MediaType: "tv", Title: "Friends", Year: 1995,
		Seasons: []catalog.SeasonSummary{{SeasonNumber: 4, AirDate: "1998-01-01"}}}
	literal := catalog.Media{ID: 900002, MediaType: "tv", Title: "Friends 1997", Year: 1997}
	for _, tc := range []struct {
		name   string
		direct []catalog.Media
		clean  []catalog.Media
		want   int64
	}{
		{name: "unique clean title", clean: []catalog.Media{friends}, want: friends.ID},
		{name: "same title resolved by the season year", clean: []catalog.Media{friends, namesake}, want: friends.ID},
		{name: "literal title stays first", direct: []catalog.Media{literal}, clean: []catalog.Media{friends}, want: literal.ID},
		{name: "same title and season year stays ambiguous", clean: []catalog.Media{friends, {
			ID: namesake.ID, MediaType: "tv", Title: "Friends", Year: 1995, Seasons: friends.Seasons,
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, source, _ := importTestApp(t)
			page := func(items []catalog.Media) catalog.Page {
				return catalog.Page{Page: 1, TotalPages: 1, TotalResults: len(items), Results: items}
			}
			importCache(t, a, "directory-identify:tv:Friends 1997", page(tc.direct))
			importCache(t, a, "directory-identify:tv:老友记 第四季", page(nil))
			importCache(t, a, "directory-identify:tv:Friends", page(tc.clean))
			for _, detail := range tc.clean {
				importCache(t, a, fmt.Sprintf("media:tv:%d", detail.ID), detail)
			}
			name := "[老友记 第四季].Friends.1997.S04E21.2160p.MAX.WEB-DL.DoVi.HDR10.H265.10bit.DDP5.1-UBWEB.mkv"
			got, err := a.directoryFileIdentity(context.Background(), DirectoryRule{MediaType: "tv"}, importScanFile{Path: filepath.Join(source, name), Parsed: media.Parse(name)})
			if tc.want == 0 {
				if err == nil || got.TMDBID != 0 {
					t.Fatalf("ambiguous release should require review: %+v %v", got, err)
				}
				return
			}
			if err != nil || got.TMDBID != tc.want {
				t.Fatalf("unexpected release identity: %+v %v", got, err)
			}
			if file := got.Files[0]; file.Season != 4 || len(file.Episodes) != 1 || file.Episodes[0] != 21 {
				t.Fatalf("release numbering changed: %+v", file)
			}
			if len(tc.direct) == 0 {
				post := "Friends.S04E21.1997.2160p.WEB-DL.mkv"
				got, err = a.directoryFileIdentity(context.Background(), DirectoryRule{MediaType: "tv"}, importScanFile{Path: filepath.Join(source, post), Parsed: media.Parse(post)})
				if err != nil || got.TMDBID != tc.want {
					t.Fatalf("post-episode season year: id=%d err=%v", got.TMDBID, err)
				}
			}
		})
	}
}

func TestDirectoryFileIdentityPostEpisodeYear(t *testing.T) {
	a, source, _ := importTestApp(t)
	series := catalog.Media{ID: 101172, MediaType: "tv", Title: "吞噬星空", OriginalTitle: "吞噬星空", Year: 2020}
	compilation := catalog.Media{ID: 292492, MediaType: "tv", Title: "吞噬星空 合集篇", OriginalTitle: "吞噬星空 合集篇", Year: 2023}
	page := catalog.Page{Page: 1, TotalPages: 1, TotalResults: 2, Results: []catalog.Media{compilation, series}}
	importCache(t, a, "directory-identify:tv:Swallowed Star", page)
	for _, tc := range []struct {
		tail string
		want int64
	}{
		{"2020.2160p.TX.WEB-DL.H.265.DDP2.0-HHWEB", series.ID},
		{"2160p.TX.WEB-DL.H.265.DDP2.0-HHWEB", 0},
		{"2160p.TX.WEB-DL.2020", 0},
	} {
		name := "Swallowed.Star.S01E251." + tc.tail + ".mkv"
		got, err := a.directoryFileIdentity(context.Background(), DirectoryRule{MediaType: "mixed"}, importScanFile{Path: filepath.Join(source, name), Parsed: media.Parse(name)})
		if tc.want == 0 {
			if err == nil || got.TMDBID != 0 {
				t.Fatalf("ambiguous title accepted: %s, %+v", name, got)
			}
			continue
		}
		if err != nil || got.TMDBID != tc.want || got.Files[0].Season != 1 || len(got.Files[0].Episodes) != 1 || got.Files[0].Episodes[0] != 251 {
			t.Fatalf("wrong release identity: %s, %+v, %v", name, got, err)
		}
	}
}

func TestDirectoryTVExactTitleUsesMoviePilotFirstAirDateOrder(t *testing.T) {
	a, source, _ := importTestApp(t)
	older := catalog.Media{ID: 32366, MediaType: "tv", Title: "Fallout", OriginalTitle: "Fallout", Year: 2006, ReleaseDate: "2006-04-23"}
	newer := catalog.Media{ID: 106379, MediaType: "tv", Title: "辐射", OriginalTitle: "Fallout", Year: 2024, ReleaseDate: "2024-04-10"}
	related := catalog.Media{ID: 311379, MediaType: "tv", Title: "Fallout Shelter: The Experiment", Year: 2027, ReleaseDate: "2027-01-01"}
	// The older exact match is ranked higher by the search API in this replay.
	// A more recent partial match is also present. No next page is cached.
	items := []catalog.Media{older, related, newer}
	importCache(t, a, "directory-identify:tv:Fallout", catalog.Page{Page: 1, TotalPages: 2, TotalResults: 25, Results: items})
	for episode := 1; episode <= 7; episode++ {
		name := fmt.Sprintf("Fallout.S02E%02d.2160p.BluRay.DoVi.x265.10bit.Atmos.TrueHD7.1-WiKi.mkv", episode)
		got, err := a.directoryFileIdentity(t.Context(), DirectoryRule{MediaType: "tv"}, importScanFile{Path: filepath.Join(source, name), Parsed: media.Parse(name)})
		if err != nil || got.TMDBID != newer.ID || got.Files[0].Season != 2 || len(got.Files[0].Episodes) != 1 || got.Files[0].Episodes[0] != episode {
			t.Fatalf("%s: %+v, %v", name, got, err)
		}
	}
	if found := directoryFirstTVTitle(items, "Fallout", 2006); found == nil || found.ID != older.ID {
		t.Fatalf("explicit premiere year lost: %+v", found)
	}
	sameYear := older
	sameYear.Year, sameYear.ReleaseDate = newer.Year, "2024-04-11"
	if found := directoryFirstTVTitle([]catalog.Media{newer, sameYear}, "Fallout", 2024); found == nil || found.ID != sameYear.ID {
		t.Fatalf("expected later premiere within the same year, got %+v", found)
	}
	sameYear.ReleaseDate = newer.ReleaseDate
	if found := directoryFirstTVTitle([]catalog.Media{newer, sameYear}, "Fallout", 2024); found == nil || found.ID != newer.ID {
		t.Fatalf("same-date source order lost: %+v", found)
	}
}

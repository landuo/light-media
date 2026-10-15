package app

import (
	"reflect"
	"strings"
	"testing"

	"light-media/internal/catalog"
)

func TestFallbackSearchFiniteStableQueries(t *testing.T) {
	movie := catalog.Media{MediaType: "movie", Title: "电影中文名", OriginalTitle: "Original Film", Year: 2024, IMDbID: "tt1234567", Aliases: []string{"香港別名", "另一个别名"}}
	wanted := []string{"电影中文名", "tt1234567", "Original Film 2024", "香港別名"}
	if got := fallbackSearchQueries(movie, ""); !reflect.DeepEqual(got, wanted) {
		t.Fatalf("got %v want %v", got, wanted)
	}
	movie.MediaType = "tv"
	wanted[2] = "Original Film"
	if got := fallbackSearchQueries(movie, ""); !reflect.DeepEqual(got, wanted) {
		t.Fatalf("TV used first-air year in original query: %v", got)
	}
}

func TestFallbackExplicitQueryFreezesRevalidation(t *testing.T) {
	identity := catalog.Media{Title: "Title", OriginalTitle: "Other", Year: 2024, IMDbID: "tt1234567", Aliases: []string{"别名"}}
	if got := fallbackSearchQueries(identity, "Original Search 2023"); !reflect.DeepEqual(got, []string{"Original Search 2023"}) {
		t.Fatalf("revalidation changed query: %v", got)
	}
	for _, invalid := range []string{"bad\nquery", strings.Repeat("中", 121)} {
		if len(fallbackSearchQueries(identity, invalid)) != 0 {
			t.Fatal("invalid frozen query silently fell back")
		}
	}
}

func TestFallbackDeduplicatesAndBoundsMetadata(t *testing.T) {
	identity := catalog.Media{MediaType: "movie", Title: "Same Title", OriginalTitle: "same   title", Year: 2024, IMDbID: "https://imdb.com/title/tt1234567", Aliases: []string{"SAME TITLE"}}
	got := fallbackSearchQueries(identity, "")
	if !reflect.DeepEqual(got, []string{"Same Title", "Same Title 2024"}) {
		t.Fatalf("duplicate or invalid identifier query: %v", got)
	}
	identity.Title = "Same Title (2024)"
	identity.OriginalTitle = ""
	identity.Year = 2024
	if got = fallbackSearchQueries(identity, ""); len(got) != 1 {
		t.Fatalf("year duplicated: %v", got)
	}
	identity.Year = 0
	identity.Title = "作品"
	if got = fallbackSearchQueries(identity, ""); len(got) > 4 {
		t.Fatal("unbounded query plan")
	}
}

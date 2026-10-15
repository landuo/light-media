package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func categoryDetailFixture(kind string) map[string]any {
	body := map[string]any{
		"id":                   int64(123),
		"overview":             "简介",
		"genres":               []Genre{{ID: 18, Name: "剧情"}},
		"original_language":    "en",
		"origin_country":       []string{"US"},
		"production_countries": []Country{{ISO3166_1: "US", Name: "United States of America"}, {ISO3166_1: "GB", Name: "United Kingdom"}},
		"credits":              map[string]any{"cast": []any{}},
		"recommendations":      map[string]any{"results": []any{}},
	}
	if kind == "tv" {
		body["name"] = "中文剧名"
		body["original_name"] = "Original Series"
		body["original_language"] = "zh"
		body["origin_country"] = []string{"CN", "HK"}
		body["production_countries"] = []Country{{ISO3166_1: "CN", Name: "China"}}
	} else {
		body["title"] = "中文电影名"
		body["original_title"] = "Original Movie"
	}
	return body
}

func categoryDetailClient(t *testing.T, body map[string]any) *Client {
	t.Helper()
	return testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewEncoder(writer).Encode(body); err != nil {
			t.Error(err)
		}
	})
}

func TestDetailPreservesCategoryMetadataAndCacheRoundTrip(t *testing.T) {
	for _, kind := range []string{"movie", "tv"} {
		t.Run(kind, func(t *testing.T) {
			body := categoryDetailFixture(kind)
			client := categoryDetailClient(t, body)
			media, err := client.Detail(context.Background(), kind, 123)
			if err != nil {
				t.Fatal(err)
			}
			if !media.CategoryMetadataKnown || media.OriginalLanguage != body["original_language"] || !reflect.DeepEqual(media.OriginCountry, body["origin_country"]) || !reflect.DeepEqual(media.ProductionCountries, body["production_countries"]) {
				t.Fatalf("source metadata lost: %+v", media)
			}
			encoded, err := json.Marshal(media)
			if err != nil {
				t.Fatal(err)
			}
			var cached Media
			if err := json.Unmarshal(encoded, &cached); err != nil {
				t.Fatal(err)
			}
			if !cached.CategoryMetadataKnown || cached.OriginalLanguage != media.OriginalLanguage || !reflect.DeepEqual(cached.OriginCountry, media.OriginCountry) || !reflect.DeepEqual(cached.ProductionCountries, media.ProductionCountries) || !reflect.DeepEqual(cached.Genres, media.Genres) || !strings.Contains(string(encoded), `"category_metadata_known":true`) {
				t.Fatalf("cache changed metadata: %s", encoded)
			}
		})
	}
}

func TestFreshPartialCategoryMetadataCountsAsRefreshedDetail(t *testing.T) {
	for _, field := range []string{"genres", "original_language", "origin_country", "production_countries"} {
		for _, missing := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/missing=%t", field, missing), func(t *testing.T) {
				body := categoryDetailFixture("movie")
				if missing {
					delete(body, field)
				} else {
					body[field] = nil
				}
				media, err := categoryDetailClient(t, body).Detail(context.Background(), "movie", 123)
				if err != nil || media.ID != 123 || !media.CategoryMetadataKnown {
					t.Fatalf("valid partial detail must count as refreshed: %+v %v", media, err)
				}
				encoded, err := json.Marshal(media)
				if err != nil || !strings.Contains(string(encoded), `"category_metadata_known":true`) {
					t.Fatalf("refreshed marker should survive cache serialization: %s %v", encoded, err)
				}
			})
		}
	}
	for _, kind := range []string{"movie", "tv"} {
		t.Run(kind+"/explicit-empty-countries-and-genres", func(t *testing.T) {
			body := categoryDetailFixture(kind)
			body["genres"], body["origin_country"], body["production_countries"] = []Genre{}, []string{}, []Country{}
			media, err := categoryDetailClient(t, body).Detail(context.Background(), kind, 123)
			if err != nil || !media.CategoryMetadataKnown || media.OriginCountry == nil || media.ProductionCountries == nil || media.Genres == nil {
				t.Fatalf("explicit empty arrays were treated as missing: %+v %v", media, err)
			}
		})
	}
	body := categoryDetailFixture("movie")
	body["original_language"] = ""
	media, err := categoryDetailClient(t, body).Detail(context.Background(), "movie", 123)
	if err != nil || !media.CategoryMetadataKnown || media.OriginalLanguage != "" {
		t.Fatalf("translated title inferred an original language: %+v %v", media, err)
	}
}

func TestOldCachedCategoryMetadataStillNeedsOneDetailRefresh(t *testing.T) {
	for _, body := range []string{
		`{"id":123,"media_type":"movie","title":"中文电影名"}`,
		`{"id":123,"media_type":"movie","title":"中文电影名","genres":[],"original_language":"en","origin_country":["US"],"production_countries":[]}`,
	} {
		var cached Media
		if err := json.Unmarshal([]byte(body), &cached); err != nil {
			t.Fatal(err)
		}
		if cached.CategoryMetadataKnown {
			t.Fatal("old cache without a marker was considered refreshed")
		}
		encoded, err := json.Marshal(cached)
		if err != nil || strings.Contains(string(encoded), "category_metadata_known") {
			t.Fatalf("false marker should remain omitted: %s %v", encoded, err)
		}
	}
}

func TestSearchMetadataNeverCertifiesCompleteDetail(t *testing.T) {
	body := categoryDetailFixture("movie")
	body["genre_ids"] = []int64{18}
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"page": 1, "total_pages": 1, "total_results": 1, "results": []any{body}})
	})
	page, err := client.Search(context.Background(), "中文电影名", "movie", 1)
	if err != nil || len(page.Results) != 1 {
		t.Fatalf("search failed: %+v %v", page, err)
	}
	media := page.Results[0]
	if media.CategoryMetadataKnown || media.OriginalLanguage != "en" || len(media.OriginCountry) != 1 || media.OriginCountry[0] != "US" || len(media.GenreIDs) != 1 || media.GenreIDs[0] != 18 {
		t.Fatalf("search conflated summaries with full details: %+v", media)
	}
}

func TestInvalidCategoryMetadataIsRejected(t *testing.T) {
	for _, test := range []struct {
		name, field string
		value       any
	}{
		{"long-language", "original_language", "eng"},
		{"region-language", "original_language", "zh-CN"},
		{"uppercase-language", "original_language", "EN"},
		{"non-ascii-language", "original_language", "汉"},
		{"control-language", "original_language", "e\n"},
		{"long-origin-country", "origin_country", []string{"USA"}},
		{"lowercase-origin-country", "origin_country", []string{"cn"}},
		{"empty-origin-country", "origin_country", []string{""}},
		{"non-ascii-origin-country", "origin_country", []string{"中国"}},
		{"empty-production-country", "production_countries", []Country{{Name: "China"}}},
		{"lowercase-production-country", "production_countries", []Country{{ISO3166_1: "cn", Name: "China"}}},
		{"many-origin-countries", "origin_country", make([]string, 101)},
		{"many-production-countries", "production_countries", make([]Country, 101)},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := categoryDetailFixture("movie")
			body[test.field] = test.value
			media, err := categoryDetailClient(t, body).Detail(context.Background(), "movie", 123)
			errorCode(t, err, "invalid_response")
			if media.CategoryMetadataKnown {
				t.Fatal("invalid metadata certified as complete")
			}
		})
	}
}

func TestCategoryCountryNamesUseProviderTextSanitization(t *testing.T) {
	body := categoryDetailFixture("movie")
	body["production_countries"] = []Country{{ISO3166_1: "US", Name: "synthetic-secret " + strings.Repeat("x", 300)}}
	media, err := categoryDetailClient(t, body).Detail(context.Background(), "movie", 123)
	if err != nil || !media.CategoryMetadataKnown || len(media.ProductionCountries) != 1 || len(media.ProductionCountries[0].Name) > 256 || strings.Contains(media.ProductionCountries[0].Name, "synthetic-secret") {
		t.Fatalf("unsafe country name: %+v %v", media, err)
	}
}

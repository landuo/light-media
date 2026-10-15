package catalog

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestDetailAppendedIMDbAndRegionalAliases(t *testing.T) {
	for _, kind := range []string{"movie", "tv"} {
		t.Run(kind, func(t *testing.T) {
			client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Query().Get("append_to_response") != "credits,recommendations,external_ids,alternative_titles" {
					t.Error("search identities not appended")
				}
				titleField, originalField, listField := "title", "original_title", "titles"
				if kind == "tv" {
					titleField, originalField, listField = "name", "original_name", "results"
				}
				fmt.Fprintf(writer, `{"id":123,%q:"中文标题",%q:"Original Title","overview":"Synopsis","external_ids":{"id":123,"imdb_id":"tt0898266","facebook_id":"private-social-value"},"alternative_titles":{"id":123,%q:[{"iso_3166_1":"US","title":"English Alias"},{"iso_3166_1":"HK","title":"香港名稱"},{"iso_3166_1":"CN","title":"大陆别名"},{"iso_3166_1":"CN","title":"  大陆别名  "},{"iso_3166_1":"US","title":"ORIGINAL TITLE"},{"iso_3166_1":"RU","title":"foreign variant"},{"iso_3166_1":"TW","title":"https://untrusted.example/title"}]},"credits":{"cast":[]},"recommendations":{"results":[]}}`, titleField, originalField, listField)
			})
			detail, err := client.Detail(context.Background(), kind, 123)
			if err != nil {
				t.Fatal(err)
			}
			if detail.IMDbID != "tt0898266" || strings.Join(detail.Aliases, "|") != "大陆别名|香港名稱|English Alias" {
				t.Fatalf("bad identities %+v", detail)
			}
			if strings.Contains(fmt.Sprintf("%+v", detail), "private-social") {
				t.Fatal("unrecognized external field exposed")
			}
		})
	}
}

func TestSearchIdentityMismatchAndInvalidIMDbRejected(t *testing.T) {
	for _, fragment := range []string{`"external_ids":{"id":999,"imdb_id":"tt0898266"}`, `"external_ids":{"imdb_id":"https://imdb.com/title/tt0898266"}`, `"imdb_id":"tt0898266","external_ids":{"imdb_id":"tt1234567"}`, `"alternative_titles":{"id":999,"titles":[]}`, `"alternative_titles":{"results":[]}`, `"external_ids":{"success":false}`} {
		client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
			fmt.Fprintf(writer, `{"id":123,"title":"电影","overview":"简介",%s,"credits":{"cast":[]},"recommendations":{"results":[]}}`, fragment)
		})
		_, err := client.Detail(context.Background(), "movie", 123)
		errorCode(t, err, "invalid_response")
	}
}

func TestAliasesAreBoundedAndUnsafeValuesSkipped(t *testing.T) {
	client := New(Credentials{APIKey: "synthetic-secret"})
	raw := wireMedia{ID: 123, AlternativeTitles: &wireAlternativeTitles{Titles: []wireAlternativeTitle{{Country: "CN", Title: "bad\ncontrol"}, {Country: "CN", Title: "synthetic-secret 别名"}, {Country: "US", Title: "tt1234567"}, {Country: "US", Title: "www.untrusted.example"}}}}
	for index := 0; index < 40; index++ {
		raw.AlternativeTitles.Titles = append(raw.AlternativeTitles.Titles, wireAlternativeTitle{Country: "CN", Title: fmt.Sprintf("别名 %d", index)})
	}
	_, aliases, err := client.searchIdentities(raw, "movie", "title", "original")
	if err != nil || len(aliases) != 16 || aliases[0] != "别名 0" {
		t.Fatalf("aliases=%v err=%v", aliases, err)
	}
	for len(raw.AlternativeTitles.Titles) <= 500 {
		raw.AlternativeTitles.Titles = append(raw.AlternativeTitles.Titles, wireAlternativeTitle{Country: "CN", Title: "x"})
	}
	_, _, err = client.searchIdentities(raw, "movie", "title", "original")
	errorCode(t, err, "invalid_response")
}

func TestSearchCardsDoNotClaimExternalIdentity(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, `{"page":1,"total_pages":1,"total_results":1,"results":[{"id":123,"title":"电影","imdb_id":"tt0898266","external_ids":{"imdb_id":"tt0898266"},"alternative_titles":{"titles":[{"iso_3166_1":"CN","title":"未确认搜索卡别名"}]}}]}`)
	})
	page, err := client.Search(context.Background(), "电影", "movie", 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Results[0].IMDbID != "" || len(page.Results[0].Aliases) != 0 {
		t.Fatal("search card claimed unconfirmed detail fields")
	}
}

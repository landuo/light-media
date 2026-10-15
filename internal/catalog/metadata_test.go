package catalog

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDetailRecommendationsCastAndEnglishFallback(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/3/tv/1418" {
			t.Errorf("wrong detail route: %s", request.URL.Path)
		}
		if request.URL.Query().Get("language") == "en-US" {
			fmt.Fprint(writer, `{"id":1418,"name":"The Big Bang Theory","overview":"English synopsis"}`)
			return
		}
		if request.URL.Query().Get("append_to_response") != "credits,recommendations,external_ids,alternative_titles" {
			t.Error("cast/recommendations not requested")
		}
		fmt.Fprint(writer, `{"id":1418,"name":"生活大爆炸","original_name":"The Big Bang Theory","first_air_date":"2007-09-24","status":"Ended","number_of_episodes":279,"number_of_seasons":12,"genres":[{"id":35,"name":"喜剧"}],"credits":{"cast":[{"id":2,"name":"演员二","character":"角色二","profile_path":"/two.jpg","order":1},{"id":1,"name":"演员一","character":"角色一","order":0}]},"recommendations":{"results":[{"id":12,"name":"推荐剧","first_air_date":"2000-01-01"}]},"seasons":[{"id":7,"season_number":7,"name":"第七季","episode_count":24,"air_date":"2013-09-26"}]}`)
	})
	detail, err := client.Detail(context.Background(), "tv", 1418)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || detail.Title != "生活大爆炸" || detail.Overview != "English synopsis" || detail.Cast[0].ID != 1 || detail.Cast[1].Character != "角色二" || detail.Recommendations[0].ID != 12 || detail.Seasons[0].SeasonNumber != 7 {
		t.Fatalf("incomplete detail: %+v", detail)
	}
}

func TestFallbackCannotChangeSourceIdentity(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("language") == "en-US" {
			fmt.Fprint(writer, `{"id":999,"title":"Wrong title","overview":"Wrong synopsis"}`)
			return
		}
		fmt.Fprint(writer, `{"id":123,"title":"中文标题","credits":{"cast":[]},"recommendations":{"results":[]}}`)
	})
	detail, err := client.Detail(context.Background(), "movie", 123)
	if err != nil || detail.ID != 123 || detail.Title != "中文标题" || detail.Overview != "" {
		t.Fatalf("fallback changed identity: %+v %v", detail, err)
	}
}

func TestDetailFailsWrongIDOrMissingAppendedData(t *testing.T) {
	for _, body := range []string{
		`{"id":999,"title":"x","overview":"x"}`,
		`{"id":123,"title":"x","overview":"x"}`,
		`{"id":123,"title":"x","overview":"x","credits":{"cast":[]},"recommendations":{"success":false,"status_code":34}}`,
	} {
		client := testClient(t, func(writer http.ResponseWriter, request *http.Request) { fmt.Fprint(writer, body) })
		_, err := client.Detail(context.Background(), "movie", 123)
		errorCode(t, err, "invalid_response")
	}
}

func TestSeasonPreservesEpisodeIDDateAndSpecials(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/3/tv/206484/season/0" {
			t.Errorf("incorrect series context: %s", request.URL.Path)
		}
		if request.URL.Query().Get("language") == "en-US" {
			fmt.Fprint(writer, `{"id":900,"season_number":0,"name":"Specials","overview":"Season description","episodes":[{"id":7706553,"season_number":0,"episode_number":5,"name":"English name","overview":"Episode description"}]}`)
			return
		}
		fmt.Fprint(writer, `{"id":900,"season_number":0,"name":"特别篇","episodes":[{"id":7706554,"season_number":0,"episode_number":6,"name":"下一集","overview":"简介","air_date":null,"guest_stars":[]},{"id":7706553,"season_number":0,"episode_number":5,"name":"偶遇小白","overview":"","air_date":"2026-09-04","guest_stars":[{"id":5,"name":"演员","character":"小白"}]}]}`)
	})
	season, err := client.Season(context.Background(), 206484, 0)
	if err != nil {
		t.Fatal(err)
	}
	if season.SeriesID != 206484 || season.ID != 900 || season.SeasonNumber != 0 || len(season.Episodes) != 2 || season.Name != "特别篇" || season.Overview != "Season description" {
		t.Fatalf("incorrect season: %+v", season)
	}
	episode := season.Episodes[0]
	if episode.ID != 7706553 || episode.EpisodeNumber != 5 || episode.SeasonNumber != 0 || episode.AirDate != "2026-09-04" || episode.Name != "偶遇小白" || episode.Overview != "Episode description" || episode.GuestStars[0].Character != "小白" {
		t.Fatalf("episode metadata conflated: %+v", episode)
	}
}

func TestSeasonRejectsDuplicateOrForeignEpisode(t *testing.T) {
	for _, episodes := range []string{
		`[{"id":10,"season_number":1,"episode_number":1,"name":"x"},{"id":11,"season_number":1,"episode_number":1,"name":"x"}]`,
		`[{"id":10,"season_number":1,"episode_number":1,"name":"x"},{"id":10,"season_number":1,"episode_number":2,"name":"x"}]`,
		`[{"id":10,"season_number":2,"episode_number":1,"name":"x"}]`,
		`[{"id":10,"season_number":1,"episode_number":1,"air_date":"2026-02-30","name":"x"}]`,
	} {
		client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
			fmt.Fprintf(writer, `{"id":123,"season_number":1,"name":"一","overview":"简介","episodes":%s}`, episodes)
		})
		_, err := client.Season(context.Background(), 12, 1)
		errorCode(t, err, "invalid_response")
	}
}

func TestPersonBiographyFallbackAndCreditRoleMerge(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/combined_credits") {
			fmt.Fprint(writer, `{"id":100,"cast":[{"id":10,"media_type":"movie","title":"电影","character":"主角","popularity":50},{"id":11,"media_type":"tv","name":"电视剧","character":"客串","popularity":10}],"crew":[{"id":10,"media_type":"movie","title":"电影","job":"Director","popularity":50},{"id":10,"media_type":"movie","title":"电影","job":"Producer","popularity":50}]}`)
			return
		}
		if request.URL.Query().Get("language") == "en-US" {
			fmt.Fprint(writer, `{"id":100,"name":"English Name","biography":"English biography"}`)
			return
		}
		fmt.Fprint(writer, `{"id":100,"name":"中文名","biography":"","birthday":"1980-05-03","deathday":null,"place_of_birth":"上海","profile_path":"/actor.jpg","also_known_as":["别名"],"known_for_department":"Acting"}`)
	})
	person, err := client.Person(context.Background(), 100)
	if err != nil || person.Name != "中文名" || person.Biography != "English biography" || person.Birthday != "1980-05-03" || person.AlsoKnownAs[0] != "别名" {
		t.Fatalf("person=%+v err=%v", person, err)
	}
	credits, err := client.PersonCredits(context.Background(), 100)
	if err != nil || credits.TotalResults != 2 || credits.TotalPages != 1 || len(credits.Results) != 2 {
		t.Fatalf("credits=%+v err=%v", credits, err)
	}
	if credits.Results[0].ID != 10 || credits.Results[0].Character != "主角" || credits.Results[0].Job != "Director / Producer" {
		t.Fatalf("roles lost: %+v", credits.Results[0])
	}
}

func TestUntrustedImagesAndCredentialEchoAreRemoved(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, `{"page":1,"total_pages":1,"total_results":1,"results":[{"id":123,"title":"synthetic-secret title","overview":"synthetic-secret text","poster_path":"https://untrusted.example/collect?token=x","backdrop_path":"/synthetic-secret.jpg"}]}`)
	})
	page, err := client.Search(context.Background(), "x", "movie", 1)
	if err != nil {
		t.Fatal(err)
	}
	media := page.Results[0]
	if strings.Contains(media.Title, "synthetic-secret") || strings.Contains(media.Overview, "synthetic-secret") || media.PosterPath != "" || media.BackdropPath != "" {
		t.Fatalf("unsafe provider field: %+v", media)
	}
}

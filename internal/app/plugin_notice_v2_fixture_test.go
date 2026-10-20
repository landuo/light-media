package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/networkproxy"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

type noticeSourceFixture struct{ Metadata, Sends atomic.Int64 }
type noticeSourceRoundTrip func(*http.Request) (*http.Response, error)

func (f noticeSourceRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func installNoticeSourceFixtures(t *testing.T, a *App, media catalog.Media, season, episodes int) *noticeSourceFixture {
	t.Helper()
	fixture := &noticeSourceFixture{}
	actors := []any{}
	for _, actor := range media.Cast {
		actors = append(actors, map[string]any{"id": actor.ID, "name": actor.Name, "character": actor.Character, "known_for_department": "Acting", "order": actor.Order})
	}
	genres := []any{}
	for _, genre := range media.Genres {
		genres = append(genres, map[string]any{"id": genre.ID, "name": genre.Name})
	}
	raw := map[string]any{"id": media.ID, "original_language": media.OriginalLanguage, "origin_country": media.OriginCountry, "production_countries": media.ProductionCountries, "genres": genres, "overview": media.Overview, "poster_path": "/source_poster.jpg", "backdrop_path": "/source_backdrop.jpg", "vote_average": 7.3, "credits": map[string]any{"cast": actors, "crew": []any{}}, "external_ids": map[string]any{"imdb_id": "tt1234567", "tvdb_id": 41}, "translations": map[string]any{"translations": []any{}}, "alternative_titles": map[string]any{"titles": []any{}, "results": []any{}}}
	date := strconv.Itoa(media.Year) + "-01-01"
	if media.MediaType == "tv" {
		raw["name"], raw["original_name"], raw["first_air_date"] = media.Title, media.OriginalTitle, date
		raw["seasons"] = []any{map[string]any{"season_number": season, "episode_count": episodes, "air_date": date}}
	} else {
		raw["title"], raw["original_title"], raw["release_date"] = media.Title, media.OriginalTitle, date
	}
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.Metadata.Add(1)
		if r.URL.Path != "/3/"+media.MediaType+"/"+strconv.FormatInt(media.ID, 10) || r.URL.Query().Get("append_to_response") == "" || r.URL.Query().Get("api_key") != "owned-source-tmdb" {
			t.Error("unexpected owned fresh metadata request", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		workflowJSON(w, raw)
	}))
	t.Cleanup(metadata.Close)
	target, _ := url.Parse(metadata.URL)
	a.config.TMDB = TMDBConfig{APIKey: "owned-source-tmdb"}
	client := catalog.NewWithTransport(catalog.Credentials{APIKey: "owned-source-tmdb"}, noticeSourceRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.themoviedb.org" {
			t.Fatal("fresh source metadata escaped its fixed official authority")
		}
		clone := r.Clone(r.Context())
		u := *r.URL
		clone.URL = &u
		clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
		return metadata.Client().Transport.RoundTrip(clone)
	}))
	a.catClient, a.catCreds = client, a.Settings().TMDB
	a.catProxy, _ = networkproxy.Resolve(a.Settings().ProxyHost, os.Getenv)
	sender := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.Sends.Add(1)
		var call map[string]any
		if r.Method != "POST" || r.URL.Path != "/owned-notice" || json.NewDecoder(r.Body).Decode(&call) != nil || sourceMap(call["kwargs"])["message"] == nil {
			t.Error("owned notice sender did not receive the actual queue call")
			w.WriteHeader(400)
			return
		}
		fmt.Fprint(w, `{"accepted":true}`)
	}))
	t.Cleanup(sender.Close)
	a.pluginStore.notices.send = func(ctx context.Context, q pluginNoticeQueueEntry) (int, error) {
		body, e := json.Marshal(q.Call)
		if e != nil {
			return 0, e
		}
		r, e := http.NewRequestWithContext(ctx, http.MethodPost, sender.URL+"/owned-notice", bytes.NewReader(body))
		if e != nil {
			return 0, e
		}
		response, e := sender.Client().Do(r)
		if e != nil {
			return 0, seedhost.ErrUnknown
		}
		defer response.Body.Close()
		var ack struct{ Accepted bool }
		if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 1024)).Decode(&ack) != nil || !ack.Accepted {
			return 0, seedhost.ErrUnknown
		}
		return 1, nil
	}
	return fixture
}

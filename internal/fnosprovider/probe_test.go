package fnosprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"light-media/internal/fnos"
)

type syntheticTransport func(*http.Request) (*http.Response, error)

func (f syntheticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(data any) *http.Response {
	body, _ := json.Marshal(map[string]any{"code": 0, "data": data})
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}
}
func syntheticRow(id string, explicit bool) map[string]any {
	var value map[string]any
	switch id {
	case "1602727":
		value = map[string]any{"title": "夜王", "type": "Movie", "release_date": "2026-01-01"}
	case "1418":
		value = map[string]any{"title": "生活大爆炸", "type": "TV", "first_air_date": "2007-01-01"}
	case "206484":
		value = map[string]any{"title": "诛仙", "type": "TV", "first_air_date": "2022-01-01"}
	default:
		panic("unexpected synthetic lookup")
	}
	if explicit {
		value["source"], value["source_id"], value["tmdb_id"] = "tmdb", id, id
	} else {
		value["source"], value["source_id"] = "trim_id", "tt"+id
	}
	return value
}
func TestPureQueryProtocolNoMutationAndNoEpisodeGuess(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: syntheticTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.Host != "fnos.example.invalid" || req.URL.Scheme != "https" || !strings.HasPrefix(req.URL.Path, "/v/api/v1/") {
			t.Fatal("origin escape")
		}
		if req.Header.Get("authx") == "" {
			t.Fatal("missing authx")
		}
		switch req.URL.Path {
		case "/v/api/v1/sys/version":
			if req.Method != "GET" {
				t.Fatal("version not GET")
			}
			return response(map[string]string{"version": "0.9.8"}), nil
		case "/v/api/v1/login":
			return response(map[string]string{"token": "synthetic-auth-token"}), nil
		case "/v/api/v1/scrap/search":
			if req.Method != "POST" || req.Header.Get("Authorization") != "synthetic-auth-token" {
				t.Fatal("query auth/method")
			}
			var body map[string]string
			if json.NewDecoder(req.Body).Decode(&body) != nil || len(body) != 3 || body["source"] != "tmdb_id" || body["lan"] != "zh-CN" {
				t.Fatal("invented query fields")
			}
			return response([]any{syntheticRow(body["source_keyword"], true)}), nil
		default:
			t.Fatal("mutation/local-GUID route called")
			return nil, nil
		}
	})}
	r := probe(context.Background(), fnos.Config{Host: "https://fnos.example.invalid", Username: "synthetic-user", Password: "synthetic-password"}, client)
	if r.Status != "partial_evidence" || !r.ProviderIdentityComplete || r.EpisodeIdentityComplete || requests != 5 || r.WriteOperations {
		t.Fatalf("%+v", r)
	}
	for _, entry := range r.Results {
		if entry.TargetEpisode != nil && entry.TargetEpisode.Status != "unverified" {
			t.Fatal("episode guessed")
		}
	}
	b, _ := json.Marshal(r)
	for _, secret := range []string{"synthetic-password", "synthetic-user", "synthetic-auth-token", "fnos.example.invalid", "authx"} {
		if bytes.Contains(b, []byte(secret)) {
			t.Fatal("secret or URL leaked")
		}
	}
}
func TestOpaqueVendorIDsAreNotStandardIDs(t *testing.T) {
	for _, s := range fixed {
		raw, _ := json.Marshal([]any{syntheticRow(s.id, false)})
		r := inspect(raw, s)
		if r.Status != "unverified" || r.ExplicitTMDBID != 0 || r.Reason != "standard_tmdb_namespace_id_not_verified" {
			t.Fatalf("%+v", r)
		}
	}
}

func TestStandardIDAssertionsMustAgreeAndRemainValid(t *testing.T) {
	s := fixed[1]
	cases := []struct {
		name     string
		tmdb     any
		sourceID any
		reason   string
	}{
		{"conflict", "1418", "206484", "standard_tmdb_id_conflict"},
		{"invalid-explicit-prefix", "tt1418", "1418", "invalid_explicit_tmdb_id"},
		{"invalid-explicit-null", nil, "1418", "invalid_explicit_tmdb_id"},
		{"invalid-explicit-zero", 0, "1418", "invalid_explicit_tmdb_id"},
		{"invalid-explicit-fraction", 1418.5, "1418", "invalid_explicit_tmdb_id"},
		{"invalid-source-prefix", "1418", "tt1418", "invalid_tmdb_source_id"},
		{"invalid-source-null", "1418", nil, "invalid_tmdb_source_id"},
		{"invalid-source-zero", "1418", 0, "invalid_tmdb_source_id"},
		{"invalid-source-array", "1418", []any{"1418"}, "invalid_tmdb_source_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value := syntheticRow(s.id, true)
			value["tmdb_id"], value["source_id"] = tc.tmdb, tc.sourceID
			raw, _ := json.Marshal([]any{value})
			r := inspect(raw, s)
			if r.Status != "unverified" || r.Reason != tc.reason || r.ExplicitTMDBID != 0 {
				t.Fatalf("assertion incorrectly chosen: %+v", r)
			}
		})
	}
	for _, source := range []string{"tmdb", "TMDB", "tmdb_id"} {
		value := syntheticRow(s.id, true)
		value["source"], value["source_id"], value["tmdb_id"] = source, int64(1418), "1418"
		raw, _ := json.Marshal([]any{value})
		if r := inspect(raw, s); r.Status != "ok" || r.ExplicitTMDBID != 1418 {
			t.Fatalf("consistent assertions %+v", r)
		}
	}
	// Different explicitly declared namespaces can legitimately use different IDs.
	value := syntheticRow(s.id, true)
	value["source"], value["source_id"] = "trim_id", "tt1418"
	raw, _ := json.Marshal([]any{value})
	if r := inspect(raw, s); r.Status != "ok" || r.ExplicitTMDBID != 1418 {
		t.Fatalf("opaque namespace %+v", r)
	}
	delete(value, "tmdb_id")
	value["source"], value["source_id"] = "tmdb_id", "1418"
	raw, _ = json.Marshal([]any{value})
	if r := inspect(raw, s); r.Status != "ok" || r.ExplicitTMDBID != 1418 {
		t.Fatalf("single explicit source assertion %+v", r)
	}
}
func TestExplicitEpisodeIDsNeedExactParentSeasonDate(t *testing.T) {
	s := fixed[1]
	makeData := func(parent string, season int) []byte {
		value := syntheticRow(s.id, true)
		value["episodes"] = []any{map[string]any{"tmdb_id": "900000001", "show_id": parent, "season_number": season, "episode_number": 17, "name": "合成单集", "air_date": "2014-01-01"}}
		raw, _ := json.Marshal([]any{value})
		return raw
	}
	r := inspect(makeData("1418", 7), s)
	if r.Status != "ok" || r.TargetEpisode.Status != "ok" || r.TargetEpisode.TMDBID != 900000001 || r.TargetEpisode.ParentTMDBID != 1418 {
		t.Fatalf("%+v", r)
	}
	for _, raw := range [][]byte{makeData("206484", 7), makeData("1418", 6)} {
		r := inspect(raw, s)
		if r.TargetEpisode.Status != "unverified" {
			t.Fatal("wrong parent/season accepted")
		}
	}
	value := syntheticRow(s.id, true)
	value["episodes"] = []any{map[string]any{"id": 900000001, "show_id": 1418, "season_number": 7, "episode_number": 17, "name": "合成单集", "air_date": "2014-01-01"}}
	raw, _ := json.Marshal([]any{value})
	r = inspect(raw, s)
	if r.TargetEpisode.Status != "unverified" {
		t.Fatal("ambiguous id namespace accepted as tmdb_id")
	}
}
func TestAuthFailureRedirectAndBodyLimit(t *testing.T) {
	for _, kind := range []string{"business", "redirect", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			requests := 0
			client := &http.Client{Transport: syntheticTransport(func(req *http.Request) (*http.Response, error) {
				requests++
				r := response(map[string]string{"token": "must-not-be-used"})
				switch kind {
				case "business":
					r.Body = io.NopCloser(strings.NewReader(`{"code":99,"msg":"synthetic-password"}`))
				case "redirect":
					r.StatusCode = 302
					r.Header.Set("Location", "https://evil.invalid/?password=synthetic-password")
				case "oversize":
					r.Body = io.NopCloser(bytes.NewReader(bytes.Repeat([]byte{'x'}, bodyLimit+1)))
				}
				return r, nil
			})}
			r := probe(context.Background(), fnos.Config{Host: "https://fnos.example.invalid", Username: "synthetic-user", Password: "synthetic-password"}, client)
			if r.Status != "failed" || requests != 1 || r.ProviderIdentityComplete || r.EpisodeIdentityComplete {
				t.Fatalf("%+v", r)
			}
			encoded, _ := json.Marshal(r)
			if bytes.Contains(encoded, []byte("synthetic-password")) || bytes.Contains(encoded, []byte("evil.invalid")) {
				t.Fatal("error/redirect leaked")
			}
		})
	}
	r := Probe(context.Background(), fnos.Config{})
	if r.Status != "unverified" || r.Requests != 0 {
		t.Fatal("unconfigured performed requests")
	}
}

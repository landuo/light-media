package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type syntheticRoundTrip func(*http.Request) (*http.Response, error)

func (f syntheticRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func syntheticResponse(value any, status int) *http.Response {
	b, _ := json.Marshal(value)
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(b))}
}

// Values and dates below are synthetic test data, not captured/live responses.
func syntheticDetails(path string) map[string]any {
	switch path {
	case "/3/movie/1602727":
		return map[string]any{"id": int64(1602727), "title": "夜王", "release_date": "2026-01-01"}
	case "/3/tv/1418":
		return map[string]any{"id": int64(1418), "name": "生活大爆炸", "first_air_date": "2007-01-01"}
	case "/3/tv/1418/season/7/episode/17":
		return map[string]any{"id": int64(900000001), "name": "合成普通剧单集", "air_date": "2014-01-01", "season_number": 7, "episode_number": 17}
	case "/3/tv/206484":
		return map[string]any{"id": int64(206484), "name": "诛仙", "first_air_date": "2022-01-01"}
	case "/3/tv/206484/season/4/episode/5":
		return map[string]any{"id": int64(900000002), "name": "合成国漫单集", "air_date": "2026-01-01", "season_number": 4, "episode_number": 5}
	default:
		panic("unexpected synthetic request route")
	}
}

func TestFixedFivePrimaryRoutesAndAuthentication(t *testing.T) {
	for _, credential := range []Credentials{{APIKey: "synthetic-api-key"}, {AccessToken: "synthetic.read.access-token"}} {
		t.Run(credential.APIKey+credential.AccessToken, func(t *testing.T) {
			var paths []string
			client := &http.Client{Transport: syntheticRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.themoviedb.org" {
					t.Fatal("host/method escape")
				}
				if r.URL.Query().Get("language") != "zh-CN" || r.URL.Query().Get("api_key") != credential.APIKey {
					t.Fatal("query authentication")
				}
				if credential.AccessToken != "" {
					if r.Header.Get("Authorization") != "Bearer "+credential.AccessToken || r.URL.Query().Has("api_key") {
						t.Fatal("Bearer authentication")
					}
				} else if r.Header.Get("Authorization") != "" {
					t.Fatal("unexpected Authorization")
				}
				paths = append(paths, r.URL.Path)
				return syntheticResponse(syntheticDetails(r.URL.Path), 200), nil
			})}
			r := run(context.Background(), credential, client)
			if r.Status != "ok" || r.Evidence != "synthetic_http_roundtrip" || r.Requests != 5 || len(paths) != 5 {
				t.Fatalf("%+v", r)
			}
			for i, expected := range samples {
				if paths[i] != "/3/"+expected.path || r.Results[i].Status != "ok" || r.Results[i].SourceKey() == "" {
					t.Fatal("route/identity mismatch")
				}
			}
			if r.Results[2].SourceID == 1418 || r.Results[2].ParentSourceID != 1418 || r.Results[2].ParentContext != "verified_series_and_fixed_request_route" {
				t.Fatal("episode ID confused with show ID")
			}
			b, _ := json.Marshal(r)
			for _, secret := range []string{credential.APIKey, credential.AccessToken, "api.themoviedb.org", "api_key=", "Authorization"} {
				if secret != "" && bytes.Contains(b, []byte(secret)) {
					t.Fatal("credential or secret URL emitted")
				}
			}
		})
	}
}

func TestNoCredentialAndInputBoundary(t *testing.T) {
	r := Run(context.Background(), Credentials{})
	if r.Status != "unverified" || r.Requests != 0 || r.Evidence != "not_requested" {
		t.Fatalf("%+v", r)
	}
	for _, input := range []string{`{"base_url":"https://evil.invalid","api_key":"secret"}`, `{"api_key":"one"} {"access_token":"two"}`, strings.Repeat("x", 65537)} {
		if _, err := ParseCredentials([]byte(input)); err == nil {
			t.Fatal("unsupported input accepted")
		}
	}
	for _, c := range []Credentials{{APIKey: "one", AccessToken: "two"}, {AccessToken: "secret\nheader"}, {APIKey: "secret?query"}} {
		r := Run(context.Background(), c)
		if r.Status != "invalid_input" || r.Requests != 0 {
			t.Fatal("invalid credentials performed requests")
		}
	}
}

func TestSourceTypeYearSeasonAndParentMismatch(t *testing.T) {
	cases := []struct {
		name, path, field string
		value             any
		index             int
		reason            string
	}{
		{"ID", "/3/movie/1602727", "id", int64(999), 0, "source_id_mismatch"},
		{"title", "/3/movie/1602727", "title", "另一个电影", 0, "localized_title_mismatch"},
		{"year", "/3/movie/1602727", "release_date", "2025-01-01", 0, "release_year_mismatch"},
		{"type", "/3/tv/1418", "media_type", "movie", 1, "media_type_mismatch"},
		{"season", "/3/tv/1418/season/7/episode/17", "season_number", 6, 2, "season_episode_mismatch"},
		{"episode", "/3/tv/206484/season/4/episode/5", "episode_number", 6, 4, "season_episode_mismatch"},
		{"parent", "/3/tv/206484/season/4/episode/5", "show_id", int64(1418), 4, "response_parent_id_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: syntheticRoundTrip(func(req *http.Request) (*http.Response, error) {
				value := syntheticDetails(req.URL.Path)
				if req.URL.Path == tc.path {
					value[tc.field] = tc.value
				}
				return syntheticResponse(value, 200), nil
			})}
			r := run(context.Background(), Credentials{APIKey: "synthetic-key"}, client)
			if r.Status != "incomplete" || r.Results[tc.index].Status != "identity_mismatch" || r.Results[tc.index].Reason != tc.reason || r.Results[tc.index].SourceID != 0 {
				t.Fatalf("%+v", r)
			}
		})
	}
	client := &http.Client{Transport: syntheticRoundTrip(func(req *http.Request) (*http.Response, error) {
		value := syntheticDetails(req.URL.Path)
		if req.URL.Path == "/3/tv/1418" {
			value["id"] = 999
		}
		return syntheticResponse(value, 200), nil
	})}
	r := run(context.Background(), Credentials{APIKey: "synthetic-key"}, client)
	if r.Results[2].Status != "unverified" || r.Results[2].Reason != "parent_series_not_verified" || r.Results[2].SourceID != 0 {
		t.Fatal("episode claimed verified with wrong parent")
	}
}

func TestErrorsBodyLimitAndSecretRedaction(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   any
		want   string
		count  int
	}{
		{"401", 401, map[string]any{"status_message": "synthetic-secret"}, "authentication_failed", 1},
		{"429", 429, map[string]any{"status_message": "synthetic-secret"}, "rate_limited", 1},
		{"404", 404, map[string]any{"status_message": "synthetic-secret"}, "not_found", 5},
		{"business-auth", 200, map[string]any{"success": false, "status_code": 7, "status_message": "synthetic-secret"}, "authentication_failed", 1},
		{"business", 200, map[string]any{"success": false, "status_code": 999, "status_message": "synthetic-secret"}, "business_error", 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: syntheticRoundTrip(func(req *http.Request) (*http.Response, error) { return syntheticResponse(tc.body, tc.status), nil })}
			r := run(context.Background(), Credentials{APIKey: "synthetic-secret"}, client)
			if r.Status != "incomplete" || r.Results[0].Status != tc.want || r.Requests != tc.count {
				t.Fatalf("%+v", r)
			}
			b, _ := json.Marshal(r)
			if bytes.Contains(b, []byte("synthetic-secret")) || bytes.Contains(b, []byte("status_message")) {
				t.Fatal("provider error leaked")
			}
		})
	}
	client := &http.Client{Transport: syntheticRoundTrip(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(bytes.Repeat([]byte{'x'}, MaxBody+1)))}, nil
	})}
	if r := run(context.Background(), Credentials{APIKey: "synthetic-key"}, client); r.Results[0].Status != "body_limit" {
		t.Fatal("oversized body accepted")
	}
	client = &http.Client{Transport: syntheticRoundTrip(func(req *http.Request) (*http.Response, error) {
		value := syntheticDetails(req.URL.Path)
		if strings.Contains(req.URL.Path, "/episode/") {
			value["name"] = "synthetic-key"
		}
		return syntheticResponse(value, 200), nil
	})}
	r := run(context.Background(), Credentials{APIKey: "synthetic-key"}, client)
	b, _ := json.Marshal(r)
	if r.Results[2].Status != "protocol_changed" || bytes.Contains(b, []byte("synthetic-key")) {
		t.Fatal("sensitive title emitted")
	}
}

func TestRedirectMalformedAndTransportErrorStayNonSuccess(t *testing.T) {
	for _, kind := range []string{"redirect", "HTML", "transport"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: syntheticRoundTrip(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Host != "api.themoviedb.org" {
					t.Fatal("followed secret redirect")
				}
				switch kind {
				case "redirect":
					r := syntheticResponse(nil, 302)
					r.Header.Set("Location", "https://evil.invalid/?api_key=synthetic-secret")
					return r, nil
				case "HTML":
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("<html>synthetic-secret</html>"))}, nil
				default:
					return nil, errors.New("https://api.themoviedb.org/?api_key=synthetic-secret")
				}
			})}
			r := run(context.Background(), Credentials{APIKey: "synthetic-secret"}, client)
			want := map[string]string{"redirect": "redirect_blocked", "HTML": "protocol_changed", "transport": "transport_error"}[kind]
			if r.Status != "incomplete" || r.Results[0].Status != want || calls != 5 {
				t.Fatalf("%+v", r)
			}
			b, _ := json.Marshal(r)
			if bytes.Contains(b, []byte("synthetic-secret")) {
				t.Fatal("URL/transport error leaked")
			}
		})
	}
}

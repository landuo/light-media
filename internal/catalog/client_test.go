package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != apiHost || !strings.HasPrefix(request.URL.Path, "/3/") {
			t.Errorf("request left fixed official origin: scheme=%q host=%q path=%q", request.URL.Scheme, request.URL.Host, request.URL.Path)
		}
		clone := request.Clone(request.Context())
		copied := *request.URL
		clone.URL = &copied
		clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
		return server.Client().Transport.RoundTrip(clone)
	})
	return newClient(Credentials{APIKey: "synthetic-secret"}, transport, 0)
}

func errorCode(t *testing.T, err error, expected string) *Error {
	t.Helper()
	var result *Error
	if !errors.As(err, &result) || result.Code != expected {
		t.Fatalf("got %v, want error code %q", err, expected)
	}
	return result
}

func TestSearchEscapingNormalizationAndPeopleFiltering(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if request.Method != "GET" || request.URL.Path != "/3/search/multi" || query.Get("query") != "绝世唐门 & x=1" || query.Get("page") != "2" || query.Get("include_adult") != "false" || query.Get("language") != "zh-CN" || query.Get("api_key") != "synthetic-secret" {
			t.Errorf("incorrect search request")
		}
		fmt.Fprint(writer, `{"page":2,"total_pages":800,"total_results":1000,"results":[{"id":123,"media_type":"tv","name":"绝世唐门","original_name":"Soul Land","first_air_date":"2023-06-24","poster_path":"/a.jpg","vote_average":8.5,"genre_ids":[16]},{"id":9,"media_type":"person","name":"演员"},{"id":10,"media_type":"movie","title":"Adult","adult":true}]}`)
	})
	page, err := client.Search(context.Background(), " 绝世唐门 & x=1 ", "all", 2)
	if err != nil || len(page.Results) != 1 || page.TotalPages != 500 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	media := page.Results[0]
	if media.ID != 123 || media.MediaType != "tv" || media.Title != "绝世唐门" || media.Year != 2023 || media.ReleaseDate != "2023-06-24" || media.GenreIDs[0] != 16 {
		t.Fatalf("incorrect normalized TV identity: %+v", media)
	}
}

func TestConfiguredTokenNeverSentAsQuery(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer synthetic.token" || request.URL.Query().Has("api_key") {
			t.Error("incorrect authentication method")
		}
		fmt.Fprint(writer, `{"page":1,"total_pages":0,"total_results":0,"results":[]}`)
	})
	client.credentials = Credentials{AccessToken: "synthetic.token"}
	if _, err := client.Browse(context.Background(), "tv", "popular", 1); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidParametersNeverMakeRequest(t *testing.T) {
	client := testClient(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP request") })
	for _, test := range []struct {
		kind, query string
		page        int
	}{{"movie", "", 1}, {"movie", "x\n", 1}, {"movie", strings.Repeat("中", 121), 1}, {"movie/../../", "x", 1}, {"movie", "x", 0}, {"movie", "x", 501}} {
		_, err := client.Search(context.Background(), test.query, test.kind, test.page)
		errorCode(t, err, "invalid_input")
	}
	if _, err := client.Browse(context.Background(), "all", "upcoming", 1); err == nil {
		t.Fatal("unsupported ranking accepted")
	}
	if _, err := client.Detail(context.Background(), "tv", -1); err == nil {
		t.Fatal("invalid identity accepted")
	}
	if _, err := client.Season(context.Background(), 12, -1); err == nil {
		t.Fatal("negative season accepted")
	}
	for _, credentials := range []Credentials{{}, {APIKey: "x", AccessToken: "y"}, {APIKey: "invalid key"}, {AccessToken: "has\nnewline"}} {
		invalid := newClient(credentials, roundTripFunc(func(*http.Request) (*http.Response, error) { t.Error("unexpected auth HTTP"); return nil, nil }), 0)
		_, err := invalid.Search(context.Background(), "x", "movie", 1)
		if err == nil {
			t.Fatal("invalid credentials accepted")
		}
	}
}

func TestRedirectNeverForwardsCredentials(t *testing.T) {
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer other.Close()
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, other.URL+"/collect", http.StatusFound)
	})
	_, err := client.Search(context.Background(), "x", "movie", 1)
	errorCode(t, err, "redirect_blocked")
	if redirected.Load() != 0 {
		t.Fatal("redirect reached another server")
	}
}

func TestBusinessErrorsAndUnsafeResponses(t *testing.T) {
	for _, test := range []struct {
		name, body, code string
		status           int
	}{
		{"business-auth", `{"success":false,"status_code":7,"status_message":"synthetic-secret"}`, "unauthorized", 200},
		{"http-auth", `{"status_message":"synthetic-secret"}`, "unauthorized", 401},
		{"not-found", `{}`, "not_found", 404},
		{"server-error", "synthetic-secret", "upstream", 503},
		{"broken-json", `{"page":1`, "invalid_response", 200},
		{"trailing-json", `{"page":1} {}`, "invalid_response", 200},
		{"missing-results", `{"page":1,"total_pages":1,"total_results":1}`, "invalid_response", 200},
		{"bad-date", `{"page":1,"results":[{"id":1,"title":"x","release_date":"invalid"}]}`, "invalid_response", 200},
		{"oversize", strings.Repeat(" ", maxBody+1), "invalid_response", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(test.status)
				fmt.Fprint(writer, test.body)
			})
			_, err := client.Search(context.Background(), "x", "movie", 1)
			errorCode(t, err, test.code)
			if strings.Contains(fmt.Sprintf("%v %#v", err, err), "synthetic-secret") {
				t.Fatal("credential entered safe error")
			}
		})
	}
}

func TestTransportErrorDoesNotExposeRequestOrCause(t *testing.T) {
	client := newClient(Credentials{APIKey: "synthetic-secret"}, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("https://api.themoviedb.org/3/search/movie?api_key=synthetic-secret")
	}), 0)
	_, err := client.Search(context.Background(), "x", "movie", 1)
	errorCode(t, err, "unavailable")
	if strings.Contains(fmt.Sprint(err), "secret") || strings.Contains(fmt.Sprint(err), "https") {
		t.Fatal("transport cause leaked")
	}
	encoded, _ := json.Marshal(client.credentials)
	if string(encoded) != "{}" || strings.Contains(fmt.Sprintf("%+v %#v", client.credentials, client), "synthetic-secret") {
		t.Fatal("formatting leaked credentials")
	}
}

func TestRateLimitCooldownAndCanceledContext(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.Header().Set("Retry-After", "9999")
		writer.WriteHeader(429)
	})
	_, err := client.Search(context.Background(), "x", "movie", 1)
	rate := errorCode(t, err, "rate_limited")
	if rate.RetryAfter != time.Minute {
		t.Fatalf("retry not bounded: %v", rate.RetryAfter)
	}
	_, err = client.Search(context.Background(), "x", "movie", 1)
	errorCode(t, err, "rate_limited")
	if calls.Load() != 1 {
		t.Fatal("request made during provider cooldown")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Search(ctx, "x", "movie", 1)
	errorCode(t, err, "canceled")
}

func TestLimiterWaitRespectsContext(t *testing.T) {
	client := testClient(t, func(writer http.ResponseWriter, request *http.Request) { t.Error("request should remain queued") })
	client.next = time.Now().Add(time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := client.Search(ctx, "x", "movie", 1)
	errorCode(t, err, "canceled")
	if time.Since(started) > time.Second {
		t.Fatal("cancellation did not interrupt limiter")
	}
}

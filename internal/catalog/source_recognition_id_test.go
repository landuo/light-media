package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// 本根是已有 RoundTripper 接缝上的真实 builder/decoder 门，不是 socket HTTP。
func TestSourceRecognitionDetailRawBeforeCompact(t *testing.T) {
	for _, kind := range []string{"movie", "tv"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			want := `{"id":42,"source_only":{"integer":9007199254740993}}`
			client := NewWithTransport(Credentials{APIKey: "owned"}, sourceInfoTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				fields := "images,credits,alternative_titles,translations,release_dates,external_ids"
				if kind == "tv" {
					fields = "images,credits,alternative_titles,translations,content_ratings,external_ids,episode_groups"
				}
				q := r.URL.Query()
				if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.themoviedb.org" || r.URL.Path != "/3/"+kind+"/42" || len(q) != 3 || q.Get("append_to_response") != fields || q.Get("language") != "zh-CN" || q.Get("api_key") != "owned" {
					t.Fatal("unexpected fixed provider request")
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(want)), Request: r}, nil
			}))
			got, err := client.SourceRecognitionDetail(context.Background(), kind, 42)
			if err != nil || string(got) != want || calls != 1 {
				t.Fatalf("raw detail changed: %s, %v, calls=%d", got, err, calls)
			}
			// SourceInformation's UI projection still has its existing requirements.
			if _, err = client.ProjectSourceInformation(kind, 42, json.RawMessage(want), nil); err == nil {
				t.Fatal("fixture no longer distinguishes raw from compact projection")
			}
		})
	}
}

func TestSourceRecognitionDetailFiniteErrors(t *testing.T) {
	calls := 0
	client := NewWithTransport(Credentials{APIKey: "owned"}, sourceInfoTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"success":false,"status_code":34}`)), Request: r}, nil
	}))
	_, err := client.SourceRecognitionDetail(context.Background(), "movie", 42)
	var provider *Error
	if !errors.As(err, &provider) || provider.Code != "not_found" || calls != 1 {
		t.Fatalf("missing response was not preserved: %v calls=%d", err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = client.SourceRecognitionDetail(ctx, "movie", 42); err == nil || calls != 1 {
		t.Fatal("canceled call reached transport or became None")
	}
	if _, err = client.SourceRecognitionDetail(context.Background(), "unknown", 42); err == nil || calls != 1 {
		t.Fatal("unsupported type reached transport")
	}
}

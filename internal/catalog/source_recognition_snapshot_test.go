package catalog

import (
	"context"
	"errors"
	"io"
	"light-media/internal/networkproxy"
	"net/http"
	"strings"
	"testing"
)

func TestSourceRecognitionSnapshotErrorsAndGuard(t *testing.T) {
	body := "{\"success\":false,\"status_code\":34,\"status_message\":\"owned missing\"}"
	calls, guards := 0, 0
	revoked := false
	deny := errors.New("owned authority revoked")
	guard := func() error {
		guards++
		if revoked {
			return deny
		}
		return nil
	}
	client := NewWithTransport(Credentials{APIKey: "owned"}, sourceInfoTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.Scheme != "https" || r.URL.Host != "api.themoviedb.org" || r.URL.Path != "/3/movie/42" {
			t.Fatal("fixed endpoint changed")
		}
		return &http.Response{StatusCode: 404, Header: http.Header{"X-Owned": []string{"original"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}))
	snapshot, err := client.SourceRecognitionDetailSnapshot(context.Background(), "movie", 42, guard)
	if err != nil || snapshot == nil || string(snapshot.Body) != body || snapshot.Status != 404 || calls != 1 || guards < 3 {
		t.Fatalf("valid error snapshot lost: %v %v", snapshot, err)
	}
	clone := snapshot.Clone()
	clone.Body[0] = 'X'
	clone.Headers.Set("X-Owned", "changed")
	if string(snapshot.Body) != body || snapshot.Headers.Get("X-Owned") != "original" {
		t.Fatal("clone leaked mutation")
	}
	body = "not-json"
	if snapshot, err = client.SourceRecognitionDetailSnapshot(context.Background(), "movie", 42, guard); err == nil || snapshot != nil || calls != 2 {
		t.Fatal("invalid response became cached snapshot")
	}
	revoked = true
	if _, err = client.SourceRecognitionDetailSnapshot(context.Background(), "movie", 42, guard); err != deny || calls != 2 {
		t.Fatal("guard denial reached transport or changed classification")
	}
	revoked = false
	client = NewWithTransport(Credentials{APIKey: "owned"}, sourceInfoTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		revoked = true
		if r.URL.Path != "/3/tv/episode_group/owned-group" || len(r.URL.Query()) != 2 {
			t.Fatal("fixed group path changed")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{\"groups\":[]}")), Request: r}, nil
	}))
	if _, err = client.SourceRecognitionGroupSnapshot(context.Background(), "owned-group", guard); err != deny || calls != 3 {
		t.Fatal("post-read authority not checked")
	}
	// The namespace is created from the SAME immutable client snapshot as HTTP,
	// not from a later Settings read. Constructor-only; no proxy connection.
	a := NewWithProxy(Credentials{APIKey: "owned-a"}, networkproxy.Config{})
	same := NewWithProxy(Credentials{APIKey: "owned-a"}, networkproxy.Config{})
	different := NewWithProxy(Credentials{APIKey: "owned-b"}, networkproxy.Config{})
	route := NewWithProxy(Credentials{APIKey: "owned-a"}, networkproxy.Config{HTTPS: "http://127.0.0.1:1"})
	if len(a.SourceRecognitionCacheNamespace()) != 64 || a.SourceRecognitionCacheNamespace() != same.SourceRecognitionCacheNamespace() || a.SourceRecognitionCacheNamespace() == different.SourceRecognitionCacheNamespace() || a.SourceRecognitionCacheNamespace() != route.SourceRecognitionCacheNamespace() {
		t.Fatal("cache namespace does not follow exact client credentials independent of proxy/self")
	}
	unknownCalls := 0
	client = NewWithTransport(Credentials{APIKey: "owned"}, sourceInfoTransport(func(r *http.Request) (*http.Response, error) {
		unknownCalls++
		return nil, errors.New("owned policy refusal")
	}))
	_, err = client.SourceRecognitionDetailSnapshot(context.Background(), "movie", 42, func() error { return nil })
	typed, ok := err.(*Error)
	if !ok || typed.Code != "recognition_transport_unknown" || unknownCalls != 1 {
		t.Fatal("unknown refusal retried or became source None", err, unknownCalls)
	}

}

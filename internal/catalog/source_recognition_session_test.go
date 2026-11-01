package catalog

import (
	"context"
	"errors"
	"io"
	"light-media/internal/networkproxy"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"syscall"
	"testing"
)

// ownedRecognitionTransport 是本测试构造时的固定 endpoint 适配，不发官方请求。
// 它不是生产 API 域配置、代理、失败 fallback 或上游转发。
type ownedRecognitionTransport struct {
	owned  *url.URL
	base   *http.Transport
	clones *atomic.Int32
}

func (t *ownedRecognitionTransport) CloneForSourceRecognition() (http.RoundTripper, error) {
	t.clones.Add(1)
	return &ownedRecognitionTransport{owned: t.owned, base: sourceRecognitionTLSClone(t.base), clones: t.clones}, nil
}
func (t *ownedRecognitionTransport) CloseIdleConnections() { t.base.CloseIdleConnections() }
func (t *ownedRecognitionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	q := r.URL.Query()
	if r.Method != "GET" || r.URL.Scheme != "https" || r.URL.Host != "api.themoviedb.org" || r.URL.Path != "/3/movie/42" || r.URL.User != nil || r.URL.Fragment != "" || len(q) != 3 || q.Get("api_key") != "owned" || q.Get("language") != "zh-CN" || q.Get("append_to_response") != "images,credits,alternative_titles,translations,release_dates,external_ids" {
		return nil, errors.New("owned request outside exact protocol")
	}
	if t.owned.Scheme != "https" || t.owned.Hostname() != "127.0.0.1" || t.owned.User != nil || t.owned.Path != "" || t.owned.RawQuery != "" || t.owned.Fragment != "" {
		return nil, errors.New("invalid owned origin")
	}
	copy := r.Clone(r.Context())
	target := *t.owned
	target.Path = "/3/movie/42"
	target.RawQuery = r.URL.RawQuery
	copy.URL = &target
	copy.Host = ""
	return t.base.RoundTrip(copy)
}

func TestSourceRecognitionOwnedRetryAndPrivateTLS(t *testing.T) {
	var requests, clones atomic.Int32
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/3/movie/42" {
			t.Error("unexpected owned route")
			http.Error(w, "owned rejected", 400)
			return
		}
		if requests.Add(1) == 1 {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			connection.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{\"id\":42,\"title\":\"Owned retry\"}"))
	}))
	defer peer.Close()
	endpoint, err := url.Parse(peer.URL)
	if err != nil {
		t.Fatal(err)
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	defer base.CloseIdleConnections()
	if base.TLSClientConfig != nil && base.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("baseline already relaxed")
	}
	transport := &ownedRecognitionTransport{owned: endpoint, base: base, clones: &clones}
	client := NewWithTransport(Credentials{APIKey: "owned"}, transport)
	defer client.CloseIdleConnections()
	guardCalls := 0
	snapshot, err := client.SourceRecognitionDetailSnapshot(context.Background(), "movie", 42, func() error { guardCalls++; return nil })
	if err != nil || snapshot == nil || requests.Load() != 2 || clones.Load() != 2 || guardCalls < 5 {
		t.Fatalf("same-path owned reset/retry failed: %v requests=%d clones=%d guard=%d", err, requests.Load(), clones.Load(), guardCalls)
	}
	if base.TLSClientConfig != nil && base.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("original transport TLS changed")
	}
	// The old client still performs strict TLS and never reaches the owned HTTP handler.
	if _, err = client.SourceRecognitionDetail(context.Background(), "movie", 42); err == nil || requests.Load() != 2 {
		t.Fatal("recognition-only TLS leaked into old catalog path")
	}
	// Constructor-only verification of the production fixed-HTTPS route. Calling
	// Proxy chooses a route but performs no request, DNS, CONNECT or fallback.
	configured := NewWithProxy(Credentials{APIKey: "owned"}, networkproxy.Config{HTTP: "http://127.0.0.1:2", HTTPS: "http://127.0.0.1:1"})
	fixed, err := configured.recognition.factory()
	if err != nil {
		t.Fatal(err)
	}
	routed, ok := fixed.(*http.Transport)
	if !ok || routed.TLSClientConfig == nil || !routed.TLSClientConfig.InsecureSkipVerify || routed.Proxy == nil {
		t.Fatal("new private HTTPS factory did not preserve selected route/TLS")
	}
	defer routed.CloseIdleConnections()
	logical, _ := http.NewRequest("GET", "https://api.themoviedb.org/3/movie/42", nil)
	selected, err := routed.Proxy(logical)
	if err != nil || selected == nil || selected.String() != "http://127.0.0.1:1" {
		t.Fatal("recognition factory selected another proxy", err)
	}
	if sourceRecognitionDefiniteNoResponse(context.Background(), nil, syscall.EPERM) || sourceRecognitionDefiniteNoResponse(context.Background(), nil, errors.New("owned unknown policy refusal")) {
		t.Fatal("policy/unknown failure became a retry")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if sourceRecognitionDefiniteNoResponse(canceled, nil, io.EOF) {
		t.Fatal("cancellation became a retry")
	}

}

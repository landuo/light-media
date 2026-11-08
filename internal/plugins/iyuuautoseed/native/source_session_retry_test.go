package download

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourceReadAuthMatchesRealSessionRetryMatrix(t *testing.T) {
	var oracle struct {
		Cases []struct {
			Method, Case string
			Replies      []json.RawMessage `json:"reply_sequence"`
			RetryAfter   *int              `json:"retry_after"`
			Requests     int               `json:"peer_requests"`
			Connects     int               `json:"tcp_connect_attempts"`
			Outcome      struct {
				Error  bool `json:"error_flag"`
				Raises string
			}
		}
	}
	raw, err := os.ReadFile("../../../../testdata/plugin-source-connection-20261105/qb-session-retry-source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 18 {
		t.Fatal("actual QbittorrentSession/adapter source matrix", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Method+"/"+sample.Case, func(t *testing.T) {
			var requests, connects atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := int(requests.Add(1)) - 1
				if len(sample.Replies) == 0 {
					t.Error("connect-refused case reached HTTP")
					return
				}
				if n >= len(sample.Replies) {
					n = len(sample.Replies) - 1
				}
				if string(sample.Replies[n]) == `"DROP"` {
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
					return
				}
				var status int
				json.Unmarshal(sample.Replies[n], &status)
				if sample.RetryAfter != nil {
					w.Header().Set("Retry-After", fmt.Sprint(*sample.RetryAfter))
				}
				w.WriteHeader(status)
				if sample.Method == "GET-app-version" {
					io.WriteString(w, "v5.2.0")
				} else {
					io.WriteString(w, `[{"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]`)
				}
			}))
			defer peer.Close()
			address := peer.Listener.Addr().String()
			if len(sample.Replies) == 0 {
				peer.Close()
			}
			client, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: "http://" + address}, IYUUOptions{SourceQBLogin: true})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.DisableKeepAlives = true
			dialer := net.Dialer{}
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				connects.Add(1)
				return dialer.DialContext(ctx, network, address)
			}
			client.client.http.Transport = transport
			start := time.Now()
			if sample.Method == "GET-app-version" {
				_, err = client.client.qb(context.Background(), "GET", "app/version", "", "")
			} else {
				_, err = client.Torrents(context.Background(), []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, false)
			}
			wantErr := sample.Outcome.Error || sample.Outcome.Raises != ""
			if (err != nil) != wantErr || requests.Load() != int32(sample.Requests) || connects.Load() != int32(sample.Connects) {
				t.Fatal("source Session policy mismatch", err, requests.Load(), connects.Load(), sample)
			}
			if sample.Method == "GET-app-version" && sample.RetryAfter != nil && time.Since(start) < 1900*time.Millisecond {
				t.Fatal("source Retry-After waits were skipped")
			}
		})
	}
}
func TestSourceRetryAfterCancellationRevocationAndSelectedNoReplay(t *testing.T) {
	var calls atomic.Int32
	var revoked atomic.Bool
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		revoked.Store(true)
		w.Header().Set("Retry-After", "10")
		http.Error(w, "owned retry-after", 503)
	}))
	defer peer.Close()
	client, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}, IYUUOptions{SourceQBLogin: true, Guard: func(context.Context) error {
		if revoked.Load() {
			return failure("owned_guard", "revoked")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	start := time.Now()
	_, err = client.client.qb(context.Background(), "GET", "app/version", "", "")
	if err == nil || calls.Load() != 1 || time.Since(start) > time.Second {
		t.Fatal("revoked owner waited/retried", err, calls.Load())
	}
	client, err = NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}, IYUUOptions{SourceQBLogin: true})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start = time.Now()
	_, err = client.client.qb(ctx, "GET", "app/version", "", "")
	if err == nil || time.Since(start) > time.Second || calls.Load() != 2 {
		t.Fatal("Retry-After ignored cancellation", err, calls.Load())
	}
	start = time.Now()
	_, err = client.client.qbSelected(context.Background(), "POST", "torrents/recheck", "hashes=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "application/x-www-form-urlencoded")
	if err == nil || time.Since(start) > time.Second || calls.Load() != 3 {
		t.Fatal("selected mutation was retried/waited", err, calls.Load())
	}
}

func TestSourceRetryAfterPlusMatchesActualInvalidHeader(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		t.Run(method, func(t *testing.T) {
			var calls atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "+1")
				http.Error(w, "owned source invalid header", 500)
			}))
			defer peer.Close()
			client, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}, IYUUOptions{SourceQBLogin: true})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			start := time.Now()
			_, err = client.client.qb(context.Background(), method, "app/version", "", "")
			if err == nil || calls.Load() != 2 || time.Since(start) > time.Second {
				t.Fatal("invalid +1 header waited or wrong manager retry", err, calls.Load(), time.Since(start))
			}
		})
	}
}

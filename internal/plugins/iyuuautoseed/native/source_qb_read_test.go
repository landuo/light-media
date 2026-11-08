package download

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourceQBReadMatchesVendorStatusAndRetryOracle(t *testing.T) {
	var oracle struct {
		Cases []struct {
			Case    string
			Replies []json.RawMessage `json:"reply_sequence"`
			Tasks   int               `json:"task_count"`
			Error   bool              `json:"error_flag"`
			Posts   int               `json:"info_POSTs"`
		}
	}
	raw, err := os.ReadFile("../../../../testdata/plugin-source-connection-20261105/qb-read-status-source-oracle.json")
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 6 {
		t.Fatal("actual source read oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Case, func(t *testing.T) {
			var info atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v2/auth/login" {
					io.WriteString(w, "Ok.")
					return
				}
				n := int(info.Add(1)) - 1
				if n >= len(sample.Replies) {
					t.Error("extra read retry")
					n = len(sample.Replies) - 1
				}
				if string(sample.Replies[n]) == `"DROP"` {
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
					return
				}
				var status int
				json.Unmarshal(sample.Replies[n], &status)
				w.WriteHeader(status)
				io.WriteString(w, `[{"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","tags":"marker"}]`)
			}))
			defer peer.Close()
			c, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}, IYUUOptions{SourceQBLogin: true})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			data, err := c.Torrents(context.Background(), []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, false)
			var rows []any
			if err == nil {
				json.Unmarshal(data, &rows)
			}
			if (err != nil) != sample.Error || len(rows) != sample.Tasks || info.Load() != int32(sample.Posts) {
				t.Fatal("source read policy mismatch", err, len(rows), info.Load(), sample)
			}
		})
	}
}
func TestSourceConstructorRetriesAuthOnlyAndGuardStopsRetry(t *testing.T) {
	for _, key := range []bool{false, true} {
		var calls atomic.Int32
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				http.Error(w, "owned transient", 503)
				return
			}
			io.WriteString(w, "Ok.")
		}))
		options := IYUUOptions{}
		if key {
			options.APIKey = "owned"
		}
		client, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}, options)
		if err != nil {
			t.Fatal(err)
		}
		err = client.SourceConstructorConnected(context.Background())
		client.Close()
		peer.Close()
		if err != nil || calls.Load() != 2 {
			t.Fatal("ctor ignored source auth retry", err, calls.Load())
		}
	}
	var calls atomic.Int32
	var revoke atomic.Bool
	sentinel := errors.New("owned retry guard")
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		revoke.Store(true)
		http.Error(w, "owned transient", 500)
	}))
	defer peer.Close()
	c, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}, IYUUOptions{Guard: func(context.Context) error {
		if revoke.Load() {
			return sentinel
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.SourceConstructorConnected(context.Background()); !errors.Is(err, sentinel) || calls.Load() != 1 {
		t.Fatal("auth retry bypassed owner guard", err, calls.Load())
	}
}
func TestSourceFindCleanupContinuesOnlyOrdinaryFailures(t *testing.T) {
	for _, mode := range []string{"500", "EOF", "204", "transient-guard"} {
		t.Run(mode, func(t *testing.T) {
			var remove, global atomic.Int32
			var queried, denied atomic.Bool
			sentinel := errors.New("owned transient cleanup guard")
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					queried.Store(true)
					io.WriteString(w, `[{"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","tags":"marker"}]`)
				case "/api/v2/torrents/removeTags":
					remove.Add(1)
					if mode == "EOF" {
						conn, _, _ := w.(http.Hijacker).Hijack()
						conn.Close()
						return
					}
					if mode == "500" {
						http.Error(w, "owned failure", 500)
						return
					}
					w.WriteHeader(204)
				case "/api/v2/torrents/deleteTags":
					global.Add(1)
					w.WriteHeader(204)
				default:
					t.Error(r.URL.Path)
				}
			}))
			defer peer.Close()
			c, err := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: peer.URL}, IYUUOptions{SourceQBLogin: true, Sleep: func(_ context.Context, d time.Duration) error {
				if d != 3*time.Second {
					t.Error("lookup timing changed")
				}
				return nil
			}, Guard: func(context.Context) error {
				if mode == "transient-guard" && queried.Load() && !denied.Swap(true) {
					return sentinel
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			hash, err := c.PluginHostSourceFindByTag(context.Background(), []string{"marker"}, nil)
			if mode == "transient-guard" {
				if !errors.Is(err, sentinel) || hash != "" || remove.Load() != 0 || global.Load() != 0 {
					t.Fatal("causal cleanup guard swallowed", hash, err, remove.Load(), global.Load())
				}
				return
			}
			if err != nil || hash == "" || remove.Load() != 1 || global.Load() != 1 {
				t.Fatal("source cleanup failed/selected write retried", hash, err, remove.Load(), global.Load())
			}
		})
	}
}

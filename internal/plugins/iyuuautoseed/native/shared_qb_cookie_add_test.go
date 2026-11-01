package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins/downloadercommands"
)

// Explicit IPv4 loopback only: never try an IPv6 or alternate-route fallback
// after a denied/failed owned listener. No NewServer/NewUnstartedServer call.
func qbCookieOwnedPeer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peer := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler}}
	peer.Start()
	return peer
}

// Owned fixed peers only. These are handwritten target protocol/safety tests,
// not execution of the pinned vendor or a substitute for the original leaf
// recording oracle. No proxy/environment rewrite or forwarding is performed.
func TestSharedQBCookieScopeWireAndVersionGuards(t *testing.T) {
	// Static source-regex witnesses, not a new source runtime oracle: pinned
	// packaging26.3 version.py194-225 wraps pre/post/dev/local in (?a:...).
	for _, version := range []string{"2.11.3+K", "2.11.3poſt1"} {
		if supported, err := downloadercommands.QBSupportsCookieAPI(version); err != nil || supported {
			t.Fatalf("ASCII-only source probe %q: %v %v", version, supported, err)
		}
		if supported, err := downloadercommands.QBRequireCookieAPI(version); supported || err == nil {
			t.Fatalf("ASCII-only vendor guard %q: %v %v", version, supported, err)
		} else if source, ok := err.(*downloadercommands.SourceException); !ok || source.Class != "InvalidVersion" {
			t.Fatalf("version classification %T", err)
		}
	}
	type step struct {
		method, path, reply string
		cookie              *string
		set                 bool
	}
	absent := ""
	for _, test := range []struct {
		name     string
		steps    []step
		wantLogs int
	}{
		{"normal-four-version-reads", []step{
			{method: "GET", path: "app/webapiVersion", reply: "2.11.3"},
			{method: "GET", path: "app/webapiVersion", reply: "2.11.3"},
			{method: "GET", path: "app/cookies", reply: `[{"domain":"other.invalid","name":"keep","value":"old","expirationDate":0},{"domain":"owned-cookie.invalid","name":"sid","value":"old"}]`},
			{method: "GET", path: "app/webapiVersion", reply: "2.11.3"},
			{method: "POST", path: "app/setCookies", reply: "ignored False", set: true},
			{method: "GET", path: "app/webapiVersion", reply: "2.11.3"},
			{method: "POST", path: "torrents/add", reply: "Ok.", cookie: &absent},
		}, 0},
		{"vendor-gates-recheck-current-version", []step{
			{method: "GET", path: "app/webapiVersion", reply: "2.11.3"},
			{method: "GET", path: "app/webapiVersion", reply: "2.11.2"},
			{method: "GET", path: "app/webapiVersion", reply: "2.11.2"},
			{method: "GET", path: "app/webapiVersion", reply: "2.11.2"},
			{method: "POST", path: "torrents/add", reply: "Ok.", cookie: &absent},
		}, 0},
		{"invalid-later-vendor-version-falls-back", []step{
			{method: "GET", path: "app/webapiVersion", reply: "2.11.3"},
			{method: "GET", path: "app/webapiVersion", reply: "owned-invalid-version"},
			{method: "GET", path: "app/webapiVersion", reply: "2.11.3"},
			{method: "POST", path: "torrents/add", reply: "Ok."},
		}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			const cookie = `sid=first; token="owned value"; sid=last`
			var mu sync.Mutex
			seen := 0
			peer := qbCookieOwnedPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if seen >= len(test.steps) {
					t.Error("extra request", r.Method, r.URL.Path)
					w.WriteHeader(500)
					return
				}
				s := test.steps[seen]
				seen++
				if r.Method != s.method || r.URL.Path != "/api/v2/"+s.path || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer owned-cookie-key" {
					t.Error("fixed request mismatch", seen, r.Method, r.URL.Path)
				}
				if r.Method == "POST" {
					if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.ParseForm() != nil {
						t.Error("form encoding")
					}
					if s.set {
						var got []map[string]any
						d := json.NewDecoder(strings.NewReader(r.PostForm.Get("cookies")))
						d.UseNumber()
						want := []map[string]any{{"domain": "other.invalid", "name": "keep", "value": "old", "expirationDate": json.Number("0")}, {"domain": "owned-cookie.invalid", "path": "/", "name": "sid", "value": "last"}, {"domain": "owned-cookie.invalid", "path": "/", "name": "token", "value": "owned value"}}
						if d.Decode(&got) != nil || !reflect.DeepEqual(got, want) || len(r.PostForm) != 1 {
							t.Errorf("ordered cookie JSON=%#v", got)
						}
					} else {
						if r.PostForm.Get("urls") != "https://owned-cookie.invalid/item.torrent" {
							t.Error("ordinary URL changed")
						}
						values, present := r.PostForm["cookie"]
						if s.cookie != nil {
							if present {
								t.Error("successful sync must omit per-add cookie", values)
							}
						} else if !present || len(values) != 1 || values[0] != cookie {
							t.Error("sync failure did not retain original cookie")
						}
					}
				}
				io.WriteString(w, s.reply)
			}))
			defer peer.Close()
			qb, _, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-cookie-wire", Kind: "qb", BaseURL: peer.URL}, "owned-cookie-key", func(ctx context.Context) error { return ctx.Err() })
			if err != nil {
				t.Fatal(err)
			}
			defer closeClient()
			logs := 0
			leaf := downloadercommands.QBittorrent{Client: qb, Config: downloadercommands.DefaultQBConfig(), Log: func(_ context.Context, level, message string) {
				logs++
				if level != "ERROR" || !strings.HasPrefix(message, "同步下载Cookie出错：") {
					t.Error("source sync catch", level, message)
				}
			}}
			result, err := leaf.AddTorrent(context.Background(), downloadercommands.Arguments{"content": "https://owned-cookie.invalid/item.torrent", "cookie": cookie})
			if err != nil || !result.Success || len(result.IDs) != 0 || logs != test.wantLogs {
				t.Fatalf("result=%#v error=%v logs=%d", result, err, logs)
			}
			mu.Lock()
			defer mu.Unlock()
			if seen != len(test.steps) {
				t.Fatalf("requests=%d want=%d", seen, len(test.steps))
			}
		})
	}
	t.Run("invalid-source-version-no-selected-write", func(t *testing.T) {
		var requests, selected atomic.Int32
		peer := qbCookieOwnedPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			if r.Method != "GET" || r.URL.Path != "/api/v2/app/webapiVersion" {
				selected.Add(1)
				t.Error("invalid version reached non-version endpoint", r.Method, r.URL.Path)
				w.WriteHeader(500)
				return
			}
			io.WriteString(w, "2.11.3+K")
		}))
		defer peer.Close()
		qb, _, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-cookie-invalid-version", Kind: "qb", BaseURL: peer.URL}, "", func(ctx context.Context) error { return ctx.Err() })
		if err != nil {
			t.Fatal(err)
		}
		defer closeClient()
		leaf := downloadercommands.QBittorrent{Client: qb, Config: downloadercommands.DefaultQBConfig()}
		result, err := leaf.AddTorrent(context.Background(), downloadercommands.Arguments{"content": "https://owned-cookie.invalid/item.torrent", "cookie": "sid=owned"})
		if err != nil || result.Success || len(result.IDs) != 0 || requests.Load() != 2 || selected.Load() != 0 {
			t.Fatalf("invalid version result=%#v err=%v requests=%d selected=%d", result, err, requests.Load(), selected.Load())
		}
	})
}

// Exact original raw23 intersection only; this does not run the mixed
// completed/add/TR projection cases or invent their packaging provenance.
func TestSharedQBCookieVersionOriginal23(t *testing.T) {
	raw, err := os.ReadFile("../reference/native-projection-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != "de6cdbe4e03a9c6d9d9828285a7c4973d264254ae40d96039dc381bc6099fdb4" {
		t.Fatal("legacy raw bytes changed")
	}
	value, err := iyuuJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	rows := value.(map[string]any)["versions"].([]any)
	if len(rows) != 23 {
		t.Fatalf("versions=%d", len(rows))
	}
	for _, item := range rows {
		row := item.(map[string]any)
		want := row["result"].(map[string]any)
		got, err := iyuuVersionAtLeast(row["input"].(string), 2, 11)
		if want["exception"] != nil {
			if err == nil {
				t.Fatalf("invalid legacy version admitted: %v", row["input"])
			}
			continue
		}
		if err != nil || got != want["value"] {
			t.Fatalf("legacy version=%v got=%v/%v want=%v", row["input"], got, err, want)
		}
	}
	// New static witnesses from bound packaging26.3 (?a:...) source tokens;
	// not claimed to have existed among or been executed by the legacy23 raw.
	for _, input := range []string{"2.11.3+İ", "2.11.3+ı", "2.11.3poſt1", "2.11.3+K"} {
		if got, err := iyuuVersionAtLeast(input, 2, 11); got || err == nil {
			t.Fatalf("non-ASCII source version accepted %q: %v/%v", input, got, err)
		}
	}
}

func TestSharedQBCookieScopeAggregateUnknownAndRecovery(t *testing.T) {
	for _, scenario := range []string{"read-500-fallback", "set-500-no-add", "add-500-store-remains", "after-set-version-500"} {
		t.Run(scenario, func(t *testing.T) {
			var reads, versions, sets, adds atomic.Int32
			var first atomic.Bool
			first.Store(true)
			var storeChanged atomic.Bool
			peer := qbCookieOwnedPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/app/webapiVersion":
					n := versions.Add(1)
					if first.Load() && scenario == "after-set-version-500" && n == 4 {
						w.WriteHeader(500)
						return
					}
					io.WriteString(w, "2.11.3")
				case "/api/v2/app/cookies":
					reads.Add(1)
					if first.Load() && scenario == "read-500-fallback" {
						w.WriteHeader(500)
						return
					}
					io.WriteString(w, "[]")
				case "/api/v2/app/setCookies":
					sets.Add(1)
					storeChanged.Store(true)
					if first.Load() && scenario == "set-500-no-add" {
						w.WriteHeader(500)
						return
					}
					io.WriteString(w, "ignored")
				case "/api/v2/torrents/add":
					adds.Add(1)
					r.ParseForm()
					if first.Load() && scenario == "read-500-fallback" {
						if r.PostForm.Get("cookie") != "sid=owned" {
							t.Error("read fallback lost cookie")
						}
					} else if _, ok := r.PostForm["cookie"]; ok {
						t.Error("sync cookie not cleared")
					}
					if first.Load() && scenario == "add-500-store-remains" {
						w.WriteHeader(500)
						return
					}
					io.WriteString(w, "Ok.")
				default:
					t.Error("unexpected path", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer peer.Close()
			qb, _, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-cookie-errors", Kind: "qb", BaseURL: peer.URL}, "", func(ctx context.Context) error { return ctx.Err() })
			if err != nil {
				t.Fatal(err)
			}
			defer closeClient()
			leaf := downloadercommands.QBittorrent{Client: qb, Config: downloadercommands.DefaultQBConfig()}
			args := downloadercommands.Arguments{"content": "https://owned-cookie.invalid/item.torrent", "cookie": "sid=owned"}
			result, err := leaf.AddTorrent(context.Background(), args)
			if scenario == "read-500-fallback" {
				if err != nil || !result.Success || sets.Load() != 0 || adds.Load() != 1 || versions.Load() != 3 || reads.Load() != 1 {
					t.Fatalf("readonly fallback result=%#v error=%v counts=%d/%d/%d/%d", result, err, versions.Load(), reads.Load(), sets.Load(), adds.Load())
				}
			} else {
				var source *downloadercommands.SourceException
				wantAdds := int32(0)
				if scenario == "add-500-store-remains" {
					wantAdds = 1
				}
				if err == nil || MutationNotDispatched(err) || errors.As(err, &source) || sets.Load() != 1 || adds.Load() != wantAdds || !storeChanged.Load() {
					t.Fatalf("selected failure lost aggregate: error=%v set=%d add=%d", err, sets.Load(), adds.Load())
				}
			}
			// A separate, explicit later operation may recover. It is not a retry of
			// the failed invocation, and the first operation's observed counts stand.
			beforeAdds, beforeSets := adds.Load(), sets.Load()
			first.Store(false)
			result, err = leaf.AddTorrent(context.Background(), args)
			if err != nil || !result.Success || adds.Load() != beforeAdds+1 || sets.Load() != beforeSets+1 {
				t.Fatalf("same client recovery=%#v error=%v", result, err)
			}
		})
	}
}

func TestSharedQBCookieScopeGuardsAndLifetime(t *testing.T) {
	for _, mode := range []string{"revoke-after-set", "cancel-after-set"} {
		t.Run(mode, func(t *testing.T) {
			var revoked atomic.Bool
			var adds, sets atomic.Int32
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			peer := qbCookieOwnedPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/app/webapiVersion":
					io.WriteString(w, "2.11.3")
				case "/api/v2/app/cookies":
					io.WriteString(w, "[]")
				case "/api/v2/app/setCookies":
					sets.Add(1)
					if mode == "revoke-after-set" {
						revoked.Store(true)
					} else {
						cancel()
					}
					io.WriteString(w, "ignored")
				case "/api/v2/torrents/add":
					adds.Add(1)
					io.WriteString(w, "Ok.")
				default:
					t.Error("unexpected route", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer peer.Close()
			qb, _, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-cookie-guard", Kind: "qb", BaseURL: peer.URL}, "", func(c context.Context) error {
				if revoked.Load() {
					return errors.New("owned authority revoked")
				}
				return c.Err()
			})
			if err != nil {
				t.Fatal(err)
			}
			defer closeClient()
			leaf := downloadercommands.QBittorrent{Client: qb, Config: downloadercommands.DefaultQBConfig()}
			_, err = leaf.AddTorrent(ctx, downloadercommands.Arguments{"content": "https://owned-cookie.invalid/item.torrent", "cookie": "sid=owned"})
			if err == nil || MutationNotDispatched(err) || sets.Load() != 1 || adds.Load() != 0 {
				t.Fatalf("terminal aggregate error=%v sets=%d adds=%d", err, sets.Load(), adds.Load())
			}
			if mode == "cancel-after-set" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation was converted", err)
			}
		})
	}
	t.Run("concurrent-aggregate-lock-and-stale-port", func(t *testing.T) {
		var mu sync.Mutex
		paths := []string{}
		peer := qbCookieOwnedPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths = append(paths, r.Method+" "+r.URL.Path)
			mu.Unlock()
			switch r.URL.Path {
			case "/api/v2/app/webapiVersion":
				io.WriteString(w, "2.11.3")
			case "/api/v2/app/cookies":
				io.WriteString(w, "[]")
			case "/api/v2/app/setCookies":
				io.WriteString(w, "ignored")
			case "/api/v2/torrents/add":
				io.WriteString(w, "Ok.")
			default:
				t.Error("unexpected route")
				w.WriteHeader(404)
			}
		}))
		defer peer.Close()
		qb, _, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-cookie-concurrent", Kind: "qb", BaseURL: peer.URL}, "", func(ctx context.Context) error { return ctx.Err() })
		if err != nil {
			t.Fatal(err)
		}
		defer closeClient()
		leaf := downloadercommands.QBittorrent{Client: qb, Config: downloadercommands.DefaultQBConfig()}
		var wg sync.WaitGroup
		errCh := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, e := leaf.AddTorrent(context.Background(), downloadercommands.Arguments{"content": "https://owned-cookie.invalid/item.torrent", "cookie": "sid=owned"})
				if e == nil && !r.Success {
					e = errors.New("owned add false")
				}
				errCh <- e
			}()
		}
		wg.Wait()
		close(errCh)
		for e := range errCh {
			if e != nil {
				t.Fatal(e)
			}
		}
		one := []string{"GET /api/v2/app/webapiVersion", "GET /api/v2/app/webapiVersion", "GET /api/v2/app/cookies", "GET /api/v2/app/webapiVersion", "POST /api/v2/app/setCookies", "GET /api/v2/app/webapiVersion", "POST /api/v2/torrents/add"}
		want := append(append([]string{}, one...), one...)
		mu.Lock()
		if !reflect.DeepEqual(paths, want) {
			t.Error("aggregate sequences interleaved", paths)
		}
		before := len(paths)
		mu.Unlock()
		var stale downloadercommands.QBAddCookiePrimitives
		var staleCtx context.Context
		_, err = qb.(downloadercommands.QBAddCookieScopePort).WithAddCookieScope(context.Background(), func(c context.Context, p downloadercommands.QBAddCookiePrimitives) (any, error) {
			stale = p
			staleCtx = c
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = stale.WebAPIVersion(staleCtx); err == nil {
			t.Fatal("stale aggregate primitive admitted")
		}
		mu.Lock()
		defer mu.Unlock()
		if len(paths) != before {
			t.Fatal("stale primitive made a request")
		}
	})
}

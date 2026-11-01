package download

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/downloadercommands"
)

// Fixed self-owned responses only; no source/vendor execution, real tasks,
// forwarding, filesystem operations, proxy changes or hidden compiler.
func TestSharedQBTagScopeWireAndVersionGates(t *testing.T) {
	type exchange struct {
		method, path, body string
		form               url.Values
	}
	steps := []exchange{
		{"POST", "torrents/info", `[{"hash":"not-hex","tags":" first,second ","size":9007199254740993,"reannounce":7,"reannounce_in":9},false]`, url.Values{}},
		{"GET", "app/webapiVersion", "2.10.9", nil},
		{"POST", "torrents/info", `[]`, url.Values{"filter": {"paused"}}},
		{"GET", "app/webapiVersion", "2.11.0", nil},
		{"POST", "torrents/info", `[]`, url.Values{"filter": {"running"}}},
		{"GET", "app/webapiVersion", "2.2.9", nil},
		{"GET", "app/webapiVersion", "2.2.9", nil},
		{"GET", "app/webapiVersion", "2.3.0", nil},
		{"POST", "torrents/removeTags", "ignored False", url.Values{"hashes": {"B|B|0|True|None"}, "tags": {"one,two,one"}}},
		{"GET", "app/webapiVersion", "2.11.0", nil},
		{"POST", "torrents/deleteTags", "ignored False", url.Values{"tags": {""}}},
	}
	var mu sync.Mutex
	seen := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if seen >= len(steps) {
			t.Error("unexpected extra HTTP", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		s := steps[seen]
		seen++
		if r.Method != s.method || r.URL.Path != "/api/v2/"+s.path || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer owned-tag-key" {
			t.Error("fixed method/path/auth differs", r.Method, r.URL.Path)
		}
		if r.Method == "POST" {
			if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.ParseForm() != nil || !reflect.DeepEqual(r.PostForm, s.form) {
				t.Errorf("step %d form=%#v want=%#v", seen, r.PostForm, s.form)
			}
		}
		io.WriteString(w, s.body)
	}))
	defer peer.Close()
	qb, _, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-tag-qb", Kind: "qb", BaseURL: peer.URL}, "owned-tag-key", func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	q := qb.(*sharedQBCommands)
	var stale downloadercommands.QBTagPrimitives
	var staleCtx context.Context
	var sleeps []time.Duration
	q.owner.iyuu.options.Sleep = func(ctx context.Context, d time.Duration) error { sleeps = append(sleeps, d); return ctx.Err() }
	result, err := q.WithTagScope(context.Background(), func(ctx context.Context, p downloadercommands.QBTagPrimitives) (any, error) {
		stale, staleCtx = p, ctx
		rows, err := p.Info(ctx, nil)
		if err != nil {
			return nil, err
		}
		if len(rows.Values) != 2 || rows.Values[1] != false {
			t.Fatalf("full rows lost: %#v", rows)
		}
		first := rows.Values[0].(map[string]any)
		if first["hash"] != "not-hex" || first["size"] != json.Number("9007199254740993") || first["reannounce_in"] != json.Number("7") {
			t.Fatalf("vendor row adaptation/precision differs: %#v", first)
		}
		if _, exists := first["reannounce"]; exists {
			t.Fatal("vendor reannounce alias not applied")
		}
		for _, status := range []any{"stopped", "resumed"} {
			if _, err := p.Info(ctx, status); err != nil {
				return nil, err
			}
		}
		if _, err := p.RemoveTags(ctx, nil, nil); err != nil {
			return nil, err
		}
		if _, err := p.DeleteTags(ctx, nil); err != nil {
			return nil, err
		}
		if _, err := p.RemoveTags(ctx, []any{"B", "B", json.Number("0"), true, nil}, []any{"one", "two", "one"}); err != nil {
			return nil, err
		}
		if _, err := p.DeleteTags(ctx, []any{}); err != nil {
			return nil, err
		}
		if err := p.Sleep(ctx, 3*time.Second); err != nil {
			return nil, err
		}
		return "owned", nil
	})
	if err != nil || result != "owned" {
		t.Fatalf("scope result=%#v err=%v", result, err)
	}
	if !reflect.DeepEqual(sleeps, []time.Duration{3 * time.Second}) {
		t.Fatalf("delay=%v", sleeps)
	}
	if _, err := stale.Info(staleCtx, nil); err == nil {
		t.Fatal("expired scoped primitive admitted")
	}
	mu.Lock()
	defer mu.Unlock()
	if seen != len(steps) {
		t.Fatalf("requests=%d want=%d", seen, len(steps))
	}
}

func TestSharedQBTagScopeErrorAndAggregate(t *testing.T) {
	t.Run("readonly-known-error-continues", func(t *testing.T) {
		var requests atomic.Int32
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" || r.URL.Path != "/api/v2/torrents/info" {
				t.Error("wrong readonly route")
			}
			if requests.Add(1) == 1 {
				w.WriteHeader(500)
				return
			}
			io.WriteString(w, `[{"hash":"owned","tags":"x"}]`)
		}))
		defer peer.Close()
		qb, _, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-read-tag", Kind: "qb", BaseURL: peer.URL}, "", func(ctx context.Context) error { return ctx.Err() })
		if err != nil {
			t.Fatal(err)
		}
		defer closeClient()
		_, err = qb.(*sharedQBCommands).WithTagScope(context.Background(), func(ctx context.Context, p downloadercommands.QBTagPrimitives) (any, error) {
			_, first := p.Info(ctx, nil)
			var source *downloadercommands.SourceException
			if !errors.As(first, &source) {
				t.Fatalf("known readonly error not source-caught: %v", first)
			}
			rows, err := p.Info(ctx, nil)
			if err == nil && len(rows.Values) != 1 {
				t.Fatal("next readonly request did not recover")
			}
			return nil, err
		})
		if err != nil || requests.Load() != 2 {
			t.Fatalf("err=%v calls=%d", err, requests.Load())
		}
	})

	for _, scenario := range []string{"first-selected-500", "second-version-500", "second-version-malformed", "second-selected-500", "revoked-after-first"} {
		t.Run(scenario, func(t *testing.T) {
			var version, remove, deleteCalls atomic.Int32
			var revoked atomic.Bool
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/app/webapiVersion":
					if r.Method != "GET" {
						t.Error("version not GET")
					}
					n := version.Add(1)
					if n == 2 && scenario == "second-version-500" {
						w.WriteHeader(500)
						return
					}
					if n == 2 && scenario == "second-version-malformed" {
						io.WriteString(w, "owned-invalid-version")
						return
					}
					io.WriteString(w, "2.11.0")
				case "/api/v2/torrents/removeTags":
					if r.Method != "POST" {
						t.Error("remove not POST")
					}
					remove.Add(1)
					if scenario == "first-selected-500" {
						w.WriteHeader(500)
						return
					}
					if scenario == "revoked-after-first" {
						revoked.Store(true)
					}
					io.WriteString(w, "Ok.")
				case "/api/v2/torrents/deleteTags":
					if r.Method != "POST" {
						t.Error("delete not POST")
					}
					deleteCalls.Add(1)
					w.WriteHeader(500)
				default:
					t.Error("unexpected route", r.URL.Path)
					w.WriteHeader(502)
				}
			}))
			defer peer.Close()
			qb, _, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-error-tags", Kind: "qb", BaseURL: peer.URL}, "", func(ctx context.Context) error {
				if revoked.Load() {
					return errors.New("owned revoked")
				}
				return ctx.Err()
			})
			if err != nil {
				t.Fatal(err)
			}
			defer closeClient()
			_, err = qb.(*sharedQBCommands).WithTagScope(context.Background(), func(ctx context.Context, p downloadercommands.QBTagPrimitives) (any, error) {
				if _, err := p.RemoveTags(ctx, "owned", "tag"); err != nil {
					return nil, err
				}
				_, err := p.DeleteTags(ctx, "tag")
				var source *downloadercommands.SourceException
				if errors.As(err, &source) {
					t.Fatalf("second error could be swallowed by source catch: %v", err)
				}
				return nil, err
			})
			var source *downloadercommands.SourceException
			if err == nil || MutationNotDispatched(err) || errors.As(err, &source) || remove.Load() != 1 {
				t.Fatalf("aggregate lost selected effect: err=%v remove=%d", err, remove.Load())
			}
			wantDelete := int32(0)
			if scenario == "second-selected-500" {
				wantDelete = 1
			}
			if deleteCalls.Load() != wantDelete {
				t.Fatalf("delete calls=%d want=%d", deleteCalls.Load(), wantDelete)
			}
		})
	}

	t.Run("authority-cancel-boundary-are-not-source", func(t *testing.T) {
		var requests atomic.Int32
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); t.Error("unexpected HTTP") }))
		defer peer.Close()
		var revoked atomic.Bool
		qb, _, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-no-wire", Kind: "qb", BaseURL: peer.URL}, "", func(ctx context.Context) error {
			if revoked.Load() {
				return errors.New("owned denied")
			}
			return ctx.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
		defer closeClient()
		q := qb.(*sharedQBCommands)
		for _, mode := range []string{"revoked", "cancel", "wrong-scope"} {
			_, err := q.WithTagScope(context.Background(), func(ctx context.Context, p downloadercommands.QBTagPrimitives) (any, error) {
				switch mode {
				case "revoked":
					revoked.Store(true)
				case "cancel":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
				case "wrong-scope":
					ctx = context.Background()
				}
				return p.Info(ctx, nil)
			})
			var source *downloadercommands.SourceException
			if err == nil || errors.As(err, &source) {
				t.Fatalf("%s swallowed: %v", mode, err)
			}
			revoked.Store(false)
		}
		if requests.Load() != 0 {
			t.Fatal("invalid scope reached HTTP")
		}
	})
}

func TestSharedQBTagActualLeafPollingAndCleanup(t *testing.T) {
	for _, scenario := range []string{"whole-list-filter-then-found", "nine-empty-reads", "cleanup-unknown-not-found-success", "nonmapping-direct-source-error", "false-hash-cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			var mu sync.Mutex
			var trace []string
			var sleeps []time.Duration
			infos := 0
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.URL.RawQuery != "" {
					t.Error("unexpected query")
				}
				trace = append(trace, r.Method+" "+r.URL.Path)
				switch r.URL.Path {
				case "/api/v2/torrents/info":
					if r.Method != "POST" || r.ParseForm() != nil || len(r.PostForm) != 0 {
						t.Error("info must be unfiltered POST except source status")
					}
					infos++
					if scenario == "nonmapping-direct-source-error" {
						io.WriteString(w, `[false]`)
						return
					}
					if scenario == "nine-empty-reads" {
						io.WriteString(w, `[]`)
						return
					}
					if infos == 1 && scenario == "whole-list-filter-then-found" {
						// A later malformed tags value invalidates the whole source
						// filter, including its earlier matching candidate.
						io.WriteString(w, `[{"hash":"too-early","tags":"one"},{"hash":"later","tags":[]}]`)
						return
					}
					if scenario == "false-hash-cleanup" {
						io.WriteString(w, `[{"hash":false,"tags":"one"}]`)
						return
					}
					io.WriteString(w, `[{"hash":"owned-second","tags":" one, extra "}]`)
				case "/api/v2/app/webapiVersion":
					if r.Method != "GET" {
						t.Error("version not GET")
					}
					io.WriteString(w, "2.11.0")
				case "/api/v2/torrents/removeTags":
					hash := "owned-second"
					if scenario == "false-hash-cleanup" {
						hash = "False"
					}
					if r.Method != "POST" || r.ParseForm() != nil || !reflect.DeepEqual(r.PostForm, url.Values{"hashes": {hash}, "tags": {"one"}}) {
						t.Error("cleanup first form differs")
					}
					if scenario == "cleanup-unknown-not-found-success" {
						w.WriteHeader(500)
						return
					}
					io.WriteString(w, "Ok.")
				case "/api/v2/torrents/deleteTags":
					if r.Method != "POST" || r.ParseForm() != nil || !reflect.DeepEqual(r.PostForm, url.Values{"tags": {"one"}}) {
						t.Error("cleanup global tag form differs")
					}
					io.WriteString(w, "Ok.")
				default:
					t.Error("unexpected fixed peer request", r.URL.Path)
					w.WriteHeader(502)
				}
			}))
			defer peer.Close()
			qb, _, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-poll-tags", Kind: "qb", BaseURL: peer.URL}, "", func(ctx context.Context) error { return ctx.Err() })
			if err != nil {
				t.Fatal(err)
			}
			defer closeClient()
			q := qb.(*sharedQBCommands)
			q.owner.iyuu.options.Sleep = func(ctx context.Context, d time.Duration) error {
				mu.Lock()
				sleeps = append(sleeps, d)
				trace = append(trace, "sleep")
				mu.Unlock()
				return ctx.Err()
			}
			leaf := downloadercommands.QBTagCommands{QBittorrent: downloadercommands.QBittorrent{Client: qb}}
			var tags any = "one"
			if scenario == "nonmapping-direct-source-error" {
				tags = nil
			}
			value, err := leaf.GetTorrentIDByTag(context.Background(), tags, nil)
			mu.Lock()
			defer mu.Unlock()
			switch scenario {
			case "false-hash-cleanup":
				if err != nil || value != false || infos != 1 || len(sleeps) != 1 {
					t.Fatalf("false hash was normalized: value=%#v err=%v infos=%d sleeps=%v", value, err, infos, sleeps)
				}
				want := []string{"sleep", "POST /api/v2/torrents/info", "GET /api/v2/app/webapiVersion", "POST /api/v2/torrents/removeTags", "GET /api/v2/app/webapiVersion", "POST /api/v2/torrents/deleteTags"}
				if !reflect.DeepEqual(trace, want) {
					t.Fatalf("false hash cleanup sequence=%v", trace)
				}
			case "nonmapping-direct-source-error":
				source, direct := err.(*downloadercommands.SourceException)
				if !direct || source.Class != "AttributeError" || MutationNotDispatched(err) || value != nil || infos != 1 || len(trace) != 2 {
					t.Fatalf("native scope lost direct source error: value=%#v err=%T/%v trace=%v", value, err, err, trace)
				}
			case "whole-list-filter-then-found":
				if err != nil || value != "owned-second" || infos != 2 || len(sleeps) != 2 {
					t.Fatalf("value=%#v err=%v infos=%d sleeps=%v", value, err, infos, sleeps)
				}
				want := []string{"sleep", "POST /api/v2/torrents/info", "sleep", "POST /api/v2/torrents/info", "GET /api/v2/app/webapiVersion", "POST /api/v2/torrents/removeTags", "GET /api/v2/app/webapiVersion", "POST /api/v2/torrents/deleteTags"}
				if !reflect.DeepEqual(trace, want) {
					t.Fatalf("actual algorithm sequence=%v", trace)
				}
			case "nine-empty-reads":
				if err != nil || value != nil || infos != 9 || len(sleeps) != 9 || len(trace) != 18 {
					t.Fatalf("exhaustion=%#v/%v infos=%d sleeps=%d trace=%v", value, err, infos, len(sleeps), trace)
				}
				for i := 0; i < 9; i++ {
					if trace[2*i] != "sleep" || trace[2*i+1] != "POST /api/v2/torrents/info" {
						t.Fatal("sleep/read order differs")
					}
				}
			case "cleanup-unknown-not-found-success":
				var source *downloadercommands.SourceException
				if err == nil || value != nil || MutationNotDispatched(err) || errors.As(err, &source) || infos != 1 || len(trace) != 4 {
					t.Fatalf("unknown cleanup was hidden: %#v/%v trace=%v", value, err, trace)
				}
			}
			for _, duration := range sleeps {
				if duration != 3*time.Second {
					t.Fatalf("source delay changed to %v", duration)
				}
			}
		})
	}
}

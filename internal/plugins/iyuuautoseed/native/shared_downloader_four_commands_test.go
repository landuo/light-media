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
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins/downloadercommands"
)

// Fixed owned HTTP responses only: no source/vendor execution, real daemon,
// forwarding, CONNECT, environment rewrite, global transport or hidden build.
func TestSharedDownloaderFourQBWireAndVersions(t *testing.T) {
	type exchange struct {
		method, path, version string
		form                  url.Values
	}
	want := []exchange{
		{method: "GET", path: "/api/v2/app/webapiVersion", version: "2.2.9"},
		{method: "GET", path: "/api/v2/app/webapiVersion", version: "2.3.0"},
		{method: "POST", path: "/api/v2/torrents/addTags", form: url.Values{}},
		{method: "GET", path: "/api/v2/app/webapiVersion", version: "2.11.0"},
		{method: "POST", path: "/api/v2/torrents/addTags", form: url.Values{"hashes": {""}, "tags": {""}}},
		{method: "GET", path: "/api/v2/app/webapiVersion", version: "2.11.0"},
		{method: "POST", path: "/api/v2/torrents/addTags", form: url.Values{"hashes": {"B|0|True|None"}, "tags": {"first,2,False,None"}}},
		{method: "POST", path: "/api/v2/torrents/setCategory", form: url.Values{"hashes": {"all"}, "category": {""}}},
		{method: "GET", path: "/api/v2/app/webapiVersion", version: "2.10.9"},
		{method: "POST", path: "/api/v2/torrents/pause", form: url.Values{}},
		{method: "GET", path: "/api/v2/app/webapiVersion", version: "2.11.0"},
		{method: "POST", path: "/api/v2/torrents/stop", form: url.Values{"hashes": {""}}},
	}
	var mu sync.Mutex
	seen := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if seen >= len(want) {
			t.Error("unexpected additional request", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		step := want[seen]
		seen++
		if r.Method != step.method || r.URL.Path != step.path || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Bearer owned-four-key" {
			t.Error("wrong fixed route/auth", r.Method, r.URL.Path)
		}
		if step.method == "GET" {
			io.WriteString(w, step.version)
			return
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Error("wrong form content type")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(r.PostForm, step.form) {
			t.Errorf("step %d form = %#v, want %#v", seen, r.PostForm, step.form)
		}
		io.WriteString(w, "False") // Public native methods ignore response content.
	}))
	defer peer.Close()
	qb, _, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-four-qb", Kind: "qb", BaseURL: peer.URL}, "owned-four-key", func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	q := qb.(*sharedQBCommands)
	ctx := context.Background()
	calls := []func() (any, error){
		func() (any, error) { return q.AddTags(ctx, "all", "not-dispatched-on-old-version") },
		func() (any, error) { return q.AddTags(ctx, nil, nil) },
		func() (any, error) { return q.AddTags(ctx, []any{}, []any{}) },
		func() (any, error) {
			return q.AddTags(ctx, []any{"B", json.Number("0"), true, nil}, []any{"first", json.Number("2"), false, nil})
		},
		func() (any, error) { return q.SetCategory(ctx, "all", "") },
		func() (any, error) { return q.Stop(ctx, nil) },
		func() (any, error) { return q.Stop(ctx, []any{}) },
	}
	for i, call := range calls {
		if value, err := call(); err != nil || value != nil {
			t.Fatalf("call %d: value=%#v err=%v", i, value, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if seen != len(want) {
		t.Fatalf("requests=%d want=%d", seen, len(want))
	}
}

func TestSharedDownloaderFourTRStopSelectors(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	var selectors []any
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != "POST" || r.URL.Path != "/transmission/rpc" || r.URL.RawQuery != "" {
			t.Error("unexpected RPC route", r.Method, r.URL.Path)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "owned-user" || pass != "owned-password" {
			t.Error("missing fixed credentials")
		}
		var request map[string]any
		d := json.NewDecoder(r.Body)
		d.UseNumber()
		if d.Decode(&request) != nil || d.Decode(new(any)) != io.EOF || len(request) != 2 {
			t.Error("invalid fixed RPC envelope")
			w.WriteHeader(400)
			return
		}
		method, _ := request["method"].(string)
		args, ok := request["arguments"].(map[string]any)
		if !ok {
			t.Error("invalid arguments")
			w.WriteHeader(400)
			return
		}
		methods = append(methods, method)
		switch method {
		case "session-get":
			if len(args) != 0 {
				t.Error("unexpected session arguments")
			}
			if r.Header.Get("X-Transmission-Session-Id") == "" {
				w.Header().Set("X-Transmission-Session-Id", "owned-four-session")
				w.WriteHeader(409)
				return
			}
		case "torrent-stop":
			if len(args) != 1 || args["ids"] == nil {
				t.Error("unexpected stop fields", args)
			}
			selectors = append(selectors, args["ids"])
		default:
			t.Error("unexpected RPC method", method)
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("X-Transmission-Session-Id") != "owned-four-session" {
			t.Error("wrong session")
		}
		io.WriteString(w, `{"result":"success","arguments":{}}`)
	}))
	defer peer.Close()
	_, tr, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-four-tr", Kind: "tr", BaseURL: peer.URL + "/transmission/rpc", Username: "owned-user", Password: "owned-password"}, "", func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	client := tr.(*sharedTRCommands)
	ctx := context.Background()
	for _, ids := range []any{nil, []any{}, int64(-1), "all", strings.Repeat("A", 40)} {
		_, err := client.Stop(ctx, ids)
		var source *downloadercommands.SourceException
		if !errors.As(err, &source) || source.Class != "ValueError" || !MutationNotDispatched(err) {
			t.Fatalf("invalid ids %#v: %v", ids, err)
		}
	}
	mu.Lock()
	noWire := len(methods) == 0
	mu.Unlock()
	if !noWire {
		t.Fatal("require_ids validation must precede any RPC")
	}
	for _, ids := range []any{int64(0), []any{false, strings.Repeat("a", 40)}, "recently-active"} {
		if value, err := client.Stop(ctx, ids); err != nil || value != nil {
			t.Fatal(value, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(methods, []string{"session-get", "session-get", "torrent-stop", "torrent-stop", "torrent-stop"}) {
		t.Fatal("fixed session/control sequence", methods)
	}
	want := []any{[]any{json.Number("0")}, []any{false, strings.Repeat("a", 40)}, "recently-active"}
	if !reflect.DeepEqual(selectors, want) {
		t.Fatal("selector altered", selectors)
	}
}

func TestSharedDownloaderFourGuardAndUnknown(t *testing.T) {
	for _, method := range []string{"tags", "category", "stop", "tr-stop"} {
		t.Run(method, func(t *testing.T) {
			var hits, selected atomic.Int32
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if method == "tr-stop" {
					var request struct {
						Method string `json:"method"`
					}
					if json.NewDecoder(r.Body).Decode(&request) != nil {
						t.Error("bad RPC")
					}
					if request.Method == "session-get" {
						io.WriteString(w, `{"result":"success","arguments":{}}`)
						return
					}
					if request.Method != "torrent-stop" {
						t.Error("unexpected RPC", request.Method)
					}
				} else if r.Method == "GET" && r.URL.Path == "/api/v2/app/webapiVersion" {
					io.WriteString(w, "2.11.0")
					return
				}
				selected.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer peer.Close()
			kind := "qb"
			if method == "tr-stop" {
				kind = "tr"
			}
			var denied atomic.Bool
			guardErr := errors.New("owned authority changed")
			q, tr, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-four-unknown", Kind: kind, BaseURL: peer.URL}, "", func(ctx context.Context) error {
				if denied.Load() {
					return guardErr
				}
				return ctx.Err()
			})
			if err != nil {
				t.Fatal(err)
			}
			defer closeClient()
			call := func(ctx context.Context) (any, error) {
				switch method {
				case "tags":
					return q.(*sharedQBCommands).AddTags(ctx, "all", "owned")
				case "category":
					return q.(*sharedQBCommands).SetCategory(ctx, "all", "owned")
				case "stop":
					return q.(*sharedQBCommands).Stop(ctx, "all")
				default:
					return tr.(*sharedTRCommands).Stop(ctx, int64(0))
				}
			}
			denied.Store(true)
			if _, err := call(context.Background()); !errors.Is(err, guardErr) || !MutationNotDispatched(err) || hits.Load() != 0 {
				t.Fatal("authority rejection reached wire", err, hits.Load())
			}
			denied.Store(false)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := call(ctx); !errors.Is(err, context.Canceled) || !MutationNotDispatched(err) || hits.Load() != 0 {
				t.Fatal("pre-cancel reached wire", err, hits.Load())
			}
			_, err = call(context.Background())
			var source *downloadercommands.SourceException
			if err == nil || MutationNotDispatched(err) || errors.As(err, &source) || selected.Load() != 1 {
				t.Fatal("delegated failure was swallowed or retried", err, selected.Load())
			}
		})
	}
}

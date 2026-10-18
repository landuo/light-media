package download

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIYUUNativeQBSourceWireAndTupleTruth(t *testing.T) {
	var mu sync.Mutex
	calls := []string{}
	queries, sleeps := 0, 0
	hash := strings.Repeat("a", 40)
	torrent, _ := testTorrent()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/v2/app/webapiVersion":
			io.WriteString(w, "2.11.0")
		case "/api/v2/torrents/add":
			if e := r.ParseMultipartForm(1 << 20); e != nil {
				t.Error(e)
				return
			}
			want := map[string]string{"savepath": "/source", "category": "movie", "tags": "original,temp", "paused": "True", "stopped": "True", "autoTMM": "True", "sequentialDownload": "True", "firstLastPiecePrio": "False", "skip_checking": "False"}
			for k, v := range want {
				if r.FormValue(k) != v {
					t.Errorf("field %s=%q want%q", k, r.FormValue(k), v)
				}
			}
			f, _, e := r.FormFile("torrent__0")
			if e != nil {
				t.Error(e)
				return
			}
			raw, _ := io.ReadAll(f)
			f.Close()
			if string(raw) != string(torrent) {
				t.Error("torrent bytes changed")
			}
			io.WriteString(w, "Fails.")
		case "/api/v2/torrents/info":
			r.ParseForm()
			if r.Method != "POST" {
				t.Error("source library info is POST")
			}
			if r.Form.Get("filter") == "completed" {
				reply(w, []any{map[string]any{"hash": hash, "state": "pausedUP", "progress": 1, "amount_left": 0}, map[string]any{"hash": hash, "state": "checkingUP", "progress": 1, "amount_left": 0}})
				return
			}
			queries++
			if queries == 1 {
				reply(w, []any{})
			} else {
				reply(w, []any{map[string]any{"hash": hash, "tags": " original , temp "}})
			}
		case "/api/v2/torrents/removeTags":
			r.ParseForm()
			if r.Form.Get("hashes") != hash || r.Form.Get("tags") != "temp" {
				t.Error("wrong temp tag cleanup")
			}
		case "/api/v2/torrents/setCategory", "/api/v2/torrents/recheck", "/api/v2/torrents/start":
			r.ParseForm()
			if r.Form.Get("hashes") != hash {
				t.Error("wrong selected hash")
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c, e := NewIYUUClient(Config{ID: "source", Kind: "qbittorrent", BaseURL: server.URL}, IYUUOptions{AutoCategory: true, Sequential: true, Sleep: func(ctx context.Context, d time.Duration) error {
		if d != 3*time.Second {
			t.Error("source polling duration")
		}
		sleeps++
		return nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	raw, e := c.Torrents(context.Background(), nil, true)
	if e != nil || len(nativeJSONList(t, raw)) != 1 {
		t.Fatalf("completed %s %v", raw, e)
	}
	path, category := "/source", "movie"
	skip := false
	added, e := c.AddPaused(context.Background(), IYUUAddSpec{Torrent: torrent, SavePath: &path, Category: &category, Tags: []string{"original", "temp"}, SkipChecking: &skip, IgnoreCategoryCheck: true})
	if e != nil || added.Accepted || !added.SourceTruthy || added.Outcome != "rejected" {
		t.Fatalf("tuple receipt %+v %v", added, e)
	}
	observed, e := c.FindByTag(context.Background(), "temp")
	if e != nil || observed != hash || sleeps != 2 {
		t.Fatalf("poll %s %d %v", observed, sleeps, e)
	}
	for _, fn := range []func() (IYUUReceipt, error){func() (IYUUReceipt, error) { return c.SetCategory(context.Background(), hash, "movie") }, func() (IYUUReceipt, error) { return c.Recheck(context.Background(), []string{hash}) }, func() (IYUUReceipt, error) { return c.Start(context.Background(), []string{hash}) }} {
		r, e := fn()
		if e != nil || !r.Accepted || r.Outcome != "confirmed" {
			t.Fatalf("control %+v %v", r, e)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 9 {
		t.Fatalf("calls %v", calls)
	}
}
func TestIYUUNativeTRSourceWireBothDialects(t *testing.T) {
	for _, dialect := range []string{"legacy", "jsonrpc"} {
		t.Run(dialect, func(t *testing.T) {
			hash := strings.Repeat("b", 40)
			torrent, _ := testTorrent()
			var mu sync.Mutex
			operations := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				var envelope map[string]any
				d := json.NewDecoder(r.Body)
				d.UseNumber()
				if d.Decode(&envelope) != nil {
					t.Error("request JSON")
					return
				}
				method := envelope["method"].(string)
				modern := envelope["jsonrpc"] == "2.0"
				id := envelope["id"]
				if !modern {
					id = envelope["tag"]
				}
				respond := func(v any) {
					if modern {
						reply(w, map[string]any{"jsonrpc": "2.0", "id": id, "result": v})
					} else {
						reply(w, map[string]any{"tag": id, "result": "success", "arguments": v})
					}
				}
				if dialect == "legacy" && modern {
					reply(w, map[string]any{"arguments": map[string]any{}, "result": "method name not recognized"})
					return
				}
				key := "params"
				if !modern {
					key = "arguments"
				}
				args := envelope[key].(map[string]any)
				operations = append(operations, method)
				switch strings.ReplaceAll(method, "-", "_") {
				case "session_get":
					respond(map[string]any{"version": "4.1"})
				case "torrent_get":
					rows := []any{}
					for _, status := range []int{0, 5, 6} {
						row := map[string]any{"id": status + 1, "status": status, "hashString": hash, "totalSize": 1024, "percentDone": 1, "downloadDir": "/source", "labels": []string{"tag"}}
						if modern {
							row = map[string]any{"id": status + 1, "status": status, "hash_string": hash, "total_size": 1024, "percent_done": 1, "download_dir": "/source", "labels": []string{"tag"}}
						}
						rows = append(rows, row)
					}
					respond(map[string]any{"torrents": rows})
				case "torrent_add":
					if args["paused"] != true || args["metainfo"] != base64.StdEncoding.EncodeToString(torrent) {
						t.Error("unsafe add args")
					}
					pathKey := "download_dir"
					addedKey := "torrent_added"
					hashKey := "hash_string"
					if !modern {
						pathKey = "download-dir"
						addedKey = "torrent-added"
						hashKey = "hashString"
					}
					if args[pathKey] != "/source" {
						t.Error("source download path")
					}
					respond(map[string]any{addedKey: map[string]any{"id": 1, hashKey: hash}})
				case "torrent_verify", "torrent_start":
					ids := args["ids"].([]any)
					if len(ids) != 1 || ids[0] != hash {
						t.Error("selected target changed")
					}
					respond(map[string]any{})
				default:
					t.Error("unexpected method", method)
				}
			}))
			defer server.Close()
			c, e := NewIYUUClient(Config{ID: "source", Kind: "transmission", BaseURL: server.URL + "/transmission/rpc"}, IYUUOptions{})
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			raw, e := c.Torrents(context.Background(), nil, true)
			if e != nil {
				t.Fatal(e)
			}
			rows := nativeJSONList(t, raw)
			if len(rows) != 1 || rows[0]["status"] != json.Number("6") || rows[0]["total_size"] != json.Number("1024") || rows[0]["download_dir"] != "/source" {
				t.Fatalf("completed projection %s", raw)
			}
			path := "/source"
			r, e := c.AddPaused(context.Background(), IYUUAddSpec{Torrent: torrent, SavePath: &path, Tags: []string{"tag"}})
			if e != nil || !r.Accepted || !r.SourceTruthy || r.Hash != hash {
				t.Fatalf("add %+v %v", r, e)
			}
			for _, fn := range []func(context.Context, []string) (IYUUReceipt, error){c.Recheck, c.Start} {
				out, e := fn(context.Background(), []string{hash})
				if e != nil || !out.Accepted {
					t.Fatalf("control %+v %v", out, e)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if len(operations) != 5 {
				t.Fatal(operations)
			}
		})
	}
}
func TestIYUUNativeMutationUnknownNotRetried(t *testing.T) {
	var mutations int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "webapiVersion") {
			io.WriteString(w, "2.11")
			return
		}
		mutations++
		http.Error(w, "synthetic-private-response", 500)
	}))
	defer server.Close()
	c, e := NewIYUUClient(Config{ID: "source", Kind: "qbittorrent", BaseURL: server.URL}, IYUUOptions{})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	torrent, _ := testTorrent()
	r, e := c.AddPaused(context.Background(), IYUUAddSpec{Torrent: torrent})
	if e == nil || r.Outcome != "unknown" || mutations != 1 || strings.Contains(fmt.Sprint(e), "private") {
		t.Fatalf("receipt %+v %v mutations%d", r, e, mutations)
	}
}

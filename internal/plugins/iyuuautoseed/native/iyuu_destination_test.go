package download

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Expected paths follow the pinned daemon's categorySavePath and
// initLoadTorrentParams, independently of the source's original add wire.
func TestIYUUDaemonDestinationQBNativeHTTPAndOriginalWire(t *testing.T) {
	str := func(s string) *string { return &s }
	for _, tc := range []struct {
		name, want        string
		save              *string
		category          *string
		auto, manual, sub bool
		categories        map[string]any
	}{
		{"nil-default", "/daemon/default", nil, nil, false, false, false, nil},
		{"empty-default", "/daemon/default", str(""), nil, false, false, false, nil},
		{"atm-overrides-explicit", "/daemon/Chinese", str("/source/unchanged"), str("国语"), true, false, false, map[string]any{"国语": map[string]any{"savePath": "/daemon/Chinese"}}},
		{"manual-category-default", "/daemon/Chinese", nil, str("国语"), false, true, false, map[string]any{"国语": map[string]any{"savePath": "/daemon/Chinese"}}},
		{"relative-category", "/daemon/default/cinema", nil, str("movie"), true, false, false, map[string]any{"movie": map[string]any{"savePath": "cinema"}}},
		{"implicit-category", "/daemon/default/new", nil, str("new"), true, false, false, map[string]any{}},
		{"subcategory-parent", "/custom/TV/国语", nil, str("TV/国语"), true, false, true, map[string]any{"TV": map[string]any{"savePath": "/custom/TV"}, "TV/国语": map[string]any{"savePath": ""}}},
		{"relative-child-uses-default", "/daemon/default/relative", nil, str("TV/国语"), true, false, true, map[string]any{"TV": map[string]any{"savePath": "/custom/TV"}, "TV/国语": map[string]any{"savePath": "relative"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			adds, reads := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Header.Get("Authorization") != "Bearer owned-default-token" {
					t.Error("daemon observation lost same-origin credential")
				}
				switch r.URL.Path {
				case "/api/v2/app/preferences":
					reads++
					reply(w, map[string]any{"save_path": "/daemon/default/", "use_subcategories": tc.sub, "use_category_paths_in_manual_mode": tc.manual})
				case "/api/v2/torrents/categories":
					reply(w, tc.categories)
				case "/api/v2/app/webapiVersion":
					io.WriteString(w, "2.11.0")
				case "/api/v2/torrents/add":
					adds++
					if e := r.ParseMultipartForm(1 << 20); e != nil {
						t.Error(e)
						return
					}
					wantSave := ""
					if tc.save != nil {
						wantSave = *tc.save
					}
					if r.FormValue("savepath") != wantSave {
						t.Error("resolved path replaced original source savepath")
					}
					wantCategory := ""
					if tc.category != nil {
						wantCategory = *tc.category
					}
					if r.FormValue("category") != wantCategory || r.FormValue("autoTMM") != iyuuBool(tc.auto) {
						t.Error("source category/ATM changed")
					}
					io.WriteString(w, "Ok.")
				default:
					t.Error("unexpected daemon route", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			client, e := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}, IYUUOptions{APIKey: "owned-default-token", AutoCategory: tc.auto})
			if e != nil {
				t.Fatal(e)
			}
			defer client.Close()
			torrent, _ := testTorrent()
			spec := IYUUAddSpec{Torrent: torrent, SavePath: tc.save, Category: tc.category, IgnoreCategoryCheck: true}
			projection, e := client.EffectiveAddDestination(context.Background(), spec)
			if e != nil || projection.EffectivePath != tc.want || projection.Version != 1 || projection.BindingSHA == "" || projection.ConfigSHA == "" {
				t.Fatal(projection, e)
			}
			added, e := client.AddPausedAt(context.Background(), spec, projection)
			if e != nil || !added.Accepted || added.Outcome != "confirmed" {
				t.Fatal(added, e)
			}
			mu.Lock()
			defer mu.Unlock()
			if adds != 1 || reads != 2 {
				t.Fatal("observation/recheck/dispatch", adds, reads)
			}
		})
	}
}

func TestIYUUDestinationChangeBeforeDispatchIsProvenZeroEffect(t *testing.T) {
	for _, tamper := range []bool{false, true} {
		t.Run(map[bool]string{false: "daemon-changed", true: "projection-tampered"}[tamper], func(t *testing.T) {
			var mu sync.Mutex
			reads, adds := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case "/api/v2/app/preferences":
					reads++
					dir := "/daemon/first"
					if reads > 1 && !tamper {
						dir = "/daemon/second"
					}
					reply(w, map[string]any{"save_path": dir})
				case "/api/v2/torrents/add":
					adds++
					io.WriteString(w, "Ok.")
				default:
					t.Error("unexpected route", r.URL.Path)
				}
			}))
			defer server.Close()
			client, e := NewIYUUClient(Config{ID: "owned", Kind: "qbittorrent", BaseURL: server.URL}, IYUUOptions{})
			if e != nil {
				t.Fatal(e)
			}
			defer client.Close()
			torrent, _ := testTorrent()
			spec := IYUUAddSpec{Torrent: torrent}
			projection, e := client.EffectiveAddDestination(context.Background(), spec)
			if e != nil {
				t.Fatal(e)
			}
			if tamper {
				projection.EffectivePath = "/forged"
			}
			added, e := client.AddPausedAt(context.Background(), spec, projection)
			if e == nil || !MutationNotDispatched(e) || added.Outcome != "rejected" {
				t.Fatal("changed destination lost zero-effect proof", added, e)
			}
			mu.Lock()
			defer mu.Unlock()
			if adds != 0 {
				t.Fatal("changed grant dispatched add", adds)
			}
		})
	}
}

func TestIYUUDestinationTRDefaultsPreserveBothDialectWire(t *testing.T) {
	for _, dialect := range []string{"legacy", "jsonrpc"} {
		t.Run(dialect, func(t *testing.T) {
			var mu sync.Mutex
			adds := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				var request map[string]any
				json.NewDecoder(r.Body).Decode(&request)
				modern := request["jsonrpc"] == "2.0"
				method, _ := request["method"].(string)
				method = strings.ReplaceAll(method, "-", "_")
				result := map[string]any{}
				if method == "session_get" {
					result = map[string]any{"version": "4.0.6", "download_dir": "/daemon/tr-default"}
				}
				if modern && dialect == "legacy" {
					reply(w, map[string]any{"result": "method name not recognized", "arguments": map[string]any{}})
					return
				}
				if method == "torrent_add" {
					adds++
					key := "arguments"
					if modern {
						key = "params"
					}
					args, _ := request[key].(map[string]any)
					if _, ok := args["download_dir"]; ok {
						t.Error("nil source download_dir replaced")
					}
					if _, ok := args["download-dir"]; ok {
						t.Error("nil legacy download-dir replaced")
					}
					result = map[string]any{"torrent_added": map[string]any{"hash_string": strings.Repeat("b", 40), "id": 1, "name": "owned"}}
				}
				if modern {
					reply(w, map[string]any{"jsonrpc": "2.0", "id": request["id"], "result": result})
				} else {
					reply(w, map[string]any{"tag": request["tag"], "result": "success", "arguments": result})
				}
			}))
			defer server.Close()
			client, e := NewIYUUClient(Config{ID: "owned", Kind: "transmission", BaseURL: server.URL + "/transmission/rpc"}, IYUUOptions{})
			if e != nil {
				t.Fatal(e)
			}
			defer client.Close()
			torrent, _ := testTorrent()
			spec := IYUUAddSpec{Torrent: torrent}
			projection, e := client.EffectiveAddDestination(context.Background(), spec)
			if e != nil || projection.EffectivePath != "/daemon/tr-default" {
				t.Fatal(projection, e)
			}
			added, e := client.AddPausedAt(context.Background(), spec, projection)
			if e != nil || !added.Accepted {
				t.Fatal(added, e)
			}
			mu.Lock()
			defer mu.Unlock()
			if adds != 1 {
				t.Fatal(adds)
			}
		})
	}
}

func TestIYUUDestinationTRExplicitEmptyCannotMintDefaultGrant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid explicit TR path reached daemon", r.URL.Path)
	}))
	defer server.Close()
	client, err := NewIYUUClient(Config{ID: "owned", Kind: "transmission", BaseURL: server.URL}, IYUUOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	empty := ""
	value, err := client.EffectiveAddDestination(context.Background(), IYUUAddSpec{SavePath: &empty})
	if err == nil || value.EffectivePath != "" {
		t.Fatal("explicit TR empty path was silently converted to default", value, err)
	}
}

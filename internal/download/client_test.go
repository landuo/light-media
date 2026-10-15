package download

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func testTorrent() ([]byte, string) {
	info := []byte("d6:lengthi1024e4:name8:demo.mkv12:piece lengthi16384e6:pieces20:xxxxxxxxxxxxxxxxxxxxe")
	h := sha1.Sum(info)
	return append(append([]byte("d4:info"), info...), 'e'), hex.EncodeToString(h[:])
}
func mustClient(t *testing.T, kind, base string) *Client {
	t.Helper()
	c, err := New(Config{ID: "living-room", Kind: kind, BaseURL: base, Username: "test-user", Password: "synthetic-password"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func qbRow(hash string) map[string]any {
	return map[string]any{"hash": hash, "name": "demo.mkv", "state": "stoppedDL", "save_path": "/downloads", "total_size": 1024, "completed": 512, "progress": 0.5, "ratio": 0, "added_on": 1234, "eta": 10, "tags": "light-media, tv"}
}

func TestQBitTorrentLifecycleAndDuplicateHasNoSideEffect(t *testing.T) {
	torrent, hash := testTorrent()
	var mu sync.Mutex
	added, logins, adds := false, 0, 0
	controls := map[string]int{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Referer() != sOrigin(r)+"/" {
			t.Error("missing same-origin referer")
		}
		if r.URL.Path == "/proxy/api/v2/auth/login" {
			_ = r.ParseForm()
			if r.Method != "POST" || r.Form.Get("username") != "test-user" || r.Form.Get("password") != "synthetic-password" {
				t.Error("bad login")
			}
			logins++
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "test-session", Path: "/"})
			_, _ = io.WriteString(w, "Ok.")
			return
		}
		cookie, err := r.Cookie("SID")
		if err != nil || cookie.Value != "test-session" {
			http.Error(w, "forbidden", 403)
			return
		}
		switch r.URL.Path {
		case "/proxy/api/v2/torrents/info":
			if r.URL.Query().Get("hashes") != "" && r.URL.Query().Get("hashes") != hash {
				t.Error("incorrect filter")
			}
			if added {
				reply(w, []any{qbRow(hash)})
			} else {
				reply(w, []any{})
			}
		case "/proxy/api/v2/torrents/add":
			if r.Method != "POST" {
				t.Error("add not POST")
			}
			if err := r.ParseMultipartForm(5 << 20); err != nil {
				t.Fatal(err)
			}
			file, _, err := r.FormFile("torrents")
			if err != nil {
				t.Fatal(err)
			}
			got, _ := io.ReadAll(file)
			_ = file.Close()
			if !bytes.Equal(got, torrent) || r.FormValue("savepath") != "/downloads" || r.FormValue("stopped") != "true" || r.FormValue("paused") != "" || r.FormValue("skip_checking") != "false" || r.FormValue("autoTMM") != "false" || r.FormValue("tags") != "light-media,tv" {
				t.Error("wrong 5.1 add parameters")
			}
			adds++
			added = true
			_, _ = io.WriteString(w, "Ok.")
		case "/proxy/api/v2/torrents/files":
			if r.URL.Query().Get("hash") != hash {
				t.Error("missing file hash")
			}
			reply(w, []any{map[string]any{"name": "demo.mkv", "size": 1024, "progress": 0.5}})
		case "/proxy/api/v2/torrents/start", "/proxy/api/v2/torrents/stop", "/proxy/api/v2/torrents/recheck", "/proxy/api/v2/torrents/delete", "/proxy/api/v2/torrents/setLocation":
			_ = r.ParseForm()
			if r.Method != "POST" || r.Form.Get("hashes") != hash {
				t.Error("unsafe selector")
			}
			if strings.HasSuffix(r.URL.Path, "/delete") && r.Form.Get("deleteFiles") != "false" {
				t.Error("data deletion requested")
			}
			if strings.HasSuffix(r.URL.Path, "/setLocation") && r.Form.Get("location") != "/media/relocated" {
				t.Error("wrong destination")
			}
			controls[r.URL.Path]++
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := mustClient(t, "qbittorrent", s.URL+"/proxy")
	result, err := c.Add(context.Background(), AddRequest{Torrent: torrent, SavePath: "/downloads", Paused: true, Tags: []string{"light-media", "tv"}})
	if err != nil || result.Hash != hash || result.Duplicate {
		t.Fatalf("first add: %+v %v", result, err)
	}
	result, err = c.Add(context.Background(), AddRequest{Torrent: torrent, SavePath: "/different", Paused: false, Tags: []string{"do-not-apply"}})
	if err != nil || !result.Duplicate {
		t.Fatalf("duplicate: %+v %v", result, err)
	}
	tasks, err := c.List(context.Background())
	if err != nil || len(tasks) != 1 || tasks[0].ID != "living-room:"+hash || tasks[0].Status != "paused" || tasks[0].Completed != 512 || len(tasks[0].Tags) != 2 {
		t.Fatalf("list: %+v %v", tasks, err)
	}
	files, err := c.Files(context.Background(), tasks[0].ID)
	if err != nil || len(files) != 1 || files[0].Completed != 512 {
		t.Fatalf("files: %+v %v", files, err)
	}
	for _, a := range []string{"start", "pause", "verify", "remove_keep_data"} {
		if err := c.Control(context.Background(), hash, a); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Relocate(context.Background(), hash, "/media/relocated"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if adds != 1 || logins != 1 || len(controls) != 5 {
		t.Fatalf("side effects adds=%d logins=%d controls=%v", adds, logins, controls)
	}
}
func sOrigin(r *http.Request) string { return "http://" + r.Host }

func TestQBitTorrentAuthRefreshAndBusinessFailure(t *testing.T) {
	_, hash := testTorrent()
	var logins, mutations int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			logins++
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: fmt.Sprint(logins), Path: "/"})
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/torrents/info":
			cookie, _ := r.Cookie("SID")
			if cookie == nil || cookie.Value == "1" {
				http.Error(w, "expired", 403)
			} else {
				reply(w, []any{qbRow(hash)})
			}
		case "/api/v2/torrents/start":
			mutations++
			_, _ = io.WriteString(w, "Fails.")
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	c := mustClient(t, "qb", s.URL)
	if err := c.Control(context.Background(), hash, "start"); err == nil || !strings.Contains(err.Error(), "unexpected_response") {
		t.Fatalf("business failure accepted: %v", err)
	}
	if logins != 2 || mutations != 1 {
		t.Fatalf("login retries=%d mutations=%d", logins, mutations)
	}
}

func TestUnsafeInputsDoNotReachServer(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer s.Close()
	c := mustClient(t, "qb", s.URL)
	torrent, hash := testTorrent()
	for _, h := range []string{"", "all", "1", hash + "|" + hash, "other:" + hash, "living-room:all"} {
		if err := c.Control(context.Background(), h, "pause"); err == nil {
			t.Fatalf("unsafe hash %q accepted", h)
		}
	}
	for _, a := range []string{"delete", "remove", "delete_data", "relocate", "start_now"} {
		if err := c.Control(context.Background(), hash, a); err == nil {
			t.Fatalf("unsafe action %q accepted", a)
		}
	}
	for _, p := range []string{"", "relative", "/downloads/../media", "/downloads/", "/downloads\x00bad"} {
		if _, err := c.Add(context.Background(), AddRequest{Torrent: torrent, SavePath: p}); err == nil {
			t.Fatalf("bad path %q accepted", p)
		}
	}
	if _, err := c.Add(context.Background(), AddRequest{Torrent: []byte("bad"), SavePath: "/downloads"}); err == nil {
		t.Fatal("bad metainfo accepted")
	}
	if _, err := c.Add(context.Background(), AddRequest{Torrent: torrent, SavePath: "/downloads", Tags: []string{"a,b"}}); err == nil {
		t.Fatal("tag injection accepted")
	}
	if requests.Load() != 0 {
		t.Fatal("unsafe input made HTTP requests")
	}
}

func TestRedirectNotFollowedAndSecretsNotInError(t *testing.T) {
	var leaks atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaks.Add(1) }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL+"/synthetic-password")
		w.WriteHeader(307)
		_, _ = io.WriteString(w, "synthetic-password")
	}))
	defer s.Close()
	c := mustClient(t, "tr", s.URL)
	_, err := c.List(context.Background())
	if err == nil || strings.Contains(err.Error(), "synthetic-password") || strings.Contains(err.Error(), s.URL) || leaks.Load() != 0 {
		t.Fatalf("redirect/secret failure: %v count=%d", err, leaks.Load())
	}
}

func TestQBitTorrentUnsafeFileAndMissingTaskRejected(t *testing.T) {
	_, hash := testTorrent()
	unsafe := true
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			if unsafe {
				reply(w, []any{qbRow(hash)})
			} else {
				reply(w, []any{})
			}
		case "/api/v2/torrents/files":
			reply(w, []any{map[string]any{"name": "../outside.mkv", "size": 10, "progress": 1}})
		default:
			_, _ = io.WriteString(w, "Ok.")
		}
	}))
	defer s.Close()
	c, err := New(Config{ID: "one", Kind: "qb", BaseURL: s.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Files(context.Background(), hash); err == nil {
		t.Fatal("unsafe file accepted")
	}
	unsafe = false
	if err := c.Control(context.Background(), hash, "pause"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing task: %v", err)
	}
}

func TestTransmissionJSONRPCAndLegacyLifecycle(t *testing.T) {
	for _, dialect := range []string{"jsonrpc", "legacy"} {
		t.Run(dialect, func(t *testing.T) {
			torrent, hash := testTorrent()
			var mu sync.Mutex
			sessionSeen, added := false, false
			handshakes, adds, negotiations := 0, 0, 0
			controls := map[string]int{}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				user, password, ok := r.BasicAuth()
				if !ok || user != "test-user" || password != "synthetic-password" {
					t.Error("bad basic auth")
				}
				if r.Method != "POST" || r.URL.Path != "/transmission/rpc" {
					t.Error("incorrect RPC endpoint")
				}
				if r.Header.Get("X-Transmission-Session-Id") != "test-csrf" {
					handshakes++
					w.Header().Set("X-Transmission-Session-Id", "test-csrf")
					w.WriteHeader(409)
					return
				}
				sessionSeen = true
				var req map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatal(err)
				}
				var method string
				_ = json.Unmarshal(req["method"], &method)
				if dialect == "legacy" && method == "session_get" {
					negotiations++
					reply(w, map[string]any{"arguments": map[string]any{}, "result": "method name not recognized"})
					return
				}
				argsKey, idKey := "params", "id"
				if dialect == "legacy" {
					argsKey, idKey = "arguments", "tag"
					if _, ok := req["jsonrpc"]; ok {
						t.Error("legacy request contains jsonrpc")
					}
				} else {
					if string(req["jsonrpc"]) != "\"2.0\"" {
						t.Error("missing JSON-RPC 2.0")
					}
				}
				var args map[string]json.RawMessage
				if err := json.Unmarshal(req[argsKey], &args); err != nil {
					t.Fatal(err)
				}
				var id uint64
				_ = json.Unmarshal(req[idKey], &id)
				normalized := strings.ReplaceAll(method, "-", "_")
				var result any = map[string]any{}
				responseHashKey := "hash_string"
				if dialect == "legacy" {
					responseHashKey = "hashString"
				}
				switch normalized {
				case "session_get":
					result = map[string]any{"version": "4.1.3"}
				case "torrent_get":
					var fields []string
					_ = json.Unmarshal(args["fields"], &fields)
					var ids []string
					_ = json.Unmarshal(args["ids"], &ids)
					if len(ids) > 0 && (len(ids) != 1 || ids[0] != hash) {
						t.Error("unsafe RPC ids")
					}
					if len(fields) == 2 && fields[1] == "files" {
						fileKey := "bytes_completed"
						if dialect == "legacy" {
							fileKey = "bytesCompleted"
						}
						result = map[string]any{"torrents": []any{map[string]any{responseHashKey: hash, "files": []any{map[string]any{"name": "folder/demo.mkv", "length": 1024, fileKey: 512}}}}}
					} else if added {
						row := map[string]any{responseHashKey: hash, "name": "demo.mkv", "status": 4, "download_dir": "/downloads", "total_size": 1024, "percent_done": 0.5, "left_until_done": 512, "upload_ratio": 0.3, "added_date": 1234, "eta": 20, "labels": []string{"tv"}}
						if dialect == "legacy" {
							for modern, old := range map[string]string{"download_dir": "downloadDir", "total_size": "totalSize", "percent_done": "percentDone", "left_until_done": "leftUntilDone", "upload_ratio": "uploadRatio", "added_date": "addedDate"} {
								row[old] = row[modern]
								delete(row, modern)
							}
							if !contains(fields, "downloadDir") || contains(fields, "download-dir") || contains(fields, "hash_string") {
								t.Error("legacy field spelling incorrect")
							}
						}
						result = map[string]any{"torrents": []any{row}}
					} else {
						result = map[string]any{"torrents": []any{}}
					}
				case "torrent_add":
					var encoded, savePath string
					_ = json.Unmarshal(args["metainfo"], &encoded)
					key := "download_dir"
					if dialect == "legacy" {
						key = "download-dir"
					}
					_ = json.Unmarshal(args[key], &savePath)
					decoded, _ := base64.StdEncoding.DecodeString(encoded)
					if !bytes.Equal(decoded, torrent) || savePath != "/downloads" || string(args["paused"]) != "true" {
						t.Error("incorrect torrent-add")
					}
					adds++
					added = true
					key = "torrent_added"
					if dialect == "legacy" {
						key = "torrent-added"
					}
					result = map[string]any{key: map[string]any{responseHashKey: hash}}
				case "torrent_start", "torrent_stop", "torrent_verify", "torrent_remove", "torrent_set_location":
					var ids []string
					_ = json.Unmarshal(args["ids"], &ids)
					if len(ids) != 1 || ids[0] != hash {
						t.Error("unsafe mutation selector")
					}
					if normalized == "torrent_remove" {
						key := "delete_local_data"
						if dialect == "legacy" {
							key = "delete-local-data"
						}
						if string(args[key]) != "false" {
							t.Error("data deletion requested")
						}
					}
					if normalized == "torrent_set_location" {
						if string(args["location"]) != "\"/moved\"" || string(args["move"]) != "true" {
							t.Error("bad relocate")
						}
					}
					controls[normalized]++
				default:
					t.Errorf("unexpected RPC method %s", method)
				}
				if dialect == "legacy" {
					reply(w, map[string]any{"arguments": result, "result": "success", "tag": id})
				} else {
					reply(w, map[string]any{"jsonrpc": "2.0", "result": result, "id": id})
				}
			}))
			defer s.Close()
			c := mustClient(t, "transmission", s.URL)
			result, err := c.Add(context.Background(), AddRequest{Torrent: torrent, SavePath: "/downloads", Paused: true, Tags: []string{"tv"}})
			if err != nil || result.Hash != hash || result.Duplicate {
				t.Fatalf("add %+v %v", result, err)
			}
			result, err = c.Add(context.Background(), AddRequest{Torrent: torrent, SavePath: "/different"})
			if err != nil || !result.Duplicate {
				t.Fatalf("duplicate %+v %v", result, err)
			}
			tasks, err := c.List(context.Background())
			if err != nil || len(tasks) != 1 || tasks[0].DownloadDir != "/downloads" || tasks[0].Completed != 512 || tasks[0].Status != "downloading" {
				t.Fatalf("list %+v %v", tasks, err)
			}
			files, err := c.Files(context.Background(), hash)
			if err != nil || len(files) != 1 || files[0].Completed != 512 {
				t.Fatalf("files %+v %v", files, err)
			}
			for _, action := range []string{"start", "pause", "verify", "remove_keep_data"} {
				if err := c.Control(context.Background(), hash, action); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.Relocate(context.Background(), hash, "/moved"); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !sessionSeen || handshakes != 1 || adds != 1 || len(controls) != 5 {
				t.Fatalf("bad lifecycle handshake=%d adds=%d controls=%v", handshakes, adds, controls)
			}
			if dialect == "legacy" && negotiations != 1 {
				t.Fatal("legacy negotiation not bounded")
			}
		})
	}
}
func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestTransmissionHandshakeIsBounded(t *testing.T) {
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("X-Transmission-Session-Id", "rotating")
		w.WriteHeader(409)
	}))
	defer s.Close()
	c := mustClient(t, "tr", s.URL)
	_, err := c.List(context.Background())
	if err == nil || requests.Load() != 3 || !strings.Contains(err.Error(), "handshake_limit") {
		t.Fatalf("unbounded handshake: %v %d", err, requests.Load())
	}
}

func TestTransmissionWrongResponseIDAndRPCFailure(t *testing.T) {
	for _, mode := range []string{"wrong_id", "rpc_error"} {
		t.Run(mode, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req map[string]any
				_ = json.NewDecoder(r.Body).Decode(&req)
				if mode == "wrong_id" {
					reply(w, map[string]any{"jsonrpc": "2.0", "id": 999, "result": map[string]any{"version": "4.1.3"}})
				} else {
					reply(w, map[string]any{"jsonrpc": "2.0", "id": req["id"], "error": map[string]any{"code": 7, "message": "synthetic-password"}})
				}
			}))
			defer s.Close()
			c := mustClient(t, "tr", s.URL)
			c.trDialect = "jsonrpc"
			_, err := c.List(context.Background())
			if err == nil || strings.Contains(err.Error(), "synthetic-password") {
				t.Fatalf("bad response accepted/leaked: %v", err)
			}
		})
	}
}

func TestTorrentHashUsesOriginalInfoAndRejectsInvalid(t *testing.T) {
	torrent, expected := testTorrent()
	hashes, err := torrentHashes(torrent, "transmission")
	if err != nil || len(hashes) != 1 || hashes[0] != expected {
		t.Fatalf("wrong exact hash %v %v", hashes, err)
	}
	if _, err := torrentHashes(append(torrent, 'x'), "qbittorrent"); err == nil {
		t.Fatal("trailing metainfo accepted")
	}
	if _, err := torrentHashes(bytes.Replace(torrent, []byte("demo.mkv"), []byte("../a.mkv"), 1), "qbittorrent"); err == nil {
		t.Fatal("unsafe torrent content path accepted")
	}
}

func TestConcurrentClientsAddOnce(t *testing.T) {
	torrent, hash := testTorrent()
	var mu sync.Mutex
	added := false
	adds := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			if added {
				reply(w, []any{qbRow(hash)})
			} else {
				reply(w, []any{})
			}
		case "/api/v2/torrents/add":
			adds++
			added = true
			_, _ = io.WriteString(w, "Ok.")
		default:
			http.NotFound(w, r)
		}
	}))
	defer s.Close()
	a, _ := New(Config{ID: "one", Kind: "qb", BaseURL: s.URL})
	b, _ := New(Config{ID: "one", Kind: "qb", BaseURL: s.URL})
	type outcome struct {
		r   AddResult
		err error
	}
	results := make(chan outcome, 2)
	for _, c := range []*Client{a, b} {
		go func(c *Client) {
			r, err := c.Add(context.Background(), AddRequest{Torrent: torrent, SavePath: "/downloads"})
			results <- outcome{r, err}
		}(c)
	}
	duplicates := 0
	for range 2 {
		o := <-results
		if o.err != nil {
			t.Fatal(o.err)
		}
		if o.r.Duplicate {
			duplicates++
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if adds != 1 || duplicates != 1 {
		t.Fatalf("adds=%d duplicates=%d", adds, duplicates)
	}
}

package iyuuautoseed

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	download "light-media/internal/plugins/iyuuautoseed/native"
)

func liveGuard(context.Context) error { return nil }
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// Both HTTP servers are owned daemons with synthetic data. These exercise the
// real native qB client, source helper, resolver, torrent cache and SQLite owner;
// no businessHost/source-decoded-text adapter participates in this test.
func TestNativeHostWholeCoreHTTPFilesAndDurableReplay(t *testing.T) {
	for _, loseCleanup := range []bool{false, true} {
		t.Run(map[bool]string{false: "confirmed", true: "lost-cleanup-ack"}[loseCleanup], func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			key := bytes.Repeat([]byte{31}, 32)
			original := strings.Repeat("a", 40)
			seed := strings.Repeat("b", 40)
			actual := strings.Repeat("c", 40)
			var mu sync.Mutex
			calls := []string{}
			tempTag := ""
			added := false
			addCount, cleanupCount, startCount := 0, 0, 0
			qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, r.Method+" "+r.URL.Path)
				r.ParseForm()
				switch r.URL.Path {
				case "/api/v2/app/webapiVersion":
					io.WriteString(w, "2.11.0")
				case "/api/v2/torrents/info":
					if r.Form.Get("filter") == "completed" {
						writeJSON(w, []any{map[string]any{"hash": original, "save_path": "/data/source", "tags": "original", "category": "movie", "state": "pausedUP", "progress": 1, "amount_left": 0, "total_size": 1073741824}})
						return
					}
					ids := r.Form.Get("hashes")
					if ids == seed {
						writeJSON(w, []any{})
						return
					}
					if added {
						writeJSON(w, []any{map[string]any{"hash": actual, "save_path": "/data/source", "tags": tempTag, "state": "pausedUP", "progress": 1, "amount_left": 0}})
					} else {
						writeJSON(w, []any{})
					}
				case "/api/v2/torrents/add":
					r.ParseMultipartForm(1 << 20)
					tags := strings.Split(r.FormValue("tags"), ",")
					tempTag = tags[len(tags)-1]
					if r.FormValue("savepath") != "/data/source" || r.FormValue("autoTMM") != "False" || r.FormValue("paused") != "True" {
						t.Error("source paused/path/ATM contract")
					}
					added = true
					addCount++
					io.WriteString(w, "Ok.")
				case "/api/v2/torrents/removeTags":
					cleanupCount++
					if r.Form.Get("hashes") != actual || r.Form.Get("tags") != tempTag {
						t.Error("wrong cleanup identity")
					}
					if loseCleanup {
						conn, _, e := w.(http.Hijacker).Hijack()
						if e == nil {
							conn.Close()
						}
						return
					}
				case "/api/v2/torrents/setCategory", "/api/v2/torrents/recheck":
					if r.Form.Get("hashes") != actual {
						t.Error("effect did not use observed identity")
					}
				case "/api/v2/torrents/start":
					startCount++
					if r.Form.Get("hashes") != actual {
						t.Error("wrong start identity")
					}
				default:
					t.Errorf("unexpected qB route %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer qb.Close()
			torrent := []byte("d4:infod6:lengthi1e4:name9:owned.mkvee")
			var site *httptest.Server
			site = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, r.Method+" "+r.URL.Path)
				switch r.URL.Path {
				case "/reseed/sites/index":
					writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"sites": []any{map[string]any{"id": 1, "base_url": site.URL + "/", "download_page": "download.php?id={id}"}}}})
				case "/reseed/sites/reportExisting":
					writeJSON(w, map[string]any{"code": 0, "data": map[string]any{"sid_sha1": "synthetic"}})
				case "/reseed/index/index":
					writeJSON(w, map[string]any{"code": 0, "data": map[string]any{original: map[string]any{"torrent": []any{map[string]any{"sid": 1, "torrent_id": 7, "info_hash": seed}}}}})
				case "/download.php":
					if r.URL.Query().Get("id") != "7" || r.URL.Query().Get("https") != "1" || r.Header.Get("Cookie") != "a=1" {
						t.Error("download route/credential contract")
					}
					w.Write(torrent)
				default:
					t.Errorf("unexpected site route %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer site.Close()
			cfg := classConfig()
			_, e := cfg.Init(map[string]any{"enabled": true, "downloaders": []any{"source"}, "sites": []any{json.Number("1")}, "token": "synthetic-token", "size": json.Number("0"), "labelsafterseed": "owned", "reuse_save_path": true, "categoryafterseed": "movie", "auto_start": true, "notify": true}, []any{json.Number("1")})
			if e != nil {
				t.Fatal(e)
			}
			snapshot, _ := cfg.Snapshot()
			identity := RunIdentity{Instance: "owned-iyuu", Generation: 1, Revision: 1, Fingerprint: "synthetic-bindings", Run: "owned-run"}
			fields := map[string]any{"url": site.URL + "/", "id": json.Number("1"), "name": "owned-site", "cookie": "a=1", "ua": "owned-UA"}
			var final State
			for attempt := 0; attempt < 2; attempt++ {
				j, e := OpenJournal(filepath.Join(dir, "journal.sqlite"), key)
				if e != nil {
					t.Fatal(e)
				}
				run, e := j.Begin(ctx, identity, snapshot, liveGuard)
				if e != nil {
					t.Fatal(e)
				}
				httpSite, e := NewNativeSiteHTTP([]SiteTransportBinding{{Ref: "site", Fields: fields, AllowedOrigins: []string{site.URL}}}, SiteTransportOptions{Guard: liveGuard, DefaultUA: "owned-UA"})
				if e != nil {
					t.Fatal(e)
				}
				cache, e := OpenFileTorrentCache(dir)
				if e != nil {
					t.Fatal(e)
				}
				host, e := NewNativeHost(NativeHostOptions{Run: run, Bindings: []NativeBinding{{Service: Service{Ref: "qb", Name: "source", Kind: "qbittorrent"}, Config: download.Config{ID: "owned", Kind: "qbittorrent", BaseURL: qb.URL}, Options: download.IYUUOptions{AutoCategory: true, Sleep: func(context.Context, time.Duration) error { return nil }}, AllowedSaveRoots: []string{"/data"}}}, Sites: []Site{{Ref: "site", Fields: fields}}, SiteHTTP: httpSite, Cache: cache})
				if e != nil {
					t.Fatal(e)
				}
				api, e := NewAPITransport(TrustedAPIBinding{Token: "synthetic-token"}, APITransportOptions{Guard: liveGuard, LoopbackOrigin: site.URL})
				if e != nil {
					t.Fatal(e)
				}
				durable, e := NewDurableAPI(ctx, run, api)
				if e != nil {
					t.Fatal(e)
				}
				helper, e := NewHelper(durable, func() time.Time { return run.Started })
				if e != nil {
					t.Fatal(e)
				}
				core, e := NewCore(cfg, host, helper)
				if e != nil {
					t.Fatal(e)
				}
				e = core.AutoSeed(ctx)
				if loseCleanup {
					if !errors.Is(e, ErrUnconfirmed) {
						t.Fatalf("lost acknowledgement must block: %v", e)
					}
				} else if e != nil {
					t.Fatal(e)
				}
				state, e := core.Snapshot()
				if e != nil {
					t.Fatal(e)
				}
				if attempt == 0 {
					final = state
				} else if !reflect.DeepEqual(final, state) {
					t.Fatalf("replay state differs\n%+v\n%+v", final, state)
				}
				host.Close()
				api.Close()
				httpSite.Close()
				cache.Close()
				j.Close()
			}
			mu.Lock()
			defer mu.Unlock()
			if addCount != 1 || cleanupCount != 1 {
				t.Fatalf("replayed mutations add%d cleanup%d calls%v", addCount, cleanupCount, calls)
			}
			if !loseCleanup && (final.Counters.Success != 1 || startCount != 1) {
				t.Fatalf("complete core flow state%+v starts%d calls%v", final, startCount, calls)
			}
			if loseCleanup && startCount != 0 {
				t.Error("continued after unknown cleanup")
			}
			if !loseCleanup {
				b, e := os.ReadFile(filepath.Join(dir, torrentCachePath(site.URL+"/download.php?id=7&https=1")))
				if e != nil || !bytes.Equal(b, torrent) {
					t.Fatalf("real torrent cache %q %v", b, e)
				}
			}
			raw, e := os.ReadFile(filepath.Join(dir, "journal.sqlite"))
			if e != nil {
				t.Fatal(e)
			}
			for _, secret := range []string{"synthetic-token", "owned-UA", "a=1", "/data/source", "IYUU自动辅种任务完成"} {
				if bytes.Contains(raw, []byte(secret)) {
					t.Errorf("plaintext escaped journal: %s", secret)
				}
			}
		})
	}
}

func TestJournalCrashIntentAndBindingFence(t *testing.T) {
	ctx := context.Background()
	filename := filepath.Join(t.TempDir(), "owned.sqlite")
	key := bytes.Repeat([]byte{91}, 32)
	id := RunIdentity{Instance: "owned", Generation: 1, Revision: 1, Fingerprint: "fixture", Run: "crash"}
	j, e := OpenJournal(filename, key)
	if e != nil {
		t.Fatal(e)
	}
	r, e := j.Begin(ctx, id, map[string]string{"token": "synthetic"}, liveGuard)
	if e != nil {
		t.Fatal(e)
	}
	calls := 0
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected owned crash")
			}
		}()
		var out any
		_ = r.step(ctx, "selected-write", "one", true, &out, func() (any, string, error) { calls++; panic("owned process exit after write") })
	}()
	j.Close()
	j, e = OpenJournal(filename, key)
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	r, e = j.Begin(ctx, id, map[string]string{"token": "synthetic"}, liveGuard)
	if e != nil {
		t.Fatal(e)
	}
	var out any
	e = r.step(ctx, "selected-write", "one", true, &out, func() (any, string, error) { calls++; return true, "confirmed", nil })
	if !errors.Is(e, ErrUnconfirmed) || calls != 1 {
		t.Fatalf("reissued pending mutation %d %v", calls, e)
	}
	if _, e = j.Begin(ctx, id, map[string]string{"token": "changed"}, liveGuard); !errors.Is(e, ErrReplayDiverged) {
		t.Fatal("changed initial config accepted")
	}
	r, e = j.Begin(ctx, RunIdentity{Instance: "owned", Generation: 1, Revision: 1, Fingerprint: "fixture", Run: "binding"}, nil, liveGuard)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.bind(ctx, "credentials", map[string]string{"cookie": "one"}); e != nil {
		t.Fatal(e)
	}
	if e = r.bind(ctx, "credentials", map[string]string{"cookie": "two"}); !errors.Is(e, ErrReplayDiverged) {
		t.Fatal("changed credential binding accepted")
	}
}

func TestSiteHTTPActualResponseDecodeAgainstInstalledOracle(t *testing.T) {
	raw, e := os.ReadFile("reference/resolver-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	v, e := decodeJSON(raw)
	if e != nil {
		t.Fatal(e)
	}
	var response atomic.Value
	response.Store(map[string]any{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := response.Load().(map[string]any)
		for k, v := range m["headers"].(map[string]any) {
			w.Header().Set(k, v.(string))
		}
		status, _ := m["status"].(json.Number).Int64()
		w.WriteHeader(int(status))
		b, _ := base64.StdEncoding.DecodeString(m["content"].(string))
		w.Write(b)
	}))
	defer server.Close()
	transport, e := NewNativeSiteHTTP([]SiteTransportBinding{{Ref: "owned", Fields: map[string]any{"url": server.URL + "/", "cookie": "a=1; b=%FF; a=last", "ua": "synthetic-UA"}, AllowedOrigins: []string{server.URL}}}, SiteTransportOptions{Guard: liveGuard, DefaultUA: "synthetic-default-UA"})
	if e != nil {
		t.Fatal(e)
	}
	defer transport.Close()
	checked := 0
	for _, row := range v.(map[string]any)["cases"].([]any) {
		c := row.(map[string]any)
		name := c["name"].(string)
		if !strings.HasPrefix(name, "encoding-") && !strings.HasPrefix(name, "content-type-") {
			continue
		}
		m := c["responses"].([]any)[0].(map[string]any)
		response.Store(m)
		got, e := transport.Page(context.Background(), "owned", DetailsPage, json.Number("7"))
		if e != nil {
			t.Fatalf("%s %v", name, e)
		}
		want := c["decoded"].([]any)[0].(map[string]any)["text"].(string)
		if got.Text != want {
			t.Errorf("%s got%q want%q", name, got.Text, want)
		}
		checked++
	}
	if checked != 33 {
		t.Fatalf("decoder corpus count%d", checked)
	}
}

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/download"
)

// Same approved owned TLS seam: explicit IPv4 listener, no implicit IPv6,
// CONNECT, forwarding, environment edits, real daemon or upstream request.
func coreIOOwnedTLS(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peer := &httptest.Server{Listener: listener, Config: &http.Server{Handler: handler, ErrorLog: log.New(io.Discard, "", 0)}}
	peer.StartTLS()
	t.Cleanup(peer.Close)
	req, err := http.NewRequest("GET", peer.URL+"/owned", nil)
	if err != nil {
		t.Fatal(err)
	}
	base := http.DefaultTransport.(*http.Transport)
	if base.Proxy != nil {
		proxy, err := base.Proxy(req)
		if err != nil || proxy != nil {
			t.Fatal("unchanged route does not allow this owned endpoint; no fallback")
		}
	}
	return peer
}

type coreIOTorrentPeer struct {
	mu              sync.Mutex
	t               *testing.T
	kind, hash, dir string
	owned           bool
	calls           []string
	deletes, tags   int
}

func (p *coreIOTorrentPeer) snapshot() ([]string, int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...), p.deletes, p.tags
}
func (p *coreIOTorrentPeer) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	reject := func() { p.t.Error("unexpected owned core I/O request", r.Method, r.URL.Path); w.WriteHeader(400) }
	if r.URL.IsAbs() || r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("Cookie") != "" {
		reject()
		return
	}
	tag := "light-media"
	if !p.owned {
		tag = "unrelated-owned-fixture"
	}
	if p.kind == "qb" {
		if r.Header.Get("Authorization") != "" {
			reject()
			return
		}
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			if r.Method != "GET" || (r.URL.RawQuery != "" && r.URL.RawQuery != "hashes="+p.hash) {
				reject()
				return
			}
			p.calls = append(p.calls, "list")
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"hash": p.hash, "name": "Owned.2024.mkv", "state": "pausedUP", "save_path": p.dir, "size": 22, "total_size": 22, "completed": 22, "progress": 1, "tags": tag}})
		case "/api/v2/torrents/delete", "/api/v2/torrents/addTags":
			if r.Method != "POST" || r.URL.RawQuery != "" || r.ParseForm() != nil || r.PostForm.Get("hashes") != p.hash || len(r.PostForm) != 2 {
				reject()
				return
			}
			if strings.HasSuffix(r.URL.Path, "delete") {
				if r.PostForm.Get("deleteFiles") != "false" {
					reject()
					return
				}
				p.calls = append(p.calls, "remove_keep_data")
				p.deletes++
			} else {
				if r.PostForm.Get("tags") != "已整理" {
					reject()
					return
				}
				p.calls = append(p.calls, "tag")
				p.tags++
			}
			_, _ = io.WriteString(w, "Ok.")
		default:
			reject()
		}
		return
	}
	if r.Method != "POST" || r.URL.Path != "/transmission/rpc" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "Basic Og==" {
		reject()
		return
	}
	var frame struct {
		Method   string                     `json:"method"`
		Params   map[string]json.RawMessage `json:"params"`
		ID       json.RawMessage            `json:"id"`
		Protocol string                     `json:"jsonrpc"`
	}
	if json.NewDecoder(r.Body).Decode(&frame) != nil || frame.Protocol != "2.0" {
		reject()
		return
	}
	result := map[string]any{}
	switch frame.Method {
	case "session_get":
		p.calls = append(p.calls, "session")
		result["version"] = "4.1.0"
	case "torrent_get":
		if raw, exists := frame.Params["ids"]; exists {
			var ids []string
			if json.Unmarshal(raw, &ids) != nil || !reflect.DeepEqual(ids, []string{p.hash}) {
				reject()
				return
			}
		}
		p.calls = append(p.calls, "list")
		result["torrents"] = []any{map[string]any{"id": 1, "hash_string": p.hash, "name": "Owned.2024.mkv", "download_dir": p.dir, "total_size": 22, "left_until_done": 0, "percent_done": 1, "status": 6, "labels": []string{tag}}}
	case "torrent_set", "torrent_remove":
		var ids []string
		if json.Unmarshal(frame.Params["ids"], &ids) != nil || !reflect.DeepEqual(ids, []string{p.hash}) || len(frame.Params) != 2 {
			reject()
			return
		}
		if frame.Method == "torrent_remove" {
			if string(frame.Params["delete_local_data"]) != "false" {
				reject()
				return
			}
			p.calls = append(p.calls, "remove_keep_data")
			p.deletes++
		} else {
			var tags []string
			if json.Unmarshal(frame.Params["labels"], &tags) != nil || !reflect.DeepEqual(tags, []string{"light-media", "已整理"}) {
				reject()
				return
			}
			p.calls = append(p.calls, "tag")
			p.tags++
		}
	default:
		reject()
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": frame.ID, "result": result})
}

func TestOriginalCorePluginDownloaderPrivateTLS(t *testing.T) {
	env := os.Environ()
	global := http.DefaultTransport.(*http.Transport)
	tlsBefore := global.TLSClientConfig
	for _, kind := range []string{"qb", "tr"} {
		t.Run(kind, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			dir := t.TempDir()
			sentinel := filepath.Join(dir, "owned-not-a-real-download")
			if err := os.WriteFile(sentinel, []byte("keep owned bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			p := &coreIOTorrentPeer{t: t, kind: kind, hash: strings.Repeat("a", 40), dir: dir, owned: true}
			peer := coreIOOwnedTLS(t, http.HandlerFunc(p.serve))
			d := DownloaderConfig{Config: download.Config{ID: "owned-core-" + kind, Kind: kind, BaseURL: peer.URL}, Name: "Owned TLS fallback", DownloadPath: dir}
			// Strict unrelated New must still fail before any application request.
			strict, err := download.New(d.Config)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = strict.List(ctx); err == nil {
				t.Fatal("ordinary download.New accepted self-signed peer")
			}
			strict.CloseIdleConnections()
			if calls, _, _ := p.snapshot(); len(calls) != 0 {
				t.Fatal("strict TLS reached owned handler", calls)
			}
			settings := a.Settings()
			settings.Downloaders = []DownloaderConfig{d}
			settings.DefaultDownloader = d.ID
			if err = a.saveSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			intent := Intent{ID: "owned-core-intent", Hash: p.hash, State: "imported", DownloaderID: d.ID, DownloaderSnapshot: downloaderSnapshot(d)}
			if err = a.Store.Put(ctx, "intent", intent.ID, intent); err != nil {
				t.Fatal(err)
			}
			if err = a.markOriginalTorrentCompleted(ctx, intent); err != nil {
				t.Fatal("actual completion fallback", err)
			}
			raw, _ := json.Marshal(map[string]string{"hash": p.hash})
			if err = a.consumeOriginalCoreDownloadFileDeleted(ctx, raw); err != nil {
				t.Fatal("actual delete fallback", err)
			}
			a.wg.Wait()
			calls, deletes, tags := p.snapshot()
			want := []string{"list", "list", "tag", "list", "list", "remove_keep_data"}
			if kind == "tr" {
				want = []string{"session", "list", "list", "tag", "session", "list", "session", "list", "remove_keep_data"}
			}
			if !reflect.DeepEqual(calls, want) || deletes != 1 || tags != 1 {
				t.Fatal("exact core fallback effects", calls, deletes, tags)
			}
			// A different frozen downloader snapshot cannot reach any protocol call.
			stale := intent
			stale.DownloaderSnapshot = downloaderSnapshot(DownloaderConfig{Config: download.Config{ID: d.ID, Kind: kind, BaseURL: peer.URL + "/different"}})
			if err = a.markOriginalTorrentCompleted(ctx, stale); err == nil {
				t.Fatal("stale snapshot not rejected")
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if err = a.markOriginalTorrentCompleted(canceled, intent); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation changed", err)
			}
			if after, _, _ := p.snapshot(); !reflect.DeepEqual(after, calls) {
				t.Fatal("guard/cancel dispatched", after)
			}
			// No source ownership tag: first core list must not produce a remove call.
			p.mu.Lock()
			p.owned = false
			p.mu.Unlock()
			if err = a.consumeOriginalCoreDownloadFileDeleted(ctx, raw); err != nil {
				t.Fatal(err)
			}
			if _, gotDeletes, gotTags := p.snapshot(); gotDeletes != 1 || gotTags != 1 {
				t.Fatal("unowned task mutated")
			}
			if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep owned bytes" {
				t.Fatal("local keep-data guard changed", err)
			}
		})
	}
	if http.DefaultTransport != global || global.TLSClientConfig != tlsBefore || !reflect.DeepEqual(os.Environ(), env) {
		t.Fatal("default TLS or environment mutated")
	}
}

func TestOriginalMetadataImagePrivateTLS(t *testing.T) {
	env := os.Environ()
	global := http.DefaultTransport.(*http.Transport)
	tlsBefore := global.TLSClientConfig
	ordinary := assetImageClient
	privateTransport, ok := originalMetadataImageClient.Transport.(*http.Transport)
	if !ok || privateTransport == global || privateTransport.TLSClientConfig == nil || !privateTransport.TLSClientConfig.InsecureSkipVerify || originalMetadataImageClient.Timeout != 20*time.Second {
		t.Fatal("image client is not private scoped TLS")
	}
	t.Cleanup(originalMetadataImageClient.CloseIdleConnections)
	var mu sync.Mutex
	var calls []string
	data := []byte("owned-image-only")
	peer := coreIOOwnedTLS(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.URL.Path)
		if r.Method != "GET" || r.URL.IsAbs() || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("unexpected owned image request")
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case "/image":
			_, _ = w.Write(data)
		case "/redirect":
			w.Header().Set("Location", "/must-not-follow")
			w.WriteHeader(http.StatusFound)
		case "/oversize":
			_, _ = w.Write(bytes.Repeat([]byte{'x'}, (8<<20)+1))
		default:
			t.Error("unexpected owned image path")
			w.WriteHeader(400)
		}
	}))
	req, _ := http.NewRequest("GET", peer.URL+"/image", nil)
	if response, err := assetImageClient.Do(req); err == nil {
		response.Body.Close()
		t.Fatal("ordinary image client accepted self-signed peer")
	}
	mu.Lock()
	n := len(calls)
	mu.Unlock()
	if n != 0 {
		t.Fatal("strict image request reached HTTP")
	}
	a := newTestApp(t)
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "Owned.mkv")
	target := filepath.Join(dir, "poster.jpg")
	if err := os.WriteFile(source, []byte("owned media only"), 0600); err != nil {
		t.Fatal(err)
	}
	settings := a.Settings()
	settings.Roots = []Root{{ID: "owned-images", Path: dir, Kind: "mixed"}}
	settings.AllowedRoots = []string{dir}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	stamp, err := browserStat(source)
	if err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, originalMetadataSnapshotKey{}, stamp)
	if err = a.fetchOriginalMetadataImage(ctx, source, peer.URL+"/image", target, false); err != nil {
		t.Fatal("actual owned sidecar", err)
	}
	if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, data) {
		t.Fatal("owned sidecar bytes", err)
	}
	for _, route := range []string{"/redirect", "/oversize"} {
		rejected := filepath.Join(dir, strings.TrimPrefix(route, "/")+".jpg")
		if err = a.fetchOriginalMetadataImage(ctx, source, peer.URL+route, rejected, false); err == nil {
			t.Fatal("existing bound/redirect accepted", route)
		}
		if _, err = os.Stat(rejected); !os.IsNotExist(err) {
			t.Fatal("rejected image published", route, err)
		}
	}
	mu.Lock()
	before := len(calls)
	mu.Unlock()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err = a.fetchOriginalMetadataImage(canceled, source, peer.URL+"/image", target, true); !errors.Is(err, context.Canceled) {
		t.Fatal("image cancellation", err)
	}
	if err = a.fetchOriginalMetadataImage(ctx, source, "https://owned:secret@127.0.0.1/image", target, true); err == nil {
		t.Fatal("URL userinfo accepted")
	}
	mu.Lock()
	after := len(calls)
	mu.Unlock()
	if after != before {
		t.Fatal("preflight rejection dispatched")
	}
	if err = os.WriteFile(source, []byte("changed owned media only"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = a.fetchOriginalMetadataImage(ctx, source, peer.URL+"/image", target, true); err == nil {
		t.Fatal("changed source published sidecar")
	}
	if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, data) {
		t.Fatal("failed overwrite altered existing target", err)
	}
	mu.Lock()
	observed := append([]string(nil), calls...)
	mu.Unlock()
	if !reflect.DeepEqual(observed, []string{"/image", "/redirect", "/oversize", "/image"}) {
		t.Fatal("owned image exact requests", observed)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".light-media-metadata-") {
			t.Fatal("owned stage leaked")
		}
	}
	if assetImageClient != ordinary || http.DefaultTransport != global || global.TLSClientConfig != tlsBefore || !reflect.DeepEqual(os.Environ(), env) {
		t.Fatal("unrelated client or environment mutated")
	}
}

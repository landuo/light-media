package download

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/plugins/downloadercommands"
)

// Only owned HTTP peers. No daemon, proxy environment rewrite, global
// transport replacement, subprocess, source Python or hidden build is used.
func TestSharedDownloaderCommandQBControls(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	versions := 0
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer owned-command-key" {
			t.Error("configured auth missing")
		}
		switch r.URL.Path {
		case "/api/v2/app/webapiVersion":
			versions++
			if versions == 1 {
				io.WriteString(w, "2.10.9")
			} else {
				io.WriteString(w, "2.11.0")
			}
		case "/api/v2/torrents/resume":
			r.ParseForm()
			if _, ok := r.Form["hashes"]; ok {
				t.Error("None selector was not omitted")
			}
		case "/api/v2/torrents/start":
			r.ParseForm()
			v, ok := r.Form["hashes"]
			if !ok || len(v) != 1 || v[0] != "" {
				t.Error("empty list did not remain present empty")
			}
		case "/api/v2/torrents/recheck":
			r.ParseForm()
			if r.Form.Get("hashes") != "B|0|True|None" {
				t.Error("selector normalized", r.Form)
			}
		case "/api/v2/torrents/delete":
			r.ParseForm()
			if r.Form.Get("hashes") != "all" || r.Form.Get("deleteFiles") != "False" {
				t.Error("delete fields", r.Form)
			}
		default:
			t.Error("unexpected owned path", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer peer.Close()
	qb, _, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-qb-control", Kind: "qb", BaseURL: peer.URL}, "owned-command-key", func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	ctx := context.Background()
	for _, call := range []func() (any, error){func() (any, error) { return qb.Start(ctx, nil) }, func() (any, error) { return qb.Start(ctx, []any{}) }, func() (any, error) { return qb.Recheck(ctx, []any{"B", json.Number("0"), true, nil}) }, func() (any, error) { return qb.DeleteKeepFiles(ctx, "all") }} {
		if _, err := call(); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"GET /api/v2/app/webapiVersion", "POST /api/v2/torrents/resume", "GET /api/v2/app/webapiVersion", "POST /api/v2/torrents/start", "POST /api/v2/torrents/recheck", "POST /api/v2/torrents/delete"}
	if !reflect.DeepEqual(paths, want) {
		t.Fatal("fresh version/selected calls", paths)
	}
}

func TestSharedDownloaderCommandQBAdd(t *testing.T) {
	data := []byte("owned unparsed torrent bytes")
	var adds, versions atomic.Int32
	peer := ownedMaterialStreamPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/app/webapiVersion":
			versions.Add(1)
			io.WriteString(w, "2.11.0")
		case "/api/v2/torrents/add":
			n := adds.Add(1)
			if n == 1 || n == 4 {
				reader, err := r.MultipartReader()
				if err != nil {
					t.Error(err)
					return
				}
				fields := map[string]string{}
				fileCount := 0
				for {
					part, err := reader.NextPart()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Error(err)
						return
					}
					raw, _ := io.ReadAll(part)
					if part.FileName() != "" {
						fileCount++
						if part.Header.Get("Content-Type") != "" {
							t.Error("source bytes part has no explicit content type")
						}
						if part.FormName() != "torrent__0" || part.FileName() != "torrent__0" || !bytes.Equal(raw, data) {
							t.Error("source file name/bytes changed")
						}
					} else {
						fields[part.FormName()] = string(raw)
					}
					part.Close()
				}
				if fileCount != 1 {
					t.Error("file count", fileCount)
				}
				want := map[string]string{"savepath": "", "category": "", "tags": "first,second", "autoTMM": "False", "sequentialDownload": "True", "firstLastPiecePrio": "False", "skip_checking": "False", "ratioLimit": "2.0"}
				if n == 4 {
					want = map[string]string{"cookie": "owned=synthetic"}
				}
				if !reflect.DeepEqual(fields, want) {
					t.Error("default paused false must omit both spellings", fields)
				}
				io.WriteString(w, "Fails.")
			} else {
				r.ParseForm()
				if n == 2 {
					want := map[string][]string{"urls": {"magnet:?xt=owned"}, "paused": {"True"}, "stopped": {"True"}, "tags": {""}, "contentLayout": {"Original"}, "downloadPath": {""}, "useDownloadPath": {"True"}}
					if !reflect.DeepEqual(map[string][]string(r.PostForm), want) {
						t.Error("nullable/source aliases", r.PostForm)
					}
					io.WriteString(w, `{"added_torrent_ids":[9007199254740993],"unknown":{"hashString":"kept"}}`)
				} else {
					if r.Form.Get("urls") != "https://owned.invalid/material.torrent" {
						t.Error("ordinary source URL changed", r.Form)
					}
					if _, ok := r.Form["cookie"]; ok {
						t.Error("nil cookie emitted")
					}
					if _, ok := r.Form["paused"]; ok {
						t.Error("nil paused emitted")
					}
					io.WriteString(w, `""`)
				}
			}
		default:
			t.Error("unexpected lookup", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer peer.Close()
	qb, _, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-qb-add", Kind: "qb", BaseURL: peer.URL}, "", func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	ctx := context.Background()
	out, err := qb.Add(ctx, downloadercommands.QBAddCall{TorrentFiles: data, SavePath: "", Category: "", Tags: []any{"first", "second"}, Paused: false, AutoManagement: false, Sequential: true, FirstLastPiece: false, Extra: downloadercommands.Arguments{"is_skip_checking": false, "ratio_limit": json.Number("2.0")}})
	if err != nil || out != "Fails." {
		t.Fatal("plaintext return", out, err)
	}
	out, err = qb.Add(ctx, downloadercommands.QBAddCall{URLs: "magnet:?xt=owned", Paused: true, Tags: []any{}, Extra: downloadercommands.Arguments{"is_root_folder": true, "download_path": ""}})
	if err != nil {
		t.Fatal(err)
	}
	m, ok := out.(map[string]any)
	if !ok || m["added_torrent_ids"].([]any)[0] != json.Number("9007199254740993") || m["unknown"].(map[string]any)["hashString"] != "kept" {
		t.Fatal("mapping/raw precision lost", out)
	}
	out, err = qb.Add(ctx, downloadercommands.QBAddCall{URLs: "https://owned.invalid/material.torrent", Extra: downloadercommands.Arguments{}})
	if err != nil {
		t.Fatal(err)
	}
	m, ok = out.(map[string]any)
	if !ok || len(m) != 0 {
		t.Fatal("valid JSON empty string must Dictionary-normalize", out)
	}
	out, err = qb.Add(ctx, downloadercommands.QBAddCall{TorrentFiles: data, Cookie: "owned=synthetic", Extra: downloadercommands.Arguments{}})
	if err != nil || out != "Fails." {
		t.Fatal("byte cookie fixed field", out, err)
	}
	if adds.Load() != 4 || versions.Load() != 4 {
		t.Fatal("add/version count", adds.Load(), versions.Load())
	}
	_, err = qb.Add(ctx, downloadercommands.QBAddCall{TorrentFiles: data, Extra: downloadercommands.Arguments{"requests_args": map[string]any{"verify": false}}})
	var boundary *downloadercommands.BoundaryError
	if !errors.As(err, &boundary) || !MutationNotDispatched(err) || adds.Load() != 4 || versions.Load() != 4 {
		t.Fatal("unbound request override reached wire", err)
	}
}

func TestSharedDownloaderCommandTRControls(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	var selected []map[string]any
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/transmission/rpc" {
			t.Error("path", r.URL.Path)
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "owned" || password != "synthetic" {
			t.Error("configured basic auth missing")
		}
		var req map[string]any
		d := json.NewDecoder(r.Body)
		d.UseNumber()
		if d.Decode(&req) != nil {
			t.Error("invalid request")
			return
		}
		if _, ok := req["tag"]; ok {
			t.Error("source has no generated tag")
		}
		if _, ok := req["jsonrpc"]; ok {
			t.Error("source legacy method used modern negotiation")
		}
		method, _ := req["method"].(string)
		methods = append(methods, method)
		if r.Header.Get("X-Transmission-Session-Id") != "owned-session" {
			if method != "session-get" {
				t.Error("selected request before readonly handshake")
			}
			w.Header().Set("X-Transmission-Session-Id", "owned-session")
			w.WriteHeader(409)
			return
		}
		if method != "session-get" {
			selected = append(selected, req)
		}
		io.WriteString(w, `{"result":"success","arguments":{"rpc-version":17}}`)
	}))
	defer peer.Close()
	_, tr, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-tr-control", Kind: "tr", BaseURL: peer.URL, Username: "owned", Password: "synthetic"}, "", func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	leaf := downloadercommands.Transmission{Client: tr}
	for _, ids := range []any{nil, []any{}, "all", strings.Repeat("A", 40), []any{"recently-active"}, json.Number("1.5")} {
		got, err := leaf.StartTorrents(context.Background(), ids)
		if err != nil || got {
			t.Fatal("invalid/empty require_ids did not source-fail before HTTP", ids, got, err)
		}
	}
	mu.Lock()
	if len(methods) != 0 {
		t.Error("invalid IDs dispatched", methods)
	}
	mu.Unlock()
	if _, err = tr.Start(context.Background(), json.Number("0")); err != nil {
		t.Fatal(err)
	}
	if _, err = tr.Recheck(context.Background(), "recently-active"); err != nil {
		t.Fatal(err)
	}
	if _, err = tr.DeleteKeepFiles(context.Background(), []any{false, strings.Repeat("b", 40)}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(methods, []string{"session-get", "session-get", "torrent-start", "torrent-verify", "torrent-remove"}) {
		t.Fatal("fixed methods", methods)
	}
	if selected[0]["arguments"].(map[string]any)["ids"].([]any)[0] != json.Number("0") || selected[1]["arguments"].(map[string]any)["ids"] != "recently-active" {
		t.Fatal("selector conversion", selected)
	}
	removed := selected[2]["arguments"].(map[string]any)
	if removed["delete-local-data"] != false || removed["ids"].([]any)[0] != false {
		t.Fatal("false data deletion/bool-int selector", removed)
	}
}

func TestSharedDownloaderCommandTRAdd(t *testing.T) {
	data := []byte("owned invalid-bencode-but-vendor-byte-input")
	var total, adds atomic.Int32
	peer := ownedMaterialStreamPeer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		total.Add(1)
		var req map[string]any
		d := json.NewDecoder(r.Body)
		d.UseNumber()
		d.Decode(&req)
		if req["method"] == "session-get" {
			io.WriteString(w, `{"result":"success","arguments":{"rpc-version":17}}`)
			return
		}
		if req["method"] != "torrent-add" {
			t.Error("unexpected method", req["method"])
			w.WriteHeader(400)
			return
		}
		n := adds.Add(1)
		args := req["arguments"].(map[string]any)
		if n == 1 {
			want := map[string]any{"metainfo": base64.StdEncoding.EncodeToString(data), "download-dir": "", "paused": false, "labels": []any{}}
			if !reflect.DeepEqual(args, want) {
				t.Error("drop-only-None projection", args)
			}
			io.WriteString(w, `{"result":"success","arguments":{"torrent-added":{"id":9007199254740993,"hashString":"legacy","hash_string":"distinct","other":{"hashString":"nested"}}}}`)
		} else if n == 2 {
			if args["filename"] != "magnet:?xt=owned" || !reflect.DeepEqual(args["labels"], []any{"one"}) {
				t.Error("magnet/string label", args)
			}
			if _, ok := args["paused"]; ok {
				t.Error("nil paused must omit")
			}
			if args["cookies"] != "owned=synthetic" {
				t.Error("fixed source cookies missing")
			}
			io.WriteString(w, `{"result":"success","arguments":{"torrent-duplicate":{"id":4,"unknown":"kept"}}}`)
		} else {
			if args["filename"] != "https://owned.invalid/material.torrent" {
				t.Error("ordinary URL changed", args)
			}
			if _, ok := args["cookies"]; ok {
				t.Error("nil cookies emitted")
			}
			io.WriteString(w, `{"result":"success","arguments":{"torrent-added":{},"torrent-duplicate":{"id":4}}}`)
		}
	}))
	defer peer.Close()
	_, tr, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-tr-add", Kind: "transmission", BaseURL: peer.URL}, "", func(ctx context.Context) error { return ctx.Err() })
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	for _, data := range []any{nil, []byte{}} {
		_, err := tr.Add(context.Background(), downloadercommands.TRAddCall{Content: data})
		var source *downloadercommands.SourceException
		if !errors.As(err, &source) || !MutationNotDispatched(err) {
			t.Fatal("empty metadata validation", err)
		}
	}
	if total.Load() != 0 {
		t.Fatal("invalid bytes contacted peer")
	}
	out, err := tr.Add(context.Background(), downloadercommands.TRAddCall{Content: data, DownloadDir: "", Paused: false, Labels: []any{}})
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["id"] != json.Number("9007199254740993") || m["hashString"] != "legacy" || m["hash_string"] != "distinct" || m["other"].(map[string]any)["hashString"] != "nested" {
		t.Fatal("raw added fields normalized", out)
	}
	out, err = tr.Add(context.Background(), downloadercommands.TRAddCall{Content: "magnet:?xt=owned", Labels: "one", Cookies: "owned=synthetic"})
	if err != nil || out.(map[string]any)["unknown"] != "kept" {
		t.Fatal("raw duplicate no required hash", out, err)
	}
	_, err = tr.Add(context.Background(), downloadercommands.TRAddCall{Content: "https://owned.invalid/material.torrent"})
	var source *downloadercommands.SourceException
	if err == nil || MutationNotDispatched(err) || errors.As(err, &source) {
		t.Fatal("ambiguous selected response/fallback", err)
	}
	if total.Load() != 4 || adds.Load() != 3 {
		t.Fatal("hidden query or replay", total.Load(), adds.Load())
	}
}

func TestSharedDownloaderCommandGuardUnknownAndClose(t *testing.T) {
	var selected, versions atomic.Int32
	var revoke atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	closed := make(chan string, 8)
	idlePeer := make(chan string, 1)
	peer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/app/webapiVersion" {
			versions.Add(1)
			io.WriteString(w, "2.11.0")
			return
		}
		if r.URL.Path != "/api/v2/torrents/recheck" {
			t.Error("unexpected owned path", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		n := selected.Add(1)
		if n == 1 {
			w.WriteHeader(503)
			return
		}
		if n == 2 {
			close(entered)
			<-release
		} else {
			idlePeer <- r.RemoteAddr
		}
		io.WriteString(w, "ignored")
	}))
	peer.Config.ConnState = func(conn net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			select {
			case closed <- conn.RemoteAddr().String():
			default:
			}
		}
	}
	peer.Start()
	defer peer.Close()
	guard := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if revoke.Load() {
			return fmt.Errorf("owned revoked")
		}
		return nil
	}
	qb, _, closeClient, err := NewSharedDownloaderCommands(Config{ID: "owned-guard", Kind: "qb", BaseURL: peer.URL}, "", guard)
	if err != nil {
		t.Fatal(err)
	}
	defer closeClient()
	_, err = qb.Recheck(context.Background(), "owned")
	var source *downloadercommands.SourceException
	if err == nil || MutationNotDispatched(err) || errors.As(err, &source) || selected.Load() != 1 {
		t.Fatal("delegated 503 retried or source-caught", err, selected.Load())
	}
	revoke.Store(true)
	_, err = qb.Start(context.Background(), nil)
	if !MutationNotDispatched(err) || MutationFailureClass(err) != "owner_denied" || versions.Load() != 0 {
		t.Fatal("pre-dispatch authority classification", err)
	}
	revoke.Store(false)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := qb.Recheck(ctx, "owned"); result <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		cancel()
		t.Fatal("selected request did not enter")
	}
	// The second operation waits on the same native lock and must honor its own
	// cancellation without sending another selected mutation.
	waitCtx, waitCancel := context.WithCancel(context.Background())
	waitCancel()
	if _, err := qb.Recheck(waitCtx, "owned"); !MutationNotDispatched(err) || MutationFailureClass(err) != "cancelled" {
		close(release)
		cancel()
		t.Fatal("cancelled concurrent waiter", err)
	}
	cancel()
	close(release)
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || MutationNotDispatched(err) {
			t.Fatal("delegated cancellation lost unknown-effect distinction", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not drain")
	}
	if selected.Load() != 2 {
		t.Fatal("selected call replay", selected.Load())
	}
	// Establish a distinct successful idle connection after cancellation. Match
	// its exact peer address, so cancellation's earlier close is not evidence for
	// this client's explicit owned idle-pool Close.
	if _, err := qb.Recheck(context.Background(), "owned"); err != nil {
		t.Fatal(err)
	}
	wantClosed := <-idlePeer
	closeClient()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case address := <-closed:
			if address == wantClosed {
				return
			}
		case <-deadline.C:
			t.Fatal("owned idle connection did not close")
		}
	}
}

func TestSharedDownloaderCommandQBTLSIsolation(t *testing.T) {
	global, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatal("test requires the unchanged standard default transport")
	}
	var roots *x509.CertPool
	if global.TLSClientConfig != nil {
		if global.TLSClientConfig.InsecureSkipVerify {
			t.Fatal("default transport was already non-verifying")
		}
		roots = global.TLSClientConfig.RootCAs
	}
	var originCalls, otherCalls atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherCalls.Add(1)
		t.Error("redirect reached a different owned origin")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer other.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer owned-tls-key" {
			t.Error("configured bearer missing at exact origin")
		}
		switch r.URL.Path {
		case "/api/v2/torrents/recheck":
			io.WriteString(w, "ignored control response")
		case "/api/v2/torrents/delete":
			w.Header().Set("Location", other.URL+"/not-followed")
			w.WriteHeader(http.StatusFound)
		default:
			t.Error("unexpected application request crossed strict TLS boundary", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer origin.Close()
	var revoked atomic.Bool
	guard := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if revoked.Load() {
			return errors.New("owned TLS grant revoked")
		}
		return nil
	}
	qb, _, closeQB, err := NewSharedDownloaderCommands(Config{ID: "owned-tls-qb", Kind: "qb", BaseURL: origin.URL}, "owned-tls-key", guard)
	if err != nil {
		t.Fatal(err)
	}
	defer closeQB()
	if _, err = qb.Recheck(context.Background(), "owned"); err != nil {
		t.Fatal("dedicated source-verifyFalse qB rejected owned self-signed TLS", err)
	}
	if originCalls.Load() != 1 {
		t.Fatal("actual HTTPS request count", originCalls.Load())
	}
	// The old IYUU constructor and the new TR command client remain verifying;
	// both fail the same self-signed peer before any HTTP handler is entered.
	old, err := NewIYUUClient(Config{ID: "owned-tls-old", Kind: "qbittorrent", BaseURL: origin.URL}, IYUUOptions{Guard: guard})
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if _, err = old.Recheck(context.Background(), []string{strings.Repeat("b", 40)}); err == nil {
		t.Fatal("old IYUU TLS verification was relaxed")
	}
	_, tr, closeTR, err := NewSharedDownloaderCommands(Config{ID: "owned-tls-tr", Kind: "transmission", BaseURL: origin.URL}, "", guard)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTR()
	if _, err = tr.Recheck(context.Background(), json.Number("0")); err == nil {
		t.Fatal("TR TLS verification was relaxed")
	}
	if originCalls.Load() != 1 {
		t.Fatal("a strict client entered the HTTPS handler", originCalls.Load())
	}
	// No cross-origin credential hop is newly authorized by certificate policy.
	if _, err = qb.DeleteKeepFiles(context.Background(), "owned"); err == nil {
		t.Fatal("selected redirect was treated as success")
	}
	if originCalls.Load() != 2 || otherCalls.Load() != 0 {
		t.Fatal("redirect boundary changed", originCalls.Load(), otherCalls.Load())
	}
	revoked.Store(true)
	if _, err = qb.Recheck(context.Background(), "owned"); !MutationNotDispatched(err) || MutationFailureClass(err) != "owner_denied" {
		t.Fatal("TLS relaxation bypassed current guard", err)
	}
	if originCalls.Load() != 2 {
		t.Fatal("revoked qB client dispatched")
	}
	if http.DefaultTransport != global {
		t.Fatal("default transport replaced")
	}
	if global.TLSClientConfig != nil {
		if global.TLSClientConfig.InsecureSkipVerify || global.TLSClientConfig.RootCAs != roots {
			t.Fatal("global TLS/CA configuration changed")
		}
	} else if roots != nil {
		t.Fatal("global CA configuration disappeared")
	}
}

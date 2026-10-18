package torrenttransfer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	download "light-media/internal/plugins/iyuuautoseed/native"
)

type transferDaemon struct {
	mu                                       sync.Mutex
	t                                        *testing.T
	kind, hash, dir                          string
	source, added, removed                   bool
	tags                                     string
	status                                   int
	writes, reads, starts, rechecks, deletes int
	lost                                     string
	server                                   *httptest.Server
}

func newTransferDaemon(t *testing.T, kind, hash string, source bool) *transferDaemon {
	d := &transferDaemon{t: t, kind: kind, hash: hash, source: source, status: 0, dir: "/destination/电影"}
	if source {
		d.dir = "/source/电影"
		d.status = 5
	}
	d.server = httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(d.server.Close)
	return d
}
func (d *transferDaemon) drop(w http.ResponseWriter, op string) bool {
	if d.lost != op {
		return false
	}
	d.lost = ""
	c, _, e := w.(http.Hijacker).Hijack()
	if e == nil {
		_ = c.Close()
	}
	return true
}
func (d *transferDaemon) rows() []any {
	if (!d.source && !d.added) || d.removed {
		return []any{}
	}
	if d.kind == "qbittorrent" {
		tags := d.tags
		if d.source {
			tags = "已整理, 自有"
		}
		return []any{map[string]any{"hash": d.hash, "save_path": d.dir, "tags": tags, "category": "电影", "state": "pausedUP", "progress": 1, "amount_left": 0}}
	}
	return []any{map[string]any{"hashString": d.hash, "downloadDir": d.dir, "labels": []string{"已整理", "自有"}, "status": d.status, "percentDone": 1, "totalSize": 1}}
}
func (d *transferDaemon) serve(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	write := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
	if d.kind == "qbittorrent" {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/app/webapiVersion":
			_, _ = io.WriteString(w, "2.11.2")
		case "/api/v2/torrents/info":
			d.reads++
			write(d.rows())
		case "/api/v2/torrents/add":
			d.writes++
			if e := r.ParseMultipartForm(16 << 20); e != nil {
				d.t.Error(e)
				w.WriteHeader(400)
				return
			}
			if r.FormValue("paused") != "True" || r.FormValue("stopped") != "True" || r.FormValue("autoTMM") != "False" {
				d.t.Errorf("original paused/manual flags: %v", r.Form)
			}
			d.dir = r.FormValue("savepath")
			d.tags = r.FormValue("tags")
			f, _, e := r.FormFile("torrent__0")
			if e != nil {
				d.t.Error(e)
			} else {
				b, _ := io.ReadAll(f)
				f.Close()
				hs, e := download.TransferContentHashes(b, d.kind)
				if e != nil || len(hs) == 0 || hs[0] != d.hash {
					d.t.Errorf("actual content/hash %v %v", hs, e)
				}
			}
			d.added = true
			if d.drop(w, "add") {
				return
			}
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/torrents/removeTags":
			d.writes++
			_ = r.ParseForm()
			if r.FormValue("hashes") != d.hash {
				d.t.Error("wrong cleanup hash")
			}
			tag := r.FormValue("tags")
			parts := []string{}
			for _, p := range strings.Split(d.tags, ",") {
				if p != tag {
					parts = append(parts, p)
				}
			}
			d.tags = strings.Join(parts, ",")
			if d.drop(w, "removeTags") {
				return
			}
		case "/api/v2/torrents/recheck":
			d.writes++
			d.rechecks++
		case "/api/v2/torrents/start", "/api/v2/torrents/resume":
			d.writes++
			d.starts++
		case "/api/v2/torrents/delete":
			d.writes++
			d.deletes++
			_ = r.ParseForm()
			if r.FormValue("deleteFiles") != "false" || r.FormValue("hashes") != d.hash {
				d.t.Error("delete must exact source and preserve data")
			}
			d.removed = true
			if d.drop(w, "delete") {
				return
			}
		default:
			d.t.Errorf("unexpected qB route %s", r.URL.Path)
			w.WriteHeader(404)
		}
		return
	}
	var req struct {
		Method string
		Params map[string]any
		ID     json.RawMessage
	}
	if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
		d.t.Error(e)
		return
	}
	result := map[string]any{}
	switch req.Method {
	case "session_get":
		result["version"] = "4.1.0"
	case "torrent_get":
		d.reads++
		result["torrents"] = d.rows()
	case "torrent_add":
		d.writes++
		if req.Params["paused"] != true {
			d.t.Error("TR add not paused")
		}
		d.dir, _ = req.Params["download_dir"].(string)
		content, _ := req.Params["metainfo"].(string)
		b, e := base64.StdEncoding.DecodeString(content)
		hs, e2 := download.TransferContentHashes(b, d.kind)
		if e != nil || e2 != nil || len(hs) == 0 || hs[0] != d.hash {
			d.t.Error("TR exact content changed")
		}
		d.added = true
		d.status = 0
		result["torrent_added"] = map[string]any{"hash_string": d.hash}
		if d.drop(w, "add") {
			return
		}
	case "torrent_verify":
		d.writes++
		d.rechecks++
	case "torrent_start":
		d.writes++
		d.starts++
	case "torrent_remove":
		d.writes++
		d.deletes++
		if req.Params["delete_local_data"] != false {
			d.t.Error("TR delete local data is forbidden")
		}
		d.removed = true
		if d.drop(w, "delete") {
			return
		}
	default:
		d.t.Errorf("unexpected TR method %s", req.Method)
		w.WriteHeader(400)
		return
	}
	write(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
}
func (d *transferDaemon) counts() (int, int, int, int, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.writes, d.reads, d.starts, d.rechecks, d.deletes
}
func ownedMaterial(t *testing.T) ([]byte, string) {
	t.Helper()
	raw, e := bencode(map[string]any{"announce": "https://tracker.invalid/announce", "info": map[string]any{"name": "owned-test.bin", "length": newByteInteger(1), "piece length": newByteInteger(1), "pieces": strings.Repeat("x", 20)}})
	if e != nil {
		t.Fatal(e)
	}
	ids, e := download.TransferContentHashes(raw, "qbittorrent")
	if e != nil || len(ids) != 1 {
		t.Fatal(ids, e)
	}
	return raw, ids[0]
}
func nativeTransferFixture(t *testing.T, fromKind, toKind, lost string) (*Journal, *Core, *NativeHost, func() (*Journal, *Core, *NativeHost), *transferDaemon, *transferDaemon) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	backup := filepath.Join(dir, "backup")
	if e := os.Mkdir(backup, 0700); e != nil {
		t.Fatal(e)
	}
	raw, hash := ownedMaterial(t)
	if e := os.WriteFile(filepath.Join(backup, hash+".torrent"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	src := newTransferDaemon(t, fromKind, hash, true)
	dst := newTransferDaemon(t, toKind, hash, false)
	if lost == "delete" {
		src.lost = lost
	} else {
		dst.lost = lost
	}
	cfg, e := RestoreConfig(map[string]any{"fromtorrentpath": backup, "fromdownloader": "source", "todownloader": "destination", "frompath": "/source", "topath": "/destination", "autostart": true, "transferemptylabel": true, "add_torrent_tags": "已整理,转移做种", "deletesource": true})
	if e != nil {
		t.Fatal(e)
	}
	key := make([]byte, 32)
	key[0] = 33
	dbpath := filepath.Join(dir, "receipts.db")
	bindings := []NativeBinding{}
	for _, v := range []struct {
		daemon    *transferDaemon
		name, ref string
	}{{src, "source", "native-source"}, {dst, "destination", "native-destination"}} {
		bindings = append(bindings, NativeBinding{Service: Service{Name: v.name, Ref: v.ref, Type: v.daemon.kind}, Config: download.Config{ID: v.ref, Kind: v.daemon.kind, BaseURL: v.daemon.server.URL}, Options: download.IYUUOptions{Sleep: func(context.Context, time.Duration) error { return nil }}})
	}
	identity := RunIdentity{Instance: "owned-original", Generation: 1, Revision: 1, Fingerprint: "owned-input-sha", Run: "owned-run"}
	initial := State{Config: cfg.values, Recheck: map[string][]string{}}
	makeRun := func() (*Journal, *Core, *NativeHost) {
		j, e := OpenJournal(dbpath, key)
		if e != nil {
			t.Fatal(e)
		}
		r, e := j.Begin(ctx, identity, initial, func(context.Context) error { return nil })
		if e != nil {
			t.Fatal(e)
		}
		h, e := NewNativeHost(NativeHostOptions{Context: ctx, Run: r, Config: cfg, Bindings: bindings, TempDir: filepath.Join(dir, "temp")})
		if e != nil {
			t.Fatal(e)
		}
		c, e := NewCore(cfg, h)
		if e != nil {
			t.Fatal(e)
		}
		return j, c, h
	}
	j, c, h := makeRun()
	return j, c, h, makeRun, src, dst
}
func TestActualNativeTransferFourDirectionsAndColdReplay(t *testing.T) {
	for _, from := range []string{"qbittorrent", "transmission"} {
		for _, to := range []string{"qbittorrent", "transmission"} {
			t.Run(from+"-"+to, func(t *testing.T) {
				j, c, h, cold, src, dst := nativeTransferFixture(t, from, to, "")
				ctx := context.Background()
				counts, e := c.Transfer(ctx)
				if e != nil || counts.Success != 1 || counts.Fail != 0 {
					t.Fatalf("actual full source transfer %+v %v", counts, e)
				}
				sw, sr, _, _, deletes := src.counts()
				dw, dr, starts, rechecks, _ := dst.counts()
				if deletes != 1 || starts != 1 || ((to == "qbittorrent") != (rechecks == 1)) {
					t.Fatalf("source/target controls deletes%d starts%d rechecks%d", deletes, starts, rechecks)
				}
				h.Close()
				j.Close()
				j, c, h = cold()
				defer j.Close()
				defer h.Close()
				counts, e = c.Transfer(ctx)
				if e != nil || counts.Success != 1 {
					t.Fatalf("cold source replay %+v %v", counts, e)
				}
				sw2, sr2, _, _, _ := src.counts()
				dw2, dr2, _, _, _ := dst.counts()
				if sw2 != sw || sr2 != sr || dw2 != dw || dr2 != dr {
					t.Fatalf("cold replay performed new daemon IO")
				}
			})
		}
	}
}
func TestActualNativeTransferUnknownNeverResubmits(t *testing.T) {
	for _, op := range []string{"add", "removeTags", "delete"} {
		t.Run(op, func(t *testing.T) {
			j, c, h, cold, src, dst := nativeTransferFixture(t, "qbittorrent", "qbittorrent", op)
			ctx := context.Background()
			_, e := c.Transfer(ctx)
			if !errors.Is(e, ErrUnconfirmed) {
				t.Fatalf("lost %s not unknown: %v", op, e)
			}
			u, e := j.HasUnconfirmed(ctx, "owned-original", 1)
			if e != nil || !u {
				t.Fatal(u, e)
			}
			sw, sr, _, _, _ := src.counts()
			dw, dr, _, _, _ := dst.counts()
			h.Close()
			j.Close()
			j, c, h = cold()
			defer j.Close()
			defer h.Close()
			_, e = c.Transfer(ctx)
			if !errors.Is(e, ErrUnconfirmed) {
				t.Fatalf("cold %s unknown lost %v", op, e)
			}
			sw2, sr2, _, _, _ := src.counts()
			dw2, dr2, _, _, _ := dst.counts()
			if sw2 != sw || sr2 != sr || dw2 != dw || dr2 != dr {
				t.Fatal("unknown retry issued additional native IO")
			}
		})
	}
}

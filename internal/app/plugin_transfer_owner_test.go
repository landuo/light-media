package app

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"light-media/internal/download"
	transfer "light-media/internal/plugins/torrenttransfer"
)

type transferAppDaemon struct {
	mu                    sync.Mutex
	t                     *testing.T
	server                *httptest.Server
	source                bool
	kind, hash, tags, dir string
	category, sourceTags  string
	added, removed        bool
	calls                 map[string]int
	lost                  string
	checking, offline     bool
}

func newTransferAppDaemon(t *testing.T, kind, hash string, source bool) *transferAppDaemon {
	d := &transferAppDaemon{t: t, source: source, kind: kind, hash: hash, calls: map[string]int{}, dir: "/transfer/destination", category: "电影", sourceTags: "已整理,自有"}
	if source {
		d.dir = "/transfer/source"
	}
	d.server = httptest.NewServer(http.HandlerFunc(d.serve))
	t.Cleanup(d.server.Close)
	return d
}
func (d *transferAppDaemon) rows() []any {
	if d.removed || (!d.source && !d.added) {
		return []any{}
	}
	if d.kind == "qbittorrent" {
		state := "pausedUP"
		if d.checking {
			state = "checkingUP"
		}
		tags := d.tags
		if d.source {
			tags = d.sourceTags
		}
		return []any{map[string]any{"hash": d.hash, "save_path": d.dir, "category": d.category, "tags": tags, "state": state, "progress": 1, "amount_left": 0}}
	}
	status := 0
	if d.source {
		status = 5
	}
	if d.checking {
		status = 2
	}
	return []any{map[string]any{"hashString": d.hash, "downloadDir": d.dir, "labels": []string{"已整理", "自有"}, "status": status, "percentDone": 1, "totalSize": 1}}
}
func (d *transferAppDaemon) drop(w http.ResponseWriter, op string) bool {
	if d.lost != op {
		return false
	}
	d.lost = ""
	conn, _, e := w.(http.Hijacker).Hijack()
	if e == nil {
		conn.Close()
	}
	return true
}
func (d *transferAppDaemon) serve(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	respond := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
	d.calls[r.URL.Path]++
	if d.offline {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if d.kind == "qbittorrent" {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			d.t.Error("Bearer downloader must not fallback to password")
			w.WriteHeader(401)
		case "/api/v2/app/webapiVersion":
			if r.Header.Get("Authorization") != "Bearer transfer-owned-key" {
				d.t.Error("native APIKey not bound")
			}
			io.WriteString(w, "2.11.2")
		case "/api/v2/torrents/info":
			respond(d.rows())
		case "/api/v2/torrents/add":
			if e := r.ParseMultipartForm(16 << 20); e != nil {
				d.t.Error(e)
				return
			}
			if r.FormValue("paused") != "True" || r.FormValue("autoTMM") != "False" || r.FormValue("sequentialDownload") != "True" || r.FormValue("firstLastPiecePrio") != "True" {
				d.t.Errorf("actual original global/add flags %v", r.Form)
			}
			d.tags = r.FormValue("tags")
			d.dir = r.FormValue("savepath")
			d.added = true
			if d.drop(w, "add") {
				return
			}
			io.WriteString(w, "Ok.")
		case "/api/v2/torrents/removeTags":
			_ = r.ParseForm()
			tag := r.FormValue("tags")
			parts := []string{}
			for _, s := range strings.Split(d.tags, ",") {
				if s != tag {
					parts = append(parts, s)
				}
			}
			d.tags = strings.Join(parts, ",")
			if d.drop(w, "cleanup") {
				return
			}
		case "/api/v2/torrents/recheck", "/api/v2/torrents/start", "/api/v2/torrents/resume":
		case "/api/v2/torrents/delete":
			_ = r.ParseForm()
			if r.FormValue("deleteFiles") != "false" {
				d.t.Error("source data deletion forbidden")
			}
			d.removed = true
			if d.drop(w, "delete") {
				return
			}
		default:
			d.t.Errorf("unowned actual qB endpoint %s", r.URL.Path)
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
	d.calls[req.Method]++
	out := map[string]any{}
	switch req.Method {
	case "session_get":
		out["version"] = "4.1"
	case "torrent_get":
		out["torrents"] = d.rows()
	case "torrent_add":
		if req.Params["paused"] != true {
			d.t.Error("TR original paused add")
		}
		d.dir, _ = req.Params["download_dir"].(string)
		d.added = true
		out["torrent_added"] = map[string]any{"hash_string": d.hash}
		if d.drop(w, "add") {
			return
		}
	case "torrent_verify", "torrent_start":
	case "torrent_remove":
		if req.Params["delete_local_data"] != false {
			d.t.Error("TR source data deletion forbidden")
		}
		d.removed = true
		if d.drop(w, "delete") {
			return
		}
	default:
		d.t.Errorf("unowned actual TR method %s", req.Method)
		w.WriteHeader(400)
		return
	}
	respond(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": out})
}
func (d *transferAppDaemon) snapshot() map[string]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[string]int{}
	for k, v := range d.calls {
		out[k] = v
	}
	return out
}
func transferTestMaterial() (string, []byte) {
	info := "d6:lengthi1e4:name9:owned.bin12:piece lengthi1e6:pieces20:" + strings.Repeat("x", 20) + "e"
	hash := sha1.Sum([]byte(info))
	return hex.EncodeToString(hash[:]), []byte("d8:announce32:https://tracker.invalid/announce4:info" + info + "e")
}
func transferAppNext(t *testing.T, a *App) PluginRun {
	t.Helper()
	r, e := a.pluginStore.ClaimRun(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	a.executePluginRun(context.Background(), r)
	r, e = a.pluginStore.Run(context.Background(), r.ID)
	if e != nil {
		t.Fatal(e)
	}
	if r.State != "succeeded" {
		t.Fatalf("actual transfer App run %s/%s: %s %s", r.InstanceID, r.Action, r.State, r.ErrorCode)
	}
	return r
}
func transferAppConfig(t *testing.T, a *App, cookie *http.Cookie, id, backup string, once bool) {
	t.Helper()
	form := callTest(t, a.Handler(), "GET", "/api/plugins/"+id+"/form", "", cookie, "")
	var data struct {
		Data struct {
			Revision int64
			Model    map[string]any `json:"model"`
		}
	}
	if form.Code != 200 || json.Unmarshal(form.Body.Bytes(), &data) != nil || !strings.Contains(form.Body.String(), "源下载器种子文件路径") {
		t.Fatal(form.Code, form.Body.String())
	}
	cfg := map[string]any{"enabled": true, "onlyonce": once, "cron": "0 0 * * *", "fromdownloader": "source", "todownloader": "destination", "fromtorrentpath": backup, "frompath": "/transfer/source", "topath": "/transfer/destination", "autostart": true, "skipverify": false, "transferemptylabel": true, "add_torrent_tags": "已整理,转移做种", "deletesource": false, "deleteduplicate": false, "notify": true}
	body, _ := json.Marshal(map[string]any{"revision": data.Data.Revision, "config": cfg})
	w := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id+"/config", string(body), cookie, "")
	if w.Code != 200 {
		t.Fatal("actual native config", w.Code, w.Body.String())
	}
	transferAppNext(t, a)
}
func TestTransferActualAppSDKInstallConfigRunDualIdentity(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		t.Run(kind, func(t *testing.T) {
			a := newTestApp(t)
			useTransferSDKProcess(t, a)
			ctx := context.Background()
			cookie := setupTestAdmin(t, a)
			hash, raw := transferTestMaterial()
			src := newTransferAppDaemon(t, kind, hash, true)
			dst := newTransferAppDaemon(t, kind, hash, false)
			a.config = Settings{Downloaders: []DownloaderConfig{{Config: download.Config{ID: "owned-from", Kind: kind, BaseURL: src.server.URL, APIKey: map[bool]string{true: "transfer-owned-key"}[kind == "qbittorrent"], Sequential: true, FirstLast: true}, Name: "source"}, {Config: download.Config{ID: "owned-to", Kind: kind, BaseURL: dst.server.URL, APIKey: map[bool]string{true: "transfer-owned-key"}[kind == "qbittorrent"], Sequential: true, FirstLast: true}, Name: "destination"}}, AllowedRoots: []string{a.Dir}}
			if e := a.saveSettings(ctx, a.Settings()); e != nil {
				t.Fatal(e)
			}
			a.pluginTransfer.pollSleep = func(context.Context, time.Duration) error { return nil }
			backup := filepath.Join(a.Dir, "owned-backup")
			if e := os.Mkdir(backup, 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(backup, hash+".torrent"), raw, 0600); e != nil {
				t.Fatal(e)
			}
			for _, id := range []string{"TorrentTransfer", "TorrentTransfermusic"} {
				w := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"`+id+`"}`, cookie, "")
				if w.Code != 200 {
					t.Fatal("install", id, w.Code, w.Body.String())
				}
				transferAppNext(t, a)
				transferAppConfig(t, a, cookie, id, backup, false)
			}
			w := callTest(t, a.Handler(), "POST", "/api/plugins/services/run", `{"instance_id":"TorrentTransfer","service_id":"TorrentTransfer.TorrentTransfer"}`, cookie, "")
			if w.Code != 200 {
				t.Fatal("public run", w.Code, w.Body.String())
			}
			run := transferAppNext(t, a)
			if run.InstanceID != "TorrentTransfer" {
				t.Fatal(run.InstanceID)
			}
			value, e := a.pluginStore.Data(ctx, "TorrentTransfer", "source-"+hash)
			if e != nil || !strings.Contains(string(value.Value), hash) {
				t.Fatalf("source history %+v %v", value, e)
			}
			if _, e = a.pluginStore.Data(ctx, "TorrentTransfermusic", "source-"+hash); e == nil {
				t.Fatal("original history leaked to clone")
			}
			in, e := a.pluginStore.Installation(ctx, "TorrentTransfermusic")
			if e != nil {
				t.Fatal(e)
			}
			owner, e := a.transferOwner(ctx, in)
			if e != nil {
				t.Fatal(e)
			}
			cp, found, e := owner.journal.ReadCheckpoint(ctx, in.InstanceID, uint64(in.Generation))
			if e != nil || !found || len(cp.Core.Recheck) != 0 {
				t.Fatalf("clone state leaked %+v %v", cp, e)
			}
			records := callTest(t, a.Handler(), "GET", "/api/plugins/TorrentTransfer/runs", "", cookie, "")
			if records.Code != 200 || !strings.Contains(records.Body.String(), "succeeded") {
				t.Fatal(records.Body.String())
			}
			manifest := transfer.Manifest("TorrentTransfermusic")
			if manifest.PluginID != "TorrentTransfermusic" || manifest.Name != "自动转移做种 分身" {
				t.Fatal(manifest)
			}
		})
	}
}

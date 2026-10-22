package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"light-media/internal/download"
	transfer "light-media/internal/plugins/torrenttransfer"
)

type transferReady struct {
	a            *App
	cookie       *http.Cookie
	src, dst     *transferAppDaemon
	hash, backup string
}

func transferReadyApp(t *testing.T, kind string) transferReady {
	t.Helper()
	a := newTestApp(t)
	useTransferSDKProcess(t, a)
	cookie := setupTestAdmin(t, a)
	hash, raw := transferTestMaterial()
	src := newTransferAppDaemon(t, kind, hash, true)
	dst := newTransferAppDaemon(t, kind, hash, false)
	key := ""
	if kind == "qbittorrent" {
		key = "transfer-owned-key"
	}
	a.config = Settings{Downloaders: []DownloaderConfig{{Config: download.Config{ID: "owned-from", Kind: kind, BaseURL: src.server.URL, APIKey: key, Sequential: true, FirstLast: true}, Name: "source"}, {Config: download.Config{ID: "owned-to", Kind: kind, BaseURL: dst.server.URL, APIKey: key, Sequential: true, FirstLast: true}, Name: "destination"}}, AllowedRoots: []string{a.Dir}}
	if e := a.saveSettings(context.Background(), a.Settings()); e != nil {
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
	if w := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"TorrentTransfer"}`, cookie, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	transferAppNext(t, a)
	transferAppConfig(t, a, cookie, "TorrentTransfer", backup, false)
	return transferReady{a, cookie, src, dst, hash, backup}
}
func transferServiceRun(t *testing.T, f transferReady) PluginRun {
	t.Helper()
	w := callTest(t, f.a.Handler(), "POST", "/api/plugins/services/run", `{"instance_id":"TorrentTransfer","service_id":"TorrentTransfer.TorrentTransfer"}`, f.cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	r, e := f.a.pluginStore.ClaimRun(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	f.a.executePluginRun(context.Background(), r)
	r, e = f.a.pluginStore.Run(context.Background(), r.ID)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestTransferActualUnknownAdmissionAndColdRecovery(t *testing.T) {
	for _, tc := range []struct{ kind, op string }{{"qbittorrent", "add"}, {"transmission", "add"}, {"qbittorrent", "cleanup"}, {"qbittorrent", "delete"}} {
		t.Run(tc.kind+"/"+tc.op, func(t *testing.T) {
			f := transferReadyApp(t, tc.kind)
			ctx := context.Background()
			if tc.op == "delete" {
				stored, e := f.a.pluginStore.Config(ctx, "TorrentTransfer")
				if e != nil {
					t.Fatal(e)
				}
				var cfg map[string]any
				json.Unmarshal(stored.Value, &cfg)
				cfg["deletesource"] = true
				b, _ := json.Marshal(map[string]any{"revision": stored.Revision, "config": cfg})
				if w := callTest(t, f.a.Handler(), "PUT", "/api/plugins/TorrentTransfer/config", string(b), f.cookie, ""); w.Code != 200 {
					t.Fatal(w.Body.String())
				}
				transferAppNext(t, f.a)
				f.src.lost = "delete"
			} else {
				f.dst.lost = tc.op
			}
			r := transferServiceRun(t, f)
			if r.State != "reconcile_pending" {
				t.Fatal("unknown not preserved", r.State, r.ErrorCode)
			}
			beforeSource, beforeTarget := f.src.snapshot(), f.dst.snapshot()
			if _, e := f.a.pluginStore.EnqueueRun(ctx, "TorrentTransfer", "run", nil); pluginErrorCodeInternal(e) != "TORRENT_TRANSFER_UNCONFIRMED" {
				t.Fatalf("direct enqueue bypassed unknown %v", e)
			}
			if w := callTest(t, f.a.Handler(), "POST", "/api/plugins/services/run", `{"instance_id":"TorrentTransfer","service_id":"TorrentTransfer.TorrentTransfer"}`, f.cookie, ""); w.Code == 200 {
				t.Fatal("service bypassed unknown")
			}
			old, e := f.a.pluginStore.Run(ctx, r.ID)
			if e != nil {
				t.Fatal(e)
			}
			if e = f.a.Close(); e != nil {
				t.Fatal(e)
			}
			cold, e := New(Options{DataDir: f.a.Dir})
			if e != nil {
				t.Fatal(e)
			}
			defer cold.Close()
			recovered, e := cold.pluginStore.Run(ctx, r.ID)
			if e != nil || recovered.State != "reconcile_pending" {
				t.Fatal(recovered, e)
			}
			if recovered.ConfigRevision != old.ConfigRevision || !reflect.DeepEqual(recovered.Input, old.Input) {
				t.Fatal("cold recovery changed original identity")
			}
			if _, e = cold.pluginStore.EnqueueRun(ctx, "TorrentTransfer", "run", nil); pluginErrorCodeInternal(e) != "TORRENT_TRANSFER_UNCONFIRMED" {
				t.Fatalf("cold enqueue bypassed unknown %v", e)
			}
			if !reflect.DeepEqual(beforeSource, f.src.snapshot()) || !reflect.DeepEqual(beforeTarget, f.dst.snapshot()) {
				t.Fatal("warm/cold admission replayed actual native IO")
			}
		})
	}
}
func TestTransferActualSourceFailureCanBeCorrected(t *testing.T) {
	f := transferReadyApp(t, "qbittorrent")
	ctx := context.Background()
	stored, e := f.a.pluginStore.Config(ctx, "TorrentTransfer")
	if e != nil {
		t.Fatal(e)
	}
	var cfg map[string]any
	if e = json.Unmarshal(stored.Value, &cfg); e != nil {
		t.Fatal(e)
	}
	cfg["cron"] = "invalid source cron"
	b, _ := json.Marshal(map[string]any{"revision": stored.Revision, "config": cfg})
	w := callTest(t, f.a.Handler(), "PUT", "/api/plugins/TorrentTransfer/config", string(b), f.cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	r, e := f.a.pluginStore.ClaimRun(ctx)
	if e != nil {
		t.Fatal(e)
	}
	f.a.executePluginRun(ctx, r)
	r, e = f.a.pluginStore.Run(ctx, r.ID)
	if e != nil || r.State != "failed" || r.ErrorCode != transferSourceFailed {
		t.Fatal("local source error mislabeled unknown", r.State, r.ErrorCode, e)
	}
	owner, e := f.a.transferOwner(ctx, mustTransferInstallation(t, f.a, "TorrentTransfer"))
	if e != nil {
		t.Fatal(e)
	}
	u, e := owner.journal.HasUnconfirmed(ctx, "TorrentTransfer", 1)
	if e != nil || u {
		t.Fatal("known source failure damaged owner", u, e)
	}
	transferAppConfig(t, f.a, f.cookie, "TorrentTransfer", f.backup, false)
	run := transferServiceRun(t, f)
	if run.State != "succeeded" {
		t.Fatal("corrected config cannot execute", run.State, run.ErrorCode)
	}
}
func mustTransferInstallation(t *testing.T, a *App, id string) PluginInstallation {
	t.Helper()
	in, e := a.pluginStore.Installation(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	return in
}
func TestTransferActualOriginalImportRemainsUnclaimed(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	for _, id := range []string{"TorrentTransfer", "TorrentTransfermusic"} {
		migration, e := a.pluginStore.BeginMigration(ctx, strings.Repeat("b", 64), id, id+".original")
		if e != nil {
			t.Fatal(e)
		}
		m, _ := json.Marshal(transfer.Manifest(id))
		config := json.RawMessage(`{"enabled":true,"onlyonce":true,"fromdownloader":"source","todownloader":"destination","fromtorrentpath":"/owned-original-backup"}`)
		_, e = a.pluginStore.Import(ctx, PluginImport{RunID: migration.ID, Installation: PluginInstallation{InstanceID: id + ".original", PluginID: id, Version: "1.10.3", ManifestSchemaVersion: 1, Manifest: m}, Config: config, Data: map[string]json.RawMessage{"same-source-hash": json.RawMessage(`{"to_download":"destination","delete_source":false}`)}})
		if e != nil {
			t.Fatal("synthetic import", e)
		}
		in := mustTransferInstallation(t, a, id+".original")
		if in.DesiredEnabled {
			t.Fatal("import activated original once flag")
		}
		if _, _, e = a.pluginStore.SaveConfigAndQueue(ctx, in.InstanceID, 1, config); pluginErrorCodeInternal(e) != "TORRENT_TRANSFER_REFERENCES_UNCLAIMED" {
			t.Fatal("same names claimed native downloader", e)
		}
		if _, e = a.pluginStore.EnqueueRun(ctx, in.InstanceID, "run", nil); e == nil {
			t.Fatal("import allowed native execute", e)
		}
		value, e := a.pluginStore.Data(ctx, in.InstanceID, "same-source-hash")
		if e != nil || !strings.Contains(string(value.Value), "destination") {
			t.Fatal("original namespaced data lost", e)
		}
	}
}

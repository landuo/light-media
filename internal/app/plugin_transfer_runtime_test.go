package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"light-media/internal/download"
	transfer "light-media/internal/plugins/torrenttransfer"
	"light-media/internal/plugins/uninstall"
)

func transferChangeConfig(t *testing.T, f transferReady, id string, change func(map[string]any)) PluginRun {
	t.Helper()
	stored, e := f.a.pluginStore.Config(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	var cfg map[string]any
	if e = json.Unmarshal(stored.Value, &cfg); e != nil {
		t.Fatal(e)
	}
	change(cfg)
	raw, _ := json.Marshal(map[string]any{"revision": stored.Revision, "config": cfg})
	w := callTest(t, f.a.Handler(), "PUT", "/api/plugins/"+id+"/config", string(raw), f.cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return transferAppNext(t, f.a)
}
func transferInvokeService(t *testing.T, f transferReady, id, service string) PluginRun {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"instance_id": id, "service_id": id + "." + service})
	w := callTest(t, f.a.Handler(), "POST", "/api/plugins/services/run", string(raw), f.cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return transferAppNext(t, f.a)
}
func transferCheckpoint(t *testing.T, a *App, id string) transfer.RuntimeCheckpoint {
	t.Helper()
	in := mustTransferInstallation(t, a, id)
	owner, e := a.transferOwner(context.Background(), in)
	if e != nil {
		t.Fatal(e)
	}
	cp, found, e := owner.journal.ReadCheckpoint(context.Background(), id, uint64(in.Generation))
	if e != nil || !found {
		t.Fatal(found, e)
	}
	return cp
}
func transferCold(t *testing.T, f transferReady) transferReady {
	t.Helper()
	if e := f.a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: f.a.Dir})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cold.Close() })
	useBuiltInPluginProcess(t, cold)
	cold.pluginTransfer.pollSleep = func(context.Context, time.Duration) error { return nil }
	f.a = cold
	return f
}
func TestTransferActualOnceAndColdRecheck(t *testing.T) {
	for _, kind := range []string{"qbittorrent", "transmission"} {
		t.Run(kind, func(t *testing.T) {
			f := transferReadyApp(t, kind)
			ctx := context.Background()
			f.dst.checking = true
			transferChangeConfig(t, f, "TorrentTransfer", func(c map[string]any) { c["onlyonce"] = true })
			stored, _ := f.a.pluginStore.Config(ctx, "TorrentTransfer")
			var cfg map[string]any
			json.Unmarshal(stored.Value, &cfg)
			if cfg["onlyonce"] != false {
				t.Fatal("source once flag not cleared")
			}
			var due string
			if e := f.a.Store.DB.QueryRow("SELECT next_due FROM plugin_schedules WHERE instance_id='TorrentTransfer' AND service_id='TorrentTransfer.transfer'").Scan(&due); e != nil {
				t.Fatal(e)
			}
			at, e := time.Parse(time.RFC3339Nano, due)
			if e != nil {
				t.Fatal(e)
			}
			n, e := f.a.pluginStore.enqueueDuePluginSchedules(ctx, at)
			if e != nil || n != 1 {
				t.Fatal(n, e)
			}
			run := transferAppNext(t, f.a)
			if run.Action != "service" {
				t.Fatal(run.Action)
			}
			cp := transferCheckpoint(t, f.a, "TorrentTransfer")
			if !reflect.DeepEqual(cp.Core.Recheck["destination"], []string{f.hash}) || len(cp.AuthorityProofs) != 1 {
				t.Fatal("actual pending recheck not durable", cp.Core.Recheck, cp.AuthorityProofs)
			}
			if n, e = f.a.pluginStore.enqueueDuePluginSchedules(ctx, at); e != nil || n != 0 {
				t.Fatal("once replay", n, e)
			}
			f = transferCold(t, f)
			if n, e = f.a.pluginStore.enqueueDuePluginSchedules(ctx, at); e != nil || n != 0 {
				t.Fatal("cold once replay", n, e)
			}
			f.dst.mu.Lock()
			f.dst.checking = false
			f.dst.mu.Unlock()
			transferInvokeService(t, f, "TorrentTransfer", "check_recheck")
			cp = transferCheckpoint(t, f.a, "TorrentTransfer")
			if len(cp.Core.Recheck["destination"]) != 0 || len(cp.AuthorityProofs) != 0 {
				t.Fatal("cold source recheck did not drain", cp)
			}
			calls := f.dst.snapshot()
			start := calls["/api/v2/torrents/start"] + calls["/api/v2/torrents/resume"] + calls["torrent_start"]
			if start != 1 {
				t.Fatal("actual target start", calls)
			}
		})
	}
}
func TestTransferActualCloneRunsIndependentSameHistoryKey(t *testing.T) {
	f := transferReadyApp(t, "qbittorrent")
	ctx := context.Background()
	transferServiceRun(t, f)
	clone := newTransferAppDaemon(t, "qbittorrent", f.hash, false)
	cfg := f.a.Settings()
	cfg.Downloaders = append(cfg.Downloaders, DownloaderConfig{Config: download.Config{ID: "owned-clone", Kind: "qbittorrent", BaseURL: clone.server.URL, APIKey: "transfer-owned-key", Sequential: true, FirstLast: true}, Name: "clone destination"})
	if e := f.a.saveSettings(ctx, cfg); e != nil {
		t.Fatal(e)
	}
	if w := callTest(t, f.a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"TorrentTransfermusic"}`, f.cookie, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	transferAppNext(t, f.a)
	transferAppConfig(t, f.a, f.cookie, "TorrentTransfermusic", f.backup, false)
	transferChangeConfig(t, f, "TorrentTransfermusic", func(c map[string]any) { c["todownloader"] = "clone destination" })
	transferInvokeService(t, f, "TorrentTransfermusic", "TorrentTransfer")
	for id, want := range map[string]string{"TorrentTransfer": "destination", "TorrentTransfermusic": "clone destination"} {
		v, e := f.a.pluginStore.Data(ctx, id, "source-"+f.hash)
		var row map[string]any
		if e != nil || json.Unmarshal(v.Value, &row) != nil || row["to_download"] != want {
			t.Fatal(id, row, e)
		}
		cp := transferCheckpoint(t, f.a, id)
		if cp.Counts.Success != 1 {
			t.Fatal("clone source not executed", id, cp.Counts)
		}
	}
	if clone.snapshot()["/api/v2/torrents/add"] != 1 || f.dst.snapshot()["/api/v2/torrents/add"] != 1 {
		t.Fatal("dual instance actual add count")
	}
}
func TestTransferActualLifecycleAndMissingEvidence(t *testing.T) {
	for _, op := range []string{"reset", "uninstall", "bulk", "missing-root", "missing-journal", "missing-deferred"} {
		t.Run(op, func(t *testing.T) {
			f := transferReadyApp(t, "qbittorrent")
			ctx := context.Background()
			run := transferServiceRun(t, f)
			beforeSrc, beforeDst := f.src.snapshot(), f.dst.snapshot()
			switch op {
			case "missing-root":
				_, _ = f.a.Store.DB.Exec("DELETE FROM plugin_transfer_roots WHERE instance_id='TorrentTransfer'")
			case "missing-journal":
				_, _ = f.a.Store.DB.Exec("DELETE FROM torrenttransfer_registry")
			case "missing-deferred":
				_, _ = f.a.Store.DB.Exec("DELETE FROM plugin_deferred_operations WHERE run_id=?", run.ID)
			case "bulk":
				r := uninstallInstall(t, f.a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"TorrentTransfer"}}, true)
				result := uninstallApply(t, f.a, &r)
				if !reflect.DeepEqual(result.Removed, []string{"TorrentTransfer"}) {
					t.Fatal(result)
				}
			default:
				w := callTest(t, f.a.Handler(), "POST", "/api/plugins/TorrentTransfer/"+op, `{}`, f.cookie, "")
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			if strings.HasPrefix(op, "missing-") {
				if _, e := f.a.pluginStore.EnqueueRun(ctx, "TorrentTransfer", "run", nil); e == nil {
					t.Fatal("missing evidence admitted queue")
				}
				if w := callTest(t, f.a.Handler(), "POST", "/api/plugins/TorrentTransfer/reset", `{}`, f.cookie, ""); w.Code == 200 {
					t.Fatal("missing evidence minted generation")
				}
			} else {
				in := mustTransferInstallation(t, f.a, "TorrentTransfer")
				if in.Generation != 2 || in.DesiredEnabled || in.Installed != (op == "reset") {
					t.Fatal(in)
				}
				if op != "reset" {
					if _, e := f.a.pluginStore.Data(ctx, "TorrentTransfer", "source-"+f.hash); e != nil {
						t.Fatal("uninstall lost retained original history", e)
					}
				}
			}
			if !reflect.DeepEqual(beforeSrc, f.src.snapshot()) || !reflect.DeepEqual(beforeDst, f.dst.snapshot()) {
				t.Fatal("lifecycle dispatched native IO")
			}
		})
	}
}
func TestTransferActualInactiveAndLostHostReply(t *testing.T) {
	for _, mode := range []string{"inactive", "reply-loss"} {
		t.Run(mode, func(t *testing.T) {
			f := transferReadyApp(t, "qbittorrent")
			if mode == "inactive" {
				f.src.offline = true
			} else {
				f.a.pluginTransfer.afterCheckpoint = func() error { return errors.New("owned lost host reply") }
			}
			r := transferServiceRun(t, f)
			if r.State != "succeeded" {
				t.Fatal(r.State, r.ErrorCode)
			}
			cp := transferCheckpoint(t, f.a, "TorrentTransfer")
			if mode == "inactive" {
				if f.dst.snapshot()["/api/v2/torrents/add"] != 0 {
					t.Fatal("inactive source dispatched")
				}
			} else if cp.Counts.Success != 1 {
				t.Fatal(cp.Counts)
			}
			beforeS, beforeD := f.src.snapshot(), f.dst.snapshot()
			f = transferCold(t, f)
			recovered, e := f.a.pluginStore.Run(context.Background(), r.ID)
			if e != nil || recovered.State != "succeeded" {
				t.Fatal(recovered, e)
			}
			if !reflect.DeepEqual(beforeS, f.src.snapshot()) || !reflect.DeepEqual(beforeD, f.dst.snapshot()) {
				t.Fatal("confirmed reply-loss cold replay")
			}
		})
	}
}

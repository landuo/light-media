package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"light-media/internal/download"
	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	transfer "light-media/internal/plugins/torrenttransfer"
)

// Uses the same real subprocess SDK boundary as executePluginRun, holding the
// prepared admission so the later owner-start window can be exercised directly.
func prepareTransferSDK(t *testing.T, a *App, r PluginRun) *pluginDeferredCall {
	t.Helper()
	ctx := context.Background()
	in := mustTransferInstallation(t, a, r.InstanceID)
	d, e := a.preparePluginDeferred(ctx, in, r)
	if e != nil {
		t.Fatal(e)
	}
	manifest, _, ok := builtin.Lookup(in.PluginID)
	if !ok {
		t.Fatal("missing actual compiled plugin")
	}
	command, e := a.pluginExecutable(in.PluginID)
	if e != nil {
		t.Fatal(e)
	}
	worker, e := (plugins.Runner{Timeout: 20 * time.Second}).Run(ctx, command, manifest, in.InstanceID, "invoke", r.Input, d.handshake)
	if e != nil {
		t.Fatal(e)
	}
	if e = d.ready(worker); e != nil {
		t.Fatal(e)
	}
	if policy := pluginInvocationPolicy(in, r, d); !policy.Lifecycle || !policy.ExcludeHostTime {
		t.Fatal("native source round incorrectly bounded by worker budget", policy)
	}
	return d
}
func TestTransferActualPrequeuedClaimedPreparedUnknown(t *testing.T) {
	for _, stage := range []string{"pending", "claimed", "prepared"} {
		t.Run(stage, func(t *testing.T) {
			f := transferReadyApp(t, "qbittorrent")
			ctx := context.Background()
			for i := 0; i < 2; i++ {
				if _, e := f.a.pluginStore.EnqueueRun(ctx, "TorrentTransfer", "run", nil); e != nil {
					t.Fatal(e)
				}
			}
			first, e := f.a.pluginStore.ClaimRun(ctx)
			if e != nil {
				t.Fatal(e)
			}
			var second PluginRun
			var held *pluginDeferredCall
			if stage != "pending" {
				second, e = f.a.pluginStore.ClaimRun(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if stage == "prepared" {
					held = prepareTransferSDK(t, f.a, second)
				}
			}
			f.dst.lost = "add"
			f.a.executePluginRun(ctx, first)
			unknown, e := f.a.pluginStore.Run(ctx, first.ID)
			if e != nil || unknown.State != "reconcile_pending" {
				t.Fatal(unknown, e)
			}
			beforeS, beforeD := f.src.snapshot(), f.dst.snapshot()
			switch stage {
			case "pending":
				if _, e = f.a.pluginStore.ClaimRun(ctx); !errors.Is(e, sql.ErrNoRows) {
					t.Fatal("old pending crossed owner unknown", e)
				}
			case "claimed":
				_, e = f.a.preparePluginDeferred(ctx, mustTransferInstallation(t, f.a, "TorrentTransfer"), second)
				if !errors.Is(e, transfer.ErrUnconfirmed) {
					t.Fatal("old claimed crossed owner unknown", e)
				}
				if _, _, e = f.a.pluginStore.readDeferred(ctx, f.a.Store.DB, second); !errors.Is(e, sql.ErrNoRows) {
					t.Fatal("blocked preparation created receipt", e)
				}
			case "prepared":
				_, e = held.execute(ctx, &second)
				if !errors.Is(e, transfer.ErrUnconfirmed) {
					t.Fatal("old prepared crossed owner unknown", e)
				}
				record, _, e := f.a.pluginStore.readDeferred(ctx, f.a.Store.DB, second)
				if e != nil || record.Phase != "prepared" {
					t.Fatal(record, e)
				}
			}
			if !reflect.DeepEqual(beforeS, f.src.snapshot()) || !reflect.DeepEqual(beforeD, f.dst.snapshot()) {
				t.Fatal("blocked old run dispatched IO")
			}
			if stage == "pending" {
				iyuuAssertUnknownDispatchDoesNotStarve(t, f.a, f.cookie, unknown)
			}
		})
	}
}
func TestTransferActualRemovedDownloaderKeepsOldRecheckProof(t *testing.T) {
	f := transferReadyApp(t, "qbittorrent")
	ctx := context.Background()
	f.dst.checking = true
	transferServiceRun(t, f)
	initial := transferCheckpoint(t, f.a, "TorrentTransfer")
	if len(initial.AuthorityProofs) != 1 {
		t.Fatal(initial)
	}
	original := f.a.Settings()
	changed := original
	changed.Downloaders = append([]DownloaderConfig(nil), original.Downloaders[:1]...)
	if e := f.a.saveSettings(ctx, changed); e != nil {
		t.Fatal(e)
	}
	transferInvokeService(t, f, "TorrentTransfer", "check_recheck")
	cp := transferCheckpoint(t, f.a, "TorrentTransfer")
	if !reflect.DeepEqual(initial.Core.Recheck, cp.Core.Recheck) || !reflect.DeepEqual(initial.AuthorityProofs, cp.AuthorityProofs) {
		t.Fatal("removed downloader erased source queue or private authority")
	}
	f = transferCold(t, f)
	if e := f.a.saveSettings(ctx, original); e != nil {
		t.Fatal(e)
	}
	f.dst.mu.Lock()
	f.dst.checking = false
	f.dst.mu.Unlock()
	transferInvokeService(t, f, "TorrentTransfer", "check_recheck")
	if len(transferCheckpoint(t, f.a, "TorrentTransfer").AuthorityProofs) != 0 {
		t.Fatal("restored exact authorized downloader could not resume")
	}
}
func TestTransferActualMixedDownloaders(t *testing.T) {
	for _, pair := range [][2]string{{"qbittorrent", "transmission"}, {"transmission", "qbittorrent"}} {
		t.Run(strings.Join(pair[:], "-"), func(t *testing.T) {
			f := transferReadyApp(t, pair[0])
			target := newTransferAppDaemon(t, pair[1], f.hash, false)
			cfg := f.a.Settings()
			key := ""
			if pair[1] == "qbittorrent" {
				key = "transfer-owned-key"
			}
			cfg.Downloaders[1].Config = download.Config{ID: "owned-to", Kind: pair[1], BaseURL: target.server.URL, APIKey: key, Sequential: true, FirstLast: true}
			if e := f.a.saveSettings(context.Background(), cfg); e != nil {
				t.Fatal(e)
			}
			r := transferServiceRun(t, f)
			if r.State != "succeeded" {
				t.Fatal(r)
			}
			cp := transferCheckpoint(t, f.a, "TorrentTransfer")
			if cp.Counts.Success != 1 {
				t.Fatal(cp.Counts)
			}
			calls := target.snapshot()
			if calls["/api/v2/torrents/add"]+calls["torrent_add"] != 1 {
				t.Fatal(calls)
			}
		})
	}
}
func TestTransferActualPrivateNoticeAndLogOwnership(t *testing.T) {
	f := transferReadyApp(t, "qbittorrent")
	ctx := context.Background()
	r := transferServiceRun(t, f)
	rows, e := f.a.Store.DB.Query("SELECT token,sealed FROM plugin_transfer_notices WHERE instance_id='TorrentTransfer'")
	if e != nil {
		t.Fatal(e)
	}
	var boxes []transferNotice
	for rows.Next() {
		var token string
		var sealed []byte
		if e = rows.Scan(&token, &sealed); e != nil {
			t.Fatal(e)
		}
		raw, e := f.a.pluginStore.unseal("run_result", "TorrentTransfer", "transfer-notice:"+token, sealed)
		if e != nil {
			t.Fatal(e)
		}
		var box transferNotice
		if e = json.Unmarshal(raw, &box); e != nil {
			t.Fatal(e)
		}
		boxes = append(boxes, box)
	}
	e = rows.Err()
	rows.Close()
	if e != nil || len(boxes) != 1 {
		t.Fatal(boxes, e)
	}
	box := boxes[0]
	if box.Version != 2 || box.Identity.Run != r.ID || box.Timezone == "" || box.HistoryID == "" {
		t.Fatal(box)
	}
	route, _, e := f.a.pluginStore.readNoticeRoute(ctx, f.a.Store.DB, box.RouteID)
	if e != nil || route.Kind != "transfer" {
		t.Fatal("real accepted source notice cannot route", route, e)
	}
	if !strings.Contains(string(box.Payload), "总数：1，成功：1") || !strings.Contains(string(box.Payload), "转移做种任务执行完成") {
		t.Fatal(string(box.Payload))
	}
	flushTestLogs(t, f.a)
	response := callTest(t, f.a.Handler(), "GET", "/api/plugins/TorrentTransfer/logs?limit=500", "", f.cookie, "")
	var result struct {
		Data struct {
			History logPage `json:"history"`
		} `json:"data"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
		t.Fatal(response.Code, response.Body.String())
	}
	logs := result.Data.History.Entries
	owned := 0
	for _, entry := range logs {
		body, _ := json.Marshal(entry)
		if strings.Contains(string(body), "transfer-owned-key") {
			t.Fatal("downloader secret leaked in actual log")
		}
		if strings.Contains(string(body), "torrent_transfer.source.") {
			owned++
			if !strings.Contains(string(body), r.ID) || !strings.Contains(string(body), `"module":"plugins"`) {
				t.Fatal("source log lost actual run ownership", string(body))
			}
		}
	}
	if owned == 0 {
		t.Fatal("source branch logs not persisted")
	}
	beforeS, beforeD := f.src.snapshot(), f.dst.snapshot()
	f = transferCold(t, f)
	if _, _, e = f.a.pluginStore.readNoticeRoute(ctx, f.a.Store.DB, box.RouteID); e != nil {
		t.Fatal("cold source notice verifier", e)
	}
	if !reflect.DeepEqual(beforeS, f.src.snapshot()) || !reflect.DeepEqual(beforeD, f.dst.snapshot()) {
		t.Fatal("notice cold read dispatched native IO")
	}
}

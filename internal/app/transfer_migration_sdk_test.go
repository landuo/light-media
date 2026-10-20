package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"light-media/internal/download"
)

func reviewedTransferSelectedCLI(t *testing.T, source reviewedTransferSnapshot, raw []byte) {
	t.Helper()
	binary := os.Getenv("LIGHT_MEDIA_REVIEWED_TRANSFER_CLI")
	if !filepath.IsAbs(binary) {
		t.Fatal("owned transfer migration CLI required")
	}
	s, _, _ := reviewedTransferOwned(t, source)
	dir := filepath.Dir(s.lock.Name())
	invoke := func(input []byte, want bool, args ...string) map[string]any {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Stdin = bytes.NewReader(input)
		output, e := cmd.CombinedOutput()
		if (e == nil) != want {
			t.Fatal("owned transfer CLI status mismatch", e)
		}
		if !want {
			return nil
		}
		var result map[string]any
		if json.Unmarshal(output, &result) != nil {
			t.Fatal("CLI response not bounded JSON")
		}
		return result
	}
	invoke(raw, false, "--target-data-dir", dir)
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	before := reviewedIYUUFileSnapshot(t, dir)
	only := invoke(raw, true, "--validate")
	if only["state"] != "validated" || only["writes"] != float64(0) || only["plugin"] != source.PluginID {
		t.Fatal("source-only CLI validation changed scope")
	}
	dry := invoke(raw, true, "--target-data-dir", dir)
	if dry["state"] != "ready" || dry["writes"] != float64(0) {
		t.Fatal("read-only target check failed")
	}
	if !bytes.Equal(reviewedIYUUJSON(before), reviewedIYUUJSON(reviewedIYUUFileSnapshot(t, dir))) {
		t.Fatal("read-only CLI changed target bytes or permissions")
	}
	applied := invoke(raw, true, "--target-data-dir", dir, "--apply")
	if applied["state"] != "imported" || applied["plugin"] != source.PluginID || applied["desired_enabled"] != false || applied["source_history_rows"] != float64(len(source.Data)) || applied["target_history_rows"] != float64(len(source.Data)) || applied["downloader_reference_mappings"] != float64(2) {
		t.Fatal("CLI did not retain original instance/history")
	}
	replay := invoke(raw, true, "--target-data-dir", dir, "--apply")
	if replay["replayed"] != true || !bytes.Equal(reviewedIYUUJSON(replay["receipt"]), reviewedIYUUJSON(applied["receipt"])) {
		t.Fatal("CLI replay replaced receipt")
	}
	before = reviewedIYUUFileSnapshot(t, dir)
	var invalid map[string]json.RawMessage
	if json.Unmarshal(raw, &invalid) != nil {
		t.Fatal("owned input invalid")
	}
	invalid["config"] = json.RawMessage(`"{}"`)
	invoke(reviewedIYUUJSON(invalid), false, "--target-data-dir", dir, "--apply")
	if !bytes.Equal(reviewedIYUUJSON(before), reviewedIYUUJSON(reviewedIYUUFileSnapshot(t, dir))) {
		t.Fatal("rejected source changed target")
	}
	cold, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	p, e := NewPluginStore(context.Background(), cold)
	if e != nil {
		t.Fatal(e)
	}
	proof, e := p.readReviewedTransferProof(context.Background(), cold.DB, source.PluginID)
	if e != nil || proof.Binding.PluginID != source.PluginID {
		t.Fatal("CLI cold binding missing", e)
	}
}

func reviewedTransferSelectedSDK(t *testing.T, source reviewedTransferSnapshot, raw []byte, lostACK bool) {
	t.Helper()
	ctx := context.Background()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	hash, material := transferTestMaterial()
	src := newTransferAppDaemon(t, "qbittorrent", hash, true)
	dst := newTransferAppDaemon(t, "transmission", hash, false)
	cfg, e := transferSourceMap([]byte(source.Config))
	if e != nil {
		t.Fatal(e)
	}
	// Preserve actual selection rules. Only owned daemon/path/schedule inputs
	// replace operational connections before this imported generation runs.
	src.sourceTags = "owned-transfer-ready"
	if v, _ := cfg["includelabels"].(string); v != "" {
		src.sourceTags = v
	}
	if v, _ := cfg["includecategory"].(string); v != "" {
		src.category = strings.Split(v, ",")[0]
	}
	owned := Settings{Downloaders: []DownloaderConfig{{Config: download.Config{ID: "owned-from", Kind: "qbittorrent", BaseURL: src.server.URL, APIKey: "transfer-owned-key"}, Name: "Owned current source"}, {Config: download.Config{ID: "owned-to", Kind: "transmission", BaseURL: dst.server.URL}, Name: "Owned current target"}}, AllowedRoots: []string{a.Dir}}
	if e = a.saveSettings(ctx, owned); e != nil {
		t.Fatal(e)
	}
	beforeImport := owned
	beforeImport.Downloaders = append([]DownloaderConfig(nil), owned.Downloaders...)
	for i, sourceDownloader := range source.Downloaders {
		endpoint, e := reviewedIYUUDownloaderEndpoint(sourceDownloader.Kind, sourceDownloader.Host, true)
		if e != nil {
			t.Fatal(e)
		}
		d := &beforeImport.Downloaders[i]
		d.BaseURL, d.Username, d.Password, d.APIKey = endpoint, sourceDownloader.Username, sourceDownloader.Password, ""
	}
	reviewedIYUUOwnedSettings(t, a.Store, beforeImport)
	m := reviewedTransferRun(t, a.pluginStore, raw, source.PluginID)
	if _, e = a.pluginStore.ImportReviewedTorrentTransfer(ctx, m.ID, raw); e != nil {
		t.Fatal("actual source SDK import failed", e)
	}
	// Every operational connection is owned before form, configuration or SDK IO.
	reviewedIYUUOwnedSettings(t, a.Store, owned)
	a.cfgMu.Lock()
	a.config = owned
	a.cfgMu.Unlock()
	a.pluginTransfer.pollSleep = func(context.Context, time.Duration) error { return nil }
	for key, value := range source.Data {
		stored, e := a.pluginStore.Data(ctx, source.PluginID, key)
		if e != nil || stored.Revision != 1 || !bytes.Equal(stored.Value, []byte(value)) {
			t.Fatal("pre-execution original history changed", e)
		}
	}
	form := callTest(t, a.Handler(), "GET", "/api/plugins/"+source.PluginID+"/form", "", cookie, "")
	if form.Code != 200 || !strings.Contains(form.Body.String(), `"title":"Owned current source"`) || !strings.Contains(form.Body.String(), `"value":"`+source.Downloaders[0].Name+`"`) {
		t.Fatal("actual original form lost renamed source selection", form.Code)
	}
	backup := filepath.Join(a.Dir, "owned-reviewed-backup")
	if e = os.Mkdir(backup, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(backup, hash+".torrent"), material, 0600); e != nil {
		t.Fatal(e)
	}
	cfg["fromtorrentpath"], cfg["frompath"], cfg["topath"], cfg["cron"] = backup, "/transfer/source", "/transfer/destination", "0 0 1 1 *"
	stored, e := a.pluginStore.Config(ctx, source.PluginID)
	if e != nil || !bytes.Equal(stored.Value, []byte(source.Config)) {
		t.Fatal("pre-execution original config changed", e)
	}
	body := reviewedIYUUJSON(map[string]any{"revision": stored.Revision, "config": cfg})
	w := callTest(t, a.Handler(), "PUT", "/api/plugins/"+source.PluginID+"/config", string(body), cookie, "")
	if w.Code != 200 {
		t.Fatal("reviewed config activation failed", w.Code)
	}
	transferAppNext(t, a)
	if lostACK {
		dst.lost = "add"
	}
	w = callTest(t, a.Handler(), "POST", "/api/plugins/services/run", string(reviewedIYUUJSON(map[string]string{"instance_id": source.PluginID, "service_id": source.PluginID + ".TorrentTransfer"})), cookie, "")
	if w.Code != 200 {
		t.Fatal("actual source public service invocation failed", w.Code)
	}
	run, e := a.pluginStore.ClaimRun(ctx)
	if e != nil || run.InstanceID != source.PluginID || run.Generation != 1 {
		t.Fatal("imported instance replaced before execution", e)
	}
	a.executePluginRun(ctx, run)
	run, e = a.pluginStore.Run(ctx, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	key := source.Downloaders[0].Name + "-" + hash
	if _, exists := source.Data[key]; exists {
		t.Fatal("owned synthetic hash collides with source history")
	}
	if lostACK {
		if run.State != "reconcile_pending" || dst.snapshot()["torrent_add"] != 1 || src.snapshot()["/api/v2/torrents/delete"] != 0 {
			t.Fatal("lost add acknowledgement was replayed or source removed", run.State)
		}
		if _, e = a.pluginStore.Data(ctx, source.PluginID, key); e == nil {
			t.Fatal("unconfirmed transfer wrote successful history")
		}
	} else {
		if run.State != "succeeded" || dst.snapshot()["torrent_add"] != 1 || src.snapshot()["/api/v2/torrents/delete"] != 1 {
			t.Fatal("actual configured transfer did not complete exactly once", run.State, run.ErrorCode)
		}
		value, e := a.pluginStore.Data(ctx, source.PluginID, key)
		if e != nil {
			t.Fatal("original-name history not saved", e)
		}
		var history map[string]any
		if json.Unmarshal(value.Value, &history) != nil || history["to_download"] != source.Downloaders[1].Name || history["to_download_id"] != hash || history["delete_source"] != true {
			t.Fatal("history changed original downloader meaning")
		}
	}
	beforeSource, beforeTarget := src.snapshot(), dst.snapshot()
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: a.Dir})
	if e != nil {
		t.Fatal("cold reviewed source bootstrap", e)
	}
	defer cold.Close()
	in, e := cold.pluginStore.Installation(ctx, source.PluginID)
	if e != nil || in.Generation != 1 {
		t.Fatal("cold source identity changed", e)
	}
	if _, e = cold.pluginStore.readReviewedTransferProof(ctx, cold.Store.DB, source.PluginID); e != nil {
		t.Fatal("cold source binding missing", e)
	}
	for key, value := range source.Data {
		actual, e := cold.pluginStore.Data(ctx, source.PluginID, key)
		if e != nil || actual.Revision != 1 || !bytes.Equal(actual.Value, []byte(value)) {
			t.Fatal("execution or cold bootstrap changed original historical receipt", e)
		}
	}
	if lostACK {
		recovered, e := cold.pluginStore.Run(ctx, run.ID)
		if e != nil || recovered.State != "reconcile_pending" || !bytes.Equal(recovered.Input, run.Input) {
			t.Fatal("cold unknown run lost original execution identity", e)
		}
		if _, e = cold.pluginStore.EnqueueRun(ctx, source.PluginID, "run", nil); pluginErrorCodeInternal(e) != "TORRENT_TRANSFER_UNCONFIRMED" {
			t.Fatal("cold unknown source permitted another transfer", e)
		}
	}
	if !reflect.DeepEqual(beforeSource, src.snapshot()) || !reflect.DeepEqual(beforeTarget, dst.snapshot()) {
		t.Fatal("cold source bootstrap replayed downloader IO")
	}
}

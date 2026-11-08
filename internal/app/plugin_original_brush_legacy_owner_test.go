package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

func TestBrushFlowLegacyOwnershipThroughHostDaemon(t *testing.T) {
	a := newTestApp(t)
	ctx := t.Context()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const taskID = "legacy-task-owned"
	valid, globalOnly, duplicate := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	wrongMarker := strings.Repeat("d", 40)
	var mu sync.Mutex
	active := map[string]string{
		valid:       "已整理, 刷流, " + originalBrushTaskTag(taskID),
		globalOnly:  "已整理, 刷流",
		duplicate:   "已整理, 刷流, " + originalBrushTaskTag(taskID),
		wrongMarker: "已整理, 刷流, 刷流-foreign",
	}
	fileName := func(hash string) string { return hash[:1] + ".mkv" }
	for hash := range active {
		if err := os.WriteFile(filepath.Join(root, fileName(hash)), []byte("owned"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var reannounced, deleted []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		selected := r.FormValue("hashes")
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "fixture-only", Path: "/"})
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v5.0.5")
		case "/api/v2/torrents/info":
			rows := []map[string]any{}
			for hash, tags := range active {
				if selected != "" && !strings.Contains(selected, hash) {
					continue
				}
				row := workflowTask(hash, 1)
				row["save_path"], row["tags"] = root, tags
				rows = append(rows, row)
			}
			workflowJSON(w, rows)
		case "/api/v2/torrents/files":
			hash := r.FormValue("hash")
			if _, ok := active[hash]; !ok {
				http.NotFound(w, r)
				return
			}
			workflowJSON(w, []map[string]any{{"name": fileName(hash), "size": 5, "progress": 1}})
		case "/api/v2/torrents/reannounce":
			reannounced = append(reannounced, selected)
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/torrents/delete":
			deleted = append(deleted, selected)
			if selected != valid || r.FormValue("deleteFiles") != "true" {
				t.Error("host dispatched an unproven deletion")
				http.Error(w, "owned scope rejected", http.StatusForbidden)
				return
			}
			if err := os.Remove(filepath.Join(root, fileName(selected))); err != nil {
				t.Error(err)
			}
			delete(active, selected)
			_, _ = io.WriteString(w, "Ok.")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(peer.Close)
	settings := a.Settings()
	downloader := DownloaderConfig{Config: download.Config{ID: "owned-legacy-brush", Kind: "qbittorrent", BaseURL: peer.URL}, Name: "Owned legacy qB"}
	settings.Downloaders = []DownloaderConfig{downloader}
	settings.AllowedRoots = []string{root}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	install := func(instance string, hashes ...string) PluginInstallation {
		t.Helper()
		meta := PluginInstallation{InstanceID: instance, PluginID: "BrushFlowLanduo", Version: "5.2.18", ManifestSchemaVersion: 1, Manifest: json.RawMessage(`{}`)}
		if err := a.pluginStore.Install(ctx, meta); err != nil {
			t.Fatal(err)
		}
		meta, err := a.pluginStore.Installation(ctx, instance)
		if err != nil {
			t.Fatal(err)
		}
		cfg := originalBrushStoredConfig{Enabled: true, Tasks: []originalBrushAddTask{{ID: taskID, SiteID: 301, Downloader: downloader.Name, Enabled: true}}}
		if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, meta, mustCleanSeedJSON(cfg)); err != nil {
			t.Fatal(err)
		}
		records := map[string]map[string]any{}
		for _, hash := range hashes {
			records[hash] = map[string]any{"task_id": taskID, "site": 301, "title": "owned source record", "deleted": false}
		}
		if err := a.pluginStore.originalBaseSaveData(ctx, instance, "task."+taskID+".torrents", mustCleanSeedJSON(records)); err != nil {
			t.Fatal(err)
		}
		return meta
	}
	meta := install("BrushFlowLanduo", valid, globalOnly, duplicate, wrongMarker)
	install("BrushFlowLanduoCopy", duplicate)
	object := &originalPluginInstance{app: a, installation: meta, hostContract: pluginResourcesSDKContract,
		manifest: plugins.Manifest{Capabilities: []string{plugins.DownloaderDeleteFilesCapability, plugins.DownloaderSourceControlCapability}}}
	lease, callCtx, err := object.reserveOriginalSubmission()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { object.releaseOriginalSubmission(lease); a.wg.Done() })
	host, closeScope := object.originalBaseSessionHost(callCtx, "check", nil)
	t.Cleanup(closeScope)
	identity := plugins.Identity{PluginID: meta.PluginID, InstanceID: meta.InstanceID, Version: meta.Version}
	deleteOne := func(hash string) (json.RawMessage, error) {
		return host(callCtx, identity, plugins.DownloaderDeleteFilesCapability, mustCleanSeedJSON(plugins.DownloaderDeleteFilesRequestV1{
			DownloaderID: downloader.ID, Hashes: []string{hash}, BrushOwnership: true,
		}))
	}
	for _, hash := range []string{globalOnly, wrongMarker, duplicate} {
		if _, err := deleteOne(hash); pluginErrorCode(err) != "DOWNLOAD_NOT_MANAGED" {
			t.Fatalf("unproven source selection was not rejected: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, fileName(hash))); err != nil {
			t.Fatal("unproven source file changed", err)
		}
	}
	control := plugins.DownloaderControlRequestV1{DownloaderID: downloader.ID, Hashes: []string{valid}, Action: "reannounce"}
	raw, err := host(callCtx, identity, plugins.DownloaderSourceControlCapability, mustCleanSeedJSON(control))
	var outcome plugins.DownloaderSourceMutationResultV1
	if err != nil || json.Unmarshal(raw, &outcome) != nil || !outcome.Accepted {
		t.Fatal("proven legacy task did not reach source reannounce", err)
	}
	scope := originalBaseCallScope{installation: meta}
	owner, err := a.originalBrushReadOwner(ctx, scope, downloader, valid)
	if err != nil || owner.TaskID != taskID || owner.AdoptedPath != root {
		t.Fatal("independent daemon and source evidence did not persist the adopted identity", err)
	}
	startHost, closeStart := object.originalBaseSessionHost(callCtx, "brush", nil)
	control.Action = "start"
	_, err = startHost(callCtx, identity, plugins.DownloaderSourceControlCapability, mustCleanSeedJSON(control))
	closeStart()
	if pluginErrorCode(err) != "DOWNLOAD_NOT_MANAGED" {
		t.Fatal("imported legacy record was allowed to start a torrent", err)
	}
	raw, err = deleteOne(valid)
	var result plugins.DownloaderDeleteFilesV1
	if err != nil || json.Unmarshal(raw, &result) != nil || !result.DaemonApplied || !result.BookkeepingCompleted {
		t.Fatal("proven legacy task did not reach protected daemon deletion", err)
	}
	if _, err := os.Stat(filepath.Join(root, fileName(valid))); !os.IsNotExist(err) {
		t.Fatal("owned daemon did not remove the selected file", err)
	}
	owner, err = a.originalBrushReadOwner(ctx, scope, downloader, valid)
	if err != nil || owner.TaskID != "" {
		t.Fatal("confirmed deletion did not retire the adopted ownership", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(reannounced, []string{valid}) || !slices.Equal(deleted, []string{valid}) {
		t.Fatal("isolated daemon observed unexpected effects", len(reannounced), len(deleted))
	}
}

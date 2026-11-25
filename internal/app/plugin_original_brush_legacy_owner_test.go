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
	const unusedTaskID = "unused-task-owned"
	valid, globalOnly, duplicate := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	wrongMarker := strings.Repeat("d", 40)
	second, disconnected := strings.Repeat("e", 40), strings.Repeat("f", 40)
	const remote = "/__brush_daemon_fixture__/downloads"
	var mu sync.Mutex
	directories := map[string]string{second: remote}
	changeDirectory := func(hash, dir string) {
		mu.Lock()
		defer mu.Unlock()
		directories[hash] = dir
	}
	active := map[string]string{
		valid:        "已整理, 刷流, " + originalBrushTaskTag(taskID),
		second:       "已整理, 刷流, " + originalBrushTaskTag(taskID),
		disconnected: "已整理, 刷流, " + originalBrushTaskTag(taskID),
		globalOnly:   "已整理, 刷流",
		duplicate:    "已整理, 刷流, " + originalBrushTaskTag(taskID),
		wrongMarker:  "已整理, 刷流, 刷流-foreign",
	}
	fileName := func(hash string) string { return hash[:1] + ".mkv" }
	for hash := range active {
		if err := os.WriteFile(filepath.Join(root, fileName(hash)), []byte("owned"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var reannounced, paused, deleted, reads, deletedTags []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		selected := r.FormValue("hashes")
		if r.URL.Path != "/api/v2/auth/login" {
			if cookie, err := r.Cookie("QBT_SID_19080"); err != nil || cookie.Value != "fixture-only" {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
		}
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "QBT_SID_19080", Value: "fixture-only", Path: "/"})
			w.WriteHeader(http.StatusNoContent)
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v5.0.5")
		case "/api/v2/torrents/info":
			if selected == "" {
				t.Error("brush ownership read enumerated the daemon")
			}
			reads = append(reads, selected)
			rows := []map[string]any{}
			for hash, tags := range active {
				if selected != "" && !strings.Contains(selected, hash) {
					continue
				}
				row := workflowTask(hash, 1)
				row["save_path"], row["tags"] = root, tags
				if dir, ok := directories[hash]; ok {
					row["save_path"] = dir
				}
				rows = append(rows, row)
			}
			workflowJSON(w, rows)
		case "/api/v2/app/webapiVersion":
			_, _ = io.WriteString(w, "2.11.0")
		case "/api/v2/torrents/stop":
			paused = append(paused, selected)
		case "/api/v2/torrents/deleteTags":
			deletedTags = append(deletedTags, r.FormValue("tags"))
		case "/api/v2/torrents/reannounce":
			reannounced = append(reannounced, selected)
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/torrents/delete":
			deleted = append(deleted, selected)
			if (selected != valid+"|"+second && selected != disconnected) || r.FormValue("deleteFiles") != "true" {
				t.Error("host dispatched an unproven deletion")
				http.Error(w, "owned scope rejected", http.StatusForbidden)
				return
			}
			for _, hash := range strings.Split(selected, "|") {
				if err := os.Remove(filepath.Join(root, fileName(hash))); err != nil {
					t.Error(err)
				}
				delete(active, hash)
			}
			if selected == disconnected {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				conn.Close()
				return
			}
			_, _ = io.WriteString(w, "Ok.")
		default:
			t.Errorf("unexpected daemon request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(peer.Close)
	settings := a.Settings()
	downloader := DownloaderConfig{Config: download.Config{ID: "owned-legacy-brush", Kind: "qbittorrent", BaseURL: peer.URL, Username: "fixture", Password: "fixture"}, Name: "Owned legacy qB"}
	var otherReads int
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/api/v2/torrents/info" && r.FormValue("hashes") == second {
			otherReads++
			row := workflowTask(second, 1)
			row["save_path"], row["tags"] = remote, "已整理, 刷流, "+originalBrushTaskTag(taskID)
			workflowJSON(w, []any{row})
			return
		}
		t.Error("brush called an unrelated downloader", r.URL.Path)
		http.Error(w, "offline fixture", http.StatusServiceUnavailable)
	}))
	t.Cleanup(other.Close)
	settings.Downloaders = []DownloaderConfig{downloader, {Config: download.Config{ID: "other", Kind: "qbittorrent", BaseURL: other.URL}, Name: "Other qB"}}
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
		cfg := originalBrushStoredConfig{Enabled: true, Tasks: []originalBrushAddTask{
			{ID: taskID, SiteID: 301, Downloader: downloader.Name, Enabled: true},
			{ID: unusedTaskID, SiteID: 301, Downloader: downloader.Name, Enabled: true},
		}}
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
	meta := install("BrushFlowLanduo", valid, globalOnly, duplicate, wrongMarker, second, disconnected)
	install("BrushFlowLanduoCopy", duplicate)
	object := &originalPluginInstance{app: a, installation: meta, hostContract: pluginResourcesSDKContract,
		manifest: plugins.Manifest{Capabilities: []string{plugins.DownloaderDeleteFilesCapability, plugins.DownloaderSourceControlCapability, plugins.DownloaderDeleteTagsCapability}}}
	lease, callCtx, err := object.reserveOriginalSubmission()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { object.releaseOriginalSubmission(lease); a.wg.Done() })
	host, closeScope := object.originalBaseSessionHost(callCtx, "check", nil)
	t.Cleanup(closeScope)
	identity := plugins.Identity{PluginID: meta.PluginID, InstanceID: meta.InstanceID, Version: meta.Version}
	deleteHashes := func(hashes ...string) (json.RawMessage, error) {
		return host(callCtx, identity, plugins.DownloaderDeleteFilesCapability, mustCleanSeedJSON(plugins.DownloaderDeleteFilesRequestV1{
			DownloaderID: downloader.ID, Hashes: hashes, BrushOwnership: true,
		}))
	}
	for _, hash := range []string{globalOnly, wrongMarker, duplicate} {
		if _, err := deleteHashes(valid, hash); pluginErrorCode(err) != "DOWNLOAD_NOT_MANAGED" {
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
	owner, _, err := a.originalBrushReadOwner(ctx, scope, downloader, valid)
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
	// Only the intended daemon can own a migrated record, even if another
	// daemon has precisely the same hash, save path and brush task marker.
	_, err = host(callCtx, identity, plugins.DownloaderDeleteFilesCapability, mustCleanSeedJSON(plugins.DownloaderDeleteFilesRequestV1{
		DownloaderID: "other", Hashes: []string{second}, BrushOwnership: true,
	}))
	if pluginErrorCode(err) != "DOWNLOAD_NOT_MANAGED" {
		t.Fatal("a different downloader claimed the same source record", err)
	}
	if _, err := deleteHashes(second, globalOnly); pluginErrorCode(err) != "DOWNLOAD_NOT_MANAGED" {
		t.Fatal("a mixed batch reached removal", err)
	}
	owner, _, err = a.originalBrushReadOwner(ctx, scope, downloader, second)
	if err != nil || owner.TaskID != taskID || owner.AdoptedPath != remote {
		t.Fatal("the daemon-only path did not bind the proven legacy identity", owner, err)
	}
	changeDirectory(second, remote+"/moved")
	if _, err := deleteHashes(second); pluginErrorCode(err) != "DOWNLOAD_NOT_MANAGED" {
		t.Fatal("a changed daemon directory did not invalidate the adopted identity", err)
	}
	changeDirectory(second, remote)
	for _, action := range []string{"pause", "reannounce"} {
		control.Action, control.Hashes = action, []string{second, wrongMarker}
		if _, err := host(callCtx, identity, plugins.DownloaderSourceControlCapability, mustCleanSeedJSON(control)); pluginErrorCode(err) != "DOWNLOAD_NOT_MANAGED" {
			t.Fatalf("an unproven hash reached %s: %v", action, err)
		}
		control.Hashes = []string{second}
		raw, err := host(callCtx, identity, plugins.DownloaderSourceControlCapability, mustCleanSeedJSON(control))
		var result plugins.DownloaderSourceMutationResultV1
		if err != nil || json.Unmarshal(raw, &result) != nil || !result.Accepted {
			t.Fatalf("remote legacy task %s: %s, %v", action, raw, err)
		}
	}
	changeDirectory(disconnected, remote+"/../untrusted")
	if _, err := deleteHashes(disconnected); pluginErrorCode(err) != "FILE_ROOT_REJECTED" {
		t.Fatal("a noncanonical directory was used as a legacy identity", err)
	}
	changeDirectory(disconnected, root)
	batch := valid + "|" + second
	raw, err = deleteHashes(valid, second, strings.ToUpper(valid))
	var result plugins.DownloaderDeleteFilesV1
	if err != nil || json.Unmarshal(raw, &result) != nil || !result.DaemonApplied || !result.BookkeepingCompleted {
		t.Fatal("proven legacy task did not reach protected daemon deletion", err)
	}
	if _, err := os.Stat(filepath.Join(root, fileName(valid))); !os.IsNotExist(err) {
		t.Fatal("owned daemon did not remove the selected file", err)
	}
	owner, _, err = a.originalBrushReadOwner(ctx, scope, downloader, valid)
	if err != nil || owner.TaskID != "" {
		t.Fatal("confirmed deletion did not retire the adopted ownership", err)
	}
	raw, err = deleteHashes(disconnected)
	result = plugins.DownloaderDeleteFilesV1{}
	if err != nil || json.Unmarshal(raw, &result) != nil || result.Outcome != "unknown" || result.FailureCode != "DOWNLOADER_OUTCOME_UNKNOWN" || result.BookkeepingCompleted || result.DaemonApplied {
		t.Fatal("lost acknowledgement must preserve uncertainty", result, err)
	}
	owner, _, err = a.originalBrushReadOwner(ctx, scope, downloader, disconnected)
	if err != nil || owner.TaskID == "" {
		t.Fatal("an unknown outcome must preserve the ownership receipt", err)
	}
	if _, err := deleteHashes(disconnected); pluginErrorCode(err) != "DOWNLOAD_NOT_FOUND" {
		t.Fatal("a subsequent read of the missing task reached another deletion", err)
	}
	for _, phase := range []string{"check", "promotion_expiry"} {
		tagHost, closeTags := object.originalBaseSessionHostBound(callCtx, phase, nil, nil, mustCleanSeedJSON(map[string]string{"task_id": unusedTaskID}))
		for _, tc := range []struct {
			downloaderID, tag string
			allowed           bool
		}{
			{downloader.ID, originalBrushTaskTag(unusedTaskID), true},
			{downloader.ID, "刷流-foreign", false},
			{"other", originalBrushTaskTag(unusedTaskID), false},
			{downloader.ID, "刷流", false},
		} {
			raw, err := tagHost(callCtx, identity, plugins.DownloaderDeleteTagsCapability, mustCleanSeedJSON(plugins.DownloaderDeleteTagsRequestV1{DownloaderID: tc.downloaderID, Tags: []string{tc.tag}}))
			var result plugins.DownloaderMutationV1
			if tc.allowed && (err != nil || json.Unmarshal(raw, &result) != nil || !result.Accepted) || !tc.allowed && pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
				t.Fatalf("%s tag %q downloader %q: receipt=%s err=%v", phase, tc.tag, tc.downloaderID, raw, err)
			}
		}
		closeTags()
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(reads, []string{valid + "|" + globalOnly, valid + "|" + wrongMarker, valid + "|" + duplicate, valid, second + "|" + globalOnly, second, second + "|" + wrongMarker, second, second + "|" + wrongMarker, second, disconnected, batch, disconnected, disconnected}) {
		t.Fatal("expected one exact ownership read per selection", reads)
	}
	if otherReads != 1 {
		t.Fatal("unexpected reads on the other downloader", otherReads)
	}
	if !slices.Equal(deletedTags, []string{originalBrushTaskTag(unusedTaskID), originalBrushTaskTag(unusedTaskID)}) {
		t.Fatal("unexpected tag mutations", deletedTags)
	}
	if !slices.Equal(reannounced, []string{valid, second}) || !slices.Equal(paused, []string{second}) || !slices.Equal(deleted, []string{batch, disconnected}) {
		t.Fatal("isolated daemon observed unexpected effects", len(reannounced), len(deleted))
	}
}

// A receipt bound to an older daemon configuration is not a confirmation, but
// it must not block the proven legacy adoption either: the verified marker,
// destination and task replace that stale binding exactly once.
func TestBrushFlowStaleOwnerReceiptIsReplacedByVerifiedLegacyEvidence(t *testing.T) {
	a := newTestApp(t)
	ctx := t.Context()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const taskID = "stale-owner-task"
	hash := strings.Repeat("e", 40)
	if err := os.WriteFile(filepath.Join(root, "e.mkv"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	var reannounced []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "fixture-only", Path: "/"})
			_, _ = io.WriteString(w, "Ok.")
		case "/api/v2/app/version":
			_, _ = io.WriteString(w, "v5.0.5")
		case "/api/v2/torrents/info":
			row := workflowTask(hash, 1)
			row["save_path"], row["tags"] = root, "已整理, 刷流, "+originalBrushTaskTag(taskID)
			workflowJSON(w, []map[string]any{row})
		case "/api/v2/torrents/files":
			workflowJSON(w, []map[string]any{{"name": "e.mkv", "size": 5, "progress": 1}})
		case "/api/v2/torrents/reannounce":
			reannounced = append(reannounced, r.FormValue("hashes"))
			_, _ = io.WriteString(w, "Ok.")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(peer.Close)
	settings := a.Settings()
	downloader := DownloaderConfig{Config: download.Config{ID: "owned-stale-brush", Kind: "qbittorrent", BaseURL: peer.URL}, Name: "Owned stale qB"}
	settings.Downloaders = []DownloaderConfig{downloader}
	settings.AllowedRoots = []string{root}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	meta := PluginInstallation{InstanceID: "BrushFlowLanduo", PluginID: "BrushFlowLanduo", Version: "5.2.18", ManifestSchemaVersion: 1, Manifest: json.RawMessage(`{}`)}
	if err := a.pluginStore.Install(ctx, meta); err != nil {
		t.Fatal(err)
	}
	meta, err = a.pluginStore.Installation(ctx, meta.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	cfg := originalBrushStoredConfig{Enabled: true, Tasks: []originalBrushAddTask{{ID: taskID, SiteID: 301, Downloader: downloader.Name, Enabled: true}}}
	if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, meta, mustCleanSeedJSON(cfg)); err != nil {
		t.Fatal(err)
	}
	if err := a.pluginStore.originalBaseSaveData(ctx, meta.InstanceID, "task."+taskID+".torrents", mustCleanSeedJSON(map[string]map[string]any{hash: {"task_id": taskID, "site": 301, "title": "owned source record", "deleted": false}})); err != nil {
		t.Fatal(err)
	}
	key := originalBrushOwnerKey(meta.InstanceID, downloader.ID, hash)
	stale := originalBrushOwner{InstanceID: meta.InstanceID, DownloaderID: downloader.ID, DownloaderKey: tokenHash("older-daemon-configuration"), Hash: hash, TaskID: taskID, AdoptedPath: root}
	sealed, err := a.pluginStore.seal(originalBrushOwnerKind, meta.InstanceID, key, mustCleanSeedJSON(stale))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.ExecContext(ctx, `INSERT INTO plugin_record(kind,id,payload,updated_at) VALUES(?,?,?,?)`, originalBrushOwnerKind, key, sealed, now()); err != nil {
		t.Fatal(err)
	}
	object := &originalPluginInstance{app: a, installation: meta, hostContract: pluginResourcesSDKContract,
		manifest: plugins.Manifest{Capabilities: []string{plugins.DownloaderSourceControlCapability}}}
	lease, callCtx, err := object.reserveOriginalSubmission()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { object.releaseOriginalSubmission(lease); a.wg.Done() })
	host, closeScope := object.originalBaseSessionHost(callCtx, "check", nil)
	t.Cleanup(closeScope)
	identity := plugins.Identity{PluginID: meta.PluginID, InstanceID: meta.InstanceID, Version: meta.Version}
	raw, err := host(callCtx, identity, plugins.DownloaderSourceControlCapability, mustCleanSeedJSON(plugins.DownloaderControlRequestV1{DownloaderID: downloader.ID, Hashes: []string{hash}, Action: "reannounce"}))
	var outcome plugins.DownloaderSourceMutationResultV1
	if err != nil || json.Unmarshal(raw, &outcome) != nil || !outcome.Accepted {
		t.Fatalf("stale receipt still blocked the proven legacy reannounce: %v", err)
	}
	if !slices.Equal(reannounced, []string{hash}) {
		t.Fatalf("daemon did not observe the reannounce: %v", reannounced)
	}
	owner, staleReceipt, err := a.originalBrushReadOwner(ctx, originalBaseCallScope{installation: meta}, downloader, hash)
	if err != nil || staleReceipt || owner.TaskID != taskID || owner.AdoptedPath != root {
		t.Fatalf("stale binding was not replaced by the verified owner: %+v stale=%v err=%v", owner, staleReceipt, err)
	}
}

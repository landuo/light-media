package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPluginAutoBackupNativeClonePrivateArchiveIsolation(t *testing.T) {
	autoBackupCloneArchiveIsolation(t, false)
}

func TestAutoBackupActualServerNativeClonePrivateArchiveIsolation(t *testing.T) {
	autoBackupCloneArchiveIsolation(t, true)
}

func autoBackupCloneArchiveIsolation(t *testing.T, server bool) {
	a := newTestApp(t)
	configureWorker := func(a *App) {
		useBuiltInPluginProcess(t, a)
		if server {
			transferServerBinary.once.Do(func() { transferServerBinary.path, transferServerBinary.err = buildTransferFixture(true) })
			if transferServerBinary.err != nil {
				t.Fatal(transferServerBinary.err)
			}
			a.pluginCommand = func(id string) (plugins.Command, error) {
				return plugins.Command{Path: transferServerBinary.path, Args: []string{"--plugin-worker", id}}, nil
			}
		}
	}
	configureWorker(a)
	cookie := setupTestAdmin(t, a)
	w := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"AutoBackup"}`, cookie, "")
	if w.Code != 200 {
		t.Fatal("fresh install", w.Code, w.Body.String())
	}
	autoTestSuccess(t, a, autoTestClaim(t, a))
	ctx := context.Background()
	instant := time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)
	a.pluginAutoBackup.now = func() time.Time { return instant }
	originalConfig, err := a.pluginStore.Config(ctx, "AutoBackup")
	if err != nil {
		t.Fatal(err)
	}
	w = callTest(t, a.Handler(), "POST", "/api/plugins/AutoBackup/clone", `{"suffix":"copy","name":"Owned backup clone"}`, cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	clone := "AutoBackupcopy"
	cfg, err := a.pluginStore.Config(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"revision": cfg.Revision, "config": map[string]any{"enabled": false, "cnt": 1}})
	w = callTest(t, a.Handler(), "PUT", "/api/plugins/"+clone+"/config", string(raw), cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	run, err := a.pluginStore.claimRunForInstance(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	autoTestSuccess(t, a, run)
	settings := a.Settings()
	settings.APIToken = "owned-autobackup-instance-synthetic-api"
	if err = a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	autoTestRun(t, a)
	sourceFiles := autoTestFiles(t, a)
	if len(sourceFiles) != 1 {
		t.Fatal("source archive missing")
	}
	sourceBytes, err := os.ReadFile(sourceFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Store.Put(ctx, "owned-clone-backup", "marker", map[string]string{"value": "clone snapshot has distinct owned content"}); err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.EnqueueRun(ctx, clone, "run", nil); err != nil {
		t.Fatal(err)
	}
	run, err = a.pluginStore.claimRunForInstance(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	autoTestSuccess(t, a, run)
	cloneRoot := filepath.Join(a.Dir, "plugin-private", clone, "backups")
	cloneFiles, err := filepath.Glob(filepath.Join(cloneRoot, "bk*.zip"))
	if err != nil || len(cloneFiles) != 1 {
		t.Fatalf("clone must own one archive in %s; got %v (%v)", cloneRoot, cloneFiles, err)
	}
	if server {
		autoBackupVerifyOwnedArchiveRestore(t, cloneFiles[0])
	}
	after, err := os.ReadFile(sourceFiles[0])
	if err != nil || !bytes.Equal(sourceBytes, after) {
		t.Fatal("clone overwrote source archive", err)
	}
	// With local retention of one, a later clone backup must not delete source output.
	instant = instant.Add(time.Second)
	if _, err = a.pluginStore.EnqueueRun(ctx, clone, "run", nil); err != nil {
		t.Fatal(err)
	}
	run, err = a.pluginStore.claimRunForInstance(ctx, clone)
	if err != nil {
		t.Fatal(err)
	}
	autoTestSuccess(t, a, run)
	after, err = os.ReadFile(sourceFiles[0])
	if err != nil || !bytes.Equal(sourceBytes, after) {
		t.Fatal("clone retention touched source", err)
	}
	cloneFiles, err = filepath.Glob(filepath.Join(cloneRoot, "bk*.zip"))
	if err != nil || len(cloneFiles) != 1 {
		t.Fatal("clone retention", cloneFiles, err)
	}
	form := callTest(t, a.Handler(), "GET", "/api/plugins/"+clone+"/form", "", cookie, "")
	if form.Code != 200 || !strings.Contains(form.Body.String(), cloneRoot) {
		t.Fatal("clone form default path not instance-owned", form.Code, form.Body.String())
	}
	// Cold reopening retains isolated roots and does not replay the backups.
	dir := a.Dir
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	configureWorker(b)
	if _, _, err = b.pluginStore.autoBackupRead(ctx, b.Store.DB, clone, 1); err != nil {
		t.Fatal("cold clone receipt", err)
	}
	if w = callTest(t, b.Handler(), "POST", "/api/plugins/"+clone+"/reset", "", cookie, ""); w.Code != 200 {
		t.Fatal("clone reset", w.Code, w.Body.String())
	}
	sourceInstall, err := b.pluginStore.Installation(ctx, "AutoBackup")
	if err != nil || sourceInstall.Generation != 1 {
		t.Fatal("reset changed source generation", err)
	}
	after, err = os.ReadFile(sourceFiles[0])
	if err != nil || !bytes.Equal(sourceBytes, after) {
		t.Fatal("reset touched source backup", err)
	}
	if _, err = os.Stat(cloneFiles[0]); err != nil {
		t.Fatal("reset removed archive", err)
	}
	current, err := b.pluginStore.Config(ctx, "AutoBackup")
	if err != nil || current.Revision != originalConfig.Revision || !bytes.Equal(current.Value, originalConfig.Value) {
		t.Fatal("source config changed", err)
	}
}

// Explicit destinations are business configuration, not automatically relocated
// by clone creation. An authorized shared target retains original same-second
// overwrite semantics; isolation is promised only for the implicit private path.
func TestPluginAutoBackupNativeCloneExplicitDestinationIsNotRewritten(t *testing.T) {
	a := newTestApp(t)
	cookie := autoTestInstall(t, a)
	ctx := context.Background()
	target := t.TempDir()
	settings := a.Settings()
	settings.AllowedRoots = append(settings.AllowedRoots, target)
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	autoTestConfig(t, a, cookie, map[string]any{"back_path": target, "cnt": 1})
	out, err := a.pluginStore.Clone(ctx, "AutoBackup", PluginCloneRequest{Suffix: "explicit"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := a.pluginStore.Config(ctx, out.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if json.Unmarshal(cfg.Value, &config) != nil || config["back_path"] != target {
		t.Fatal("copied explicit path changed", string(cfg.Value))
	}
	raw, _ := json.Marshal(map[string]any{"revision": cfg.Revision, "config": config})
	w := callTest(t, a.Handler(), "PUT", "/api/plugins/"+out.InstanceID+"/config", string(raw), cookie, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	run, err := a.pluginStore.claimRunForInstance(ctx, out.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	autoTestSuccess(t, a, run)
	if _, err = a.pluginStore.EnqueueRun(ctx, out.InstanceID, "run", nil); err != nil {
		t.Fatal(err)
	}
	run, err = a.pluginStore.claimRunForInstance(ctx, out.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	autoTestSuccess(t, a, run)
	files, err := filepath.Glob(filepath.Join(target, "bk*.zip"))
	if err != nil || len(files) != 1 {
		t.Fatal("explicit archive target was relocated", files, err)
	}
	files, err = filepath.Glob(filepath.Join(a.Dir, "plugin-private", out.InstanceID, "backups", "bk*.zip"))
	if err != nil || len(files) != 0 {
		t.Fatal("unexpected implicit archive", files, err)
	}
}

func autoBackupVerifyOwnedArchiveRestore(t *testing.T, filename string) {
	t.Helper()
	dest := t.TempDir()
	reader, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	names := map[string]bool{}
	for _, entry := range reader.File {
		if entry.Name != "light-media.db" && entry.Name != "secret.key" && entry.Name != "category.yaml" {
			t.Fatal("unexpected owned archive member", entry.Name)
		}
		names[entry.Name] = true
		in, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		out, err := os.OpenFile(filepath.Join(dest, entry.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			in.Close()
			t.Fatal(err)
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		in.Close()
		if copyErr != nil || closeErr != nil {
			t.Fatal(copyErr, closeErr)
		}
	}
	if !names["light-media.db"] || !names["secret.key"] {
		t.Fatal("restore files missing", names)
	}
	restored, err := New(Options{DataDir: dest})
	if err != nil {
		t.Fatal("independent owned restore", err)
	}
	defer restored.Close()
	if restored.Settings().APIToken != "owned-autobackup-instance-synthetic-api" {
		t.Fatal("restore key did not decrypt actual settings")
	}
	var marker map[string]string
	if err = restored.Store.Get(context.Background(), "owned-clone-backup", "marker", &marker); err != nil || marker["value"] != "clone snapshot has distinct owned content" {
		t.Fatal("restore lost clone snapshot", marker, err)
	}
	var cuts int
	if err = restored.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id='AutoBackupcopy' AND state='canceled' AND error_code='AUTO_BACKUP_SNAPSHOT_CUT'`).Scan(&cuts); err != nil || cuts != 1 {
		t.Fatal("actual server snapshot cut lost after restore", cuts, err)
	}
	var pending int
	if err = restored.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id='AutoBackupcopy' AND state IN ('pending','running')`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("backup replayed restored action", pending, err)
	}
}

func TestPluginAutoBackupLocalArchiveAuthenticatedNotice(t *testing.T) {
	a := newTestApp(t)
	cookie := autoTestInstall(t, a)
	autoTestConfig(t, a, cookie, map[string]any{"notify": true})
	run := autoTestRun(t, a)
	ctx := context.Background()
	state, _, err := a.pluginStore.autoBackupRead(ctx, a.Store.DB, run.InstanceID, run.Generation)
	if err != nil {
		t.Fatal(err)
	}
	confirmed := false
	for _, effect := range state.Runs[run.ID].Effects {
		if strings.HasPrefix(effect.Kind, "webdav_") {
			t.Fatal("local backup invoked remote storage")
		}
		if effect.Kind == "notice" && effect.Phase == "confirmed" {
			confirmed = true
		}
	}
	if !confirmed {
		t.Fatal("notice was not authenticated and confirmed")
	}
	var routeID string
	if err = a.Store.DB.QueryRow(`SELECT id FROM plugin_notice_routes WHERE instance_id='AutoBackup'`).Scan(&routeID); err != nil {
		t.Fatal(err)
	}
	route, _, err := a.pluginStore.readNoticeRoute(ctx, a.Store.DB, routeID)
	if err != nil || !route.Complete || route.Kind != "auto_backup" {
		t.Fatal("local notice route", err)
	}
	var notification Notification
	if err = a.Store.Get(ctx, "notification", route.HistoryID, &notification); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notification.Title, "自动备份") || !strings.Contains(notification.Message, "成功") {
		t.Fatal("source notification meaning changed", notification.Title, notification.Message)
	}
	notification.Read = true
	if err = a.Store.Put(ctx, "notification", notification.ID, &notification); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.pluginStore.readNoticeRoute(ctx, a.Store.DB, routeID); err != nil {
		t.Fatal("read status invalidated notice", err)
	}
	notification.Message += "forged"
	if err = a.Store.Put(ctx, "notification", notification.ID, &notification); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.pluginStore.readNoticeRoute(ctx, a.Store.DB, routeID); err == nil {
		t.Fatal("forged history accepted")
	}
}

func TestPluginAutoBackupCancellationBeforeIntentDoesNotInventUnknown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, priorUnknown := range []bool{false, true} {
		call := autoBackupCall{uncertain: priorUnknown}
		applied := false
		_, err := call.effect(ctx, "restore_key_copy", nil, func() (any, error) { applied = true; return nil, nil })
		if !errors.Is(err, context.Canceled) || applied || call.uncertain != priorUnknown {
			t.Fatal("pre-intent cancellation altered outcome", err, applied, call.uncertain)
		}
	}
}
func TestPluginAutoBackupCancellationAfterApplyBeforeReceiptRemainsUnknown(t *testing.T) {
	a := newTestApp(t)
	cookie := autoTestInstall(t, a)
	a.pluginAutoBackup.afterEffect = func(kind string) error {
		if kind == "sqlite_snapshot" {
			return context.Canceled
		}
		return nil
	}
	ctx := context.Background()
	if _, err := a.pluginStore.EnqueueRun(ctx, "AutoBackup", "run", nil); err != nil {
		t.Fatal(err)
	}
	run := autoTestExecute(t, a, autoTestClaim(t, a))
	a.pluginAutoBackup.afterEffect = nil
	if run.State != "reconcile_pending" {
		t.Fatal("post-apply cancellation was treated as no effect", run.State)
	}
	state, _, err := a.pluginStore.autoBackupRead(ctx, a.Store.DB, run.InstanceID, run.Generation)
	if err != nil {
		t.Fatal(err)
	}
	action := state.Runs[run.ID]
	if action.Phase != "started" || len(action.Effects) != 1 || action.Effects[0].Kind != "sqlite_snapshot" || action.Effects[0].Phase != "started" {
		t.Fatal("unknown actual snapshot evidence lost", action)
	}
	var staging map[string]string
	if json.Unmarshal(action.Effects[0].Args, &staging) != nil || staging["staging"] == "" {
		t.Fatal("snapshot intent missing owned stage")
	}
	w := callTest(t, a.Handler(), "POST", "/api/plugins/AutoBackup/reset", "", cookie, "")
	if w.Code == 200 || !strings.Contains(w.Body.String(), "AUTO_BACKUP_UNCERTAIN") {
		t.Fatal("unknown cancellation allowed reset", w.Code, w.Body.String())
	}
	current, err := a.pluginStore.Installation(ctx, "AutoBackup")
	if err != nil || current.Generation != run.Generation {
		t.Fatal("unknown cancellation advanced generation", err)
	}
	after, _, err := a.pluginStore.autoBackupRead(ctx, a.Store.DB, run.InstanceID, run.Generation)
	if err != nil || !bytes.Equal(mustCleanSeedJSON(state), mustCleanSeedJSON(after)) {
		t.Fatal("reset rewrote unknown journal", err)
	}
}

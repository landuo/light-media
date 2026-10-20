package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	seed "light-media/internal/plugins/cleaninvalidseed"
	"light-media/internal/plugins/removelink"
	"light-media/internal/plugins/uninstall"
)

func TestPluginCleanInvalidSeedFormPreviewOnlyReadsCompiledDownloaderNames(t *testing.T) {
	a := newTestApp(t)
	a.config = Settings{Downloaders: []DownloaderConfig{{Name: "Owned qB"}}}
	manifest, _ := json.Marshal(seed.Manifest())
	preview := PluginInstallation{InstanceID: "CleanInvalidSeed", PluginID: "CleanInvalidSeed", Version: "2.1", ManifestSchemaVersion: 1, Manifest: manifest}
	form, err := a.pluginCleanInvalidSeedHost(context.Background(), preview, nil, seed.FormCapability, json.RawMessage(`{}`))
	if err != nil || !bytes.Contains(form, []byte("Owned qB")) {
		t.Fatal("valid install preview", err)
	}
	for _, kind := range []string{"instance", "generation", "manifest", "request_fields"} {
		t.Run(kind, func(t *testing.T) {
			forged := preview
			raw := json.RawMessage(`{}`)
			switch kind {
			case "instance":
				forged.InstanceID = "another-instance"
			case "generation":
				forged.Generation = 1
			case "manifest":
				forged.Manifest = json.RawMessage(`{"plugin_id":"CleanInvalidSeed"}`)
			case "request_fields":
				raw = json.RawMessage(`{"path":"/never-authorized"}`)
			}
			if _, err := a.pluginCleanInvalidSeedHost(context.Background(), forged, nil, seed.FormCapability, raw); err == nil {
				t.Fatal("forged preview accepted")
			}
		})
	}
	var installed, runs, journals int
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_installations`).Scan(&installed)
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runs)
	_ = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='plugin_clean_seed_meta'`).Scan(&journals)
	if installed != 0 || runs != 0 || journals != 0 {
		t.Fatal("read-only form preview created execution state", installed, runs, journals)
	}
}
func cleanSeedHTTPCall(t *testing.T, a *App, cookie *http.Cookie, method, path string, value any, want int) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(value)
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != want {
		t.Fatalf("%s %s: status=%d body=%s", method, path, w.Code, w.Body.String())
	}
	return w
}
func TestPluginCleanInvalidSeedHTTPLifecycleAuthResetRevisionZeroAndReinstall(t *testing.T) {
	a := newTestApp(t)
	a.config = Settings{}
	run := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), map[string]any{"enabled": false}, true)
	cleanSeedOwnerInvoke(t, a, run)
	cookie := setupTestAdmin(t, a)
	ctx := context.Background()
	body := map[string]any{"revision": 1, "config": map[string]any{"enabled": false, "onlyonce": true}}
	cleanSeedHTTPCall(t, a, nil, "PUT", "/api/plugins/CleanInvalidSeed/config", body, 401)
	cleanSeedHTTPCall(t, a, cookie, "PUT", "/api/plugins/CleanInvalidSeed/config", body, 200)
	c, err := a.pluginStore.Config(ctx, run.InstanceID)
	if err != nil || c.Revision != 2 || !bytes.Contains(c.Value, []byte(`"onlyonce":true`)) {
		t.Fatal("HTTP publication ran source before SDK handshake", c.Revision, err)
	}
	if got := cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID)); got.State != "succeeded" {
		t.Fatal(got.State, got.ErrorCode)
	}
	c, err = a.pluginStore.Config(ctx, run.InstanceID)
	if err != nil || c.Revision != 3 || !bytes.Contains(c.Value, []byte(`"onlyonce":false`)) {
		t.Fatal("source flag cleanup not committed", c.Revision, err)
	}
	cleanSeedHTTPCall(t, a, cookie, "POST", "/api/plugins/CleanInvalidSeed/reload", map[string]any{}, 200)
	if got := cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID)); got.State != "succeeded" || got.Action != "reload" {
		t.Fatal("explicit reload", got.State, got.ErrorCode)
	}
	var once int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id=? AND active=1`, run.InstanceID).Scan(&once); err != nil || once != 0 {
		t.Fatal("reload retained stopped old once plan", once, err)
	}
	cleanSeedHTTPCall(t, a, cookie, "POST", "/api/plugins/CleanInvalidSeed/reset", map[string]any{}, 200)
	i, err := a.pluginStore.Installation(ctx, run.InstanceID)
	if err != nil || i.Generation != 2 || !i.Installed || i.DesiredEnabled {
		t.Fatal("reset identity", i.Generation, err)
	}
	if _, err = a.pluginStore.Config(ctx, run.InstanceID); !missingPluginValue(err) {
		t.Fatal("reset kept public config", err)
	}
	cleanSeedHTTPCall(t, a, cookie, "PUT", "/api/plugins/CleanInvalidSeed/config", map[string]any{"revision": 0, "config": map[string]any{"enabled": false}}, 200)
	got := cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID))
	if got.State != "succeeded" || got.Generation != 2 || got.ConfigRevision != 1 {
		t.Fatal("first save after reset", got.State, got.ErrorCode)
	}
	cleanSeedHTTPCall(t, a, cookie, "POST", "/api/plugins/CleanInvalidSeed/uninstall", map[string]any{}, 200)
	i, err = a.pluginStore.Installation(ctx, run.InstanceID)
	if err != nil || i.Installed || i.Generation != 3 {
		t.Fatal("retirement", i.Generation, err)
	}
	// Catalog is deliberately not registered in this slice. Exercise the real
	// store's reinstall contract; UI install receives a later catalog regression.
	manifest, _ := json.Marshal(seed.Manifest())
	_, err = a.pluginStore.InstallAndQueue(ctx, PluginInstallation{InstanceID: run.InstanceID, PluginID: "CleanInvalidSeed", Version: "2.1", ManifestSchemaVersion: 1, Manifest: manifest}, map[string]any{"enabled": false})
	if err != nil {
		t.Fatal(err)
	}
	got = cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID))
	if got.State != "succeeded" || got.Generation <= 3 {
		t.Fatal("reinstall borrowed retired owner", got.State, got.Generation, got.ErrorCode)
	}
}

func TestPluginCleanInvalidSeedMixedBulkUninstallKeepsCommittedOwnersOnLaterFailure(t *testing.T) {
	for _, failSQL := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "own_config_commit_failure"}[failSQL], func(t *testing.T) {
			a := newTestApp(t)
			a.config = Settings{}
			clean := uninstallInstall(t, a, "a-clean", seed.Manifest(), map[string]any{"enabled": false}, true)
			cleanSeedOwnerInvoke(t, a, clean)
			remove := uninstallInstall(t, a, "z-remove", removelink.Manifest(), map[string]any{"enabled": false}, true)
			removeLinkOwnerWorker(t, a, remove)
			run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"z-remove", "a-clean"}}, true)
			if failSQL {
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER fail_mixed_cleanup_uninstall BEFORE UPDATE ON plugin_values WHEN NEW.instance_id='PluginUnInstall' BEGIN SELECT RAISE(ABORT,'synthetic late own-config SQL failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			_, err := a.applyPluginUninstall(context.Background(), &run)
			if failSQL {
				if err == nil {
					t.Fatal("late SQL failure reported success")
				}
				for _, instance := range []string{clean.InstanceID, remove.InstanceID} {
					i, e := a.pluginStore.Installation(context.Background(), instance)
					if e != nil || i.Installed || i.Generation != 2 {
						t.Fatal("later source config failure undid an earlier package", instance, i.Generation, e)
					}
				}
				if owner := a.pluginCleanInvalidSeed.owners[clean.InstanceID]; owner != nil && len(owner.gate) != 0 {
					t.Fatal("clean owner reservation leaked")
				}
				if owner := a.pluginRemoveLink.owners[remove.InstanceID]; owner != nil && len(owner.gate) != 0 {
					t.Fatal("remove owner reservation leaked")
				}
				if _, e := a.Store.DB.Exec(`DROP TRIGGER fail_mixed_cleanup_uninstall`); e != nil {
					t.Fatal(e)
				}
				if _, retryErr := a.applyPluginUninstall(context.Background(), &run); retryErr == nil {
					t.Fatal("failed source operation replayed")
				}
				err = nil
			}
			if err != nil {
				t.Fatal("mixed cleanup retirement", err)
			}
			for _, instance := range []string{clean.InstanceID, remove.InstanceID} {
				i, e := a.pluginStore.Installation(context.Background(), instance)
				if e != nil || i.Installed || i.Generation != 2 {
					t.Fatal("target did not retire", instance, i.Generation, e)
				}
			}
			if _, err = a.pluginStore.Config(context.Background(), clean.InstanceID); err != nil {
				t.Fatal("uninstall discarded source config audit", err)
			}
			if a.pluginCleanInvalidSeed.owners[clean.InstanceID] != nil {
				t.Fatal("retired source owner remained active")
			}
		})
	}
}

func TestPluginCleanInvalidSeedHTTPPreflightRejectsMissingDirectoryWithoutPublishing(t *testing.T) {
	a := newTestApp(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.config = Settings{AllowedRoots: []string{root}}
	run := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), map[string]any{"enabled": false}, true)
	cleanSeedOwnerInvoke(t, a, run)
	cookie := setupTestAdmin(t, a)
	missing := filepath.Join(root, "missing")
	bad := map[string]any{"revision": 1, "config": map[string]any{"enabled": true, "cron": "0 0 * * *", "download_dirs": missing + ":" + missing}}
	reqRaw, _ := json.Marshal(bad)
	req := httptest.NewRequest("PUT", "/api/plugins/CleanInvalidSeed/config", bytes.NewReader(reqRaw))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code < 400 {
		t.Fatal("invalid directory was published", w.Code)
	}
	c, err := a.pluginStore.Config(context.Background(), run.InstanceID)
	if err != nil || c.Revision != 1 || !bytes.Equal(c.Value, json.RawMessage(`{"enabled":false}`)) {
		t.Fatal("preflight failure changed core config", c.Revision, err)
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?`, run.InstanceID).Scan(&count); err != nil || count != 1 {
		t.Fatal("preflight failure leaked source SDK admission", count, err)
	}
	cleanSeedHTTPCall(t, a, cookie, "PUT", "/api/plugins/CleanInvalidSeed/config", map[string]any{"revision": 1, "config": map[string]any{"enabled": false, "download_dirs": root + ":" + root}}, 200)
	if got := cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID)); got.State != "succeeded" {
		t.Fatal("normal corrected save remained blocked", got.State, got.ErrorCode)
	}
}

func TestPluginCleanInvalidSeedHTTPReloadCancelsOnlyPendingSourceOccurrenceAtSameRevision(t *testing.T) {
	a := newTestApp(t)
	a.config = Settings{}
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 17, 1, 2, 10, 0, time.UTC).UnixNano())
	a.pluginCleanInvalidSeed.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	run := uninstallInstall(t, a, "CleanInvalidSeed", seed.Manifest(), map[string]any{"enabled": false, "onlyonce": true}, true)
	cleanSeedOwnerInvoke(t, a, run)
	plan := cleanSeedOwnerPlan(t, a, run.InstanceID, "once")
	at := plan.Due.Add(200 * time.Millisecond)
	clock.Store(at.UnixNano())
	if n, err := a.pluginStore.enqueueDuePluginSchedules(context.Background(), at); err != nil || n != 1 {
		t.Fatal("real once event", n, err)
	}
	var queuedID string
	if err := a.Store.DB.QueryRow(`SELECT run_id FROM plugin_schedule_occurrences WHERE instance_id=? AND state='queued'`, run.InstanceID).Scan(&queuedID); err != nil {
		t.Fatal(err)
	}
	before, err := a.pluginStore.Run(context.Background(), queuedID)
	if err != nil {
		t.Fatal(err)
	}
	cookie := setupTestAdmin(t, a)
	cleanSeedHTTPCall(t, a, cookie, "POST", "/api/plugins/CleanInvalidSeed/reload", map[string]any{}, 200)
	after, err := a.pluginStore.Run(context.Background(), queuedID)
	if err != nil || after.State != "canceled" || after.ConfigRevision != before.ConfigRevision || !bytes.Equal(after.Input, before.Input) {
		t.Fatal("same-revision reload stranded or rewrote old source admission", after.State, err)
	}
	var state string
	if err = a.Store.DB.QueryRow(`SELECT state FROM plugin_schedule_occurrences WHERE run_id=?`, queuedID).Scan(&state); err != nil || state != "canceled" {
		t.Fatal("old real occurrence not atomically retired", state, err)
	}
	got := cleanSeedOwnerInvoke(t, a, cleanSeedOwnerClaim(t, a, run.InstanceID))
	if got.State != "succeeded" || got.Action != "reload" || got.ConfigRevision != before.ConfigRevision {
		t.Fatal("reload did not retain its actual meaning", got.Action, got.State, got.ErrorCode)
	}
	var active int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id=? AND active=1`, run.InstanceID).Scan(&active); err != nil || active != 0 {
		t.Fatal("consumed onlyonce flag replayed old date", active, err)
	}
	initial, err := a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || initial.State != "succeeded" {
		t.Fatal("historical completed invocation was canceled", err)
	}
}

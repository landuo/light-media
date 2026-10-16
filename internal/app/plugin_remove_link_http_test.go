package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

func removeLinkHTTPRun(t *testing.T, a *App, instance string) PluginRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	run, err := a.pluginStore.claimRunForInstance(ctx, instance)
	if err != nil {
		t.Fatal("claim cleanup run", err)
	}
	a.executePluginRun(ctx, run)
	actual, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || actual.State != "succeeded" {
		t.Fatal("cleanup SDK run", actual.State, actual.ErrorCode, err)
	}
	return actual
}
func removeLinkHTTPCall(t *testing.T, a *App, cookie *http.Cookie, method, path string, value any) {
	t.Helper()
	raw := ""
	if value != nil {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		raw = string(body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r := httptest.NewRequest(method, path, strings.NewReader(raw)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
}
func TestPluginRemoveLinkHTTPDisabledWithoutRootsAndResetFirstSave(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	removeLinkHTTPCall(t, a, cookie, "POST", "/api/plugins/install", map[string]any{"plugin_id": "RemoveLink"})
	removeLinkHTTPRun(t, a, "RemoveLink")
	removeLinkHTTPCall(t, a, cookie, "PUT", "/api/plugins/RemoveLink/config", map[string]any{"revision": 1, "config": map[string]any{"enabled": false, "monitor_dirs": ""}})
	removeLinkHTTPRun(t, a, "RemoveLink")
	removeLinkHTTPCall(t, a, cookie, "POST", "/api/plugins/RemoveLink/reset", map[string]any{})
	reset, err := a.pluginStore.Installation(context.Background(), "RemoveLink")
	if err != nil || !reset.Installed || reset.DesiredEnabled {
		t.Fatal("reset installation", reset, err)
	}
	if _, err = a.pluginStore.Config(context.Background(), "RemoveLink"); !missingPluginValue(err) {
		t.Fatal("reset retained config", err)
	}
	removeLinkHTTPCall(t, a, cookie, "PUT", "/api/plugins/RemoveLink/config", map[string]any{"revision": 0, "config": map[string]any{"enabled": false, "monitor_dirs": ""}})
	afterReset := removeLinkHTTPRun(t, a, "RemoveLink")
	if afterReset.Generation != reset.Generation || afterReset.ConfigRevision != 1 {
		t.Fatal("first save did not use reset generation", afterReset.Generation, afterReset.ConfigRevision)
	}
	removeLinkHTTPCall(t, a, cookie, "POST", "/api/plugins/RemoveLink/uninstall", map[string]any{})
	retained, err := a.pluginStore.Config(context.Background(), "RemoveLink")
	if err != nil || retained.Revision != 1 {
		t.Fatal("ordinary uninstall lost config", err)
	}
	removeLinkHTTPCall(t, a, cookie, "POST", "/api/plugins/install", map[string]any{"plugin_id": "RemoveLink"})
	reinstalled := removeLinkHTTPRun(t, a, "RemoveLink")
	if reinstalled.Generation <= reset.Generation {
		t.Fatal("reinstall reused retired owner", reinstalled.Generation)
	}
}
func TestPluginRemoveLinkHTTPUninstallDrainsOwnedHardlinkWithParentAllowedRoot(t *testing.T) {
	a, source, library := importTestApp(t)
	useBuiltInPluginProcess(t, a)
	// A common valid layout authorizes the parent and gives the library its
	// stable media ID. Native source authority must use the actual child.
	a.config.AllowedRoots = []string{filepath.Dir(source)}
	a.pluginRemoveLink.watch = func(_ context.Context, _ []removelink.Watch, _ func(string) (*os.File, error)) (cleanuphost.Watcher, error) {
		return &removeLinkSQLTestWatcher{events: make(chan cleanuphost.Event, 8), errors: make(chan error, 1)}, nil
	}
	origin, target := filepath.Join(source, "owned-http.mkv"), filepath.Join(library, "owned-http.mkv")
	if err := os.WriteFile(origin, []byte("owned synthetic linked media"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(origin, target); err != nil {
		t.Fatal(err)
	}
	cookie := setupTestAdmin(t, a)
	removeLinkHTTPCall(t, a, cookie, "POST", "/api/plugins/install", map[string]any{"plugin_id": "RemoveLink"})
	removeLinkHTTPRun(t, a, "RemoveLink")
	config := map[string]any{"enabled": true, "monitor_dirs": source + "\n" + library, "delayed_deletion": true, "delay_seconds": 60, "delete_history": false, "delete_scrap_infos": false, "delete_torrents": false, "notify": false}
	removeLinkHTTPCall(t, a, cookie, "PUT", "/api/plugins/RemoveLink/config", map[string]any{"revision": 1, "config": config})
	run := removeLinkHTTPRun(t, a, "RemoveLink")
	installation, err := a.pluginStore.Installation(context.Background(), "RemoveLink")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := a.removeLinkOwner(context.Background(), installation)
	if err != nil || owner.h == nil {
		t.Fatal("continuous owner", err)
	}
	if err = os.Remove(origin); err != nil {
		t.Fatal(err)
	}
	event := cleanuphost.Event{ID: "owned-http-deleted", Kind: "deleted", Path: origin, Mode: "hardlink", At: time.Now()}
	if err = owner.h.Observe(context.Background(), event); err != nil {
		t.Fatal("trusted owned observation", err)
	}
	state, err := owner.j.Read(context.Background())
	if err != nil || len(state.Machine.Queue) != 1 {
		t.Fatal("delayed task was not retained", len(state.Machine.Queue), err)
	}
	if _, err = os.Lstat(target); err != nil {
		t.Fatal("delay was skipped", err)
	}
	removeLinkHTTPCall(t, a, cookie, "POST", "/api/plugins/RemoveLink/uninstall", map[string]any{})
	if _, err = os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("source Stop did not drain linked file", err)
	}
	final, err := owner.j.Read(context.Background())
	if err != nil || final.Transition == nil || final.Transition.Phase != "applied" || final.Binding.Generation != run.Generation {
		t.Fatal("retired receipt", err)
	}
	retired, err := a.pluginStore.Installation(context.Background(), "RemoveLink")
	if err != nil || retired.Installed || retired.Generation != run.Generation+1 {
		t.Fatal("core retirement", retired, err)
	}
}

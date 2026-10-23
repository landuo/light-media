package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins/builtin"
)

func updateReviewFixture(t *testing.T) (*PluginStore, PluginStoredValue) {
	t.Helper()
	p, r := updateCategoryFixture(t)
	c, e := p.SaveConfig(context.Background(), "CategoryEditor", r.Revision, map[string]any{"enabled": true, "content": "movie: {}"})
	if e != nil {
		t.Fatal(e)
	}
	r.Revision = c.Revision
	out, e := p.updateBuiltin(context.Background(), "CategoryEditor", r)
	if e != nil || !out.ReviewPending || out.Initialization != nil || !out.MigrationCommitted {
		t.Fatal(out, e)
	}
	return p, c
}
func TestPluginUpdateReviewBlocksImplicitActivation(t *testing.T) {
	p, c := updateReviewFixture(t)
	ctx := context.Background()
	in := lifecycleInstallation(t, p, "CategoryEditor")
	gate, e := p.pluginUpdateReviewRead(ctx, p.store.DB, in)
	if e != nil || gate == nil || gate.Revision != c.Revision {
		t.Fatal(gate, e)
	}
	for _, action := range []string{"init", "reload"} {
		if _, e = p.EnqueueRun(ctx, in.InstanceID, action, json.RawMessage(`{}`)); e == nil {
			t.Fatal("implicit activation", action)
		}
	}
	if _, e = p.SaveConfig(ctx, in.InstanceID, c.Revision, map[string]any{"enabled": false}); e == nil {
		t.Fatal("raw config bypass")
	}
	if _, _, e = p.SaveConfigAndQueue(ctx, in.InstanceID, c.Revision, map[string]any{"enabled": true}); e == nil {
		t.Fatal("unauthenticated save bypass")
	}
	after, _ := p.Config(ctx, in.InstanceID)
	if after.Revision != c.Revision || !bytes.Equal(after.Value, c.Value) {
		t.Fatal("rejected operation changed config")
	}
	out, e := p.updateBuiltin(ctx, in.InstanceID, pluginUpdateRequest{TargetVersion: "1.3"})
	if e != nil || out.Changed {
		t.Fatal("same version", out, e)
	}
	if gate, e = p.pluginUpdateReviewRead(ctx, p.store.DB, lifecycleInstallation(t, p, in.InstanceID)); e != nil || gate == nil {
		t.Fatal("no-op lost review", e)
	}
}
func TestPluginUpdateReviewSaveRollbackAndExactInstance(t *testing.T) {
	p, c := updateReviewFixture(t)
	ctx := context.Background()
	id := "CategoryEditor"
	before := lifecycleInstallation(t, p, id)
	auth := context.WithValue(ctx, pluginConfigSaveRequestKey{}, id)
	wrong := context.WithValue(ctx, pluginConfigSaveRequestKey{}, "another-instance")
	if _, _, e := p.SaveConfigAndQueue(wrong, id, c.Revision, map[string]any{"enabled": false}); e == nil {
		t.Fatal("foreign save released")
	}
	if _, _, e := p.SaveConfigAndQueue(auth, id, c.Revision-1, map[string]any{"enabled": false}); e == nil {
		t.Fatal("stale save released")
	}
	if _, e := p.store.DB.Exec(`CREATE TRIGGER owned_review_abort BEFORE INSERT ON plugin_runs BEGIN SELECT RAISE(ABORT,'owned rollback'); END`); e != nil {
		t.Fatal(e)
	}
	if _, _, e := p.SaveConfigAndQueue(auth, id, c.Revision, map[string]any{"enabled": false}); e == nil {
		t.Fatal("expected rollback")
	}
	after := lifecycleInstallation(t, p, id)
	if !bytes.Equal(before.Manifest, after.Manifest) {
		t.Fatal("failed save removed gate")
	}
	got, _ := p.Config(ctx, id)
	if got.Revision != c.Revision || !bytes.Equal(got.Value, c.Value) {
		t.Fatal("rollback changed config")
	}
	if _, e := p.store.DB.Exec(`DROP TRIGGER owned_review_abort`); e != nil {
		t.Fatal(e)
	}
	_, run, e := p.SaveConfigAndQueue(auth, id, c.Revision, map[string]any{"enabled": false})
	if e != nil {
		t.Fatal(e)
	}
	gate, e := p.pluginUpdateReviewRead(ctx, p.store.DB, lifecycleInstallation(t, p, id))
	if e != nil || gate != nil {
		t.Fatal("authenticated save gate", e)
	}
	if run.Action != "init" || run.InstanceID != id {
		t.Fatal(run)
	}
	if _, _, e = p.SaveConfigAndQueue(auth, id, c.Revision, map[string]any{"enabled": true}); e == nil {
		t.Fatal("old concurrent save accepted")
	}
}
func TestPluginUpdateReviewCloneDoesNotInheritAuthorization(t *testing.T) {
	p, c := updateReviewFixture(t)
	ctx := context.Background()
	clone, e := p.Clone(ctx, "CategoryEditor", PluginCloneRequest{Suffix: "Review", Name: "owned review clone"})
	if e != nil {
		t.Fatal(e)
	}
	gate, e := p.pluginUpdateReviewRead(ctx, p.store.DB, clone)
	if e != nil || gate != nil {
		t.Fatal("clone inherited review", e)
	}
	raw, e := p.Config(ctx, clone.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	var cfg map[string]any
	_ = json.Unmarshal(raw.Value, &cfg)
	if cfg["enabled"] != false || clone.DesiredEnabled {
		t.Fatal("clone inherited activation", cfg)
	}
	gate, e = p.pluginUpdateReviewRead(ctx, p.store.DB, lifecycleInstallation(t, p, "CategoryEditor"))
	if e != nil || gate == nil || gate.Revision != c.Revision {
		t.Fatal("clone changed source", e)
	}
}
func TestPluginUpdateReviewRequiredRecordIntegrity(t *testing.T) {
	for _, kind := range []string{"missing", "revision", "generation", "config"} {
		t.Run(kind, func(t *testing.T) {
			p, _ := updateReviewFixture(t)
			ctx := context.Background()
			in := lifecycleInstallation(t, p, "CategoryEditor")
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(in.Manifest, &fields)
			var r pluginUpdateReview
			_ = json.Unmarshal(fields[pluginUpdateReviewField], &r)
			switch kind {
			case "missing":
				delete(fields, pluginUpdateReviewField)
			case "revision":
				r.Revision++
				fields[pluginUpdateReviewField], _ = json.Marshal(r)
			case "generation":
				r.Generation++
				fields[pluginUpdateReviewField], _ = json.Marshal(r)
			case "config":
				r.ConfigSHA = deferredInputSHA([]byte(`{}`))
				fields[pluginUpdateReviewField], _ = json.Marshal(r)
			}
			in.Manifest, _ = json.Marshal(fields)
			if _, e := p.pluginUpdateReviewRead(ctx, p.store.DB, in); e == nil {
				t.Fatal("invalid review accepted")
			}
		})
	}
	_, p := pluginStoreFixture(t)
	installCloneSource(t, p, builtin.CategoryEditorManifest())
	if e := p.requirePluginUpdateActivation(context.Background(), p.store.DB, "CategoryEditor"); e != nil {
		t.Fatal("ordinary installation incorrectly gated", e)
	}
}
func TestPluginUpdateReviewUninstallRetainsResetClears(t *testing.T) {
	p, _ := updateReviewFixture(t)
	ctx := context.Background()
	id := "CategoryEditor"
	if e := p.Uninstall(ctx, id); e != nil {
		t.Fatal(e)
	}
	in := lifecycleInstallation(t, p, id)
	gate, e := p.pluginUpdateReviewRead(ctx, p.store.DB, in)
	if e != nil || gate == nil || in.Installed {
		t.Fatal("uninstall lost gate", e)
	}
	if e = p.requirePluginUpdateActivation(ctx, p.store.DB, id); e == nil {
		t.Fatal("uninstall bypass")
	}
	if e = p.Install(ctx, PluginInstallation{InstanceID: id, PluginID: in.PluginID, Version: in.Version, ManifestSchemaVersion: in.ManifestSchemaVersion, Manifest: in.Manifest}); e == nil {
		t.Fatal("reinstall bypassed retained review")
	}
	if e = p.Reset(ctx, id); e != nil {
		t.Fatal("trusted Store tombstone Reset regressed", e)
	}
	in = lifecycleInstallation(t, p, id)
	if gate, e = p.pluginUpdateReviewRead(ctx, p.store.DB, in); e != nil || gate != nil || in.Installed {
		t.Fatal("tombstone reset retained intent or installed", e)
	}
	if _, e = p.Config(ctx, id); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("tombstone reset retained config", e)
	}
	resetStore, _ := updateReviewFixture(t)
	if e = resetStore.Reset(ctx, id); e != nil {
		t.Fatal(e)
	}
	in = lifecycleInstallation(t, resetStore, id)
	if gate, e = resetStore.pluginUpdateReviewRead(ctx, resetStore.store.DB, in); e != nil || gate != nil {
		t.Fatal("installed reset retained removed intent", e)
	}
	// A concurrent install after an inactive snapshot must not be cleared.
	concurrent, _ := updateReviewFixture(t)
	if e = concurrent.Uninstall(ctx, id); e != nil {
		t.Fatal(e)
	}
	inactive := lifecycleInstallation(t, concurrent, id)
	before, sealed := pluginDefaultsStoredConfig(t, concurrent, id)
	admission, proof, e := concurrent.beginResetAdmission(ctx, inactive, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = concurrent.store.DB.Exec(`UPDATE plugin_installations SET installed=1 WHERE instance_id=?`, id); e != nil {
		t.Fatal(e)
	}
	e = concurrent.Reset(admission, id)
	if pluginErrorCode(e) != "PLUGIN_SNAPSHOT_STALE" {
		t.Fatal("installed-state CAS missed", e)
	}
	if final := proof.finish(e); !errors.Is(final, e) {
		t.Fatal(final)
	}
	assertPluginDefaultsConfigUnchanged(t, concurrent, id, before, sealed)

}

func TestPluginUpdateReviewActualServerExplicitSaveAfterRestart(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	cookie := setupTestAdmin(t, a)
	transferServerBinary.once.Do(func() { transferServerBinary.path, transferServerBinary.err = buildTransferFixture(true) })
	if transferServerBinary.err != nil {
		t.Fatal(transferServerBinary.err)
	}
	a.pluginCommand = func(id string) (plugins.Command, error) {
		return plugins.Command{Path: transferServerBinary.path, Args: []string{"--plugin-worker", id}}, nil
	}
	installCloneSource(t, a.pluginStore, builtin.CategoryEditorManifest())
	if _, e := a.pluginStore.SaveConfig(ctx, "CategoryEditor", 0, map[string]any{"enabled": true, "content": customJapaneseMovieCategory}); e != nil {
		t.Fatal(e)
	}
	request := updateOldCategory(t, a.pluginStore, "CategoryEditor")
	body, _ := json.Marshal(request)
	if w := callTest(t, a.Handler(), "POST", "/api/plugins/CategoryEditor/update", string(body), cookie, ""); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"review_pending":true`)) {
		t.Fatal(w.Code, w.Body.String())
	}
	// A fresh Store facade reads only durable gate evidence, not process memory.
	fresh, e := NewPluginStore(ctx, a.Store)
	if e != nil {
		t.Fatal(e)
	}
	if e = fresh.requirePluginUpdateActivation(ctx, a.Store.DB, "CategoryEditor"); pluginErrorCode(e) != "PLUGIN_UPDATE_REVIEW_REQUIRED" {
		t.Fatal("cold recovery", e)
	}
	if e = a.initializeNativePlugins(ctx); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id='CategoryEditor'`).Scan(&n); e != nil || n != 0 {
		t.Fatal("restart enqueued old intent", n, e)
	}
	if _, e = os.Stat(filepath.Join(a.Dir, "category.yaml")); !os.IsNotExist(e) {
		t.Fatal("old intent replayed", e)
	}
	if w := callTest(t, a.Handler(), "GET", "/api/plugins/CategoryEditor/form", "", cookie, ""); w.Code != 200 {
		t.Fatal("review settings inaccessible", w.Code, w.Body.String())
	}
	stored, e := a.pluginStore.Config(ctx, "CategoryEditor")
	if e != nil {
		t.Fatal(e)
	}
	save, _ := json.Marshal(map[string]any{"revision": stored.Revision, "config": map[string]any{"enabled": true, "content": customJapaneseMovieCategory}})
	if w := callTest(t, a.Handler(), "PUT", "/api/plugins/CategoryEditor/config", string(save), cookie, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	run, e := a.pluginStore.ClaimRun(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.executePluginRun(ctx, run); e != nil {
		t.Fatal(e)
	}
	actual, e := a.pluginStore.Run(ctx, run.ID)
	if e != nil || actual.State != "succeeded" || string(actual.Result) != `{"enabled":false,"applied":true}` {
		t.Fatal(actual.State, actual.ErrorCode, e)
	}
	data, e := os.ReadFile(filepath.Join(a.Dir, "category.yaml"))
	if e != nil || string(data) != customJapaneseMovieCategory {
		t.Fatal("explicit save did not apply", e)
	}
	gate, e := a.pluginStore.pluginUpdateReviewRead(ctx, a.Store.DB, lifecycleInstallation(t, a.pluginStore, "CategoryEditor"))
	if e != nil || gate != nil {
		t.Fatal("gate remained", e)
	}
	executionLeaseAssertCount(t, a.pluginStore, 0)
	if w := callTest(t, a.Handler(), "POST", "/api/plugins/CategoryEditor/uninstall", "{}", cookie, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	beforeHTTPReset := lifecycleInstallation(t, a.pluginStore, "CategoryEditor")
	if w := callTest(t, a.Handler(), "POST", "/api/plugins/CategoryEditor/reset", "{}", cookie, ""); w.Code != 404 {
		t.Fatal("HTTP tombstone boundary widened", w.Code, w.Body.String())
	}
	afterHTTPReset := lifecycleInstallation(t, a.pluginStore, "CategoryEditor")
	if beforeHTTPReset.Generation != afterHTTPReset.Generation || !bytes.Equal(beforeHTTPReset.Manifest, afterHTTPReset.Manifest) {
		t.Fatal("rejected HTTP Reset mutated tombstone")
	}
}

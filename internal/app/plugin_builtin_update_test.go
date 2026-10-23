package app

import (
	"bytes"
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins/builtin"
)

func updateOldCategory(t *testing.T, p *PluginStore, instance string) pluginUpdateRequest {
	t.Helper()
	ctx := context.Background()
	in, err := p.Installation(ctx, instance)
	if err != nil {
		t.Fatal(err)
	}
	// A persisted previous-binary installation, not a second executable catalog.
	var fields map[string]json.RawMessage
	if json.Unmarshal(in.Manifest, &fields) != nil {
		t.Fatal("manifest")
	}
	fields["version"] = json.RawMessage(`"1.2"`)
	in.Manifest, _ = json.Marshal(fields)
	in.Version = "1.2"
	in.Manifest, err = pluginLifecycleManifest(in, pluginLifecycleCold)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := p.seal("manifest", instance, "main", in.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.store.DB.ExecContext(ctx, `UPDATE plugin_installations SET version='1.2',manifest=? WHERE instance_id=?`, sealed, instance); err != nil {
		t.Fatal(err)
	}
	config, err := p.Config(ctx, instance)
	if err != nil {
		t.Fatal(err)
	}
	return pluginUpdateRequest{FromVersion: "1.2", TargetVersion: "1.3", Generation: in.Generation, Revision: config.Revision}
}
func updateCategoryFixture(t *testing.T) (*PluginStore, pluginUpdateRequest) {
	t.Helper()
	_, p := pluginStoreFixture(t)
	installCloneSource(t, p, builtin.CategoryEditorManifest())
	if _, err := p.SaveConfig(context.Background(), "CategoryEditor", 0, json.RawMessage(`{"enabled":false,"content":"movie: {}","unknown":9007199254740993}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SaveData(context.Background(), "CategoryEditor", "history", 0, map[string]any{"owned": true}); err != nil {
		t.Fatal(err)
	}
	return p, updateOldCategory(t, p, "CategoryEditor")
}
func TestPluginBuiltinUpdatePreservesConfigDataAndQueuesTarget(t *testing.T) {
	p, request := updateCategoryFixture(t)
	ctx := context.Background()
	before, sealed := pluginDefaultsStoredConfig(t, p, "CategoryEditor")
	data, _ := p.Data(ctx, "CategoryEditor", "history")
	out, err := p.updateBuiltin(ctx, "CategoryEditor", request)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Changed || !out.MigrationCommitted || out.Version != "1.3" || out.Generation != request.Generation+1 || out.Initialization == nil || out.Initialization.Version != "1.3" {
		t.Fatal(out)
	}
	assertPluginDefaultsConfigUnchanged(t, p, "CategoryEditor", before, sealed)
	after, _ := p.Data(ctx, "CategoryEditor", "history")
	if after.Revision != data.Revision || !bytes.Equal(after.Value, data.Value) {
		t.Fatal("data lost")
	}
	in := lifecycleInstallation(t, p, "CategoryEditor")
	lifecycleWantMode(t, p, in, pluginFormInstalledCold)
}
func TestPluginBuiltinUpdateSameVersionNoOpWithActiveRun(t *testing.T) {
	_, p := pluginStoreFixture(t)
	installCloneSource(t, p, builtin.CategoryEditorManifest())
	ctx := context.Background()
	_, queued, err := p.SaveConfigAndQueue(ctx, "CategoryEditor", 0, map[string]any{"enabled": false})
	if err != nil {
		t.Fatal(err)
	}
	run := executionLeaseClaim(t, p, queued)
	runctx := executionLeaseActivate(t, p, run)
	before := lifecycleInstallation(t, p, "CategoryEditor")
	out, err := p.updateBuiltin(ctx, "CategoryEditor", pluginUpdateRequest{TargetVersion: "1.3"})
	if err != nil || out.Changed || out.Initialization != nil {
		t.Fatal(out, err)
	}
	if runctx.Err() != nil {
		t.Fatal("same version canceled run")
	}
	after := lifecycleInstallation(t, p, "CategoryEditor")
	if !bytes.Equal(before.Manifest, after.Manifest) || before.Generation != after.Generation {
		t.Fatal("no-op mutated identity")
	}
	executionLeaseFinish(t, p, runctx, run)
	if err = p.releaseSupervisedRun(run); err != nil {
		t.Fatal(err)
	}
}
func TestPluginBuiltinUpdateRollbackPreservesAllOldBytes(t *testing.T) {
	p, request := updateCategoryFixture(t)
	ctx := context.Background()
	before := lifecycleInstallation(t, p, "CategoryEditor")
	config, sealed := pluginDefaultsStoredConfig(t, p, "CategoryEditor")
	data, _ := p.Data(ctx, "CategoryEditor", "history")
	if _, err := p.store.DB.Exec(`CREATE TRIGGER owned_update_abort BEFORE INSERT ON plugin_runs WHEN NEW.version='1.3' BEGIN SELECT RAISE(ABORT,'owned migration rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if out, err := p.updateBuiltin(ctx, "CategoryEditor", request); err == nil || out.MigrationCommitted {
		t.Fatal("rollback reported success", out, err)
	}
	after := lifecycleInstallation(t, p, "CategoryEditor")
	if after.Version != before.Version || after.Generation != before.Generation || !bytes.Equal(after.Manifest, before.Manifest) {
		t.Fatal("rollback changed installation")
	}
	assertPluginDefaultsConfigUnchanged(t, p, "CategoryEditor", config, sealed)
	afterData, _ := p.Data(ctx, "CategoryEditor", "history")
	if !bytes.Equal(data.Value, afterData.Value) {
		t.Fatal("rollback changed data")
	}
	p.runSupervisor.mu.Lock()
	fenced := len(p.runSupervisor.fences)
	p.runSupervisor.mu.Unlock()
	if fenced != 0 {
		t.Fatal("known SQL rollback kept fence")
	}
}
func TestPluginBuiltinUpdateRejectsStaleAndUnsupported(t *testing.T) {
	for _, kind := range []string{"generation", "revision", "target", "from", "config"} {
		t.Run(kind, func(t *testing.T) {
			p, r := updateCategoryFixture(t)
			before := lifecycleInstallation(t, p, "CategoryEditor")
			switch kind {
			case "generation":
				r.Generation++
			case "revision":
				r.Revision++
			case "target":
				r.TargetVersion = "9.9"
			case "from":
				r.FromVersion = "1.0"
			case "config":
				if _, e := p.SaveConfig(context.Background(), "CategoryEditor", r.Revision, map[string]any{"enabled": "not-bool"}); e != nil {
					t.Fatal(e)
				}
				r.Revision++
			}
			if out, err := p.updateBuiltin(context.Background(), "CategoryEditor", r); err == nil || out.Changed {
				t.Fatal("unsupported update accepted", out, err)
			}
			after := lifecycleInstallation(t, p, "CategoryEditor")
			if after.Version != before.Version || after.Generation != before.Generation {
				t.Fatal("rejection mutated installation")
			}
		})
	}
}
func TestPluginBuiltinUpdateFormalCloneIsolation(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	installCloneSource(t, p, builtin.CategoryEditorManifest())
	if _, err := p.SaveConfig(ctx, "CategoryEditor", 0, map[string]any{"enabled": false, "content": "owned source"}); err != nil {
		t.Fatal(err)
	}
	a, err := p.Clone(ctx, "CategoryEditor", PluginCloneRequest{Suffix: "A", Name: "owned A", Version: "display-9"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Clone(ctx, "CategoryEditor", PluginCloneRequest{Suffix: "B", Name: "owned B"})
	if err != nil {
		t.Fatal(err)
	}
	request := updateOldCategory(t, p, a.InstanceID)
	_ = updateOldCategory(t, p, b.InstanceID)
	beforeB := lifecycleInstallation(t, p, b.InstanceID)
	meta, err := p.instanceMetadata(ctx, p.store.DB, a.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.updateBuiltin(ctx, a.InstanceID, request)
	if err != nil || out.Initialization != nil {
		t.Fatal("cold clone autoactivated", out, err)
	}
	afterB := lifecycleInstallation(t, p, b.InstanceID)
	if !bytes.Equal(beforeB.Manifest, afterB.Manifest) || beforeB.Version != afterB.Version {
		t.Fatal("sibling changed")
	}
	afterMeta, err := p.instanceMetadata(ctx, p.store.DB, a.InstanceID)
	if err != nil || afterMeta != meta {
		t.Fatal("lineage/display changed", err)
	}
	source := lifecycleInstallation(t, p, "CategoryEditor")
	if source.Generation != 1 || source.Version != "1.3" {
		t.Fatal("source changed")
	}
}
func TestPluginBuiltinUpdateActualAuthenticatedHTTP(t *testing.T) {
	a := newTestApp(t)
	cookie := setupTestAdmin(t, a)
	in := pluginFormHTTPInstallation(t, a, "CategoryEditor", json.RawMessage(`{"enabled":false,"content":"owned"}`))
	request := updateOldCategory(t, a.pluginStore, in.InstanceID)
	raw, _ := json.Marshal(request)
	path := "/api/plugins/CategoryEditor/update"
	if w := callTest(t, a.Handler(), http.MethodPost, path, string(raw), nil, ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := callTest(t, a.Handler(), http.MethodPost, path, string(raw), cookie, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	current := lifecycleInstallation(t, a.pluginStore, in.InstanceID)
	if current.Version != "1.3" {
		t.Fatal(current.Version)
	}
}

func TestPluginBuiltinUpdateActualServerInitializesTarget(t *testing.T) {
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
	if _, err := a.pluginStore.SaveConfig(ctx, "CategoryEditor", 0, map[string]any{"enabled": false, "content": customJapaneseMovieCategory}); err != nil {
		t.Fatal(err)
	}
	request := updateOldCategory(t, a.pluginStore, "CategoryEditor")
	raw, _ := json.Marshal(request)
	if w := callTest(t, a.Handler(), "POST", "/api/plugins/CategoryEditor/update", string(raw), cookie, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(a.Dir, "category.yaml")); !os.IsNotExist(err) {
		t.Fatal("migration transaction executed business effect", err)
	}
	run, err := a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.executePluginRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	initial, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || initial.State != "succeeded" || string(initial.Result) != `{"enabled":false,"applied":false}` {
		t.Fatal("safe upgrade init", initial.State, err)
	}
	if _, err = os.Stat(filepath.Join(a.Dir, "category.yaml")); !os.IsNotExist(err) {
		t.Fatal("safe init replayed one-shot", err)
	}
	stored, err := a.pluginStore.Config(ctx, "CategoryEditor")
	if err != nil {
		t.Fatal(err)
	}
	save, _ := json.Marshal(map[string]any{"revision": stored.Revision, "config": map[string]any{"enabled": true, "content": customJapaneseMovieCategory}})
	if w := callTest(t, a.Handler(), "PUT", "/api/plugins/CategoryEditor/config", string(save), cookie, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	run, err = a.pluginStore.ClaimRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = a.executePluginRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	actual, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || actual.State != "succeeded" || string(actual.Result) != `{"enabled":false,"applied":true}` {
		t.Fatal(actual.State, actual.ErrorCode, err)
	}
	data, err := os.ReadFile(filepath.Join(a.Dir, "category.yaml"))
	if err != nil || string(data) != customJapaneseMovieCategory {
		t.Fatal("target worker failed real owned file effect", err)
	}
	config, err := a.pluginStore.Config(ctx, "CategoryEditor")
	if err != nil || !bytes.Contains(config.Value, []byte(`"enabled":false`)) {
		t.Fatal("1.3 clear flag missing", err)
	}
	executionLeaseAssertCount(t, a.pluginStore, 0)
}

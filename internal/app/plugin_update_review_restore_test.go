package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"light-media/internal/plugins/builtin"
)

func reviewRestoreFixture(t *testing.T) (*PluginStore, PluginInstallation, pluginReviewRestoreRequest) {
	t.Helper()
	p, c := updateReviewFixture(t)
	if err := p.Uninstall(context.Background(), "CategoryEditor"); err != nil {
		t.Fatal(err)
	}
	in := lifecycleInstallation(t, p, "CategoryEditor")
	return p, in, pluginReviewRestoreRequest{in.Version, in.Generation, c.Revision}
}
func reviewRestoreValues(t *testing.T, p *PluginStore, id string) []byte {
	t.Helper()
	rows, err := p.store.DB.Query(`SELECT kind,item_key,revision,sealed FROM plugin_values WHERE instance_id=? ORDER BY kind,item_key`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var values []any
	for rows.Next() {
		var kind, key string
		var revision int64
		var sealed []byte
		if err = rows.Scan(&kind, &key, &revision, &sealed); err != nil {
			t.Fatal(err)
		}
		values = append(values, []any{kind, key, revision, sealed})
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestPluginReviewRestorePreservesIntentWithoutExecution(t *testing.T) {
	p, before, request := reviewRestoreFixture(t)
	ctx := context.Background()
	values := reviewRestoreValues(t, p, before.InstanceID)
	var runsBefore int
	if err := p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runsBefore); err != nil {
		t.Fatal(err)
	}
	out, err := p.restoreReviewedPlugin(ctx, before.InstanceID, request)
	if err != nil || !out.Restored || !out.ReviewPending || out.Generation != before.Generation+1 || out.Version != before.Version {
		t.Fatal(out, err)
	}
	after := lifecycleInstallation(t, p, before.InstanceID)
	if !after.Installed || after.DesiredEnabled != before.DesiredEnabled || !bytes.Equal(values, reviewRestoreValues(t, p, before.InstanceID)) {
		t.Fatal("restore changed configuration/KV/intent")
	}
	gate, err := p.pluginUpdateReviewRead(ctx, p.store.DB, after)
	if err != nil || gate == nil || gate.Generation != after.Generation || gate.Revision != request.Revision {
		t.Fatal(gate, err)
	}
	var runsAfter int
	if err = p.store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs`).Scan(&runsAfter); err != nil || runsAfter != runsBefore {
		t.Fatal("restore queued work", runsAfter, err)
	}
	if _, err = p.EnqueueRun(ctx, after.InstanceID, "init", nil); err == nil {
		t.Fatal("restore released review without explicit save")
	}
	repeat, err := p.restoreReviewedPlugin(ctx, after.InstanceID, request)
	if err == nil || repeat.Restored {
		t.Fatal("duplicate restore rebuilt generation", repeat, err)
	}
	again := lifecycleInstallation(t, p, after.InstanceID)
	if again.Generation != after.Generation || !bytes.Equal(again.Manifest, after.Manifest) {
		t.Fatal("duplicate changed installation")
	}
	// Only the existing authenticated configuration save may release this gate.
	auth := context.WithValue(ctx, pluginConfigSaveRequestKey{}, after.InstanceID)
	_, run, err := p.SaveConfigAndQueue(auth, after.InstanceID, request.Revision, map[string]any{"enabled": false, "content": "movie: {}"})
	if err != nil || run.InstanceID != after.InstanceID || run.Action != "init" {
		t.Fatal(run, err)
	}
}
func TestPluginReviewRestoreFormalCloneKeepsIdentity(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	installCloneSource(t, p, builtin.CategoryEditorManifest())
	if _, err := p.SaveConfig(ctx, "CategoryEditor", 0, map[string]any{"enabled": false, "content": "owned source"}); err != nil {
		t.Fatal(err)
	}
	clone, err := p.Clone(ctx, "CategoryEditor", PluginCloneRequest{Suffix: "Restore", Name: "owned restore clone"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := p.Config(ctx, clone.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.SaveConfig(ctx, clone.InstanceID, cfg.Revision, map[string]any{"enabled": true, "content": "owned clone"}); err != nil {
		t.Fatal(err)
	}
	update := updateOldCategory(t, p, clone.InstanceID)
	if out, err := p.updateBuiltin(ctx, clone.InstanceID, update); err != nil || !out.ReviewPending {
		t.Fatal(out, err)
	}
	if err = p.Uninstall(ctx, clone.InstanceID); err != nil {
		t.Fatal(err)
	}
	before := lifecycleInstallation(t, p, clone.InstanceID)
	metadata, err := p.instanceMetadata(ctx, p.store.DB, clone.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	source := lifecycleInstallation(t, p, "CategoryEditor")
	sourceValues := reviewRestoreValues(t, p, source.InstanceID)
	cloneValues := reviewRestoreValues(t, p, clone.InstanceID)
	cfg, err = p.Config(ctx, clone.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := p.restoreReviewedPlugin(ctx, clone.InstanceID, pluginReviewRestoreRequest{before.Version, before.Generation, cfg.Revision}); err != nil || !out.Restored {
		t.Fatal(out, err)
	}
	after := lifecycleInstallation(t, p, clone.InstanceID)
	afterMetadata, err := p.instanceMetadata(ctx, p.store.DB, clone.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(metadata)
	b, _ := json.Marshal(afterMetadata)
	sourceAfter := lifecycleInstallation(t, p, source.InstanceID)
	if !bytes.Equal(a, b) || after.Generation != before.Generation+1 || !bytes.Equal(cloneValues, reviewRestoreValues(t, p, clone.InstanceID)) || !bytes.Equal(sourceValues, reviewRestoreValues(t, p, source.InstanceID)) || !bytes.Equal(source.Manifest, sourceAfter.Manifest) {
		t.Fatal("clone restore crossed namespace or lost lineage")
	}
}
func TestPluginReviewRestoreRejectsStaleOrCorrupt(t *testing.T) {
	for _, kind := range []string{"version", "generation", "revision", "manifest_schema", "missing_review", "config", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			p, in, request := reviewRestoreFixture(t)
			ctx := context.Background()
			switch kind {
			case "version":
				request.Version = "9.9"
			case "generation":
				request.Generation++
			case "revision":
				request.Revision++
			case "manifest_schema":
				if _, err := p.store.DB.Exec(`UPDATE plugin_installations SET manifest_schema=999 WHERE instance_id=?`, in.InstanceID); err != nil {
					t.Fatal(err)
				}
			case "missing_review":
				lifecycleEditManifest(t, p, in.InstanceID, func(fields map[string]json.RawMessage) { delete(fields, pluginUpdateReviewField) })
			case "config":
				if _, err := p.store.DB.Exec(`UPDATE plugin_values SET sealed=? WHERE instance_id=? AND kind='config'`, []byte("owned corrupt"), in.InstanceID); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			before := lifecycleInstallation(t, p, in.InstanceID)
			values := reviewRestoreValues(t, p, in.InstanceID)
			out, err := p.restoreReviewedPlugin(ctx, in.InstanceID, request)
			if err == nil || out.Restored {
				t.Fatal("invalid restore accepted", out, err)
			}
			after := lifecycleInstallation(t, p, in.InstanceID)
			if before.Generation != after.Generation || before.Installed != after.Installed || !bytes.Equal(before.Manifest, after.Manifest) || !bytes.Equal(values, reviewRestoreValues(t, p, in.InstanceID)) {
				t.Fatal("rejected restore changed bytes")
			}
		})
	}
}
func TestPluginReviewRestoreRollbackAndNoGenericInstallBypass(t *testing.T) {
	p, in, request := reviewRestoreFixture(t)
	ctx := context.Background()
	values := reviewRestoreValues(t, p, in.InstanceID)
	if err := p.Install(ctx, in); err == nil {
		t.Fatal("ordinary install bypassed held gate")
	}
	if _, err := p.store.DB.Exec(`CREATE TRIGGER owned_restore_sequence_failure BEFORE INSERT ON plugin_install_sequence BEGIN SELECT RAISE(ABORT,'owned restore sequence failure'); END`); err != nil {
		t.Fatal(err)
	}
	out, err := p.restoreReviewedPlugin(ctx, in.InstanceID, request)
	if err == nil || !strings.Contains(err.Error(), "owned restore sequence failure") || out.Restored {
		t.Fatal("did not reach late install rollback", out, err)
	}
	after := lifecycleInstallation(t, p, in.InstanceID)
	if after.Generation != in.Generation || after.Installed || !bytes.Equal(after.Manifest, in.Manifest) || !bytes.Equal(values, reviewRestoreValues(t, p, in.InstanceID)) {
		t.Fatal("partial restore committed")
	}
	p.runSupervisor.mu.Lock()
	fences := len(p.runSupervisor.fences)
	p.runSupervisor.mu.Unlock()
	if fences != 0 {
		t.Fatal("known rollback leaked fence")
	}
	if _, err = p.store.DB.Exec(`DROP TRIGGER owned_restore_sequence_failure`); err != nil {
		t.Fatal(err)
	}
	if out, err = p.restoreReviewedPlugin(ctx, in.InstanceID, request); err != nil || !out.Restored {
		t.Fatal("rollback prevented later explicit restore", out, err)
	}
}
func TestPluginReviewRestorePermitIsTransactionBound(t *testing.T) {
	p, in, request := reviewRestoreFixture(t)
	ctx := context.Background()
	target, review, err := p.reviewedRestoreTarget(ctx, p.store.DB, in, request)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	permit := &pluginReviewRestorePermit{store: p, tx: tx, previous: in, target: target, review: *review}
	// Even a host-local struct without its captured fence/join cannot authorize install.
	if err = p.installTxWithReviewRestore(ctx, tx, target, permit); err == nil {
		t.Fatal("unadmitted permit authorized restore")
	}
	if permit.used {
		t.Fatal("failed admission consumed permit")
	}
}

func TestPluginReviewRestoreRequiresHeldTombstoneAndQuiescence(t *testing.T) {
	t.Run("ordinary tombstone", func(t *testing.T) {
		_, p := pluginStoreFixture(t)
		ctx := context.Background()
		installCloneSource(t, p, builtin.CategoryEditorManifest())
		c, err := p.SaveConfig(ctx, "CategoryEditor", 0, map[string]any{"enabled": false})
		if err != nil {
			t.Fatal(err)
		}
		if err = p.Uninstall(ctx, "CategoryEditor"); err != nil {
			t.Fatal(err)
		}
		in := lifecycleInstallation(t, p, "CategoryEditor")
		if out, err := p.restoreReviewedPlugin(ctx, in.InstanceID, pluginReviewRestoreRequest{in.Version, in.Generation, c.Revision}); err == nil || out.Restored {
			t.Fatal("ordinary tombstone acquired review restore", out, err)
		}
	})
	t.Run("existing fence", func(t *testing.T) {
		p, in, request := reviewRestoreFixture(t)
		ctx := context.Background()
		_, admission, err := p.beginResetAdmission(ctx, in, false)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if e := admission.finish(context.Canceled); e == nil {
				t.Error("expected original transition cancellation")
			}
		}()
		if out, err := p.restoreReviewedPlugin(ctx, in.InstanceID, request); err == nil || out.Restored {
			t.Fatal("overlapping transition restored", out, err)
		}
		after := lifecycleInstallation(t, p, in.InstanceID)
		if after.Installed || after.Generation != in.Generation || !bytes.Equal(after.Manifest, in.Manifest) {
			t.Fatal("busy restore mutated tombstone")
		}
	})
}

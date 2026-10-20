package app

import (
	"bytes"
	"context"
	"testing"
)

func TestBrushFlowIndependentHeadRejectsPartialGenerationRollback(t *testing.T) {
	a, in := brushDefault(t)
	ctx := context.Background()
	if e := a.pluginStore.Reset(ctx, in.InstanceID); e != nil {
		t.Fatal(e)
	}
	latest, e := a.pluginStore.Installation(ctx, in.InstanceID)
	if e != nil || latest.Generation != in.Generation+1 {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`UPDATE plugin_installations SET generation=? WHERE instance_id=?`, in.Generation, in.InstanceID); e != nil {
		t.Fatal(e)
	}
	for _, audit := range []bool{false, true} {
		if _, _, e = a.pluginStore.brushFlowReadMode(ctx, a.Store.DB, in.InstanceID, in.Generation, audit); e == nil {
			t.Fatal("old valid root selected by plaintext rollback", audit)
		}
	}
	if _, e = a.pluginStore.EnqueueRun(ctx, in.InstanceID, "run", nil); e == nil {
		t.Fatal("rollback admitted mutation")
	}
	if e = a.pluginStore.Reset(ctx, in.InstanceID); e == nil {
		t.Fatal("rollback retired wrong generation")
	}
}
func TestBrushFlowHeadDeletionCorruptionAndMissingInstallationFailClosed(t *testing.T) {
	for _, damage := range []string{"delete", "ciphertext", "installation"} {
		t.Run(damage, func(t *testing.T) {
			a, in := brushDefault(t)
			ctx := context.Background()
			query := `DELETE FROM plugin_brush_flow_heads WHERE instance_id=?`
			if damage == "ciphertext" {
				query = `UPDATE plugin_brush_flow_heads SET sealed=x'00' WHERE instance_id=?`
			}
			if damage == "installation" {
				if _, e := a.Store.DB.Exec(`PRAGMA foreign_keys=OFF`); e != nil {
					t.Fatal(e)
				}
				defer a.Store.DB.Exec(`PRAGMA foreign_keys=ON`)
				query = `DELETE FROM plugin_installations WHERE instance_id=?`
			}
			if _, e := a.Store.DB.Exec(query, in.InstanceID); e != nil {
				t.Fatal(e)
			}
			if _, _, e := a.pluginStore.brushFlowReadCurrent(ctx, a.Store.DB, in.InstanceID, in.Generation); e == nil {
				t.Fatal("damaged head accepted")
			}
			tx, e := a.Store.DB.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			if _, e = a.pluginStore.brushFlowTransitionParentTx(ctx, tx, in.InstanceID); e == nil {
				t.Fatal("damaged authority allowed lifecycle genesis")
			}
		})
	}
}
func TestBrushFlowHeadAndGenerationCommitAtomically(t *testing.T) {
	a, in := brushDefault(t)
	ctx := context.Background()
	p := a.pluginStore
	_, before, e := p.brushFlowHeadRead(ctx, a.Store.DB, in.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`CREATE TRIGGER owned_head_rollback BEFORE UPDATE ON plugin_brush_flow_heads BEGIN SELECT RAISE(ABORT,'owned head rollback'); END`); e != nil {
		t.Fatal(e)
	}
	if e = p.Reset(ctx, in.InstanceID); e == nil {
		t.Fatal("head failure committed lifecycle")
	}
	current, _ := p.Installation(ctx, in.InstanceID)
	_, after, e := p.brushFlowHeadRead(ctx, a.Store.DB, in.InstanceID)
	if e != nil || current.Generation != in.Generation || !bytes.Equal(before, after) {
		t.Fatal("split generation/head", e, current.Generation)
	}
	if _, _, e = p.brushFlowRead(ctx, a.Store.DB, in.InstanceID, in.Generation); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`DROP TRIGGER owned_head_rollback`); e != nil {
		t.Fatal(e)
	}
	if e = p.Reset(ctx, in.InstanceID); e != nil {
		t.Fatal(e)
	}
	// Old generation remains readable for its original identities, but cannot be
	// rewritten after its digest became the new head's sealed parent.
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	old, prior, e := p.brushFlowRead(ctx, tx, in.InstanceID, in.Generation)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.brushFlowSave(ctx, tx, old, prior); e == nil {
		t.Fatal("retired ancestor rewritten")
	}
}

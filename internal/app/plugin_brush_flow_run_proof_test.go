package app

import (
	"bytes"
	"context"
	bf "light-media/internal/plugins/brushflow"
	"light-media/internal/plugins/builtin"
	"testing"
)

func TestBrushFlowActualLiveProofDoesNotCacheCiphertextMetadataOrHead(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	uninstallInstall(t, a, "foreign-owner", builtin.SystemProcessManifest(), map[string]any{}, false)
	observed := 0
	a.pluginBrushFlow.afterProof = func(ctx context.Context, c *brushFlowCall) {
		observed++
		p := a.pluginStore
		if current, ok := p.brushFlowRunProofCall(ctx, c.run.ID); !ok || current != c {
			t.Fatal("actual SDK did not bind private live proof")
		}
		if _, ok := p.brushFlowRunProofCall(context.Background(), c.run.ID); ok {
			t.Fatal("cold caller acquired private proof")
		}
		other := c.run.ID + "other"
		if _, ok := p.brushFlowRunProofCall(ctx, other); ok {
			t.Fatal("foreign identity acquired private proof")
		}
		actual, e := p.brushFlowLoadRun(ctx, a.Store.DB, c.run.ID)
		if e != nil || !bytes.Equal(actual.Input, c.run.Input) || c.Guard(ctx) != nil {
			t.Fatal("actual admitted proof rejected", e)
		}
		var input []byte
		if e = a.Store.DB.QueryRow(`SELECT input FROM plugin_runs WHERE id=?`, c.run.ID).Scan(&input); e != nil {
			t.Fatal(e)
		}
		changed := append([]byte(nil), input...)
		changed[len(changed)-1] ^= 1
		if _, e = a.Store.DB.Exec(`UPDATE plugin_runs SET input=? WHERE id=?`, changed, c.run.ID); e != nil {
			t.Fatal(e)
		}
		if _, e = p.brushFlowLoadRun(ctx, a.Store.DB, c.run.ID); e == nil {
			t.Fatal("cached plaintext masked changed ciphertext")
		}
		if c.Guard(ctx) == nil {
			t.Fatal("changed ciphertext authorized business guard")
		}
		if _, e = a.Store.DB.Exec(`UPDATE plugin_runs SET input=? WHERE id=?`, input, c.run.ID); e != nil {
			t.Fatal(e)
		}
		for _, damage := range []struct {
			statement string
			value     any
			restore   any
		}{{"config_revision", c.run.ConfigRevision + 1, c.run.ConfigRevision}, {"state", "failed", "running"}, {"instance_id", "foreign-owner", c.run.InstanceID}} {
			if _, e = a.Store.DB.Exec(`UPDATE plugin_runs SET `+damage.statement+`=? WHERE id=?`, damage.value, c.run.ID); e != nil {
				t.Fatal(e)
			}
			if c.Guard(ctx) == nil {
				t.Fatal("cached metadata authorized changed SQL", damage.statement)
			}
			if _, e = a.Store.DB.Exec(`UPDATE plugin_runs SET `+damage.statement+`=? WHERE id=?`, damage.restore, c.run.ID); e != nil {
				t.Fatal(e)
			}
		}
		var head []byte
		if e = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_brush_flow_heads WHERE instance_id=?`, c.run.InstanceID).Scan(&head); e != nil {
			t.Fatal(e)
		}
		changed = append([]byte(nil), head...)
		changed[len(changed)-1] ^= 1
		if _, e = a.Store.DB.Exec(`UPDATE plugin_brush_flow_heads SET sealed=? WHERE instance_id=?`, changed, c.run.InstanceID); e != nil {
			t.Fatal(e)
		}
		if c.Guard(ctx) == nil {
			t.Fatal("cached run proof masked changed owner head")
		}
		if _, e = a.Store.DB.Exec(`UPDATE plugin_brush_flow_heads SET sealed=? WHERE instance_id=?`, head, c.run.InstanceID); e != nil {
			t.Fatal(e)
		}
		if e = c.Guard(ctx); e != nil {
			t.Fatal("restored exact evidence rejected", e)
		}
	}
	_, run := brushInstall(t, a, bf.DefaultConfig())
	brushFinish(t, a, run)
	if observed != 1 {
		t.Fatal("actual SDK proof observer", observed)
	}
}

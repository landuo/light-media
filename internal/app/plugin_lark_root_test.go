package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	lark "light-media/internal/plugins/larkmessager"
	larkhost "light-media/internal/plugins/larkmessager/host"
)

func larkRootInstall(t *testing.T, p *PluginStore, instance string) {
	t.Helper()
	raw, e := json.Marshal(lark.Manifest())
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Install(context.Background(), PluginInstallation{InstanceID: instance, PluginID: "LarkMessager", Version: "0.9.1", ManifestSchemaVersion: 1, Manifest: raw}); e != nil {
		t.Fatal(e)
	}
}
func TestPluginLarkActualStoreGenerationRootsSurviveCold(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	p, e := NewPluginStore(ctx, store)
	if e != nil {
		t.Fatal(e)
	}
	larkRootInstall(t, p, "LarkMessager")
	larkRootInstall(t, p, "LarkMessager.other")
	first, e := p.larkRootRead(ctx, store.DB, "LarkMessager", 1)
	if e != nil || first.Root.Sequence != 0 || len(first.Admissions) != 0 {
		t.Fatal(first, e)
	}
	other, e := p.larkRootRead(ctx, store.DB, "LarkMessager.other", 1)
	if e != nil || first.Root.Native.Namespace == other.Root.Native.Namespace {
		t.Fatal("native owner scopes aliased", e)
	}
	if e = p.Reset(ctx, "LarkMessager"); e != nil {
		t.Fatal(e)
	}
	reset, e := p.larkRootRead(ctx, store.DB, "LarkMessager", 2)
	if e != nil || reset.Root.ParentSHA != first.Digest || reset.Root.Event != "reset" {
		t.Fatal("reset did not retain exact prior authority", e)
	}
	if e = p.Uninstall(ctx, "LarkMessager"); e != nil {
		t.Fatal(e)
	}
	larkRootInstall(t, p, "LarkMessager")
	in, e := p.Installation(ctx, "LarkMessager")
	if e != nil || in.Generation != 4 {
		t.Fatal(in, e)
	}
	final, e := p.larkRootRead(ctx, store.DB, "LarkMessager", 4)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.Close(); e != nil {
		t.Fatal(e)
	}
	store, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	p, e = NewPluginStore(ctx, store)
	if e != nil {
		t.Fatal(e)
	}
	cold, e := p.larkRootRead(ctx, store.DB, "LarkMessager", 4)
	if e != nil || cold.Digest != final.Digest {
		t.Fatal("cold changed authority", e)
	}
	tx, e := store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	proofs, e := larkhost.AuditTx(ctx, store.DB, tx, store.key, larkhost.Group{Owner: "LarkMessager", Generation: 4}, pluginLarkAuthority{p, "LarkMessager", 4})
	if e != nil || len(proofs) != 0 {
		t.Fatal("actual lifecycle authority cannot validate native genesis", e)
	}
}
func TestPluginLarkActualLifecycleMissingEvidenceCannotMintGenesis(t *testing.T) {
	for _, kind := range []string{"owner_root", "owner_chain", "native_root", "retired_parent", "metadata_substitution"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			store, p := pluginStoreFixture(t)
			larkRootInstall(t, p, "LarkMessager")
			if kind == "retired_parent" {
				if e := p.Reset(ctx, "LarkMessager"); e != nil {
					t.Fatal(e)
				}
			}
			before, e := p.Installation(ctx, "LarkMessager")
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "owner_root":
				_, e = store.DB.Exec(`DELETE FROM plugin_lark_owner_roots WHERE instance_id='LarkMessager'`)
			case "owner_chain":
				_, e = store.DB.Exec(`DELETE FROM plugin_lark_owner_chain WHERE instance_id='LarkMessager'`)
			case "native_root":
				_, e = store.DB.Exec(`DELETE FROM lark_native_roots`)
			case "retired_parent":
				_, e = store.DB.Exec(`DELETE FROM plugin_lark_owner_roots WHERE instance_id='LarkMessager' AND generation=1`)
			case "metadata_substitution":
				_, e = store.DB.Exec(`UPDATE plugin_installations SET version='0.9.2' WHERE instance_id='LarkMessager'`)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = p.Reset(ctx, "LarkMessager"); e == nil {
				t.Fatal("reset recreated lost evidence", kind)
			}
			after, e := p.Installation(ctx, "LarkMessager")
			if e != nil || after.Generation != before.Generation {
				t.Fatal("failed transition committed", e)
			}
			if e = p.Uninstall(ctx, "LarkMessager"); e == nil {
				t.Fatal("uninstall bypassed lost evidence", kind)
			}
		})
	}
}

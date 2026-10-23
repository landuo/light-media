package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"light-media/internal/plugins/builtin"
)

func serviceManifestRead(t *testing.T, p *PluginStore, instance string) error {
	t.Helper()
	ctx := context.Background()
	tx, err := p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	in, err := p.pluginLifecycleInstallation(ctx, tx, instance)
	if err != nil {
		return err
	}
	manifest, err := p.pluginServiceProviderManifest(ctx, tx, in)
	if err == nil && (manifest.PluginID != in.PluginID || manifest.Version != in.Version || manifest.Name == "") {
		t.Fatal("provider identity changed", manifest)
	}
	return err
}

func TestPluginServiceProviderAuthenticatedHostFields(t *testing.T) {
	for _, mode := range []string{"cold", "started", "legacy", "clone", "review"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			_, p := pluginStoreFixture(t)
			installCloneSource(t, p, builtin.CategoryEditorManifest())
			if _, err := p.SaveConfig(ctx, "CategoryEditor", 0, map[string]any{"enabled": false}); err != nil {
				t.Fatal(err)
			}
			id := "CategoryEditor"
			switch mode {
			case "started":
				if _, err := p.EnqueueRun(ctx, id, "init", nil); err != nil {
					t.Fatal(err)
				}
				run, err := p.ClaimRun(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err = finishClaimedFixtureRun(ctx, p, run, "succeeded", "", nil); err != nil {
					t.Fatal(err)
				}
			case "legacy":
				lifecycleEditManifest(t, p, id, func(f map[string]json.RawMessage) { delete(f, pluginLifecycleField) })
			case "clone":
				in, err := p.Clone(ctx, id, PluginCloneRequest{Suffix: "Service", Name: "owned clone"})
				if err != nil {
					t.Fatal(err)
				}
				id = in.InstanceID
			case "review":
				cfg, err := p.Config(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = p.SaveConfig(ctx, id, cfg.Revision, map[string]any{"enabled": true, "content": "movie: {}"}); err != nil {
					t.Fatal(err)
				}
				request := updateOldCategory(t, p, id)
				out, err := p.updateBuiltin(ctx, id, request)
				if err != nil || !out.ReviewPending {
					t.Fatal(out, err)
				}
			}
			before := lifecycleInstallation(t, p, id)
			if err := serviceManifestRead(t, p, id); err != nil {
				t.Fatal(mode, err)
			}
			after := lifecycleInstallation(t, p, id)
			if !bytes.Equal(before.Manifest, after.Manifest) {
				t.Fatal("read rewrote authenticated host fields")
			}
		})
	}
}

func TestPluginServiceProviderRejectsInvalidHostOrBusinessFields(t *testing.T) {
	for _, change := range []string{"unknown", "lifecycle-null", "lifecycle-identity", "lifecycle-generation", "inactive", "clone-null", "clone-missing-metadata", "clone-extra-field", "review-without-required", "business-version", "business-schema"} {
		t.Run(change, func(t *testing.T) {
			_, p := pluginStoreFixture(t)
			installCloneSource(t, p, builtin.CategoryEditorManifest())
			id := "CategoryEditor"
			if change == "clone-missing-metadata" || change == "clone-extra-field" {
				in, err := p.Clone(context.Background(), id, PluginCloneRequest{Suffix: "Corrupt"})
				if err != nil {
					t.Fatal(err)
				}
				id = in.InstanceID
				if change == "clone-missing-metadata" {
					if _, err = p.store.DB.Exec(`DELETE FROM plugin_instance_metadata WHERE instance_id=?`, id); err != nil {
						t.Fatal(err)
					}
				}
			}
			lifecycleEditManifest(t, p, id, func(f map[string]json.RawMessage) {
				switch change {
				case "unknown":
					f["not_a_host_extension"] = json.RawMessage(`true`)
				case "lifecycle-null":
					f[pluginLifecycleField] = json.RawMessage(`null`)
				case "lifecycle-identity", "lifecycle-generation", "inactive":
					var m pluginLifecycleMarker
					if json.Unmarshal(f[pluginLifecycleField], &m) != nil {
						t.Fatal("marker")
					}
					if change == "lifecycle-identity" {
						m.InstanceID = "Another"
					}
					if change == "lifecycle-generation" {
						m.Generation++
					}
					if change == "inactive" {
						m.State = pluginLifecycleInactive
					}
					f[pluginLifecycleField], _ = json.Marshal(m)
				case "clone-null":
					f["host_instance"] = json.RawMessage(`null`)
				case "clone-extra-field":
					var m map[string]json.RawMessage
					json.Unmarshal(f["host_instance"], &m)
					m["forged"] = json.RawMessage(`true`)
					f["host_instance"], _ = json.Marshal(m)
				case "review-without-required":
					f[pluginUpdateReviewField] = json.RawMessage(`{}`)
				case "business-version":
					f["version"] = json.RawMessage(`"9.9"`)
				case "business-schema":
					f["schema_version"] = json.RawMessage(`2`)
				}
			})
			if err := serviceManifestRead(t, p, id); err == nil {
				t.Fatal("invalid provider accepted", change)
			}
		})
	}
}

func TestPluginServiceProviderRejectsMismatchedReviewConfig(t *testing.T) {
	p, _ := updateReviewFixture(t)
	ctx := context.Background()
	id := "CategoryEditor"
	if err := serviceManifestRead(t, p, id); err != nil {
		t.Fatal(err)
	}
	sealed, err := p.seal("config", id, "main", []byte(`{"enabled":false,"content":"different"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.store.DB.ExecContext(ctx, `UPDATE plugin_values SET sealed=? WHERE instance_id=? AND kind='config' AND item_key='main'`, sealed, id); err != nil {
		t.Fatal(err)
	}
	if err = serviceManifestRead(t, p, id); err == nil {
		t.Fatal("review config digest mismatch accepted")
	}
}

package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/brushflow"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/uninstall"
)

func lifecycleInstallation(t *testing.T, p *PluginStore, instance string) PluginInstallation {
	t.Helper()
	in, err := p.Installation(context.Background(), instance)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func lifecycleWantMode(t *testing.T, p *PluginStore, in PluginInstallation, want pluginFormMode) pluginFormResolution {
	t.Helper()
	got, err := p.resolvePluginFormMode(context.Background(), in)
	if err != nil || got.mode != want {
		t.Fatalf("mode=%v want=%v error=%v", got.mode, want, err)
	}
	return got
}

func lifecycleEditManifest(t *testing.T, p *PluginStore, instance string, edit func(map[string]json.RawMessage)) {
	t.Helper()
	in := lifecycleInstallation(t, p, instance)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(in.Manifest, &fields); err != nil {
		t.Fatal(err)
	}
	edit(fields)
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := p.seal("manifest", instance, "main", raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.store.DB.Exec(`UPDATE plugin_installations SET manifest=? WHERE instance_id=?`, sealed, instance); err != nil {
		t.Fatal(err)
	}
}

func lifecyclePreview(in PluginInstallation) PluginInstallation {
	in.Installed = false
	in.Generation = 0
	return in
}

func TestPluginLifecycleFreshInstallAndCloneChain(t *testing.T) {
	for _, manifest := range []plugins.Manifest{builtin.SystemProcessManifest(), builtin.CategoryEditorManifest()} {
		t.Run(manifest.PluginID, func(t *testing.T) {
			_, p := pluginStoreFixture(t)
			ctx := context.Background()
			raw, _ := json.Marshal(manifest)
			var fields map[string]json.RawMessage
			json.Unmarshal(raw, &fields)
			fields["future_extension"] = json.RawMessage(`{"zero":0,"flag":false,"text":"preserved"}`)
			fields[pluginLifecycleField] = json.RawMessage(`{"state":"started","instance_id":"incoming-source"}`)
			raw, _ = json.Marshal(fields)
			in := PluginInstallation{InstanceID: manifest.PluginID, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw, DesiredEnabled: true}
			if err := p.Install(ctx, in); err != nil {
				t.Fatal(err)
			}
			in = lifecycleInstallation(t, p, in.InstanceID)
			lifecycleWantMode(t, p, in, pluginFormInstalledCold)
			var stored map[string]json.RawMessage
			json.Unmarshal(in.Manifest, &stored)
			if !bytes.Equal(stored["future_extension"], fields["future_extension"]) {
				t.Fatal("install lost an unrelated manifest field")
			}
			_, queued, err := p.SaveConfigAndQueue(ctx, in.InstanceID, 0, map[string]any{"enabled": false})
			if err != nil {
				t.Fatal(err)
			}
			lifecycleWantMode(t, p, in, pluginFormInstalledCold)
			claimed, err := p.ClaimRun(ctx)
			if err != nil || claimed.ID != queued.ID {
				t.Fatal(claimed, err)
			}
			if err = p.FinishRun(ctx, claimed.ID, "failed", "SYNTHETIC_FAILURE", nil); err != nil {
				t.Fatal(err)
			}
			lifecycleWantMode(t, p, in, pluginFormInstalledStarted)
			clone, err := p.Clone(ctx, in.InstanceID, PluginCloneRequest{Suffix: "copy"})
			if err != nil {
				t.Fatal(err)
			}
			lifecycleWantMode(t, p, clone, pluginFormInstalledCold)
			if state, err := pluginLifecycleState(clone); err != nil || state != pluginLifecycleCold {
				t.Fatal("Clone returned source instead of current target manifest", state, err)
			}
			grandchild, err := p.Clone(ctx, clone.InstanceID, PluginCloneRequest{Suffix: "again"})
			if err != nil {
				t.Fatal(err)
			}
			lifecycleWantMode(t, p, grandchild, pluginFormInstalledCold)
			meta, err := p.instanceMetadata(ctx, p.store.DB, grandchild.InstanceID)
			if err != nil || meta.SourceInstanceID != clone.InstanceID {
				t.Fatal("host_instance lost", meta, err)
			}
			json.Unmarshal(grandchild.Manifest, &stored)
			if !bytes.Equal(stored["future_extension"], fields["future_extension"]) {
				t.Fatal("clone lost an unrelated manifest field")
			}
			if _, err := p.resolvePluginFormMode(ctx, lifecyclePreview(clone)); err == nil {
				t.Fatal("installed clone accepted as preview")
			}
			if _, _, err = p.SaveConfigAndQueue(ctx, clone.InstanceID, 1, map[string]any{"enabled": false}); err != nil {
				t.Fatal(err)
			}
			if _, err = p.ClaimRun(ctx); err != nil {
				t.Fatal(err)
			}
			lifecycleWantMode(t, p, clone, pluginFormInstalledStarted)
			lifecycleWantMode(t, p, grandchild, pluginFormInstalledCold)
		})
	}
}

func TestPluginLifecycleOwnerAbsenceDoesNotChooseMode(t *testing.T) {
	for _, plugin := range []string{"SeedRescue", "TorrentTransfer"} {
		t.Run(plugin, func(t *testing.T) {
			s, p := pluginStoreFixture(t)
			ctx := context.Background()
			manifest, _, ok := builtin.Lookup(plugin)
			if !ok {
				t.Fatal(plugin)
			}
			installCloneSource(t, p, manifest)
			clone, err := p.Clone(ctx, plugin, PluginCloneRequest{Suffix: "cold"})
			if err != nil {
				t.Fatal(err)
			}
			lifecycleWantMode(t, p, clone, pluginFormInstalledCold)
			in := lifecycleInstallation(t, p, plugin)
			if _, _, err = p.SaveConfigAndQueue(ctx, plugin, 0, map[string]any{}); err != nil {
				t.Fatal(err)
			}
			if _, err = p.ClaimRun(ctx); err != nil {
				t.Fatal(err)
			}
			rootTable := "plugin_seed_rescue_roots"
			if plugin == "TorrentTransfer" {
				rootTable = "plugin_transfer_roots"
			}
			if _, err = s.DB.Exec(`DELETE FROM `+rootTable+` WHERE instance_id=?`, plugin); err != nil {
				t.Fatal(err)
			}
			lifecycleWantMode(t, p, in, pluginFormInstalledStarted)
			lifecycleEditManifest(t, p, plugin, func(fields map[string]json.RawMessage) { delete(fields, pluginLifecycleField) })
			lifecycleWantMode(t, p, in, pluginFormInstalledUnknown)
			var n int
			if err = s.DB.QueryRow(`SELECT COUNT(*) FROM `+rootTable+` WHERE instance_id=?`, plugin).Scan(&n); err != nil || n != 0 {
				t.Fatal("Form resolver restored an owner", n, err)
			}
		})
	}
}

func TestPluginLifecycleAnyClaimedActionStartsGeneration(t *testing.T) {
	for _, action := range []string{"init", "reload", "page", "api", "event", "arbitrary_action"} {
		t.Run(action, func(t *testing.T) {
			_, p := pluginStoreFixture(t)
			ctx := context.Background()
			m := builtin.SystemProcessManifest()
			installCloneSource(t, p, m)
			if _, err := p.SaveConfig(ctx, m.PluginID, 0, map[string]any{}); err != nil {
				t.Fatal(err)
			}
			in := lifecycleInstallation(t, p, m.PluginID)
			queued, err := p.EnqueueRun(ctx, in.InstanceID, action, nil)
			if err != nil {
				t.Fatal(err)
			}
			lifecycleWantMode(t, p, in, pluginFormInstalledCold)
			claimed, err := p.ClaimRun(ctx)
			if err != nil || claimed.ID != queued.ID {
				t.Fatal(claimed, err)
			}
			lifecycleWantMode(t, p, in, pluginFormInstalledStarted)
		})
	}
}

func TestPluginLifecycleResetReinstallAndInactiveGenerations(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	_, queued, err := p.SaveConfigAndQueue(ctx, m.PluginID, 0, map[string]any{"private": "retained"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.ClaimRun(ctx); err != nil {
		t.Fatal(err)
	}
	if err = p.FinishRun(ctx, queued.ID, "succeeded", "", nil); err != nil {
		t.Fatal(err)
	}
	before := lifecycleInstallation(t, p, m.PluginID)
	if err = p.Reset(ctx, m.PluginID); err != nil {
		t.Fatal(err)
	}
	reset := lifecycleInstallation(t, p, m.PluginID)
	if reset.Generation != before.Generation+1 || reset.DesiredEnabled {
		t.Fatal(reset)
	}
	lifecycleWantMode(t, p, reset, pluginFormInstalledCold)
	if _, err = p.Config(ctx, m.PluginID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("reset retained config", err)
	}
	if _, err = p.resolvePluginFormMode(ctx, before); err == nil {
		t.Fatal("stale generation accepted")
	}
	if err = p.Uninstall(ctx, m.PluginID); err != nil {
		t.Fatal(err)
	}
	inactive := lifecycleInstallation(t, p, m.PluginID)
	if state, err := pluginLifecycleState(inactive); err != nil || state != pluginLifecycleInactive {
		t.Fatal(state, err)
	}
	lifecycleWantMode(t, p, lifecyclePreview(inactive), pluginFormPreview)
	if err = p.Reset(ctx, m.PluginID); err != nil {
		t.Fatal(err)
	}
	inactive = lifecycleInstallation(t, p, m.PluginID)
	if state, err := pluginLifecycleState(inactive); err != nil || state != pluginLifecycleInactive {
		t.Fatal("uninstalled reset became cold", state, err)
	}
	if err = p.Install(ctx, before); err != nil {
		t.Fatal(err)
	}
	reinstalled := lifecycleInstallation(t, p, m.PluginID)
	if reinstalled.Generation != inactive.Generation+1 {
		t.Fatal(reinstalled)
	}
	lifecycleWantMode(t, p, reinstalled, pluginFormInstalledCold)
}

func TestPluginFormModePreviewAuthenticatesTargetAndNeverReadsRetainedConfig(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	raw, _ := json.Marshal(m)
	request := PluginInstallation{InstanceID: m.PluginID, PluginID: m.PluginID, Version: m.Version, ManifestSchemaVersion: m.SchemaVersion, Manifest: json.RawMessage(`{"untrusted":"caller"}`)}
	lifecycleWantMode(t, p, request, pluginFormPreview)
	request.InstanceID += "invented"
	if _, err := p.resolvePluginFormMode(ctx, request); err == nil {
		t.Fatal("absent forged clone identity accepted")
	}
	request.InstanceID = m.PluginID
	installCloneSource(t, p, m)
	if _, err := p.SaveConfig(ctx, m.PluginID, 0, map[string]any{"secret": "tombstone-private"}); err != nil {
		t.Fatal(err)
	}
	clone, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "preview"})
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Uninstall(ctx, clone.InstanceID); err != nil {
		t.Fatal(err)
	}
	preview := lifecyclePreview(clone)
	var changesBefore, changesAfter int64
	if err = s.DB.QueryRow(`SELECT total_changes()`).Scan(&changesBefore); err != nil {
		t.Fatal(err)
	}
	got := lifecycleWantMode(t, p, preview, pluginFormPreview)
	if !bytes.Equal(got.installation.Manifest, raw) || got.installation.Installed || got.installation.Generation != 0 || got.installation.DesiredEnabled {
		t.Fatal("preview leaked tombstone metadata", got.installation)
	}
	if err = s.DB.QueryRow(`SELECT total_changes()`).Scan(&changesAfter); err != nil || changesAfter != changesBefore {
		t.Fatal("read-only resolver wrote state", changesBefore, changesAfter, err)
	}
	retained, err := p.Config(ctx, clone.InstanceID)
	if err != nil || !bytes.Contains(retained.Value, []byte("tombstone-private")) {
		t.Fatal("preview changed retained config", err)
	}
	// Simulate an authenticated tombstone from an older compiled target. The
	// current compiled version, not the tombstone version, selects preview.
	lifecycleEditManifest(t, p, clone.InstanceID, func(fields map[string]json.RawMessage) {
		fields["version"] = json.RawMessage(`"0.0.1"`)
		var marker pluginLifecycleMarker
		json.Unmarshal(fields[pluginLifecycleField], &marker)
		marker.Version = "0.0.1"
		fields[pluginLifecycleField], _ = json.Marshal(marker)
	})
	if _, err = s.DB.Exec(`UPDATE plugin_installations SET version='0.0.1' WHERE instance_id=?`, clone.InstanceID); err != nil {
		t.Fatal(err)
	}
	lifecycleWantMode(t, p, preview, pluginFormPreview)
	preview.Version = "0.0.1"
	if _, err = p.resolvePluginFormMode(ctx, preview); err == nil {
		t.Fatal("obsolete target selected implementation")
	}
}

func TestPluginFormModeIgnoresCallerManifestAndRejectsIdentitySpoof(t *testing.T) {
	_, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	in := lifecycleInstallation(t, p, m.PluginID)
	request := in
	request.Manifest = json.RawMessage(`{"host_lifecycle":{"state":"started"},"mode":"preview"}`)
	request.DesiredEnabled = !in.DesiredEnabled
	got := lifecycleWantMode(t, p, request, pluginFormInstalledCold)
	if !bytes.Equal(got.installation.Manifest, in.Manifest) || got.installation.DesiredEnabled != in.DesiredEnabled {
		t.Fatal("caller metadata became authoritative")
	}
	for _, mutate := range []func(*PluginInstallation){
		func(v *PluginInstallation) { v.Generation++ },
		func(v *PluginInstallation) { v.Installed = false },
		func(v *PluginInstallation) { v.Generation = 0 },
		func(v *PluginInstallation) { v.Generation = 0; v.Installed = false },
		func(v *PluginInstallation) { v.PluginID = "CategoryEditor" },
		func(v *PluginInstallation) { v.Version = "0.0.1" },
		func(v *PluginInstallation) { v.ManifestSchemaVersion++ },
	} {
		forged := in
		mutate(&forged)
		if _, err := p.resolvePluginFormMode(ctx, forged); err == nil {
			t.Fatal("identity spoof accepted", forged)
		}
	}
	lifecycleEditManifest(t, p, m.PluginID, func(fields map[string]json.RawMessage) { delete(fields, pluginLifecycleField) })
	lifecycleWantMode(t, p, in, pluginFormInstalledUnknown)
	if _, _, err := p.SaveConfigAndQueue(ctx, in.InstanceID, 0, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ClaimRun(ctx); err != nil {
		t.Fatal(err)
	}
	lifecycleWantMode(t, p, request, pluginFormInstalledStarted)
}

func TestPluginLifecycleMalformedAndCrossBoundEvidenceFailClosed(t *testing.T) {
	cases := map[string]func(*pluginLifecycleMarker){
		"instance":   func(m *pluginLifecycleMarker) { m.InstanceID += "other" },
		"plugin":     func(m *pluginLifecycleMarker) { m.PluginID = "CategoryEditor" },
		"version":    func(m *pluginLifecycleMarker) { m.Version = "0.0.1" },
		"generation": func(m *pluginLifecycleMarker) { m.Generation++ },
		"purpose":    func(m *pluginLifecycleMarker) { m.Purpose = "untrusted" },
		"state":      func(m *pluginLifecycleMarker) { m.State = "preview" },
		"inactive":   func(m *pluginLifecycleMarker) { m.State = pluginLifecycleInactive },
		"null":       nil, "extra": nil, "ciphertext": nil,
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, p := pluginStoreFixture(t)
			ctx := context.Background()
			m := builtin.SystemProcessManifest()
			installCloneSource(t, p, m)
			in := lifecycleInstallation(t, p, m.PluginID)
			_, queued, err := p.SaveConfigAndQueue(ctx, in.InstanceID, 0, map[string]any{})
			if err != nil {
				t.Fatal(err)
			}
			if name == "ciphertext" {
				if _, err = s.DB.Exec(`UPDATE plugin_installations SET manifest=x'001122' WHERE instance_id=?`, in.InstanceID); err != nil {
					t.Fatal(err)
				}
			} else {
				lifecycleEditManifest(t, p, in.InstanceID, func(fields map[string]json.RawMessage) {
					var marker pluginLifecycleMarker
					json.Unmarshal(fields[pluginLifecycleField], &marker)
					if mutate != nil {
						mutate(&marker)
					}
					fields[pluginLifecycleField], _ = json.Marshal(marker)
					if name == "null" {
						fields[pluginLifecycleField] = json.RawMessage(`null`)
					}
					if name == "extra" {
						fields[pluginLifecycleField] = append(fields[pluginLifecycleField][:len(fields[pluginLifecycleField])-1], []byte(`,"extra":true}`)...)
					}
				})
			}
			if _, err = p.resolvePluginFormMode(ctx, in); err == nil {
				t.Fatal("invalid evidence selected mode")
			}
			if _, err = p.ClaimRun(ctx); err == nil {
				t.Fatal("invalid evidence committed a claim")
			}
			actual, err := p.Run(ctx, queued.ID)
			if err != nil || actual.State != "pending" {
				t.Fatal("failed marker claim did not roll back", actual, err)
			}
		})
	}
}

func TestPluginLifecycleClaimsAndResetRollbackAtomically(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	in := lifecycleInstallation(t, p, m.PluginID)
	_, queued, err := p.SaveConfigAndQueue(ctx, in.InstanceID, 0, map[string]any{"value": "retained"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`CREATE TRIGGER lifecycle_marker_fail BEFORE UPDATE OF manifest ON plugin_installations BEGIN SELECT RAISE(ABORT,'synthetic marker failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ClaimRun(ctx); err == nil {
		t.Fatal("failed marker update committed claim")
	}
	actual, err := p.Run(ctx, queued.ID)
	if err != nil || actual.State != "pending" {
		t.Fatal(actual, err)
	}
	lifecycleWantMode(t, p, in, pluginFormInstalledCold)
	if err = p.Reset(ctx, in.InstanceID); err == nil {
		t.Fatal("failed reset marker committed generation")
	}
	if after := lifecycleInstallation(t, p, in.InstanceID); after.Generation != in.Generation {
		t.Fatal("reset partially committed", after)
	}
	if _, err = s.DB.Exec(`DROP TRIGGER lifecycle_marker_fail`); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = p.ClaimRun(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	lifecycleWantMode(t, p, in, pluginFormInstalledCold)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE plugin_runs SET state='running' WHERE id=?`, queued.ID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = p.markPluginExecutionStartedTx(ctx, tx, queued); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	lifecycleWantMode(t, p, in, pluginFormInstalledCold)
	if err = p.iyuuResetWithEffect(ctx, in.InstanceID, func(context.Context, *sql.Tx) error { return errors.New("synthetic later effect failure") }); err == nil {
		t.Fatal("later reset failure committed")
	}
	lifecycleWantMode(t, p, in, pluginFormInstalledCold)
	if _, err = p.Config(ctx, in.InstanceID); err != nil {
		t.Fatal("failed reset removed config", err)
	}
	// A stale admission is canceled, not execution evidence.
	if _, err = p.SaveConfig(ctx, in.InstanceID, 1, map[string]any{"value": "new"}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ClaimRun(ctx); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	actual, err = p.Run(ctx, queued.ID)
	if err != nil || actual.State != "canceled" {
		t.Fatal(actual, err)
	}
	lifecycleWantMode(t, p, in, pluginFormInstalledCold)
}

func TestPluginLifecycleUninstallManagerStampsEveryTarget(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	for _, m := range []plugins.Manifest{builtin.CategoryEditorManifest(), builtin.SystemProcessManifest()} {
		uninstallInstall(t, a, m.PluginID, m, map[string]any{}, false)
	}
	run := uninstallInstall(t, a, "PluginUnInstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{"CategoryEditor", "SystemProcess"}}, true)
	result := uninstallApply(t, a, &run)
	if len(result.Removed) != 2 {
		t.Fatal(result)
	}
	for _, instance := range result.Removed {
		in := lifecycleInstallation(t, a.pluginStore, instance)
		if state, err := pluginLifecycleState(in); err != nil || state != pluginLifecycleInactive {
			t.Fatal(instance, state, err)
		}
		lifecycleWantMode(t, a.pluginStore, lifecyclePreview(in), pluginFormPreview)
		if _, err := a.pluginStore.resolvePluginFormMode(ctx, in); err == nil {
			t.Fatal("tombstone retained installed mode")
		}
	}
}

func TestPluginLifecycleAuthenticatedSnapshotIsNotMonotonicLedger(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	in := lifecycleInstallation(t, p, m.PluginID)
	_, queued, err := p.SaveConfigAndQueue(ctx, in.InstanceID, 0, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot []byte
	if err = s.DB.QueryRow(`SELECT manifest FROM plugin_installations WHERE instance_id=?`, in.InstanceID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ClaimRun(ctx); err != nil {
		t.Fatal(err)
	}
	lifecycleWantMode(t, p, in, pluginFormInstalledStarted)
	// Model restoration of the old authenticated manifest and matching pending
	// run snapshot. No external trusted counter exists to detect this rollback.
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE plugin_installations SET manifest=? WHERE instance_id=?`, snapshot, in.InstanceID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE plugin_runs SET state='pending' WHERE id=?`, queued.ID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	lifecycleWantMode(t, p, in, pluginFormInstalledCold)
}

// A bounded source guard complements transition tests without launching three
// unrelated business workers. Keep the four production claim sites visibly
// routed through the same pre-commit helper when adding future fast lanes.
func TestPluginLifecycleProductionClaimAndGenerationHooks(t *testing.T) {
	for _, file := range []string{"plugin_runs.go", "plugin_brush_flow_fastlane.go", "plugin_seed_rescue_fastlane.go", "plugin_lark_module_producers.go"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		claim := strings.Index(text, "UPDATE plugin_runs SET state=")
		if file == "plugin_runs.go" {
			claim = strings.Index(text, "UPDATE plugin_runs SET state=?,error_code=?,updated_at=? WHERE id=? AND state='pending'")
		}
		if claim < 0 {
			t.Fatal("production claim not found", file)
		}
		tail := text[claim:]
		hook, commit := strings.Index(tail, "markPluginExecutionStartedTx"), strings.Index(tail, "tx.Commit()")
		if hook < 0 || commit < 0 || hook > commit || !strings.Contains(tail[:hook], "RowsAffected()") {
			t.Fatal("claim helper missing before commit/after affected-row check", file)
		}
	}
	for _, file := range []string{"plugin_uninstall.go", "plugin_clean_invalid_seed_http.go", "plugin_scrape_clean_http.go", "plugin_remove_link_http.go"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(string(raw), "recordPluginGenerationLifecycleTx") != 1 {
			t.Fatal("generation helper missing", file)
		}
	}
}

func TestPluginFormModeReaderRevalidatesInsideDefaultsTransaction(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(map[bool]string{false: "still_cold", true: "claimed_and_finished"}[started], func(t *testing.T) {
			_, p := pluginStoreFixture(t)
			ctx := context.Background()
			m := builtin.SystemProcessManifest()
			installCloneSource(t, p, m)
			if _, err := p.SaveConfig(ctx, m.PluginID, 0, map[string]any{}); err != nil {
				t.Fatal(err)
			}
			in := lifecycleInstallation(t, p, m.PluginID)
			lifecycleWantMode(t, p, in, pluginFormInstalledCold)
			want := pluginFormInstalledCold
			if started {
				queued, err := p.EnqueueRun(ctx, in.InstanceID, "page", nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = p.ClaimRun(ctx); err != nil {
					t.Fatal(err)
				}
				if err = p.FinishRun(ctx, queued.ID, "succeeded", "", nil); err != nil {
					t.Fatal(err)
				}
				want = pluginFormInstalledStarted
			}
			stop := errors.New("synthetic projection rollback after mode read")
			calls := 0
			readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			_, err := p.normalizeInstanceDefaults(readCtx, in, 1, map[string]json.RawMessage{"new_default": json.RawMessage(`false`)}, plugins.ConfigDefaultsPure, func(ctx context.Context, tx PluginDefaultsTransaction, change PluginDefaultsChange) error {
				calls++
				got, err := p.resolvePluginFormModeRead(ctx, tx, change.Installation)
				if err != nil {
					return err
				}
				if got.mode != want {
					t.Fatalf("reused stale cold decision: got %v want %v", got.mode, want)
				}
				return stop
			})
			if calls != 1 || !errors.Is(err, stop) {
				t.Fatal("existing reader view was not used", calls, err)
			}
			config, err := p.Config(ctx, in.InstanceID)
			if err != nil || config.Revision != 1 || string(config.Value) != `{}` {
				t.Fatal("failed projector changed config", config, err)
			}
		})
	}
}

func TestPluginLifecycleCloneIdentityAndCrossGenerationReplay(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	source := lifecycleInstallation(t, p, m.PluginID)
	clone, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "binding"})
	if err != nil {
		t.Fatal(err)
	}
	var sourceFields map[string]json.RawMessage
	json.Unmarshal(source.Manifest, &sourceFields)
	lifecycleEditManifest(t, p, clone.InstanceID, func(fields map[string]json.RawMessage) {
		fields[pluginLifecycleField] = sourceFields[pluginLifecycleField]
	})
	if _, err = p.resolvePluginFormMode(ctx, clone); err == nil {
		t.Fatal("source marker granted clone state")
	}
	// A valid target copy cannot forgive missing authenticated clone metadata.
	sealed, err := p.seal("manifest", clone.InstanceID, "main", clone.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE plugin_installations SET manifest=? WHERE instance_id=?`, sealed, clone.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`DELETE FROM plugin_instance_metadata WHERE instance_id=?`, clone.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err = p.resolvePluginFormMode(ctx, clone); pluginErrorCode(err) != "PLUGIN_INSTANCE_METADATA_INVALID" {
		t.Fatal("missing clone metadata did not fail its own proof", err)
	}
	var oldCiphertext []byte
	if err = s.DB.QueryRow(`SELECT manifest FROM plugin_installations WHERE instance_id=?`, source.InstanceID).Scan(&oldCiphertext); err != nil {
		t.Fatal(err)
	}
	if err = p.Reset(ctx, source.InstanceID); err != nil {
		t.Fatal(err)
	}
	current := lifecycleInstallation(t, p, source.InstanceID)
	if _, err = s.DB.Exec(`UPDATE plugin_installations SET manifest=? WHERE instance_id=?`, oldCiphertext, source.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err = p.resolvePluginFormMode(ctx, current); pluginErrorCode(err) != "PLUGIN_LIFECYCLE_INVALID" {
		t.Fatal("authenticated older generation did not fail its binding", err)
	}
	// Cross-instance AEAD is covered independently with intact clone metadata
	// in TestPluginLifecycleManifestCiphertextRejectsOtherInstanceOnly.
}

func TestPluginLifecycleCloneFinalFailureLeavesNoColdInstallation(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	m := builtin.SystemProcessManifest()
	installCloneSource(t, p, m)
	if _, err := s.DB.Exec(`CREATE TRIGGER lifecycle_clone_metadata_fail BEFORE INSERT ON plugin_instance_metadata BEGIN SELECT RAISE(ABORT,'synthetic metadata failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Clone(ctx, m.PluginID, PluginCloneRequest{Suffix: "rollback"}); err == nil {
		t.Fatal("failed clone committed cold evidence")
	}
	for _, table := range []string{"plugin_installations", "plugin_values", "plugin_instance_metadata"} {
		var count int
		if err := s.DB.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE instance_id=?`, m.PluginID+"rollback").Scan(&count); err != nil || count != 0 {
			t.Fatal(table, count, err)
		}
	}
}

func TestPluginLifecyclePrivateHTTPClaimsRejectMarkerBeforeCommit(t *testing.T) {
	for _, plugin := range []string{"BrushFlowLanduo", "SeedRescue"} {
		t.Run(plugin, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			cookie := setupTestAdmin(t, a)
			manifest, _, ok := builtin.Lookup(plugin)
			if !ok {
				t.Fatal(plugin)
			}
			installCloneSource(t, a.pluginStore, manifest)
			var config any = map[string]any{}
			if plugin == "BrushFlowLanduo" {
				config = brushflow.DefaultConfig()
			}
			if _, err := a.pluginStore.SaveConfig(ctx, plugin, 0, config); err != nil {
				t.Fatal(err)
			}
			in := lifecycleInstallation(t, a.pluginStore, plugin)
			request := plugins.HTTPRequest{RequestID: id("ph_"), Key: "status", Method: "GET", Path: "/status", Params: map[string]string{}}
			queued, err := a.enqueuePluginHTTP(ctx, in, request, pluginAPIScope{Key: request.Key, Method: request.Method, RequestID: request.RequestID, Authority: pluginAPIAuthority{Kind: "session", Proof: tokenHash(cookie.Value)}})
			if err != nil {
				t.Fatal(err)
			}
			lifecycleEditManifest(t, a.pluginStore, plugin, func(fields map[string]json.RawMessage) { fields[pluginLifecycleField] = json.RawMessage(`null`) })
			if plugin == "BrushFlowLanduo" {
				err = a.executeBrushFlowHTTP(ctx, queued)
			} else {
				err = a.executeSeedRescueHTTP(ctx, queued)
			}
			if pluginErrorCode(err) != "PLUGIN_LIFECYCLE_INVALID" {
				t.Fatal("private claim bypassed shared lifecycle proof", err)
			}
			actual, err := a.pluginStore.Run(ctx, queued.ID)
			if err != nil || actual.State != "pending" {
				t.Fatal("failed private claim committed running", actual, err)
			}
		})
	}
}

func TestPluginLifecyclePrivateLarkClaimRejectsMarkerBeforeCommit(t *testing.T) {
	a := newTestApp(t)
	installUnregisteredLarkConfig(t, a, map[string]any{"enabled": true})
	core, ctx := larkStartedCoreFixture(t, a)
	child, err := a.larkPrepareCoreModule(ctx, core, pluginLarkModuleInput{Name: "message_parser", Source: "Lark", Metadata: core.inbox.Payload})
	if err != nil {
		t.Fatal(err)
	}
	lifecycleEditManifest(t, a.pluginStore, child.InstanceID, func(fields map[string]json.RawMessage) { fields[pluginLifecycleField] = json.RawMessage(`null`) })
	if _, err = a.executeLarkSourceRun(ctx, child); pluginErrorCode(err) != "PLUGIN_LIFECYCLE_INVALID" {
		t.Fatal("private Lark claim bypassed shared lifecycle proof", err)
	}
	actual, err := a.pluginStore.Run(ctx, child.ID)
	if err != nil || actual.State != "pending" {
		t.Fatal("failed private Lark claim committed running", actual, err)
	}
}

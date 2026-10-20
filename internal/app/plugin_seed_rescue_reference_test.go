package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	sr "light-media/internal/plugins/seedrescue"
)

func TestSeedRescueOriginalImportCannotActivateCoincidentNativeID(t *testing.T) {
	a, _, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	native := identityRequire(t, a, "s1").SourceID
	m, err := a.pluginStore.BeginMigration(ctx, strings.Repeat("a", 64), "SeedRescue", "OwnedOriginalRescue")
	if err != nil {
		t.Fatal(err)
	}
	manifest := sr.NativeManifest()
	raw, _ := json.Marshal(manifest)
	in := PluginImport{RunID: m.ID, Installation: PluginInstallation{InstanceID: m.InstanceID, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: manifest.SchemaVersion, Manifest: raw}, Config: mustCleanSeedJSON(sr.Object{"enabled": true, "onlyonce": true, "target_site_id": native, "source_site_ids": []int64{native}, "downloader": "Owned qB"}), Data: map[string]json.RawMessage{sr.JobsKey: mustCleanSeedJSON(sr.Object{"owned-task": sr.Object{"target_site_id": native, "state": "waiting_iyuu"}})}}
	if _, err = a.pluginStore.Import(ctx, in); err != nil {
		t.Fatal(err)
	}
	before, err := a.pluginStore.Config(ctx, m.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.pluginStore.SaveConfigAndQueue(ctx, m.InstanceID, before.Revision, before.Value); pluginErrorCode(err) != "PLUGIN_SITE_PROTOCOL_UNCLAIMED" {
		t.Fatal("coincident old site ID activated without source mapping", err)
	}
	installation, err := a.pluginStore.Installation(ctx, m.InstanceID)
	if err != nil || installation.DesiredEnabled {
		t.Fatal("blocked save released import staging")
	}
	after, err := a.pluginStore.Config(ctx, m.InstanceID)
	if err != nil || after.Revision != before.Revision || !bytes.Equal(before.Value, after.Value) {
		t.Fatal("blocked save changed original config")
	}
	state, _, err := a.pluginStore.seedRescueRead(ctx, a.Store.DB, m.InstanceID, installation.Generation)
	if err != nil || len(state.Values) != 1 || len(state.Runs) != 0 || state.AppliedRevision != 0 {
		t.Fatal("blocked import lost readable authenticated history", err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_installations SET desired_enabled=1 WHERE instance_id=?`, m.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.EnqueueRun(ctx, m.InstanceID, "run", nil); pluginErrorCode(err) != "PLUGIN_SITE_PROTOCOL_UNCLAIMED" {
		t.Fatal("legacy enabled flag bypassed reference gate", err)
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?`, m.InstanceID).Scan(&count); err != nil || count != 0 {
		t.Fatal("unbound import queued work", count, err)
	}
}

func TestSeedRescueMissingProtocolPreservesColdPendingAndBlocksHost(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	const instance = "OwnedUnboundRescue"
	run := uninstallInstall(t, a, instance, sr.NativeManifest(), sr.Object{"enabled": false, "onlyonce": false}, true)
	// Return the owned admission to pending, then model missing pre-upgrade
	// reference evidence. Its source input and identity must remain untouched.
	if _, err := a.Store.DB.Exec(`UPDATE plugin_runs SET state='pending' WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`DELETE FROM native_site_reference_protocols WHERE instance_id=?`, instance); err != nil {
		t.Fatal(err)
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err = b.initializeNativePlugins(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = b.pluginStore.claimRunForInstance(ctx, instance); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("unbound pending task was consumed", err)
	}
	after, err := b.pluginStore.Run(ctx, run.ID)
	if err != nil || after.State != "pending" || !bytes.Equal(after.Input, run.Input) || after.ConfigRevision != run.ConfigRevision || after.Generation != run.Generation {
		t.Fatal("cold startup replaced source admission", err)
	}
	owner := &pluginSeedRescueOwner{a: b, instance: instance, generation: run.Generation}
	call := &seedRescueCall{o: owner, run: run, revision: run.ConfigRevision}
	owner.active = call
	if err = call.Guard(ctx); pluginErrorCode(err) != "PLUGIN_SITE_PROTOCOL_UNCLAIMED" {
		t.Fatal("native IO guard ignored missing source namespace", err)
	}
}

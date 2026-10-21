package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"light-media/internal/plugins/removelink"
	cleanuphost "light-media/internal/plugins/removelink/host"
)

// These are manufactured snapshots, not configuration collected from MoviePilot.
// The pinned source SHA identifies plugin code only. All files belong to TempDir.
func removeLinkIsolationSnapshot(t *testing.T, instance, source, target, marker string) []byte {
	t.Helper()
	cfg := map[string]any{
		"enabled": false, "notify": true, "delete_history": true, "delete_torrents": true,
		"delete_scrap_infos": true, "delayed_deletion": true, "delay_seconds": 47,
		"monitor_dirs": source, "exclude_dirs": source,
		"monitor_strm_deletion": true, "strm_path_mappings": source + ":local:" + source,
		"exclude_keywords": marker, "custom_scrap_extensions": ".owned",
	}
	config, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(reviewedRemoveLinkSnapshot{
		Version: 1, SourceContainer: strings.Repeat("a", 64), PluginID: "RemoveLink", InstanceID: instance,
		SourceSHA: reviewedRemoveLinkSourceSHA, InstalledCount: 1, Config: string(config),
		Data: map[string]json.RawMessage{}, Paths: []reviewedRemoveLinkPath{{Source: source, Target: target}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func removeLinkIsolationSettings(t *testing.T, a *App, root string) {
	t.Helper()
	settings := Settings{AllowedRoots: []string{root}}
	sealed, err := a.Store.Seal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`INSERT INTO records(kind,id,payload,updated_at) VALUES('config','main',?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload`, sealed, now()); err != nil {
		t.Fatal(err)
	}
	a.cfgMu.Lock()
	a.config = settings
	a.cfgMu.Unlock()
}
func removeLinkIsolationNoEffects(t *testing.T, a *App) {
	t.Helper()
	for _, table := range []string{"plugin_runs", "plugin_schedules", "plugin_schedule_occurrences", "plugin_event_subscriptions", "plugin_event_batches", "plugin_event_deliveries", "plugin_cleanup_journal_meta", "plugin_cleanup_journal_rows", "plugin_cleanup_private_outbox", "plugin_cleanup_core_receipts", "plugin_cleanup_source_results"} {
		var exists, n int
		if err := a.Store.DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists == 0 {
			continue
		}
		if err := a.Store.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("disabled import created %s rows: %d (%v)", table, n, err)
		}
	}
	var n int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_values WHERE kind='data'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("source has no persistent KV: %d (%v)", n, err)
	}
	a.pluginRemoveLink.mu.Lock()
	n = len(a.pluginRemoveLink.owners)
	a.pluginRemoveLink.mu.Unlock()
	if n != 0 {
		t.Fatalf("disabled imports started %d cleanup owners", n)
	}
}
func removeLinkIsolationProof(t *testing.T, a *App, instance string) ([]byte, reviewedRemoveLinkProof) {
	t.Helper()
	var sealed []byte
	var proof reviewedRemoveLinkProof
	if err := a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind=? AND id=?`, reviewedRemoveLinkMappingKind, instance).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	plain, err := a.pluginStore.unseal(reviewedRemoveLinkMappingKind, instance, "main", sealed)
	if err != nil || json.Unmarshal(plain, &proof) != nil {
		t.Fatal("cannot authenticate instance proof", err)
	}
	if bytes.Contains(sealed, []byte(instance)) {
		t.Fatal("proof stored as plaintext")
	}
	return sealed, proof
}
func TestReviewedRemoveLinkSyntheticClonesColdStartIsolation(t *testing.T) {
	t.Setenv("LIGHT_MEDIA_MEDIA_ROOTS", "")
	ctx := context.Background()
	owned := t.TempDir()
	dir := filepath.Join(owned, "app")
	a, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if a != nil {
			_ = a.Close()
		}
	}()
	targets := []string{filepath.Join(owned, "clone-one"), filepath.Join(owned, "clone-two")}
	instances := []string{"RemoveLink_owned_one", "RemoveLink_owned_two"}
	raw := make([][]byte, 2)
	runs := make([]PluginMigrationRun, 2)
	receipts := make([]PluginMigrationReceipt, 2)
	configs := make([]PluginStoredValue, 2)
	proofBytes := make([][]byte, 2)
	proofs := make([]reviewedRemoveLinkProof, 2)
	for i, target := range targets {
		if err = os.MkdirAll(target, 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(target, "keep.mkv"), []byte("owned media sentinel"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(target, "keep.nfo"), []byte("owned metadata sentinel"), 0600); err != nil {
			t.Fatal(err)
		}
		raw[i] = removeLinkIsolationSnapshot(t, instances[i], filepath.Join(owned, "synthetic-source-"+instances[i]), target, "marker-"+instances[i])
	}
	removeLinkIsolationSettings(t, a, owned)
	for i := range instances {
		fp, e := ReviewedRemoveLinkFingerprint(raw[i])
		if e != nil {
			t.Fatal(e)
		}
		runs[i], err = a.pluginStore.BeginMigration(ctx, fp, "RemoveLink", instances[i])
		if err != nil {
			t.Fatal(err)
		}
		receipts[i], err = a.pluginStore.ImportReviewedRemoveLink(ctx, runs[i].ID, raw[i])
		if err != nil {
			t.Fatal(err)
		}
		configs[i], err = a.pluginStore.Config(ctx, instances[i])
		if err != nil {
			t.Fatal(err)
		}
		var storedCiphertext []byte
		if err = a.Store.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, instances[i]).Scan(&storedCiphertext); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(storedCiphertext, []byte(targets[i])) || bytes.Contains(storedCiphertext, []byte("marker-"+instances[i])) {
			t.Fatal("clone configuration stored without encryption")
		}
		var cfg map[string]any
		if err = json.Unmarshal(configs[i].Value, &cfg); err != nil {
			t.Fatal(err)
		}
		if cfg["monitor_dirs"] != targets[i] || cfg["exclude_dirs"] != targets[i] || cfg["strm_path_mappings"] != targets[i]+":local:"+targets[i] || cfg["exclude_keywords"] != "marker-"+instances[i] {
			t.Fatal("clone configuration references crossed namespaces")
		}
		installation, e := a.pluginStore.Installation(ctx, instances[i])
		if e != nil || installation.DesiredEnabled || !installation.Installed {
			t.Fatal("import did not remain installed and disabled", e)
		}
		proofBytes[i], proofs[i] = removeLinkIsolationProof(t, a, instances[i])
		if proofs[i].Binding.InstanceID != instances[i] || proofs[i].Migration != runs[i].ID || proofs[i].ImportDigest != receipts[i].Digest || proofs[i].Binding.SourceFingerprint != fp {
			t.Fatal("proof does not bind exact migration and clone")
		}
	}
	if reflect.DeepEqual(proofs[0], proofs[1]) || bytes.Equal(configs[0].Value, configs[1].Value) {
		t.Fatal("distinct clone evidence collapsed")
	}
	if _, err = a.pluginStore.unseal(reviewedRemoveLinkMappingKind, instances[1], "main", proofBytes[0]); err == nil {
		t.Fatal("sibling proof decrypted in another namespace")
	}
	removeLinkIsolationNoEffects(t, a)
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a = nil
	a, err = New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	var watches atomic.Int32
	a.pluginRemoveLink.watch = func(context.Context, []removelink.Watch, func(string) (*os.File, error)) (cleanuphost.Watcher, error) {
		watches.Add(1)
		return nil, errors.New("disabled migration must not watch")
	}
	a.Start(ctx)
	removeLinkIsolationNoEffects(t, a)
	if watches.Load() != 0 {
		t.Fatal("cold startup attempted a watcher")
	}
	for i := range instances {
		current, e := a.pluginStore.Config(ctx, instances[i])
		if e != nil || !bytes.Equal(current.Value, configs[i].Value) {
			t.Fatal("cold config changed", e)
		}
		sealed, proof := removeLinkIsolationProof(t, a, instances[i])
		if !bytes.Equal(sealed, proofBytes[i]) || !reflect.DeepEqual(proof, proofs[i]) {
			t.Fatal("cold proof changed")
		}
		if _, e = a.pluginStore.SaveConfig(ctx, instances[i], current.Revision, map[string]any{"enabled": false, "exclude_keywords": "later-edit-" + instances[i]}); e != nil {
			t.Fatal(e)
		}
		edited, e := a.pluginStore.Config(ctx, instances[i])
		if e != nil {
			t.Fatal(e)
		}
		replay, e := a.pluginStore.ImportReviewedRemoveLink(ctx, runs[i].ID, raw[i])
		if e != nil || replay != receipts[i] {
			t.Fatal("cold source replay changed receipt", e)
		}
		current, e = a.pluginStore.Config(ctx, instances[i])
		if e != nil || current.Revision != edited.Revision || !bytes.Equal(current.Value, edited.Value) {
			t.Fatal("replay overwrote later clone edit", e)
		}
		sealed, _ = removeLinkIsolationProof(t, a, instances[i])
		if !bytes.Equal(sealed, proofBytes[i]) {
			t.Fatal("replay replaced historical proof")
		}
		var changed reviewedRemoveLinkSnapshot
		if err = json.Unmarshal(raw[i], &changed); err != nil {
			t.Fatal(err)
		}
		changed.Config = strings.Replace(changed.Config, "marker-"+instances[i], "drifted-marker", 1)
		altered, _ := json.Marshal(changed)
		if _, e = a.pluginStore.ImportReviewedRemoveLink(ctx, runs[i].ID, altered); e == nil {
			t.Fatal("changed source reused original run")
		}
	}
	removeLinkIsolationNoEffects(t, a)
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	a = nil
	for _, target := range targets {
		for name, want := range map[string]string{"keep.mkv": "owned media sentinel", "keep.nfo": "owned metadata sentinel"} {
			got, e := os.ReadFile(filepath.Join(target, name))
			if e != nil || string(got) != want {
				t.Fatal("disabled cold startup modified owned media", e)
			}
		}
	}
	// After runtime shutdown, verify no shutdown work was persisted either.
	a, err = New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	removeLinkIsolationNoEffects(t, a)
	// A copied authenticated ciphertext cannot stand in for the sibling proof.
	if _, err = a.Store.DB.Exec(`UPDATE records SET payload=? WHERE kind=? AND id=?`, proofBytes[0], reviewedRemoveLinkMappingKind, instances[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.ImportReviewedRemoveLink(ctx, runs[1].ID, raw[1]); err == nil {
		t.Fatal("cross-clone proof substitution accepted")
	}
	if _, err = a.pluginStore.ImportReviewedRemoveLink(ctx, runs[0].ID, raw[0]); err != nil {
		t.Fatal("sibling substitution contaminated intact clone", err)
	}
	removeLinkIsolationNoEffects(t, a)
}

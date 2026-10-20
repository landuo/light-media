package app

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic public one-switch configuration uses the source JSON formatting.
func reviewedStatusTestInput() []byte {
	in := reviewedServerStatusSnapshot{Version: 1, SourceContainer: "cb82e3f550881334ef3814426016d08d909bccd88d3297522756ba31654d129a", SourceSHA: "63583e9706aeee31a181b2fefa4e80c793ae4f2b9a76c9efea5956f8b0043d90", Config: `{"enable": true}`, InstalledCount: 1}
	raw, _ := json.Marshal(in)
	return raw
}
func TestReviewedServerStatusStrictSelectedInput(t *testing.T) {
	raw := reviewedStatusTestInput()
	if e := ValidateReviewedServerStatusInput(raw); e != nil {
		t.Fatal(e)
	}
	var values map[string]any
	_ = json.Unmarshal(raw, &values)
	for _, key := range []string{"version", "source_container", "source_sha256", "config", "installed_count", "kv_count", "file_count", "symlink_count"} {
		t.Run(key, func(t *testing.T) {
			clone := map[string]any{}
			for k, v := range values {
				clone[k] = v
			}
			delete(clone, key)
			b, _ := json.Marshal(clone)
			if ValidateReviewedServerStatusInput(b) == nil {
				t.Fatal("missing field accepted")
			}
			clone[key] = nil
			b, _ = json.Marshal(clone)
			if ValidateReviewedServerStatusInput(b) == nil {
				t.Fatal("null accepted")
			}
		})
	}
	for _, b := range [][]byte{append(raw, []byte(` {}`)...), bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1), bytes.Replace(raw, []byte(`"kv_count":0`), []byte(`"kv_count":1`), 1), append(raw[:len(raw)-1], []byte(`,"other":true}`)...)} {
		if ValidateReviewedServerStatusInput(b) == nil {
			t.Fatal("unreviewed payload accepted")
		}
	}
}
func TestReviewedServerStatusAtomicColdReplayKeepsPreferencesAndUserEdits(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	if _, e := s.DB.Exec(`INSERT INTO records VALUES('plugin_dashboard_settings','desktop',X'00','owned')`); e != nil {
		t.Fatal(e)
	}
	run, e := p.BeginMigration(ctx, strings.Repeat("8", 64), "MPServerStatus", "MPServerStatus")
	if e != nil {
		t.Fatal(e)
	}
	raw := reviewedStatusTestInput()
	receipt, e := p.ImportReviewedServerStatus(ctx, run.ID, raw)
	if e != nil {
		t.Fatal(e)
	}
	config, e := p.Config(ctx, "MPServerStatus")
	if e != nil || !bytes.Equal(config.Value, []byte(`{"enable":true}`)) {
		t.Fatal("source raw config changed", e)
	}
	installation, e := p.Installation(ctx, "MPServerStatus")
	if e != nil || !installation.DesiredEnabled {
		t.Fatal("read-only package hidden", e)
	}
	var sealed []byte
	if e = s.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id='MPServerStatus' AND kind='config'`).Scan(&sealed); e != nil || bytes.Contains(sealed, []byte("enable")) {
		t.Fatal("configuration not encrypted", e)
	}
	dir := filepath.Dir(s.lock.Name())
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p, e = NewPluginStore(ctx, s)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.SaveConfig(ctx, "MPServerStatus", config.Revision, map[string]any{"enable": false, "owned": true}); e != nil {
		t.Fatal(e)
	}
	replay, e := p.ImportReviewedServerStatus(ctx, run.ID, raw)
	if e != nil || replay != receipt {
		t.Fatal("receipt replay changed", e)
	}
	current, e := p.Config(ctx, "MPServerStatus")
	if e != nil || bytes.Equal(current.Value, config.Value) {
		t.Fatal("replay overwrote user config", e)
	}
	var pref []byte
	if e = s.DB.QueryRow(`SELECT payload FROM records WHERE kind='plugin_dashboard_settings' AND id='desktop'`).Scan(&pref); e != nil || !bytes.Equal(pref, []byte{0}) {
		t.Fatal("preferences changed", e)
	}
	var n int
	if e = s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_runs)+(SELECT COUNT(*) FROM plugin_schedules)+(SELECT COUNT(*) FROM plugin_event_subscriptions)`).Scan(&n); e != nil || n != 0 {
		t.Fatal("offline migration executed work", n, e)
	}
}
func TestReviewedServerStatusReceiptFailureAndWrongAuthority(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	raw := reviewedStatusTestInput()
	in, e := validateReviewedServerStatus(raw)
	if e != nil {
		t.Fatal(e)
	}
	run, e := p.BeginMigration(ctx, strings.Repeat("7", 64), "MPServerStatus", "MPServerStatus")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`CREATE TRIGGER owned_failure BEFORE INSERT ON plugin_migration_receipts BEGIN SELECT RAISE(ABORT,'owned'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = p.ImportReviewedServerStatus(ctx, run.ID, raw); e == nil {
		t.Fatal("commit failure ignored")
	}
	var n int
	if e = s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_installations)+(SELECT COUNT(*) FROM plugin_values)+(SELECT COUNT(*) FROM plugin_migration_receipts)`).Scan(&n); e != nil || n != 0 {
		t.Fatal("partial migration", n, e)
	}
	if _, e = s.DB.Exec(`DROP TRIGGER owned_failure`); e != nil {
		t.Fatal(e)
	}
	wrong, e := p.BeginMigration(ctx, strings.Repeat("6", 64), "SystemProcess", "MPServerStatus")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Import(ctx, serverStatusSnapshotImport(in, wrong.ID)); e == nil {
		t.Fatal("wrong source authority accepted")
	}
	value := serverStatusSnapshotImport(in, run.ID)
	value.Installation.Version = "1.5"
	if _, e = p.Import(ctx, value); e == nil {
		t.Fatal("wrong compiled manifest accepted")
	}
	if _, e = p.ImportReviewedServerStatus(ctx, run.ID, raw); e != nil {
		t.Fatal("same plan cannot retry", e)
	}
}

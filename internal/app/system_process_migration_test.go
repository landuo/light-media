package app

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func ownedSystemProcessImport(t *testing.T, p *PluginStore) PluginImport {
	t.Helper()
	ctx := context.Background()
	run, e := p.BeginMigration(ctx, strings.Repeat("9", 64), "SystemProcess", "SystemProcess")
	if e != nil {
		t.Fatal(e)
	}
	in, e := systemProcessSnapshotImport(reviewedSystemProcessSnapshot{Config: `{"enable":true,"dashboard_widget_size":6,"dashboard_widget_refresh":5}`, Dashboard: map[string]string{
		"Dashboard":                 `{"SystemProcess":true,"FutureWidget":false}`,
		"DashboardOrder":            `["FutureWidget","SystemProcess"]`,
		"DashboardGridLayout":       `{"items":{"FutureWidget":{"x":3,"y":1,"w":4,"h":12}},"enabled":{"SystemProcess":true},"updatedAt":42}`,
		"DashboardGridLayoutTablet": `{"items":{},"enabled":{"SystemProcess":false}}`,
		"DashboardGridLayoutMobile": `{"items":{"FutureWidget":{"x":0,"y":1,"w":1}},"enabled":{"SystemProcess":true}}`,
	}}, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	return in
}

func TestReviewedSystemProcessAtomicMigrationColdReplay(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	in := ownedSystemProcessImport(t, p)
	receipt, e := p.Import(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	config, e := p.Config(ctx, "SystemProcess")
	if e != nil || !bytes.Equal(config.Value, in.Config) {
		t.Fatal("source config not preserved", e)
	}
	installation, e := p.Installation(ctx, "SystemProcess")
	if e != nil || !installation.DesiredEnabled {
		t.Fatal("reviewed read-only package remained hidden", e)
	}
	for key, want := range in.systemProcessDashboard {
		var sealed []byte
		if e = s.DB.QueryRow(`SELECT payload FROM records WHERE kind='plugin_dashboard_settings' AND id=?`, key).Scan(&sealed); e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(sealed, []byte("FutureWidget")) {
			t.Fatal("source user preferences stored unencrypted")
		}
		plain, e := p.unseal("plugin_dashboard_settings", "admin", key, sealed)
		if e != nil || !bytes.Equal(plain, want) {
			t.Fatal("raw source preference changed", key, e)
		}
	}
	// A cold store must retain both source configuration and all device layouts.
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
	// Later user edits must survive replay of the original migration receipt.
	if _, e = p.SaveConfig(ctx, "SystemProcess", config.Revision, map[string]any{"enable": false, "dashboard_widget_size": 8}); e != nil {
		t.Fatal(e)
	}
	edited := []byte(`{"items":{"OwnedLater":{"x":0,"y":0,"w":3}}}`)
	sealed, e := p.seal("plugin_dashboard_settings", "admin", "desktop", edited)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(`UPDATE records SET payload=? WHERE kind='plugin_dashboard_settings' AND id='desktop'`, sealed); e != nil {
		t.Fatal(e)
	}
	replay, e := p.Import(ctx, in)
	if e != nil || replay != receipt {
		t.Fatal("cold replay changed receipt", e)
	}
	var actual []byte
	if e = s.DB.QueryRow(`SELECT payload FROM records WHERE kind='plugin_dashboard_settings' AND id='desktop'`).Scan(&actual); e != nil || !bytes.Equal(actual, sealed) {
		t.Fatal("replay overwrote user layout", e)
	}
	current, e := p.Config(ctx, "SystemProcess")
	if e != nil || bytes.Equal(current.Value, in.Config) {
		t.Fatal("replay overwrote edited config", e)
	}
	var effects int
	if e = s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_runs)+(SELECT COUNT(*) FROM plugin_schedules)+(SELECT COUNT(*) FROM plugin_event_subscriptions)`).Scan(&effects); e != nil || effects != 0 {
		t.Fatal("migration created runtime effects", effects, e)
	}
	changed := in
	changed.systemProcessDashboard = map[string]json.RawMessage{}
	for k, v := range in.systemProcessDashboard {
		changed.systemProcessDashboard[k] = v
	}
	changed.systemProcessDashboard["order"] = json.RawMessage(`[]`)
	if _, e = p.Import(ctx, changed); pluginErrorCode(e) != "PLUGIN_MIGRATION_CONFLICT" {
		t.Fatal("changed source replay accepted", e)
	}
}

func TestReviewedSystemProcessMigrationRollsBackEveryWrite(t *testing.T) {
	for _, where := range []string{"preferences", "receipt"} {
		t.Run(where, func(t *testing.T) {
			s, p := pluginStoreFixture(t)
			in := ownedSystemProcessImport(t, p)
			trigger := `CREATE TRIGGER owned_fail BEFORE INSERT ON records WHEN NEW.kind='plugin_dashboard_settings' AND NEW.id='tablet' BEGIN SELECT RAISE(ABORT,'owned'); END`
			if where == "receipt" {
				trigger = `CREATE TRIGGER owned_fail BEFORE INSERT ON plugin_migration_receipts BEGIN SELECT RAISE(ABORT,'owned'); END`
			}
			if _, e := s.DB.Exec(trigger); e != nil {
				t.Fatal(e)
			}
			if _, e := p.Import(context.Background(), in); e == nil {
				t.Fatal("injected transaction failure ignored")
			}
			var n int
			if e := s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_installations WHERE instance_id='SystemProcess')+(SELECT COUNT(*) FROM plugin_values WHERE instance_id='SystemProcess')+(SELECT COUNT(*) FROM records WHERE kind='plugin_dashboard_settings')+(SELECT COUNT(*) FROM plugin_migration_receipts)`).Scan(&n); e != nil || n != 0 {
				t.Fatal("partial migration committed", n, e)
			}
			if _, e := s.DB.Exec(`DROP TRIGGER owned_fail`); e != nil {
				t.Fatal(e)
			}
			if _, e := p.Import(context.Background(), in); e != nil {
				t.Fatal("same plan could not recover", e)
			}
		})
	}
}

func TestReviewedSystemProcessMigrationRefusesExistingLayoutAndWrongAuthority(t *testing.T) {
	s, p := pluginStoreFixture(t)
	in := ownedSystemProcessImport(t, p)
	if _, e := s.DB.Exec(`INSERT INTO records VALUES('plugin_dashboard_settings','desktop',X'00','owned')`); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Import(context.Background(), in); e == nil || e.Error() != "target_dashboard_preferences_already_exist" {
		t.Fatal("existing target layout overwritten", e)
	}
	if _, e := s.DB.Exec(`DELETE FROM records WHERE kind='plugin_dashboard_settings'`); e != nil {
		t.Fatal(e)
	}
	wrong := in
	wrong.Installation.PluginID = "RemoteIdentifiers"
	if _, e := p.Import(context.Background(), wrong); e == nil {
		t.Fatal("unrelated plugin acquired dashboard write")
	}
	for _, raw := range []string{`{}`, `{"version":1,"version":1}`, `{"version":null}`, `{"config":"owned-private-value"}`} {
		if e := ValidateReviewedSystemProcessInput([]byte(raw)); e == nil || strings.Contains(e.Error(), "owned-private-value") {
			t.Fatal("malformed source accepted or disclosed")
		}
	}
	ordinary := in
	ordinary.systemProcessDashboard = nil
	if _, e := p.Import(context.Background(), ordinary); e != nil {
		t.Fatal(e)
	}
	installation, e := p.Installation(context.Background(), "SystemProcess")
	if e != nil || installation.DesiredEnabled {
		t.Fatal("ordinary unreviewed import activated package", e)
	}
}

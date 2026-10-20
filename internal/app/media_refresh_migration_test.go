package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewedMediaRefreshRejectsUnreviewedInput(t *testing.T) {
	for _, raw := range []string{"", "null", "{}", `{"version":1}`, `{"version":1,"reference":null}`} {
		if ValidateReviewedMediaRefreshInput([]byte(raw)) == nil {
			t.Fatal("unreviewed source accepted")
		}
	}
}

func reviewedMediaRefreshTestConnection(t *testing.T, s *Store, ref reviewedMediaRefreshReference, change bool) {
	t.Helper()
	username := ref.Username
	if change {
		username = "owned-other-account"
	}
	sealed, e := s.Seal(Settings{FNOS: FNConfig{Host: ref.Host, Username: username, Password: "owned-no-real-credential", Enabled: true}})
	if e != nil {
		t.Fatal("owned configuration seal failed")
	}
	if _, e = s.DB.Exec(`INSERT INTO records VALUES('config','main',?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload`, sealed, now()); e != nil {
		t.Fatal("owned configuration setup failed")
	}
}

func TestReviewedMediaRefreshSelectedStdin(t *testing.T) {
	if os.Getenv("LIGHT_MEDIA_REVIEWED_MEDIA_REFRESH") != "owned-selected-stdin" {
		t.Skip("requires selected source stdin and only owned temporary stores")
	}
	raw, e := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
	if e != nil {
		t.Fatal("selected input read failed")
	}
	in, mapped, e := validateReviewedMediaRefresh(raw)
	if e != nil {
		t.Fatal("selected input validation failed")
	}
	for _, key := range []string{"version", "source_container", "source_sha256", "config", "installed_count", "kv_count", "file_count", "symlink_count", "reference"} {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		delete(fields, key)
		b, _ := json.Marshal(fields)
		if ValidateReviewedMediaRefreshInput(b) == nil {
			t.Fatal("missing reviewed input accepted")
		}
		fields[key] = json.RawMessage(`null`)
		b, _ = json.Marshal(fields)
		if ValidateReviewedMediaRefreshInput(b) == nil {
			t.Fatal("null reviewed input accepted")
		}
	}
	ctx := context.Background()
	s, p := pluginStoreFixture(t)
	reviewedMediaRefreshTestConnection(t, s, *in.Reference, false)
	run, e := p.BeginMigration(ctx, strings.Repeat("4", 64), "MediaServerRefresh", "MediaServerRefresh")
	if e != nil {
		t.Fatal(e)
	}
	receipt, e := p.ImportReviewedMediaRefresh(ctx, run.ID, raw)
	if e != nil {
		t.Fatal("selected import failed", e)
	}
	stored, e := p.Config(ctx, "MediaServerRefresh")
	if e != nil || !bytes.Equal(stored.Value, mapped) {
		t.Fatal("source delay, enable state or mapped reference changed")
	}
	installation, e := p.Installation(ctx, "MediaServerRefresh")
	if e != nil || !installation.DesiredEnabled {
		t.Fatal("reviewed source enable state lost")
	}
	var sealed []byte
	if e = s.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id='MediaServerRefresh' AND kind='config'`).Scan(&sealed); e != nil || bytes.Contains(sealed, mapped) {
		t.Fatal("configuration was not encrypted")
	}
	if e = s.DB.QueryRow(`SELECT payload FROM records WHERE kind='plugin_media_reference_mapping' AND id='MediaServerRefresh'`).Scan(&sealed); e != nil {
		t.Fatal("reference map missing")
	}
	mapBefore := append([]byte(nil), sealed...)
	decoded, e := p.unseal("plugin_media_reference_mapping", "MediaServerRefresh", "main", sealed)
	var binding reviewedMediaRefreshBinding
	if e != nil || json.Unmarshal(decoded, &binding) != nil || binding != reviewedMediaRefreshBindingValue() {
		t.Fatal("reference map binding invalid")
	}
	var count int
	if e = s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_runs)+(SELECT COUNT(*) FROM plugin_schedules)+(SELECT COUNT(*) FROM plugin_event_subscriptions)`).Scan(&count); e != nil || count != 0 {
		t.Fatal("offline import executed work")
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
	reviewedMediaRefreshTestConnection(t, s, *in.Reference, true)
	if _, e = p.SaveConfig(ctx, "MediaServerRefresh", stored.Revision, map[string]any{"enabled": false, "delay": 1, "mediaservers": []string{}, "owned_edit": true}); e != nil {
		t.Fatal(e)
	}
	edited, e := p.Config(ctx, "MediaServerRefresh")
	if e != nil {
		t.Fatal(e)
	}
	replay, e := p.ImportReviewedMediaRefresh(ctx, run.ID, raw)
	if e != nil || replay != receipt {
		t.Fatal("cold receipt replay changed")
	}
	current, e := p.Config(ctx, "MediaServerRefresh")
	if e != nil || current.Revision != edited.Revision || !bytes.Equal(current.Value, edited.Value) {
		t.Fatal("receipt replay overwrote user edits")
	}
	if e = s.DB.QueryRow(`SELECT payload FROM records WHERE kind='plugin_media_reference_mapping' AND id='MediaServerRefresh'`).Scan(&sealed); e != nil || !bytes.Equal(mapBefore, sealed) {
		t.Fatal("historical mapping changed on replay")
	}
	for _, scenario := range []string{"missing_target", "changed_account", "receipt_failure", "wrong_source"} {
		t.Run(scenario, func(t *testing.T) {
			owned, plugin := pluginStoreFixture(t)
			if scenario != "missing_target" {
				reviewedMediaRefreshTestConnection(t, owned, *in.Reference, scenario == "changed_account")
			}
			if scenario == "receipt_failure" {
				if _, e := owned.DB.Exec(`CREATE TRIGGER owned_receipt_failure BEFORE INSERT ON plugin_migration_receipts BEGIN SELECT RAISE(ABORT,'owned'); END`); e != nil {
					t.Fatal(e)
				}
			}
			source := "MediaServerRefresh"
			if scenario == "wrong_source" {
				source = "OwnedOtherPlugin"
			}
			r, e := plugin.BeginMigration(ctx, strings.Repeat("5", 64), source, "MediaServerRefresh")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = plugin.ImportReviewedMediaRefresh(ctx, r.ID, raw); e == nil {
				t.Fatal("invalid target or authority committed")
			}
			if e = owned.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_installations)+(SELECT COUNT(*) FROM plugin_values)+(SELECT COUNT(*) FROM plugin_migration_receipts)+(SELECT COUNT(*) FROM records WHERE kind='plugin_media_reference_mapping')`).Scan(&count); e != nil || count != 0 {
				t.Fatal("failed import partially committed")
			}
		})
	}
	t.Log("selected_source_media_refresh_mapping_atomic_aead_cold_replay_and_rollback_verified")
}

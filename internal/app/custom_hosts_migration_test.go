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

func TestReviewedCustomHostsRejectsUnreviewedAndNullInput(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"version":1}`, `{"config":"{}"}`, `{"version":1,"version":1}`, `{} {}`} {
		if ValidateReviewedCustomHostsInput([]byte(raw)) == nil {
			t.Fatal("unreviewed source accepted")
		}
	}
	s, p := pluginStoreFixture(t)
	run, e := p.BeginMigration(context.Background(), strings.Repeat("5", 64), "CustomHosts", "CustomHosts")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.ImportReviewedCustomHosts(context.Background(), run.ID, []byte(`{}`)); e == nil {
		t.Fatal("unreviewed import accepted")
	}
	var n int
	if e = s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_installations)+(SELECT COUNT(*) FROM plugin_values)+(SELECT COUNT(*) FROM plugin_migration_receipts)`).Scan(&n); e != nil || n != 0 {
		t.Fatal("invalid input changed target", n, e)
	}
}

// The actual selected private configuration arrives only on stdin in this
// explicit owned fixture. It is never embedded in testdata, arguments or logs.
// The ordinary suite skips this case; the delivery controller executes it.
func TestReviewedCustomHostsSelectedStdin(t *testing.T) {
	if os.Getenv("LIGHT_MEDIA_REVIEWED_CUSTOM_HOSTS") != "owned-selected-stdin" {
		t.Skip("requires actual selected source on stdin and only owned temporary stores")
	}
	raw, e := io.ReadAll(io.LimitReader(os.Stdin, (1<<20)+1))
	if e != nil || ValidateReviewedCustomHostsInput(raw) != nil {
		t.Fatal("selected input validation failed")
	}
	in, e := validateReviewedCustomHosts(raw)
	if e != nil {
		t.Fatal("selected input validation failed")
	}
	var source any
	if json.Unmarshal([]byte(in.Config), &source) != nil {
		t.Fatal("source JSON invalid")
	}
	expected, _ := json.Marshal(source)
	ctx := context.Background()
	s, p := pluginStoreFixture(t)
	run, e := p.BeginMigration(ctx, strings.Repeat("6", 64), "CustomHosts", "CustomHosts")
	if e != nil {
		t.Fatal(e)
	}
	receipt, e := p.ImportReviewedCustomHosts(ctx, run.ID, raw)
	if e != nil {
		t.Fatal("selected atomic import failed")
	}
	stored, e := p.Config(ctx, "CustomHosts")
	var storedSource any
	decodeErr := json.Unmarshal(stored.Value, &storedSource)
	canonicalStored, _ := json.Marshal(storedSource)
	if e != nil || decodeErr != nil || !bytes.Equal(canonicalStored, expected) {
		t.Fatal("source configuration changed")
	}
	i, e := p.Installation(ctx, "CustomHosts")
	if e != nil || !i.DesiredEnabled || i.PluginID != "CustomHosts" {
		t.Fatal("source enabled intent lost")
	}
	var sealed []byte
	if e = s.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id='CustomHosts' AND kind='config'`).Scan(&sealed); e != nil || bytes.Contains(sealed, stored.Value) {
		t.Fatal("source configuration not encrypted")
	}
	var n int
	if e = s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_runs)+(SELECT COUNT(*) FROM plugin_schedules)+(SELECT COUNT(*) FROM plugin_event_subscriptions)`).Scan(&n); e != nil || n != 0 {
		t.Fatal("offline import executed work", n)
	}
	for _, key := range []string{"version", "source_container", "source_sha256", "config", "installed_count", "kv_count", "file_count", "symlink_count"} {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		delete(fields, key)
		b, _ := json.Marshal(fields)
		if ValidateReviewedCustomHostsInput(b) == nil {
			t.Fatal("missing reviewed input field accepted")
		}
		fields[key] = json.RawMessage(`null`)
		b, _ = json.Marshal(fields)
		if ValidateReviewedCustomHostsInput(b) == nil {
			t.Fatal("null reviewed input field accepted")
		}
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
	if _, e = p.SaveConfig(ctx, "CustomHosts", stored.Revision, map[string]any{"enabled": false, "hosts": "", "err_hosts": []string{}, "owned_edit": true}); e != nil {
		t.Fatal(e)
	}
	edited, e := p.Config(ctx, "CustomHosts")
	if e != nil {
		t.Fatal(e)
	}
	replay, e := p.ImportReviewedCustomHosts(ctx, run.ID, raw)
	if e != nil || replay != receipt {
		t.Fatal("cold receipt replay changed")
	}
	current, e := p.Config(ctx, "CustomHosts")
	if e != nil || !bytes.Equal(current.Value, edited.Value) || current.Revision != edited.Revision {
		t.Fatal("replay overwrote user edit")
	}
	failed, fp := pluginStoreFixture(t)
	fr, e := fp.BeginMigration(ctx, strings.Repeat("7", 64), "CustomHosts", "CustomHosts")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = failed.DB.Exec(`CREATE TRIGGER owned_fail BEFORE INSERT ON plugin_migration_receipts BEGIN SELECT RAISE(ABORT,'owned'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = fp.ImportReviewedCustomHosts(ctx, fr.ID, raw); e == nil {
		t.Fatal("receipt failure committed import")
	}
	if e = failed.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_installations)+(SELECT COUNT(*) FROM plugin_values)+(SELECT COUNT(*) FROM plugin_migration_receipts)`).Scan(&n); e != nil || n != 0 {
		t.Fatal("receipt failure partially committed")
	}
	wrong, e := fp.BeginMigration(ctx, strings.Repeat("8", 64), "OtherPlugin", "CustomHosts")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = fp.ImportReviewedCustomHosts(ctx, wrong.ID, raw); e == nil {
		t.Fatal("wrong source authority accepted")
	}
	t.Log("selected_source_atomic_aead_cold_replay_user_edit_and_rollback_verified")
}

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func ownedSiteMessageInput(t *testing.T, value bool) []byte {
	t.Helper()
	yes, zero := true, 0
	in := sourceSiteMessageSettings{Version: 1, Mode: sourceSiteMessageMode, Known: &yes, Value: &value, Normalized: []string{}, SourceHashes: map[string]string{"/app/app/core/config.py": reviewedSourceSettingsHashes["/app/app/core/config.py"], "/app/app/utils/system.py": reviewedSourceSettingsHashes["/app/app/utils/system.py"]}, EnvPath: "/config/app.env", EnvSHA: strings.Repeat("2", 64), CapturesEqual: &yes, ConfigUnchanged: &yes, AppImports: &zero, Constructors: &zero, Audit: sourceSiteMessageAudit{&zero, &zero, &zero}}
	b, e := json.Marshal(in)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func ownedSiteMessageTarget(t *testing.T) string {
	t.Helper()
	dir := ownedSourceSettingsTarget(t)
	if _, e := ImportReviewedSourceSettings(context.Background(), dir, ownedSourceSettingsInput(t), true); e != nil {
		t.Fatal(e)
	}
	return dir
}

func TestReviewedSiteMessageSettingsOfflineDryApplyReplayAndColdPolicy(t *testing.T) {
	for _, value := range []bool{false, true} {
		t.Run(strconv.FormatBool(value), func(t *testing.T) {
			ctx := context.Background()
			dir := ownedSiteMessageTarget(t)
			raw := ownedSiteMessageInput(t, value)
			before := ownedSourceSettingsFiles(t, dir)
			result, e := ImportReviewedSiteMessageSettings(ctx, dir, raw, false)
			if e != nil || result.State != "ready" || result.Writes != 0 || result.Records != 1 || result.Jobs != 0 || result.Calls != 0 {
				t.Fatal(result, e)
			}
			if !reflect.DeepEqual(before, ownedSourceSettingsFiles(t, dir)) {
				t.Fatal("dry run changed a byte/metadata/key/schema")
			}
			result, e = ImportReviewedSiteMessageSettings(ctx, dir, raw, true)
			if e != nil || result.State != "imported" || result.Writes != 1 || result.Jobs != 0 || result.Calls != 0 {
				t.Fatal(result, e)
			}
			after := ownedSourceSettingsFiles(t, dir)
			for _, apply := range []bool{false, true} {
				result, e = ImportReviewedSiteMessageSettings(ctx, dir, raw, apply)
				if e != nil || result.State != "already_imported" || result.Writes != 0 {
					t.Fatal(result, e)
				}
				if !reflect.DeepEqual(after, ownedSourceSettingsFiles(t, dir)) {
					t.Fatal("replay changed persistent files")
				}
			}
			a, e := New(Options{DataDir: dir})
			if e != nil {
				t.Fatal(e)
			}
			defer a.Close()
			if a.Settings().ReadSiteMessages != nil {
				t.Fatal("source import overwrote native user preference")
			}
			policy, e := a.sourceSiteMessagePolicy(ctx, a.Settings())
			if e != nil || policy.enabled != value || policy.sourceProof == "" {
				t.Fatal("cold source flag lost", policy, e)
			}
			projection := a.publicSettings()
			if projection["read_site_messages"] != nil || projection["effective_read_site_messages"] != value || projection["read_site_messages_error"] != nil || a.Settings().ReadSiteMessages != nil {
				t.Fatal("public value misrepresented inherited flag or changed native preference")
			}
			explicit := !value
			c := a.Settings()
			c.ReadSiteMessages = &explicit
			if e = a.saveSettings(ctx, c); e != nil {
				t.Fatal(e)
			}
			policy, e = a.sourceSiteMessagePolicy(ctx, a.Settings())
			if e != nil || policy.enabled != explicit {
				t.Fatal("explicit user flag overwritten", e)
			}
			projection = a.publicSettings()
			if projection["read_site_messages"] != explicit || projection["effective_read_site_messages"] != explicit {
				t.Fatal("public value ignored explicit user choice")
			}
			var n int
			if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind IN ('source_statistics_settings','source_network_settings','source_runtime_settings','source_settings_migration')`).Scan(&n); e != nil || n != 4 {
				t.Fatal("prior source domains changed", n, e)
			}
			var body []byte
			if e = a.Store.DB.QueryRow(`SELECT payload FROM records WHERE kind=?`, sourceSiteMessageKind).Scan(&body); e != nil || bytes.Contains(body, []byte(sourceSiteMessageMode)) {
				t.Fatal("source proof not encrypted", e)
			}
			// A running target, even without active jobs, remains unavailable to importer.
			if _, e = ImportReviewedSiteMessageSettings(ctx, dir, raw, true); e == nil {
				t.Fatal("running target admitted")
			}
		})
	}
}

func TestReviewedSiteMessageSettingsRejectsInvalidPartialAndConflictingParent(t *testing.T) {
	for _, mode := range []string{"no-parent", "different-env", "missing-parent-domain", "corrupt-parent-domain", "changed-parent-domain", "corrupt-parent-receipt", "conflicting-flag", "rollback", "queued-work"} {
		t.Run(mode, func(t *testing.T) {
			dir := ownedSiteMessageTarget(t)
			ctx := context.Background()
			raw := ownedSiteMessageInput(t, false)
			store, e := OpenStore(dir)
			if e != nil {
				t.Fatal(e)
			}
			p := &PluginStore{store: store}
			switch mode {
			case "no-parent":
				_, e = store.DB.Exec(`DELETE FROM records WHERE kind='source_settings_migration'`)
			case "different-env":
				var in sourceSiteMessageSettings
				json.Unmarshal(raw, &in)
				in.EnvSHA = strings.Repeat("3", 64)
				raw, _ = json.Marshal(in)
			case "missing-parent-domain":
				_, e = store.DB.Exec(`DELETE FROM records WHERE kind='source_runtime_settings'`)
			case "corrupt-parent-domain":
				_, e = store.DB.Exec(`UPDATE records SET payload=zeroblob(length(payload)) WHERE kind='source_network_settings'`)
			case "changed-parent-domain":
				var input sourceSettingsInput
				json.Unmarshal(ownedSourceSettingsInput(t), &input)
				input.Runtime.Timezone = "Etc/UTC"
				body, _ := json.Marshal(input.Runtime)
				var sealed []byte
				sealed, e = p.seal("source_runtime_settings", "system", "main", body)
				if e == nil {
					_, e = store.DB.Exec(`UPDATE records SET payload=? WHERE kind='source_runtime_settings'`, sealed)
				}
			case "corrupt-parent-receipt":
				_, e = store.DB.Exec(`UPDATE records SET payload=zeroblob(length(payload)) WHERE kind='source_settings_migration'`)
			case "conflicting-flag":
				var sealed []byte
				sealed, e = p.seal(sourceSiteMessageKind, "system", "main", ownedSiteMessageInput(t, true))
				if e == nil {
					_, e = store.DB.Exec(`INSERT INTO records(kind,id,payload,updated_at) VALUES(?,'main',?,'owned')`, sourceSiteMessageKind, sealed)
				}
			case "rollback":
				_, e = store.DB.Exec(`CREATE TRIGGER owned_site_message_fail BEFORE INSERT ON records WHEN NEW.kind='source_site_message_settings' BEGIN SELECT RAISE(ABORT,'owned'); END`)
			case "queued-work":
				_, e = store.DB.Exec(`INSERT INTO jobs(id,workflow_id,kind,state,payload,run_at,created_at,updated_at) VALUES('owned-message-job','owned-workflow','owned','queued','{}',0,'owned','owned')`)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = store.Close(); e != nil {
				t.Fatal(e)
			}
			before := ownedSourceSettingsFiles(t, dir)
			for _, apply := range []bool{false, true} {
				if mode == "rollback" && !apply {
					continue
				}
				_, e = ImportReviewedSiteMessageSettings(ctx, dir, raw, apply)
				if e == nil {
					t.Fatal("bad source/target admitted", apply)
				}
			}
			if !reflect.DeepEqual(before, ownedSourceSettingsFiles(t, dir)) {
				t.Fatal("failed import changed existing target")
			}
		})
	}
}

func TestReviewedSiteMessageSettingsInputAndTargetBoundaries(t *testing.T) {
	raw := ownedSiteMessageInput(t, false)
	if result, e := ValidateReviewedSiteMessageSettingsInput(raw); e != nil || result.State != "validated" || result.Writes != 0 {
		t.Fatal(e, result)
	}
	for _, field := range []string{"site_message", "known", "captures_equal", "original_config_unchanged", "original_app_imports", "original_settings_constructors", "audit", "normalization_fields", "source_hashes", "mode", "version", "env_sha256"} {
		t.Run(field, func(t *testing.T) {
			var v map[string]any
			json.Unmarshal(raw, &v)
			v[field] = nil
			b, _ := json.Marshal(v)
			if _, e := ValidateReviewedSiteMessageSettingsInput(b); e == nil {
				t.Fatal("missing/null field admitted")
			}
		})
	}
	for _, b := range [][]byte{append([]byte(`{"site_message":true,`), raw[1:]...), []byte(`{"version":1}`), append(raw, []byte(` {}`)...), bytes.Replace(raw, []byte(`"denied_network":0`), []byte(`"denied_network":1`), 1), bytes.Replace(raw, []byte(`"site_message":false`), []byte(`"site_message":"false"`), 1)} {
		if _, e := ValidateReviewedSiteMessageSettingsInput(b); e == nil {
			t.Fatal("ambiguous/side effect source admitted")
		}
	}
	dir := ownedSiteMessageTarget(t)
	link := filepath.Join(t.TempDir(), "linked")
	if e := os.Symlink(dir, link); e != nil {
		t.Fatal(e)
	}
	if _, e := ImportReviewedSiteMessageSettings(context.Background(), link, raw, true); e == nil {
		t.Fatal("symlink target admitted")
	}
}

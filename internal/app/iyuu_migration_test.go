package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/download"
	"light-media/internal/sites"
)

func TestReviewedIYUURejectsUnreviewedInput(t *testing.T) {
	for _, raw := range []string{"", "null", "{}", `{"version":1}`, `{"version":1,"config":null}`} {
		if ValidateReviewedIYUUAutoSeedInput([]byte(raw)) == nil {
			t.Fatal("unreviewed source accepted")
		}
	}
}

func TestReviewedIYUUDownloaderEffectiveEndpoint(t *testing.T) {
	for _, c := range []struct {
		kind, raw string
		original  bool
		want      string
	}{
		{"qbittorrent", "127.0.0.1:18080/", true, "http://127.0.0.1:18080"},
		{"qbittorrent", "http://127.0.0.1:18080/proxy/", false, "http://127.0.0.1:18080/proxy"},
		{"transmission", "http://127.0.0.1:19091", true, "http://127.0.0.1:19091/transmission/rpc"},
		{"transmission", "http://127.0.0.1:19091/ignored-prefix", true, "http://127.0.0.1:19091/transmission/rpc"},
		{"transmission", "http://127.0.0.1:19091/transmission/rpc/", false, "http://127.0.0.1:19091/transmission/rpc"},
		{"transmission", "https://EXAMPLE.invalid:443/proxy/rpc/", false, "https://example.invalid/proxy/rpc"},
		{"transmission", "http://[::1]:80/", false, "http://[::1]/transmission/rpc"},
	} {
		got, e := reviewedIYUUDownloaderEndpoint(c.kind, c.raw, c.original)
		if e != nil || got != c.want {
			t.Fatal("effective daemon endpoint changed", c.kind, e)
		}
	}
	for _, raw := range []string{"127.0.0.1:19091", "ftp://owned.invalid", "http://user:pass@owned.invalid", "https://owned.invalid/?token=private", "https://owned.invalid/#x", "https://owned.invalid/%2fsecret"} {
		if _, e := reviewedIYUUDownloaderEndpoint("transmission", raw, false); e == nil {
			t.Fatal("ambiguous target endpoint accepted")
		}
	}
}

func reviewedIYUUOwnedTarget(t *testing.T, in reviewedIYUUSnapshot) (*Store, *PluginStore, Settings) {
	t.Helper()
	s, p := pluginStoreFixture(t)
	return s, p, reviewedIYUUOwnedTargetSettings(t, s, in)
}
func reviewedIYUUOwnedTargetSettings(t *testing.T, s *Store, in reviewedIYUUSnapshot) Settings {
	t.Helper()
	settings := Settings{}
	for i, source := range in.Sites {
		kind, origin, _, e := siteImportOrigin(source.URL)
		if e != nil {
			t.Fatal("source origin invalid")
		}
		id := "owned-site-" + itoaSourceSite(source.OriginalID)
		settings.Sites = append(settings.Sites, SiteConfig{Config: sites.Config{ID: id, Kind: kind, Name: "owned", BaseURL: origin}, Enabled: true})
		identity := nativeSiteIdentity{SourceID: int64(100 + i), SiteID: id, CanonicalOrigin: origin, State: "active", CreatedAt: now()}
		if _, e = s.DB.Exec(`INSERT INTO native_site_identities VALUES(?,?,?,?,?,?,?)`, identity.SourceID, id, origin, identity.State, identity.CreatedAt, "", s.siteIdentityMAC("mapping", identity)); e != nil {
			t.Fatal(e)
		}
	}
	for _, source := range in.Downloaders {
		endpoint, e := reviewedIYUUDownloaderEndpoint(source.Kind, source.Host, true)
		if e != nil {
			t.Fatal(e)
		}
		enabled := true
		settings.Downloaders = append(settings.Downloaders, DownloaderConfig{Config: download.Config{ID: "owned-" + source.Kind, Kind: source.Kind, BaseURL: endpoint, Username: source.Username, Password: source.Password}, Name: source.Name, Enabled: &enabled})
	}
	reviewedIYUUOwnedSettings(t, s, settings)
	return settings
}
func reviewedIYUUOwnedSettings(t *testing.T, s *Store, settings Settings) {
	t.Helper()
	sealed, e := s.Seal(settings)
	if e != nil {
		t.Fatal("owned target settings seal failed")
	}
	if _, e = s.DB.Exec(`INSERT INTO records VALUES('config','main',?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload`, sealed, now()); e != nil {
		t.Fatal(e)
	}
}
func reviewedIYUUOwnedProtocol(t *testing.T, p *PluginStore, in PluginInstallation, wantValid bool) {
	t.Helper()
	tx, e := p.store.DB.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	_, e = p.readSiteReferenceProtocol(context.Background(), tx, in)
	if (e == nil) != wantValid {
		t.Fatal("original/native namespace proof mismatch", e)
	}
	if wantValid {
		root, _, e := p.iyuuRootReadTx(context.Background(), tx, in.InstanceID, in.Generation)
		if e != nil {
			t.Fatal(e)
		}
		if unknown, e := p.iyuuRootChainTx(context.Background(), tx, in.InstanceID, in.Generation, root.Journal, true); e != nil || unknown {
			t.Fatal("cold original owner chain invalid", e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
}

func TestReviewedIYUUSelectedStdin(t *testing.T) {
	if os.Getenv("LIGHT_MEDIA_REVIEWED_IYUU") != "owned-selected-stdin" {
		t.Skip("requires selected source stdin and owned stores only")
	}
	raw, e := io.ReadAll(io.LimitReader(os.Stdin, pluginImportLimit+1))
	if e != nil || len(raw) > pluginImportLimit {
		t.Fatal("selected source read failed")
	}
	source, data, e := validateReviewedIYUU(raw)
	if e != nil {
		t.Fatal("reviewed source metadata/config/KV/reference validation failed")
	}
	fingerprint, e := ReviewedIYUUAutoSeedFingerprint(raw)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	s, p, settings := reviewedIYUUOwnedTarget(t, source)
	run, e := p.BeginMigration(ctx, fingerprint, "IYUUAutoSeed", "IYUUAutoSeed")
	if e != nil {
		t.Fatal(e)
	}
	receipt, e := p.ImportReviewedIYUUAutoSeed(ctx, run.ID, raw)
	if e != nil {
		t.Fatal("selected original import failed", e)
	}
	installation, e := p.Installation(ctx, "IYUUAutoSeed")
	if e != nil || installation.DesiredEnabled {
		t.Fatal("offline import activated original schedules")
	}
	stored, e := p.Config(ctx, "IYUUAutoSeed")
	if e != nil {
		t.Fatal(e)
	}
	var original, mapped map[string]json.RawMessage
	if json.Unmarshal([]byte(source.Config), &original) != nil || json.Unmarshal(stored.Value, &mapped) != nil {
		t.Fatal("configuration parse failed")
	}
	var converted []int64
	if json.Unmarshal(mapped["sites"], &converted) != nil || len(converted) != 17 {
		t.Fatal("site reference conversion incomplete")
	}
	for i, id := range converted {
		if id != int64(100+i) || id == source.Sites[i].OriginalID {
			t.Fatal("coincident old integer was treated as native identity")
		}
	}
	delete(original, "sites")
	delete(mapped, "sites")
	if !bytes.Equal(mustCleanSeedJSON(original), mustCleanSeedJSON(mapped)) {
		t.Fatal("non-reference source configuration or caches changed")
	}
	for key, value := range data {
		got, e := p.Data(ctx, "IYUUAutoSeed", key)
		if e != nil || !bytes.Equal(got.Value, value) || got.Revision != 1 {
			t.Fatal("original history changed")
		}
		var sealed []byte
		if e = s.DB.QueryRow(`SELECT sealed FROM plugin_values WHERE instance_id='IYUUAutoSeed' AND kind='data' AND item_key=?`, key).Scan(&sealed); e != nil || bytes.Contains(sealed, value) {
			t.Fatal("history not encrypted")
		}
	}
	for i, site := range source.Sites {
		identity, e := (&App{Store: s}).migratedSiteIdentity(ctx, fingerprint, site.OriginalID)
		if e != nil || identity.SourceID != int64(100+i) {
			t.Fatal("independent original-to-native receipt invalid", e)
		}
	}
	reviewedIYUUOwnedProtocol(t, p, installation, true)
	var count int
	if e = s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM plugin_runs)+(SELECT COUNT(*) FROM plugin_schedules)+(SELECT COUNT(*) FROM plugin_event_subscriptions)`).Scan(&count); e != nil || count != 0 {
		t.Fatal("offline migration executed business")
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
	reviewedIYUUOwnedProtocol(t, p, installation, true)
	var edited map[string]json.RawMessage
	if json.Unmarshal(stored.Value, &edited) != nil {
		t.Fatal("owned edit parse failed")
	}
	edited["notify"] = json.RawMessage(`true`)
	changed, e := p.SaveConfig(ctx, "IYUUAutoSeed", stored.Revision, edited)
	if e != nil {
		t.Fatal("converted native config could not be edited", e)
	}
	settings.Downloaders[0].Username = "owned-later-change"
	reviewedIYUUOwnedSettings(t, s, settings)
	replayed, e := p.ImportReviewedIYUUAutoSeed(ctx, run.ID, raw)
	if e != nil || replayed != receipt {
		t.Fatal("cold source replay did not retain original receipt", e)
	}
	current, e := p.Config(ctx, "IYUUAutoSeed")
	if e != nil || current.Revision != changed.Revision || !bytes.Equal(current.Value, changed.Value) {
		t.Fatal("replay overwrote user edits")
	}
	for _, scenario := range []string{"late_receipt", "site_receipt", "binding", "protocol", "wrong_account", "deleted_target", "wrong_source", "wrong_fingerprint"} {
		t.Run(scenario, func(t *testing.T) {
			s, p, settings := reviewedIYUUOwnedTarget(t, source)
			sourceID := "IYUUAutoSeed"
			fp := fingerprint
			if scenario == "wrong_source" {
				sourceID = "OwnedOtherPlugin"
			}
			if scenario == "wrong_fingerprint" {
				fp = strings.Repeat("a", 64)
			}
			run, e := p.BeginMigration(ctx, fp, sourceID, "IYUUAutoSeed")
			if e != nil {
				t.Fatal(e)
			}
			if scenario == "wrong_account" {
				settings.Downloaders[1].Username = "owned-other-account"
				reviewedIYUUOwnedSettings(t, s, settings)
			}
			if scenario == "deleted_target" {
				identity, e := s.nativeSiteByID(ctx, 100)
				if e != nil {
					t.Fatal(e)
				}
				identity.State = "deleted"
				identity.DeletedAt = now()
				tx, e := s.DB.BeginTx(ctx, nil)
				if e != nil {
					t.Fatal(e)
				}
				if e = s.writeNativeSiteIdentityTx(ctx, tx, identity); e != nil {
					tx.Rollback()
					t.Fatal(e)
				}
				if e = tx.Commit(); e != nil {
					t.Fatal(e)
				}
			}
			tables := []string{"plugin_installations", "plugin_values", "plugin_migration_receipts", "plugin_iyuu_roots", "native_site_source_snapshots", "native_site_migration_receipts", "native_site_reference_protocols"}
			presence := map[string]int{}
			for _, table := range tables {
				var exists int
				if e = s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); e != nil {
					t.Fatal(e)
				}
				presence[table] = exists
			}
			trigger := ""
			switch scenario {
			case "late_receipt":
				trigger = `BEFORE INSERT ON plugin_migration_receipts BEGIN SELECT RAISE(ABORT,'owned receipt failure'); END`
			case "site_receipt":
				trigger = `BEFORE INSERT ON native_site_migration_receipts BEGIN SELECT RAISE(ABORT,'owned site receipt failure'); END`
			case "binding":
				trigger = `BEFORE INSERT ON records WHEN NEW.kind='plugin_original_iyuu_references' BEGIN SELECT RAISE(ABORT,'owned mapping failure'); END`
			case "protocol":
				trigger = `BEFORE UPDATE ON native_site_reference_protocols WHEN (SELECT COUNT(*) FROM native_site_migration_receipts)>0 BEGIN SELECT RAISE(ABORT,'owned protocol failure'); END`
			}
			if trigger != "" {
				if _, e = s.DB.Exec(`CREATE TRIGGER owned_import_failure ` + trigger); e != nil {
					t.Fatal(e)
				}
			}
			if _, e = p.ImportReviewedIYUUAutoSeed(ctx, run.ID, raw); e == nil {
				t.Fatal("unmatched target/source or failed write accepted")
			}
			for _, table := range tables {
				var exists int
				if e = s.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); e != nil || exists != presence[table] {
					t.Fatal("partial schema survived rollback", table, e)
				}
				if exists == 0 {
					continue
				}
				if e = s.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); e != nil || count != 0 {
					t.Fatal("partial reference/config/history import survived", table, e)
				}
			}
			if e = s.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind=?`, reviewedIYUUMappingKind).Scan(&count); e != nil || count != 0 {
				t.Fatal("partial reference binding survived")
			}
		})
	}
	if _, e = s.DB.Exec(`DELETE FROM plugin_migration_receipts WHERE source_fingerprint=?`, fingerprint); e != nil {
		t.Fatal(e)
	}
	reviewedIYUUOwnedProtocol(t, p, installation, false)
	if _, e = p.SaveConfig(ctx, "IYUUAutoSeed", current.Revision, current.Value); e == nil {
		t.Fatal("missing final migration receipt admitted activation")
	}
	if _, e = s.DB.Exec(`INSERT INTO plugin_migration_receipts(source_fingerprint,source_plugin_id,instance_id,run_id,digest,imported_at) VALUES(?,?,?,?,?,?)`, receipt.SourceFingerprint, receipt.SourcePluginID, receipt.InstanceID, receipt.RunID, receipt.Digest, receipt.ImportedAt); e != nil {
		t.Fatal(e)
	}
	reviewedIYUUOwnedProtocol(t, p, installation, true)
	if _, e = s.DB.Exec(`DELETE FROM native_site_migration_receipts WHERE source_fingerprint=? AND original_source_id=?`, fingerprint, source.Sites[0].OriginalID); e != nil {
		t.Fatal(e)
	}
	reviewedIYUUOwnedProtocol(t, p, installation, false)
	if _, e = p.SaveConfig(ctx, "IYUUAutoSeed", current.Revision, current.Value); e == nil {
		t.Fatal("missing independent receipt was replaced by config save")
	}
	t.Run("offline_cli", func(t *testing.T) { reviewedIYUUSelectedCLI(t, source, raw) })
	for _, kind := range []string{"qbittorrent", "transmission"} {
		t.Run("sdk_"+kind, func(t *testing.T) { reviewedIYUUSelectedSDK(t, source, raw, kind) })
	}
	t.Log("selected_original_iyuu_verified: 329 KV, 17 independent numeric-reference mappings, two downloader authorities, unchanged caches/history, cold replay and atomic rollback; no workers/network/business")
}

func TestReviewedIYUUCollectorWireDigest(t *testing.T) {
	data := map[string]json.RawMessage{"original-hash": json.RawMessage(`[{"downloader":"owned","torrents":[]}]`)}
	if reviewedIYUUDataDigest(data) != "e4174e0cc7cd13f1191170c53cf26973e1d760914ebec728996e893648ddfeec" {
		t.Fatal("independent collector wire hash differs")
	}
}

func reviewedIYUUFileSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	e := filepath.WalkDir(dir, func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		relative, e := filepath.Rel(dir, path)
		if e != nil {
			return e
		}
		if entry.IsDir() {
			out[relative] = info.Mode().String()
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unexpected owned fixture file type")
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		out[relative] = fmt.Sprintf("%s:%x", info.Mode().String(), sha256.Sum256(raw))
		return nil
	})
	if e != nil {
		t.Fatal("owned fixture snapshot failed")
	}
	return out
}
func reviewedIYUUSelectedCLI(t *testing.T, source reviewedIYUUSnapshot, raw []byte) {
	t.Helper()
	binary := os.Getenv("LIGHT_MEDIA_REVIEWED_IYUU_CLI")
	if binary == "" || !filepath.IsAbs(binary) {
		t.Fatal("owned CLI binary required for selected-source verification")
	}
	ctx := context.Background()
	s, _, _ := reviewedIYUUOwnedTarget(t, source)
	dir := filepath.Dir(s.lock.Name())
	invoke := func(input []byte, wantSuccess bool, args ...string) map[string]any {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Stdin = bytes.NewReader(input)
		out, e := cmd.CombinedOutput()
		if (e == nil) != wantSuccess {
			t.Fatal("owned offline CLI status mismatch", e, string(out))
		}
		if !wantSuccess {
			return nil
		}
		var result map[string]any
		if json.Unmarshal(out, &result) != nil {
			t.Fatal("owned CLI did not return bounded JSON")
		}
		return result
	}
	// A live target is rejected before writable Store/schema startup.
	invoke(raw, false, "--target-data-dir", dir)
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	before := reviewedIYUUFileSnapshot(t, dir)
	sourceOnly := invoke(raw, true, "--validate")
	if sourceOnly["state"] != "validated" || sourceOnly["writes"] != float64(0) {
		t.Fatal("source-only validation wrote a target")
	}
	dry := invoke(raw, true, "--target-data-dir", dir)
	if dry["state"] != "ready" || dry["writes"] != float64(0) {
		t.Fatal("read-only target check failed")
	}
	if !bytes.Equal(reviewedIYUUJSON(before), reviewedIYUUJSON(reviewedIYUUFileSnapshot(t, dir))) {
		t.Fatal("dry-run altered database/key/lock/files")
	}
	applied := invoke(raw, true, "--target-data-dir", dir, "--apply")
	if applied["state"] != "imported" || applied["desired_enabled"] != false || applied["scheduled"] != false || applied["executed"] != false || applied["site_reference_mappings"] != float64(17) || applied["downloader_reference_mappings"] != float64(2) || applied["target_history_rows"] != float64(329) {
		t.Fatal("CLI lost the actual import scope")
	}
	replay := invoke(raw, true, "--target-data-dir", dir, "--apply")
	if replay["replayed"] != true || !bytes.Equal(reviewedIYUUJSON(replay["receipt"]), reviewedIYUUJSON(applied["receipt"])) {
		t.Fatal("CLI replay changed source receipt")
	}
	before = reviewedIYUUFileSnapshot(t, dir)
	var invalid map[string]json.RawMessage
	if json.Unmarshal(raw, &invalid) != nil {
		t.Fatal("owned mutation parse failed")
	}
	invalid["config"] = json.RawMessage(`"{}"`)
	invoke(reviewedIYUUJSON(invalid), false, "--target-data-dir", dir, "--apply")
	if !bytes.Equal(reviewedIYUUJSON(before), reviewedIYUUJSON(reviewedIYUUFileSnapshot(t, dir))) {
		t.Fatal("rejected source altered target")
	}
	cold, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	p, e := NewPluginStore(ctx, cold)
	if e != nil {
		t.Fatal(e)
	}
	installation, e := p.Installation(ctx, "IYUUAutoSeed")
	if e != nil {
		t.Fatal(e)
	}
	reviewedIYUUOwnedProtocol(t, p, installation, true)
	var count int
	if e = cold.DB.QueryRow(`SELECT COUNT(*) FROM plugin_values WHERE instance_id='IYUUAutoSeed' AND kind='data'`).Scan(&count); e != nil || count != 329 {
		t.Fatal("CLI cold history missing")
	}
}

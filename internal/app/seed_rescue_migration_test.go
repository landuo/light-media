package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	sr "light-media/internal/plugins/seedrescue"
	"light-media/internal/sites"
)

func TestReviewedSeedRejectsUnreviewedInput(t *testing.T) {
	for _, raw := range []string{"", "null", "{}", `{"version":1}`, `{"version":1,"config":null}`} {
		if ValidateReviewedSeedRescueInput([]byte(raw)) == nil {
			t.Fatal("unreviewed SeedRescue snapshot admitted")
		}
	}
}
func seedMigrationIdentity(t *testing.T, s *Store, id int64, appID, origin, state string) nativeSiteIdentity {
	t.Helper()
	v := nativeSiteIdentity{SourceID: id, SiteID: appID, CanonicalOrigin: origin, State: state, CreatedAt: now()}
	if state == "deleted" {
		v.DeletedAt = now()
	}
	if _, e := s.DB.Exec(`INSERT INTO native_site_identities VALUES(?,?,?,?,?,?,?)`, v.SourceID, v.SiteID, v.CanonicalOrigin, v.State, v.CreatedAt, v.DeletedAt, s.siteIdentityMAC("mapping", v)); e != nil {
		t.Fatal(e)
	}
	return v
}
func TestSeedRescueAliasesDoNotConfuseNativeAndOriginalNumbers(t *testing.T) {
	s, p := pluginStoreFixture(t)
	ctx := context.Background()
	one := seedMigrationIdentity(t, s, 1, "owned-one", "http://127.0.0.1:21001", "active")
	two := seedMigrationIdentity(t, s, 2, "owned-two", "http://127.0.0.1:21002", "active")
	seedMigrationIdentity(t, s, 3, "owned-new", "http://127.0.0.1:21003", "active")
	b := &reviewedSeedBinding{ReservedCeiling: 18, Sites: []reviewedIYUUBoundSite{{OriginalID: 1, NativeID: 2, SiteID: two.SiteID}, {OriginalID: 2, NativeID: 1, SiteID: one.SiteID}}}
	cases := map[int64]string{1: two.SiteID, 2: one.SiteID, 21: "owned-new"}
	for alias, want := range cases {
		got, e := p.seedRescueSiteIdentityTx(ctx, s.DB, b, alias)
		if e != nil || got.SiteID != want {
			t.Fatal("alias selected wrong identity", alias, e)
		}
		back, e := seedRescueSiteAlias(b, got)
		if e != nil || back != alias {
			t.Fatal("option/lookup not inverse", alias, e)
		}
	}
	if _, e := p.seedRescueSiteIdentityTx(ctx, s.DB, b, 3); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("reserved absent ID fell through to native3", e)
	}
	for _, alias := range []int64{0, 19, 20, seedRescueMaximumAlias + 1} {
		if _, e := p.seedRescueSiteIdentityTx(ctx, s.DB, b, alias); e == nil {
			t.Fatal("noncanonical alias admitted", alias)
		}
	}
	got, e := p.seedRescueSiteIdentityTx(ctx, s.DB, nil, 1)
	if e != nil || got.SiteID != one.SiteID {
		t.Fatal("fresh native namespace changed", e)
	}
	if _, e := seedRescueSiteAlias(b, nativeSiteIdentity{SourceID: seedRescueMaximumAlias, SiteID: "overflow"}); e == nil {
		t.Fatal("unsafe JavaScript integer emitted")
	}
}
func reviewedSeedOwned(t *testing.T, source reviewedSeedSnapshot) (*Store, *PluginStore, Settings) {
	t.Helper()
	s, p := pluginStoreFixture(t)
	settings := reviewedIYUUOwnedTargetSettings(t, s, reviewedIYUUSnapshot{Sites: source.Sites, Downloaders: source.Downloaders})
	// Two original IDs deliberately collide with different physical native IDs.
	for i, native := range map[int]int64{15: 2, 16: 1} {
		old, e := s.nativeSiteByAppID(context.Background(), settings.Sites[i].ID)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.DB.Exec(`DELETE FROM native_site_identities WHERE source_id=?`, old.SourceID); e != nil {
			t.Fatal(e)
		}
		seedMigrationIdentity(t, s, native, old.SiteID, old.CanonicalOrigin, "active")
	}
	return s, p, settings
}
func reviewedSeedRun(t *testing.T, p *PluginStore, raw []byte) PluginMigrationRun {
	t.Helper()
	fingerprint, e := ReviewedSeedRescueFingerprint(raw)
	if e != nil {
		t.Fatal("source fingerprint", e)
	}
	m, e := p.BeginMigration(context.Background(), fingerprint, sr.PluginID, sr.PluginID)
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func TestReviewedSeedSelectedStdin(t *testing.T) {
	if os.Getenv("LIGHT_MEDIA_REVIEWED_SEED") != "owned-selected-stdin" {
		t.Skip("requires reviewed source stdin; owned encrypted stores only")
	}
	raw, e := io.ReadAll(io.LimitReader(os.Stdin, pluginImportLimit+1))
	if e != nil || len(raw) > pluginImportLimit {
		t.Fatal("source read failed")
	}
	source, data, e := validateReviewedSeed(raw)
	if e != nil {
		t.Fatal("actual source profile rejected", e)
	}
	ctx := context.Background()

	for _, kind := range []string{"rename", "wrong-account", "wrong-endpoint", "missing-site"} {
		t.Run("initial-target-"+kind, func(t *testing.T) {
			s, p, settings := reviewedSeedOwned(t, source)
			switch kind {
			case "rename":
				settings.Downloaders[0].Name = "Owned pre-migration renamed QB"
			case "wrong-account":
				settings.Downloaders[0].Password = "owned-different-account"
			case "wrong-endpoint":
				settings.Downloaders[0].BaseURL = "http://127.0.0.1:29999"
			case "missing-site":
				settings.Sites = settings.Sites[1:]
			}
			reviewedIYUUOwnedSettings(t, s, settings)
			m := reviewedSeedRun(t, p, raw)
			_, e := p.ImportReviewedSeedRescue(ctx, m.ID, raw)
			if (e == nil) != (kind == "rename") {
				t.Fatal("initial identity validation mismatch", e)
			}
			if kind == "rename" {
				a := &App{Store: s, pluginStore: p, config: settings}
				o := &pluginSeedRescueOwner{a: a, instance: sr.PluginID, generation: 1}
				d, e := o.boundDownloader(ctx, source.Downloaders[0].Name)
				if e != nil || d.ID != settings.Downloaders[0].ID {
					t.Fatal("initial renamed daemon rebound", e)
				}
			} else {
				var count int
				if e := s.DB.QueryRow(`SELECT COUNT(*) FROM plugin_installations`).Scan(&count); e != nil || count != 0 {
					t.Fatal("rejected target partially installed", e)
				}
			}
		})
	}
	t.Run("missing-binding-replay-does-not-recreate", func(t *testing.T) {
		s, p, _ := reviewedSeedOwned(t, source)
		m := reviewedSeedRun(t, p, raw)
		if _, e := p.ImportReviewedSeedRescue(ctx, m.ID, raw); e != nil {
			t.Fatal(e)
		}
		if _, e := s.DB.Exec(`DELETE FROM records WHERE kind='plugin_original_seed_rescue_references'`); e != nil {
			t.Fatal(e)
		}
		if _, e := p.ImportReviewedSeedRescue(ctx, m.ID, raw); e == nil {
			t.Fatal("replay inferred missing binding anew")
		}
		var count int
		if e := s.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='plugin_original_seed_rescue_references'`).Scan(&count); e != nil || count != 0 {
			t.Fatal("replay recreated missing binding", e)
		}
	})
	t.Run("cold-aliases-options-replay-and-lifecycle", func(t *testing.T) {
		s, p, settings := reviewedSeedOwned(t, source)
		defer func() { s.Close() }()
		m := reviewedSeedRun(t, p, raw)
		if e := ValidateReviewedSeedRescueTarget(ctx, s.DB, s.key, raw); e != nil {
			t.Fatal("readonly target validation", e)
		}
		receipt, e := p.ImportReviewedSeedRescue(ctx, m.ID, raw)
		if e != nil {
			t.Fatal("reviewed import", e)
		}
		installation, e := p.Installation(ctx, sr.PluginID)
		if e != nil || installation.DesiredEnabled {
			t.Fatal("import activated source plugin", e)
		}
		stored, e := p.Config(ctx, sr.PluginID)
		if e != nil || !bytes.Equal(stored.Value, []byte(source.Config)) {
			t.Fatal("raw source config changed", e)
		}
		for k, v := range data {
			got, e := p.Data(ctx, sr.PluginID, k)
			if e != nil || got.Revision != 1 || !bytes.Equal(got.Value, v) {
				t.Fatal("raw source KV changed", e)
			}
		}
		state, _, e := p.seedRescueRead(ctx, s.DB, sr.PluginID, installation.Generation)
		if e != nil || len(state.Values) != 3 || len(state.Runs) != 0 {
			t.Fatal("imported owner proof", e)
		}
		binding, e := p.seedRescueNamespaceTx(ctx, s.DB, sr.PluginID)
		if e != nil || binding == nil {
			t.Fatal("reviewed namespace absent", e)
		}
		for _, site := range binding.Sites {
			got, e := p.seedRescueSiteIdentityTx(ctx, s.DB, binding, site.OriginalID)
			if e != nil || got.SourceID != site.NativeID || got.SiteID != site.SiteID {
				t.Fatal("source site binding mismatch", e)
			}
			if site.OriginalID == 1 && got.SourceID == 1 || site.OriginalID == 18 && got.SourceID != 1 {
				t.Fatal("coincident native identity used")
			}
		}
		a := &App{Store: s, pluginStore: p, config: settings}
		tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if e != nil {
			t.Fatal(e)
		}
		options, e := a.seedRescueOptionsTx(ctx, tx, sr.PluginID)
		tx.Rollback()
		if e != nil || len(options["sites"].([]sr.Object)) != 17 {
			t.Fatal("instance options", e)
		}
		for _, option := range options["sites"].([]sr.Object) {
			id := option["value"].(int64)
			if id > 18 || id == 3 {
				t.Fatal("native option leaked into source namespace")
			}
		}
		// Rename the original QB while retaining its source select value and ID.
		settings.Downloaders[0].Name = "Owned renamed qB"
		a.config = settings
		reviewedIYUUOwnedSettings(t, s, settings)
		owner := &pluginSeedRescueOwner{a: a, instance: sr.PluginID, generation: installation.Generation}
		d, e := owner.boundDownloader(ctx, binding.DownloaderName)
		if e != nil || d.ID != binding.DownloaderID || d.Name != "Owned renamed qB" {
			t.Fatal("source downloader label was retargeted", e)
		}
		dups := settings
		dups.Downloaders = append(append([]DownloaderConfig{}, settings.Downloaders...), settings.Downloaders[0])
		dups.Downloaders[1].ID = "owned-collision"
		dups.Downloaders[1].Name = binding.DownloaderName
		a.config = dups
		if _, e = owner.boundDownloader(ctx, binding.DownloaderName); e == nil {
			t.Fatal("downloader alias collision silently admitted")
		}
		a.config = settings
		seedMigrationIdentity(t, s, 3, "owned-new", "http://127.0.0.1:21003", "active")
		settings.Sites = append(settings.Sites, SiteConfig{Config: sites.Config{ID: "owned-new", Name: "Owned new site", Kind: "hhanclub", BaseURL: "http://127.0.0.1:21003"}, Enabled: true})
		a.config = settings
		reviewedIYUUOwnedSettings(t, s, settings)
		tx, e = s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if e != nil {
			t.Fatal(e)
		}
		options, e = a.seedRescueOptionsTx(ctx, tx, sr.PluginID)
		tx.Rollback()
		if e != nil || len(options["sites"].([]sr.Object)) != 18 {
			t.Fatal("new site option", e)
		}
		found := false
		for _, option := range options["sites"].([]sr.Object) {
			if option["value"] == int64(21) {
				found = true
			}
		}
		if !found {
			t.Fatal("new site deterministic alias absent")
		}
		if _, e = p.seedRescueSiteIdentityTx(ctx, s.DB, binding, 3); !errors.Is(e, sql.ErrNoRows) {
			t.Fatal("reserved absent old ID rebound", e)
		}
		var old reviewedIYUUBoundSite
		for _, v := range binding.Sites {
			if v.OriginalID == 1 {
				old = v
			}
		}
		id, e := s.nativeSiteByAppID(ctx, old.SiteID)
		if e != nil {
			t.Fatal(e)
		}
		id.State = "deleted"
		id.DeletedAt = now()
		tx, e = s.DB.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		if e = s.writeNativeSiteIdentityTx(ctx, tx, id); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
		seedMigrationIdentity(t, s, 200, "owned-readded", old.Origin, "active")
		got, e := p.seedRescueSiteIdentityTx(ctx, s.DB, binding, 1)
		if e != nil || got.State != "deleted" || got.SiteID != old.SiteID {
			t.Fatal("same URL re-add revived original alias", e)
		}
		replay, e := p.ImportReviewedSeedRescue(ctx, m.ID, raw)
		if e != nil || replay != receipt {
			t.Fatal("replay replaced edited target", e)
		}
		dir := filepath.Dir(s.lock.Name())
		if e = s.Close(); e != nil {
			t.Fatal(e)
		}
		s, e = OpenStore(dir)
		if e != nil {
			t.Fatal(e)
		}
		p, e = NewPluginStore(ctx, s)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = p.seedRescueNamespaceTx(ctx, s.DB, sr.PluginID); e != nil {
			t.Fatal("cold namespace", e)
		}
		if _, _, e = p.seedRescueRead(ctx, s.DB, sr.PluginID, 1); e != nil {
			t.Fatal("cold history owner", e)
		}
		if e = p.Uninstall(ctx, sr.PluginID); e != nil {
			t.Fatal(e)
		}
		if e = p.Install(ctx, installation); e != nil {
			t.Fatal("retained reinstall", e)
		}
		rebound, e := p.seedRescueNamespaceTx(ctx, s.DB, sr.PluginID)
		if e != nil || rebound == nil {
			t.Fatal("reinstall relabeled old IDs as native", e)
		}
		fresh, e := p.Installation(ctx, sr.PluginID)
		if e != nil || fresh.Generation != 3 {
			t.Fatal("lifecycle generation", e)
		}
		if _, _, e = p.seedRescueRead(ctx, s.DB, sr.PluginID, 3); e != nil {
			t.Fatal("retained cold owner", e)
		}
		if e = p.Reset(ctx, sr.PluginID); e != nil {
			t.Fatal("reset", e)
		}
		empty, e := p.seedRescueNamespaceTx(ctx, s.DB, sr.PluginID)
		if e != nil || empty != nil {
			t.Fatal("empty reset retained legacy namespace", e)
		}
		var count int
		if e = s.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id='SeedRescue'`).Scan(&count); e != nil || count != 0 {
			t.Fatal("migration/lifecycle executed business", e)
		}
	})
	for name, statement := range map[string]string{
		"binding":         `UPDATE records SET payload=x'00' WHERE kind='plugin_original_seed_rescue_references'`,
		"source-snapshot": `DELETE FROM native_site_source_snapshots`,
		"site-receipt":    `DELETE FROM native_site_migration_receipts WHERE original_source_id=1`,
		"final-receipt":   `DELETE FROM plugin_migration_receipts`,
		"final-digest":    `UPDATE plugin_migration_receipts SET digest='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'`,
	} {
		t.Run("missing-proof-"+name, func(t *testing.T) {
			s, p, _ := reviewedSeedOwned(t, source)
			m := reviewedSeedRun(t, p, raw)
			if _, e := p.ImportReviewedSeedRescue(ctx, m.ID, raw); e != nil {
				t.Fatal(e)
			}
			tx, e := s.DB.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			if _, e = tx.Exec(statement); e != nil {
				t.Fatal(e)
			}
			if _, e = p.seedRescueNamespaceTx(ctx, tx, sr.PluginID); e == nil {
				t.Fatal("missing independent proof accepted")
			}
		})
	}
	for name, trigger := range map[string]string{
		"config":   `BEFORE INSERT ON plugin_values WHEN NEW.kind='config' BEGIN SELECT RAISE(ABORT,'owned config failure'); END`,
		"data":     `BEFORE INSERT ON plugin_values WHEN NEW.kind='data' BEGIN SELECT RAISE(ABORT,'owned data failure'); END`,
		"owner":    `BEFORE UPDATE ON plugin_seed_rescue_state BEGIN SELECT RAISE(ABORT,'owned owner failure'); END`,
		"source":   `BEFORE INSERT ON native_site_source_snapshots BEGIN SELECT RAISE(ABORT,'owned source failure'); END`,
		"site-map": `BEFORE INSERT ON native_site_migration_receipts BEGIN SELECT RAISE(ABORT,'owned map failure'); END`,
		"binding":  `BEFORE INSERT ON records WHEN NEW.kind='plugin_original_seed_rescue_references' BEGIN SELECT RAISE(ABORT,'owned binding failure'); END`,
		"receipt":  `BEFORE INSERT ON plugin_migration_receipts BEGIN SELECT RAISE(ABORT,'owned receipt failure'); END`,
	} {
		t.Run("rollback-"+name, func(t *testing.T) {
			s, p, _ := reviewedSeedOwned(t, source)
			m := reviewedSeedRun(t, p, raw)
			if _, e := s.DB.Exec(`CREATE TRIGGER owned_seed_migration_failure ` + trigger); e != nil {
				t.Fatal(e)
			}
			if _, e := p.ImportReviewedSeedRescue(ctx, m.ID, raw); e == nil {
				t.Fatal("partial migration committed")
			}
			for _, table := range []string{"plugin_installations", "plugin_values", "plugin_migration_receipts", "native_site_source_snapshots", "native_site_migration_receipts", "native_site_reference_protocols"} {
				var count int
				if e := s.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); e != nil || count != 0 {
					t.Fatal("partial migration survived", table, e)
				}
			}
		})
	}
	t.Run("offline-cli", func(t *testing.T) { reviewedSeedSelectedCLI(t, source, raw) })
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "actual-sdk", true: "actual-sdk-lost-ack"}[lost], func(t *testing.T) { reviewedSeedSelectedSDK(t, source, raw, lost) })
	}
	t.Log("reviewed SeedRescue original source: raw config + 3 KV/1140 jobs preserved; 17 independently authenticated aliases, stable QB, cold/replay/retained lifecycle and atomic failures; CLI and same-generation native owned SDK flow checked separately above")
}

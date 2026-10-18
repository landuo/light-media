package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// This is an owned pre-upgrade database fixture, not a claim that any real
// user installation has been collected or migrated. The host-only observation
// is supplied explicitly; HTTP forms cannot assert its source implementation.
func legacySiteProtocolFixture(t *testing.T) (*App, *http.Cookie, legacySiteControlObservation, PluginRun) {
	t.Helper()
	a, cookie, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	old, _ := a.pluginStore.Config(ctx, "MergeSiteSwitch")
	watcher := []byte(`{"enable_sites":["s1"],"search_sites":["s1"],"search_follow_enable_sites":true}`)
	next := json.RawMessage(`{"enable_sites":["s2"],"search_sites":["s1"],"search_follow_enable_sites":false,"statistic_sites":["s1","s1","1"]}`)
	stored, run, e := a.pluginStore.SaveConfigAndQueue(ctx, "MergeSiteSwitch", old.Revision, next)
	if e != nil {
		t.Fatal(e)
	}
	in, _ := a.pluginStore.Installation(ctx, "MergeSiteSwitch")
	sealed, e := a.pluginStore.seal("site_controls_watch", in.InstanceID, "main", watcher)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`UPDATE plugin_site_control_watchers SET config=?,config_revision=? WHERE instance_id=?`, sealed, old.Revision, in.InstanceID); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`DELETE FROM native_site_reference_protocols WHERE instance_id=? AND generation=?`, in.InstanceID, in.Generation); e != nil {
		t.Fatal(e)
	}
	one, two := identityRequire(t, a, "s1"), identityRequire(t, a, "s2")
	o := legacySiteControlObservation{Fingerprint: strings.Repeat("a", 64), SourceCodeSHA: oldNativeMergeSiteSwitchSHA, Instance: in.InstanceID, Generation: in.Generation, Revision: stored.Revision, Selections: []legacySiteReferenceSelection{
		{Field: "config.enable_sites", Ordinal: 0, Target: builtin.SiteReference{Native: two.SourceID}},
		{Field: "config.search_sites", Ordinal: 0, Target: builtin.SiteReference{Native: one.SourceID}},
		{Field: "config.statistic_sites", Ordinal: 0, Target: builtin.SiteReference{Native: one.SourceID}},
		{Field: "config.statistic_sites", Ordinal: 1, Target: builtin.SiteReference{Custom: "s1"}},
		{Field: "config.statistic_sites", Ordinal: 2, Target: builtin.SiteReference{Custom: "1"}},
		{Field: "watcher.enable_sites", Ordinal: 0, Target: builtin.SiteReference{Native: one.SourceID}},
		{Field: "watcher.search_sites", Ordinal: 0, Target: builtin.SiteReference{Native: one.SourceID}},
	}}
	return a, cookie, o, run
}

func TestPluginSiteSwitchSourceProtocolPreservesCustomCollisionAbsentAndDisabled(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "disabled"}[disabled], func(t *testing.T) {
			a, cookie, _, _ := siteSwitchFixture(t)
			if disabled {
				installSiteControlPeerFixture(t, a, "CustomSites", map[string]any{"enabled": false, "sites": []map[string]string{{"id": "s1", "name": "fixture custom"}}})
			}
			refs := []builtin.SiteReference{{Native: identityRequire(t, a, "s1").SourceID}, {Custom: "s1"}, {Custom: "1"}}
			siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []int64{identityRequire(t, a, "s1").SourceID}, "statistic_sites": refs})
			run := siteSwitchExecute(t, a, true)
			stored, _ := a.pluginStore.Config(context.Background(), "MergeSiteSwitch")
			var got builtin.SiteControls
			if json.Unmarshal(stored.Value, &got) != nil || !reflect.DeepEqual(got.References("statistic_sites"), refs) {
				t.Fatal("custom string coerced", string(stored.Value))
			}
			var n int
			a.Store.DB.QueryRow(`SELECT COUNT(*) FROM native_site_reference_sources`).Scan(&n)
			if n != 0 {
				t.Fatal("ordinary source save manufactured legacy proof")
			}
			input, _ := a.pluginStore.Run(context.Background(), run.ID)
			var inv plugins.Invocation
			if json.Unmarshal(input.Input, &inv) != nil {
				t.Fatal("input")
			}
			var cfg builtin.SiteControls
			json.Unmarshal(inv.Config, &cfg)
			if !reflect.DeepEqual(cfg.References("statistic_sites"), refs) {
				t.Fatal("admission lost typed identity")
			}
		})
	}
}

func TestPluginSiteSwitchOriginalImportCannotUseCoincidentNativeIDOrClaimLegacy(t *testing.T) {
	a, _, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	m, e := a.pluginStore.BeginMigration(ctx, strings.Repeat("b", 64), "MergeSiteSwitch", "MergeSiteSwitch.original")
	if e != nil {
		t.Fatal(e)
	}
	manifest := builtin.MergeSiteSwitchManifest()
	raw, _ := json.Marshal(manifest)
	in := PluginImport{RunID: m.ID, Installation: PluginInstallation{InstanceID: m.InstanceID, PluginID: "MergeSiteSwitch", Version: manifest.Version, ManifestSchemaVersion: 1, Manifest: raw}, Config: json.RawMessage(`{"enable_sites":[1],"statistic_sites":[1,"1"]}`)}
	if _, e = a.pluginStore.Import(ctx, in); e != nil {
		t.Fatal(e)
	}
	before, _ := a.pluginStore.Config(ctx, m.InstanceID)
	if _, _, e = a.pluginStore.SaveConfigAndQueue(ctx, m.InstanceID, before.Revision, before.Value); pluginErrorCode(e) != "PLUGIN_SITE_PROTOCOL_UNCLAIMED" {
		t.Fatal("coincident original numeric ID activated", e)
	}
	after, _ := a.pluginStore.Config(ctx, m.InstanceID)
	install, _ := a.pluginStore.Installation(ctx, m.InstanceID)
	if !bytes.Equal(before.Value, after.Value) || before.Revision != after.Revision || install.DesiredEnabled {
		t.Fatal("unclaimed import changed")
	}
	o := legacySiteControlObservation{Fingerprint: strings.Repeat("b", 64), SourceCodeSHA: oldNativeMergeSiteSwitchSHA, Instance: m.InstanceID, Generation: install.Generation, Revision: before.Revision}
	if _, e = a.registerLegacySiteControlSource(ctx, o); pluginErrorCode(e) != "PLUGIN_SITE_SOURCE_INVALID" {
		t.Fatal("original numeric namespace relabeled as old native opaque", e)
	}
	var count int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE instance_id=?`, m.InstanceID).Scan(&count)
	if count != 0 {
		t.Fatal("import queued execution")
	}
}

func TestPluginSiteSwitchUnclaimedColdStartPreservesPendingAndInitializedWatcher(t *testing.T) {
	a, _, _, run := legacySiteProtocolFixture(t)
	ctx := context.Background()
	var before []byte
	a.Store.DB.QueryRow(`SELECT config FROM plugin_site_control_watchers WHERE instance_id='MergeSiteSwitch'`).Scan(&before)
	dir := a.Dir
	if e := a.Close(); e != nil {
		t.Fatal(e)
	}
	b, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	if e = b.initializeNativePlugins(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = b.pluginStore.claimRunForInstance(ctx, "MergeSiteSwitch"); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("unclaimed admission consumed", e)
	}
	old, e := b.pluginStore.Run(ctx, run.ID)
	if e != nil || old.State != "pending" || !bytes.Equal(old.Input, run.Input) {
		t.Fatal("old pending identity changed", old.State, e)
	}
	var after []byte
	b.Store.DB.QueryRow(`SELECT config FROM plugin_site_control_watchers WHERE instance_id='MergeSiteSwitch'`).Scan(&after)
	if !bytes.Equal(before, after) {
		t.Fatal("cold startup replaced last initialized flags")
	}
	// A blocked source plugin cannot starve an unrelated proven plugin.
	installStoredPlugin(t, b.pluginStore, "unrelated-source-protocol-fixture")
	if _, _, e = b.pluginStore.SaveConfigAndQueue(ctx, "unrelated-source-protocol-fixture", 0, map[string]bool{}); e != nil {
		t.Fatal(e)
	}
	next, e := b.pluginStore.ClaimRun(ctx)
	if e != nil || next.InstanceID != "unrelated-source-protocol-fixture" {
		t.Fatal("unclaimed first job blocked other plugins", next.InstanceID, e)
	}
}

func TestPluginSiteSwitchLegacyTransitionExplicitSelectionsAndStaleSource(t *testing.T) {
	for _, kind := range []string{"missing", "duplicate", "wrong-native", "custom-relabel", "source-code", "fingerprint", "stale"} {
		t.Run(kind, func(t *testing.T) {
			a, _, o, _ := legacySiteProtocolFixture(t)
			ctx := context.Background()
			switch kind {
			case "missing":
				o.Selections = o.Selections[1:]
			case "duplicate":
				o.Selections = append(o.Selections, o.Selections[0])
			case "wrong-native":
				o.Selections[0].Target = builtin.SiteReference{Native: identityRequire(t, a, "s1").SourceID}
			case "custom-relabel":
				o.Selections[3].Target = builtin.SiteReference{Custom: "foreign"}
			case "source-code":
				o.SourceCodeSHA = strings.Repeat("c", 64)
			case "fingerprint":
				o.Fingerprint = "guessed"
			}
			id, e := a.registerLegacySiteControlSource(ctx, o)
			if kind != "stale" {
				if e == nil {
					t.Fatal("unproven/ambiguous legacy transition admitted", kind)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			old, _ := a.pluginStore.Config(ctx, o.Instance)
			if _, e = a.pluginStore.SaveConfig(ctx, o.Instance, old.Revision, json.RawMessage(`{"enable_sites":["s1"]}`)); e != nil {
				t.Fatal(e)
			}
			if _, e = a.transitionLegacySiteControlSource(ctx, id); e == nil {
				t.Fatal("stale source applied")
			}
			var n int
			a.Store.DB.QueryRow(`SELECT COUNT(*) FROM native_site_reference_transitions`).Scan(&n)
			if n != 0 {
				t.Fatal("stale source left transition receipt")
			}
		})
	}
}

func TestPluginSiteSwitchLegacyTransitionRollbackCoversWatcherRunsProofAndQueue(t *testing.T) {
	for _, trigger := range []string{
		`CREATE TRIGGER owned_stop BEFORE INSERT ON native_site_reference_transitions BEGIN SELECT RAISE(ABORT,'owned receipt failure'); END`,
		`CREATE TRIGGER owned_stop BEFORE UPDATE ON plugin_site_control_watchers BEGIN SELECT RAISE(IGNORE); END`,
		`CREATE TRIGGER owned_stop BEFORE UPDATE OF state ON plugin_runs WHEN NEW.state='canceled' BEGIN SELECT RAISE(IGNORE); END`,
		`CREATE TRIGGER owned_stop BEFORE INSERT ON plugin_runs BEGIN SELECT RAISE(ABORT,'owned queue failure'); END`,
		`CREATE TRIGGER owned_stop BEFORE UPDATE ON native_site_reference_transitions BEGIN SELECT RAISE(IGNORE); END`,
	} {
		t.Run(strings.Split(trigger, " ON ")[1], func(t *testing.T) {
			a, _, o, run := legacySiteProtocolFixture(t)
			ctx := context.Background()
			id, e := a.registerLegacySiteControlSource(ctx, o)
			if e != nil {
				t.Fatal(e)
			}
			before, _ := a.pluginStore.Config(ctx, o.Instance)
			var watch []byte
			a.Store.DB.QueryRow(`SELECT config FROM plugin_site_control_watchers WHERE instance_id=?`, o.Instance).Scan(&watch)
			if _, e = a.Store.DB.Exec(trigger); e != nil {
				t.Fatal(e)
			}
			if _, e = a.transitionLegacySiteControlSource(ctx, id); e == nil {
				t.Fatal("injected noncommit accepted")
			}
			after, _ := a.pluginStore.Config(ctx, o.Instance)
			old, _ := a.pluginStore.Run(ctx, run.ID)
			var got []byte
			a.Store.DB.QueryRow(`SELECT config FROM plugin_site_control_watchers WHERE instance_id=?`, o.Instance).Scan(&got)
			if !bytes.Equal(before.Value, after.Value) || before.Revision != after.Revision || !bytes.Equal(watch, got) || old.State != "pending" || !bytes.Equal(old.Input, run.Input) {
				t.Fatal("partial transition escaped transaction")
			}
			var n int
			a.Store.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM native_site_reference_protocols WHERE instance_id=?)+(SELECT COUNT(*) FROM native_site_reference_transitions)+(SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND state='pending')`, o.Instance, o.Instance).Scan(&n)
			if n != 1 {
				t.Fatal("protocol/receipt/new queue escaped rollback", n)
			}
		})
	}
}

func TestPluginSiteSwitchLegacyTransitionActualSDKColdReopenAndIdempotentReceipt(t *testing.T) {
	a, cookie, o, run := legacySiteProtocolFixture(t)
	ctx := context.Background()
	id, e := a.registerLegacySiteControlSource(ctx, o)
	if e != nil {
		t.Fatal(e)
	}
	before := a.Settings()
	receipt, e := a.transitionLegacySiteControlSource(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	if receipt.AfterRevision != o.Revision+1 || len(receipt.Transfers) != 1 || receipt.Transfers[0].OldID != run.ID || receipt.Transfers[0].NewID == "" {
		t.Fatal("transfer chain missing")
	}
	if !reflect.DeepEqual(before, a.Settings()) {
		t.Fatal("format conversion applied queued settings early")
	}
	old, _ := a.pluginStore.Run(ctx, run.ID)
	next, _ := a.pluginStore.Run(ctx, receipt.Transfers[0].NewID)
	if old.State != "canceled" || !bytes.Equal(old.Input, run.Input) || next.ConfigRevision != receipt.AfterRevision || tokenHash(string(next.Input)) != receipt.Transfers[0].NewInputSHA {
		t.Fatal("immutable admission lost")
	}
	var inv plugins.Invocation
	json.Unmarshal(next.Input, &inv)
	if string(inv.Input) != `{"config_saved":true}` {
		t.Fatal("saved config intent lost", string(inv.Input))
	}
	var fields builtin.SiteControls
	json.Unmarshal(inv.Config, &fields)
	if !reflect.DeepEqual(fields.References("statistic_sites"), []builtin.SiteReference{{Native: identityRequire(t, a, "s1").SourceID}, {Custom: "s1"}, {Custom: "1"}}) {
		t.Fatal("ambiguous custom strings changed")
	}
	// Converted watcher still uses the previously initialized follow flag.
	site := a.Settings().Sites[1]
	site.Enabled = true
	if _, e = a.saveManagedSite(ctx, site); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(a.Settings().IndexerSites, []string{"s2"}) {
		t.Fatal("queued false flag activated before its new run", a.Settings().IndexerSites)
	}
	_ = cookie
	siteSwitchExecute(t, a, true)
	if a.Settings().Sites[0].Enabled || !a.Settings().Sites[1].Enabled || !reflect.DeepEqual(a.Settings().IndexerSites, []string{"s1"}) {
		t.Fatal("new typed SDK admission did not apply original saved intent")
	}
	site = a.Settings().Sites[1]
	site.Enabled = false
	if _, e = a.saveManagedSite(ctx, site); e != nil {
		t.Fatal(e)
	}
	dir := a.Dir
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	b, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	again, e := b.transitionLegacySiteControlSource(ctx, id)
	if e != nil || !reflect.DeepEqual(receipt, again) || b.Settings().Sites[1].Enabled {
		t.Fatal("restart/retry replayed transition or saved operation", e)
	}
	committed, e := b.pluginStore.Run(ctx, next.ID)
	if e != nil || committed.State != "succeeded" || !bytes.Equal(committed.Input, next.Input) || committed.ConfigRevision != next.ConfigRevision {
		t.Fatal("committed admission identity drifted", committed.State, e)
	}
	// A protocol marker alone cannot replace the private transition receipt.
	if _, e = b.Store.DB.Exec(`DELETE FROM native_site_reference_transitions WHERE source_id=?`, id); e != nil {
		t.Fatal(e)
	}
	in, _ := b.pluginStore.Installation(ctx, o.Instance)
	if _, e = b.pluginStore.readSiteReferenceProtocol(ctx, b.Store.DB, in); pluginErrorCode(e) != "PLUGIN_SITE_PROTOCOL_INVALID" {
		t.Fatal("orphan protocol proof accepted", e)
	}
}

func TestPluginSiteSwitchProtocolInstallResetReinstallAndTamperedScope(t *testing.T) {
	ctx := context.Background()
	s, p := pluginStoreFixture(t)
	manifest := builtin.MergeSiteSwitchManifest()
	raw, _ := json.Marshal(manifest)
	makeIn := func(id string) PluginInstallation {
		return PluginInstallation{InstanceID: id, PluginID: manifest.PluginID, Version: manifest.Version, ManifestSchemaVersion: 1, Manifest: raw}
	}
	for _, id := range []string{"MergeSiteSwitch", "MergeSiteSwitch.clone"} {
		if e := p.Install(ctx, makeIn(id)); e != nil {
			t.Fatal(e)
		}
		if _, e := p.SaveConfig(ctx, id, 0, map[string]any{"statistic_sites": []any{int64(1), "1"}}); e != nil {
			t.Fatal(e)
		}
		in, _ := p.Installation(ctx, id)
		if _, e := p.readSiteReferenceProtocol(ctx, s.DB, in); e != nil {
			t.Fatal(e)
		}
	}
	if e := p.Uninstall(ctx, "MergeSiteSwitch"); e != nil {
		t.Fatal(e)
	}
	if e := p.Install(ctx, makeIn("MergeSiteSwitch")); e != nil {
		t.Fatal("retained authenticated protocol reinstall", e)
	}
	in, _ := p.Installation(ctx, "MergeSiteSwitch")
	if _, e := p.readSiteReferenceProtocol(ctx, s.DB, in); e != nil {
		t.Fatal(e)
	}
	var other []byte
	s.DB.QueryRow(`SELECT sealed FROM native_site_reference_protocols WHERE instance_id='MergeSiteSwitch.clone'`).Scan(&other)
	if _, e := s.DB.Exec(`UPDATE native_site_reference_protocols SET sealed=? WHERE instance_id=? AND generation=?`, other, in.InstanceID, in.Generation); e != nil {
		t.Fatal(e)
	}
	if _, e := p.readSiteReferenceProtocol(ctx, s.DB, in); pluginErrorCode(e) != "PLUGIN_SITE_PROTOCOL_INVALID" {
		t.Fatal("protocol scope substitution", e)
	}
	if e := p.Reset(ctx, in.InstanceID); e != nil {
		t.Fatal(e)
	}
	reset, _ := p.Installation(ctx, in.InstanceID)
	if _, e := p.readSiteReferenceProtocol(ctx, s.DB, reset); e != nil {
		t.Fatal("empty reset protocol not reestablished", e)
	}
	if _, e := p.Config(ctx, in.InstanceID); !errors.Is(e, sql.ErrNoRows) {
		t.Fatal("reset retained ambiguous source config")
	}
}

func TestPluginSiteSwitchSourceCoreStringCannotBeGuessedOrPartiallyApplied(t *testing.T) {
	a, cookie, _, _ := siteSwitchFixture(t)
	before := a.Settings()
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []builtin.SiteReference{{Custom: "s2"}}, "search_sites": []int64{identityRequire(t, a, "s1").SourceID}})
	failed := siteSwitchExecute(t, a, false)
	if failed.ErrorCode != "PLUGIN_SITE_REFERENCE_UNMAPPED" || !reflect.DeepEqual(before, a.Settings()) {
		t.Fatal("source custom string was guessed or partly applied", failed.ErrorCode)
	}
	var receipts int
	a.Store.DB.QueryRow(`SELECT COUNT(*) FROM native_site_reference_sources`).Scan(&receipts)
	if receipts != 0 {
		t.Fatal("ordinary invalid input claimed legacy provenance")
	}
}

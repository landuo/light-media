package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

func assertSiteControlAdmission(t *testing.T, a *App, original PluginRun, state string) PluginRun {
	t.Helper()
	got, err := a.pluginStore.Run(context.Background(), original.ID)
	if err != nil || got.State != state || got.ConfigRevision != original.ConfigRevision || got.Generation != original.Generation || got.Version != original.Version || got.Action != original.Action || !bytes.Equal(got.Input, original.Input) {
		t.Fatal("immutable site admission drifted", got.ID, got.State, err)
	}
	return got
}
func normalizationProof(t *testing.T, a *App, run PluginRun) siteControlNormalizationReceipt {
	t.Helper()
	var sealed []byte
	if err := a.Store.DB.QueryRow(`SELECT result FROM plugin_site_control_operations WHERE run_id=? AND instance_id=? AND generation=? AND config_revision=?`, run.ID, run.InstanceID, run.Generation, run.ConfigRevision).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	raw, err := a.pluginStore.unseal("site_controls_result", run.InstanceID, run.ID, sealed)
	var proof siteControlNormalizationReceipt
	if err != nil || cleanupJournalStrict(raw, &proof) != nil || proof.Version != 2 {
		t.Fatal("missing versioned normalization proof", err)
	}
	return proof
}
func proveSiteControlColdRecovery(t *testing.T, a *App, committed PluginRun, canceled []PluginRun) {
	t.Helper()
	ctx := context.Background()
	site := a.Settings().Sites[1]
	site.Enabled = false
	if _, err := a.saveManagedSite(ctx, site); err != nil {
		t.Fatal(err)
	}
	// A process exit after the native transaction, before publishing Runner's
	// terminal result, must recover by the original admitted revision.
	if _, err := a.Store.DB.Exec(`UPDATE plugin_runs SET state='running',result=NULL,error_code='PLUGIN_RESULT_SAVE_FAILED' WHERE id=?`, committed.ID); err != nil {
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
	useBuiltInPluginProcess(t, b)
	assertSiteControlAdmission(t, b, committed, "succeeded")
	for _, old := range canceled {
		assertSiteControlAdmission(t, b, old, "canceled")
	}
	if b.Settings().Sites[1].Enabled {
		t.Fatal("cold recovery replayed committed config_saved")
	}
	if err := b.initializeNativePlugins(ctx); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 3; n++ {
		run, err := b.pluginStore.claimRunForInstance(ctx, "MergeSiteSwitch")
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if run.ID == committed.ID {
			t.Fatal("old committed action was re-admitted")
		}
		for _, old := range canceled {
			if old.ID == run.ID {
				t.Fatal("canceled continuation replayed")
			}
		}
		b.executePluginRun(ctx, run)
		got, _ := b.pluginStore.Run(ctx, run.ID)
		if got.State != "succeeded" {
			t.Fatal("new startup init failed", got.ErrorCode)
		}
		var result struct{ Configured, Applied bool }
		if json.Unmarshal(got.Result, &result) != nil || !result.Configured || result.Applied {
			t.Fatal("startup replayed saved-config effects")
		}
	}
	if b.Settings().Sites[1].Enabled {
		t.Fatal("startup re-applied stale enable list")
	}
	var count int
	if err := b.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_site_control_operations WHERE instance_id='MergeSiteSwitch'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("saved intent applied more than once", count, err)
	}
	if _, err := b.pluginStore.siteControlReceipt(ctx, committed); err != nil {
		t.Fatal("original admission cannot read result after new revisions", err)
	}
	for _, old := range canceled {
		assertSiteControlAdmission(t, b, old, "canceled")
	}
}

func TestPluginSiteSwitchNormalizationKeepsTwoAdmissionsAndColdRecovery(t *testing.T) {
	for _, action := range []string{"init", "reload"} {
		t.Run(action, func(t *testing.T) {
			a, cookie, _, _ := siteSwitchFixture(t)
			ctx := context.Background()
			two := identityRequire(t, a, "s2").SourceID
			one := identityRequire(t, a, "s1").SourceID
			siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []int64{two}, "search_sites": []int64{one}, "rss_sites": []int64{one}, "search_follow_enable_sites": true, "rss_follow_enable_sites": true})
			first, err := a.pluginStore.claimRunForInstance(ctx, "MergeSiteSwitch")
			if err != nil {
				t.Fatal(err)
			}
			var activation json.RawMessage
			if action == "init" {
				activation = json.RawMessage(`{"config_saved":true}`)
			}
			second, err := a.pluginStore.EnqueueRun(ctx, "MergeSiteSwitch", action, activation)
			if err != nil {
				t.Fatal(err)
			}
			a.executePluginRun(ctx, first)
			assertSiteControlAdmission(t, a, first, "succeeded")
			got := assertSiteControlAdmission(t, a, second, "canceled")
			if got.ErrorCode != "PLUGIN_SITE_NORMALIZATION_SUPERSEDED" {
				t.Fatal("cancellation has no explicit reason")
			}
			config, err := a.pluginStore.Config(ctx, "MergeSiteSwitch")
			if err != nil {
				t.Fatal(err)
			}
			var normalized builtin.SiteControls
			if json.Unmarshal(config.Value, &normalized) != nil || config.Revision != first.ConfigRevision+1 || !reflect.DeepEqual(normalized.References("search_sites"), []builtin.SiteReference{{Native: two}}) || !reflect.DeepEqual(normalized.References("rss_sites"), []builtin.SiteReference{{Native: two}}) {
				t.Fatal("normalized result revision missing")
			}
			var watcherRevision int64
			if err = a.Store.DB.QueryRow(`SELECT config_revision FROM plugin_site_control_watchers WHERE instance_id=?`, first.InstanceID).Scan(&watcherRevision); err != nil || watcherRevision != config.Revision {
				t.Fatal("watcher not bound to committed result", err)
			}
			proof := normalizationProof(t, a, first)
			if proof.AdmissionRevision != first.ConfigRevision || proof.ResultRevision != config.Revision || proof.InputSHA != tokenHash(string(first.Input)) || len(proof.Canceled) != 1 || proof.Canceled[0].ID != second.ID || proof.Canceled[0].InputSHA != tokenHash(string(second.Input)) || proof.Canceled[0].Revision != second.ConfigRevision {
				t.Fatal("canceled identity not authenticated")
			}
			proveSiteControlColdRecovery(t, a, first, []PluginRun{second})
		})
	}
}

func TestPluginSiteSwitchLegacyTransferredAdmissionsDoNotDriftOnNormalization(t *testing.T) {
	a, _, o, old := legacySiteProtocolFixture(t)
	ctx := context.Background()
	// Seed only this owned pre-upgrade fixture with a follow=true save whose
	// current source selection differs, so normalization changes the JSON.
	before, err := a.pluginStore.Config(ctx, o.Instance)
	if err != nil {
		t.Fatal(err)
	}
	var cfg builtin.SiteControls
	if json.Unmarshal(before.Value, &cfg) != nil {
		t.Fatal("fixture config")
	}
	cfg["search_follow_enable_sites"] = json.RawMessage(`true`)
	raw, _ := json.Marshal(cfg)
	sealed, err := a.pluginStore.seal("config", o.Instance, "main", raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_values SET sealed=? WHERE instance_id=? AND kind='config' AND item_key='main'`, sealed, o.Instance); err != nil {
		t.Fatal(err)
	}
	var inv plugins.Invocation
	if json.Unmarshal(old.Input, &inv) != nil {
		t.Fatal("fixture invocation")
	}
	inv.Config = raw
	old.Input, _ = json.Marshal(inv)
	sealed, err = a.pluginStore.seal("run", o.Instance, old.ID, old.Input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_runs SET input=? WHERE id=?`, sealed, old.ID); err != nil {
		t.Fatal(err)
	}
	duplicate := old
	duplicate.ID = id("pr_")
	duplicate.CreatedAt = now()
	duplicate.UpdatedAt = now()
	sealed, err = a.pluginStore.seal("run", o.Instance, duplicate.ID, duplicate.Input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`INSERT INTO plugin_runs VALUES(?,?,?,?,?,?,?,?,NULL,'',?,?)`, duplicate.ID, duplicate.InstanceID, duplicate.Version, duplicate.Generation, duplicate.ConfigRevision, duplicate.Action, duplicate.State, sealed, duplicate.CreatedAt, duplicate.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	source, err := a.registerLegacySiteControlSource(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	transfer, err := a.transitionLegacySiteControlSource(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(transfer.Transfers) != 2 {
		t.Fatal("both prior admissions need transfer evidence")
	}
	replacements := map[string]PluginRun{}
	for _, item := range transfer.Transfers {
		next, err := a.pluginStore.Run(ctx, item.NewID)
		if err != nil {
			t.Fatal(err)
		}
		if tokenHash(string(next.Input)) != item.NewInputSHA || next.ConfigRevision != transfer.AfterRevision {
			t.Fatal("replacement identity mismatch before execution")
		}
		replacements[next.ID] = next
	}
	first, err := a.pluginStore.claimRunForInstance(ctx, o.Instance)
	if err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(ctx, first)
	assertSiteControlAdmission(t, a, replacements[first.ID], "succeeded")
	canceled := []PluginRun{}
	for id, next := range replacements {
		if id != first.ID {
			assertSiteControlAdmission(t, a, next, "canceled")
			canceled = append(canceled, next)
		}
	}
	assertSiteControlAdmission(t, a, old, "canceled")
	assertSiteControlAdmission(t, a, duplicate, "canceled")
	proof := normalizationProof(t, a, first)
	if len(proof.Canceled) != 1 || proof.Canceled[0].InputSHA != tokenHash(string(canceled[0].Input)) {
		t.Fatal("transferred continuation was silently rewritten")
	}
	// The original transfer receipt remains valid after normalization cancels a
	// redundant replacement; no original or replacement input/revision changes.
	again, err := a.transitionLegacySiteControlSource(ctx, source)
	if err != nil || !reflect.DeepEqual(transfer, again) {
		t.Fatal("transfer receipt changed", err)
	}
	proveSiteControlColdRecovery(t, a, first, append(canceled, old, duplicate))
}

func TestPluginSiteSwitchNormalizationReceiptAuthenticatesOriginalInput(t *testing.T) {
	a, cookie, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []int64{identityRequire(t, a, "s2").SourceID}, "search_follow_enable_sites": true})
	run := siteSwitchExecute(t, a, true)
	proof := normalizationProof(t, a, run)
	proof.InputSHA = tokenHash("different owned input")
	raw, _ := json.Marshal(proof)
	sealed, err := a.pluginStore.seal("site_controls_result", run.InstanceID, run.ID, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`UPDATE plugin_site_control_operations SET result=? WHERE run_id=?`, sealed, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = a.pluginStore.siteControlReceipt(ctx, run); pluginErrorCode(err) != "PLUGIN_SITE_RECEIPT_INVALID" {
		t.Fatal("mismatched private source binding accepted", err)
	}
}

func TestPluginSiteSwitchNormalizationRollbackRetainsQueuedIdentityAndWatcher(t *testing.T) {
	a, cookie, _, _ := siteSwitchFixture(t)
	ctx := context.Background()
	var watcher []byte
	if err := a.Store.DB.QueryRow(`SELECT config FROM plugin_site_control_watchers WHERE instance_id='MergeSiteSwitch'`).Scan(&watcher); err != nil {
		t.Fatal(err)
	}
	siteSwitchSave(t, a, cookie, map[string]any{"enable_sites": []int64{identityRequire(t, a, "s2").SourceID}, "search_follow_enable_sites": true})
	original, err := a.pluginStore.Config(ctx, "MergeSiteSwitch")
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.pluginStore.claimRunForInstance(ctx, "MergeSiteSwitch")
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.pluginStore.EnqueueRun(ctx, "MergeSiteSwitch", "reload", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.DB.Exec(`CREATE TRIGGER fail_normalization_proof BEFORE INSERT ON plugin_site_control_operations BEGIN SELECT RAISE(ABORT,'owned normalization receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	a.executePluginRun(ctx, first)
	assertSiteControlAdmission(t, a, first, "failed")
	assertSiteControlAdmission(t, a, second, "pending")
	current, err := a.pluginStore.Config(ctx, "MergeSiteSwitch")
	if err != nil || current.Revision != original.Revision || !bytes.Equal(current.Value, original.Value) {
		t.Fatal("normalization escaped rollback", err)
	}
	var after []byte
	if err = a.Store.DB.QueryRow(`SELECT config FROM plugin_site_control_watchers WHERE instance_id='MergeSiteSwitch'`).Scan(&after); err != nil || !bytes.Equal(watcher, after) {
		t.Fatal("watcher escaped rollback", err)
	}
	if a.Settings().Sites[1].Enabled {
		t.Fatal("native settings escaped rollback")
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_site_control_operations WHERE run_id=?`, first.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("uncommitted cancellation/normalization receipt visible", err)
	}
}

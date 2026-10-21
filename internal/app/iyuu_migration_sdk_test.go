package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise the imported namespace through the existing real SDK/native HTTP
// add/recheck/start and cold lost-ACK fixture. Actual credentials are used only
// for the owned import identity check, then replaced before any SDK execution.
func reviewedIYUUSelectedSDK(t *testing.T, source reviewedIYUUSnapshot, raw []byte, kind string) {
	t.Helper()
	original, e := iyuuSourceMap([]byte(source.Config))
	if e != nil {
		t.Fatal("reviewed configuration parse failed")
	}
	if _, exists := source.Data[strings.Repeat("a", 40)]; exists {
		t.Fatal("owned synthetic hash collides with source history")
	}
	setup := &iyuuActualSDKSetup{SuccessCacheBase: len(original["success_caches"].([]any))}
	setup.Prepare = func(t *testing.T, a *App, config map[string]any) PluginRun {
		ctx := context.Background()
		owned := a.Settings()
		shadow := reviewedIYUUOwnedTargetSettings(t, a.Store, source)
		shadow.Sites = append(shadow.Sites, owned.Sites...)
		reviewedIYUUOwnedSettings(t, a.Store, shadow)
		fingerprint, e := ReviewedIYUUAutoSeedFingerprint(raw)
		if e != nil {
			t.Fatal(e)
		}
		migration, e := a.pluginStore.BeginMigration(ctx, fingerprint, "IYUUAutoSeed", "IYUUAutoSeed")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = a.pluginStore.ImportReviewedIYUUAutoSeed(ctx, migration.ID, raw); e != nil {
			t.Fatal("actual source import before SDK failed", e)
		}
		ownedStoppedPluginSourceHandoff(t, a.pluginStore, "IYUUAutoSeed")
		installation, e := a.pluginStore.Installation(ctx, "IYUUAutoSeed")
		if e != nil || installation.DesiredEnabled {
			t.Fatal("source import activated the SDK")
		}
		// Every configured transport below belongs to this test. The additional
		// native site with ID 1 deliberately predates the original numeric import.
		safe := owned
		safe.Sites = nil
		for i := range source.Sites {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Error("unexpected request to an owned unused imported site")
				w.WriteHeader(http.StatusNotFound)
			}))
			t.Cleanup(server.Close)
			site := shadow.Sites[i]
			site.Kind = "hhanclub"
			site.BaseURL = server.URL
			safe.Sites = append(safe.Sites, site)
		}
		safe.Sites = append(safe.Sites, owned.Sites...)
		// Publish the source inventory as the previous owned fixture state so the
		// normal settings transaction updates the same stable site identities.
		a.cfgMu.Lock()
		a.config = shadow
		a.cfgMu.Unlock()
		if e = a.saveSettings(ctx, safe); e != nil {
			t.Fatal("owned settings handoff failed", e)
		}
		stored, e := a.pluginStore.Config(ctx, "IYUUAutoSeed")
		if e != nil {
			t.Fatal(e)
		}
		_, queued, e := a.pluginStore.SaveConfigAndQueue(ctx, "IYUUAutoSeed", stored.Revision, config)
		if e != nil {
			t.Fatal("converted namespace could not enter normal SDK initialization", e)
		}
		run, e := a.pluginStore.claimRunForInstance(ctx, "IYUUAutoSeed")
		if e != nil || run.ID != queued.ID {
			t.Fatal("owned SDK did not claim the intended initialization", e)
		}
		return run
	}
	setup.Verify = func(t *testing.T, a *App, run PluginRun) {
		ctx := context.Background()
		for key, value := range source.Data {
			stored, e := a.pluginStore.Data(ctx, "IYUUAutoSeed", key)
			if e != nil || stored.Revision != 1 || !bytes.Equal(stored.Value, []byte(value)) {
				t.Fatal("native SDK overwrote original history")
			}
		}
		var count int
		if e := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_values WHERE instance_id='IYUUAutoSeed' AND kind='data'`).Scan(&count); e != nil || count != 330 {
			t.Fatal("original history plus one owned addition not retained")
		}
		config, e := a.pluginStore.Config(ctx, "IYUUAutoSeed")
		if e != nil {
			t.Fatal(e)
		}
		fields, e := iyuuSourceMap(config.Value)
		if e != nil {
			t.Fatal(e)
		}
		for _, key := range []string{"error_caches", "permanent_error_caches"} {
			if !bytes.Equal(reviewedIYUUJSON(fields[key]), reviewedIYUUJSON(original[key])) {
				t.Fatal("native SDK cleared original cache class")
			}
		}
		members := map[string]bool{}
		for _, v := range fields["success_caches"].([]any) {
			if key, ok := v.(string); ok {
				members[key] = true
			}
		}
		for _, v := range original["success_caches"].([]any) {
			if !members[v.(string)] {
				t.Fatal("native SDK dropped original success cache")
			}
		}
		installation, e := a.pluginStore.Installation(ctx, "IYUUAutoSeed")
		if e != nil || installation.Generation != 1 || run.Generation != 1 {
			t.Fatal("fixture reinstalled instead of exercising the imported owner")
		}
		reviewedIYUUOwnedProtocol(t, a.pluginStore, installation, true)
		tx, e := a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		protocol, e := a.pluginStore.readSiteReferenceProtocol(ctx, tx, installation)
		if e != nil || !strings.HasPrefix(protocol.Source, reviewedIYUUProtocolPrefix) {
			t.Fatal("SDK bypassed the actual imported namespace")
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
		t.Log("actual imported owner/native SDK verified: original329 history unchanged, one synthetic addition, original cache classes retained, cold recovery without repeated native effects; all transports owned")
	}
	testIYUUActualAppSDKWithSetup(t, kind, false, setup)
}

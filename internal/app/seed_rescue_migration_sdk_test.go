package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	sr "light-media/internal/plugins/seedrescue"
	"light-media/internal/sites"
)

func reviewedSeedSelectedCLI(t *testing.T, source reviewedSeedSnapshot, raw []byte) {
	t.Helper()
	binary := os.Getenv("LIGHT_MEDIA_REVIEWED_SEED_CLI")
	if !filepath.IsAbs(binary) {
		t.Fatal("owned seed migration CLI required")
	}
	s, _, _ := reviewedSeedOwned(t, source)
	dir := filepath.Dir(s.lock.Name())
	invoke := func(input []byte, want bool, args ...string) map[string]any {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Stdin = bytes.NewReader(input)
		output, e := cmd.CombinedOutput()
		if (e == nil) != want {
			t.Fatal("owned SeedRescue CLI status mismatch", e)
		}
		if !want {
			return nil
		}
		var result map[string]any
		if json.Unmarshal(output, &result) != nil {
			t.Fatal("CLI response not bounded JSON")
		}
		return result
	}
	invoke(raw, false, "--target-data-dir", dir)
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	before := reviewedIYUUFileSnapshot(t, dir)
	only := invoke(raw, true, "--validate")
	if only["state"] != "validated" || only["writes"] != float64(0) {
		t.Fatal("source validation changed a target")
	}
	dry := invoke(raw, true, "--target-data-dir", dir)
	if dry["state"] != "ready" || dry["writes"] != float64(0) {
		t.Fatal("readonly target validation failed")
	}
	if !bytes.Equal(reviewedIYUUJSON(before), reviewedIYUUJSON(reviewedIYUUFileSnapshot(t, dir))) {
		t.Fatal("readonly CLI changed bytes or permissions")
	}
	applied := invoke(raw, true, "--target-data-dir", dir, "--apply")
	if applied["state"] != "imported" || applied["desired_enabled"] != false || applied["site_reference_mappings"] != float64(17) || applied["downloader_reference_mappings"] != float64(1) || applied["target_kv_rows"] != float64(3) || applied["source_history_tasks"] != float64(1140) {
		t.Fatal("CLI did not preserve source scope")
	}
	replay := invoke(raw, true, "--target-data-dir", dir, "--apply")
	if replay["replayed"] != true || !bytes.Equal(reviewedIYUUJSON(replay["receipt"]), reviewedIYUUJSON(applied["receipt"])) {
		t.Fatal("CLI replay replaced receipt")
	}
	before = reviewedIYUUFileSnapshot(t, dir)
	var invalid map[string]json.RawMessage
	if json.Unmarshal(raw, &invalid) != nil {
		t.Fatal("invalid owned source")
	}
	invalid["config"] = json.RawMessage(`"{}"`)
	invoke(reviewedIYUUJSON(invalid), false, "--target-data-dir", dir, "--apply")
	if !bytes.Equal(reviewedIYUUJSON(before), reviewedIYUUJSON(reviewedIYUUFileSnapshot(t, dir))) {
		t.Fatal("rejected source changed target")
	}
	cold, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	p, e := NewPluginStore(context.Background(), cold)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.seedRescueNamespaceTx(context.Background(), cold.DB, sr.PluginID); e != nil {
		t.Fatal("CLI cold namespace missing", e)
	}
	if _, _, e = p.seedRescueRead(context.Background(), cold.DB, sr.PluginID, 1); e != nil {
		t.Fatal("CLI cold history proof missing", e)
	}
}

// The shared actual HTTP/SDK fixture initializes this very imported generation.
// Actual source credentials are used only for initial identity comparison, then
// all running core connections and the effective plugin config are owned data.
func reviewedSeedSelectedSDK(t *testing.T, source reviewedSeedSnapshot, raw []byte, lostACK bool) {
	t.Helper()
	ctx := context.Background()
	var instance *App
	rescueNativeHTTPFlowConfigured(t, lostACK, func(a *App, cfg sr.Object, target, peer nativeSiteIdentity) (PluginInstallation, PluginRun) {
		instance = a
		base := a.Settings()
		temporary := base
		temporary.Sites = []SiteConfig{}
		tx, e := a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		for i, site := range source.Sites {
			kind, origin, _, e := siteImportOrigin(site.URL)
			if e != nil {
				t.Fatal(e)
			}
			id := "owned-original-" + itoaSourceSite(site.OriginalID)
			if site.OriginalID == 11 {
				id = target.SiteID
			} else if site.OriginalID == 1 {
				id = peer.SiteID
			}
			temporary.Sites = append(temporary.Sites, SiteConfig{Config: sites.Config{ID: id, Kind: kind, Name: "Owned source profile", BaseURL: origin}, Enabled: true})
			if id == target.SiteID || id == peer.SiteID {
				current, e := a.Store.readNativeSiteIdentity(ctx, tx, "site_id", id)
				if e != nil {
					t.Fatal(e)
				}
				current.CanonicalOrigin = origin
				if e = a.Store.writeNativeSiteIdentityTx(ctx, tx, current); e != nil {
					t.Fatal(e)
				}
			} else {
				current := nativeSiteIdentity{SourceID: int64(100 + i), SiteID: id, CanonicalOrigin: origin, State: "active", CreatedAt: now()}
				if _, e = tx.ExecContext(ctx, `INSERT INTO native_site_identities VALUES(?,?,?,?,?,?,?)`, current.SourceID, id, origin, "active", current.CreatedAt, "", a.Store.siteIdentityMAC("mapping", current)); e != nil {
					t.Fatal(e)
				}
			}
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
		temporary.Downloaders = append([]DownloaderConfig{}, base.Downloaders...)
		original := source.Downloaders[0]
		endpoint, e := reviewedIYUUDownloaderEndpoint("qbittorrent", original.Host, true)
		if e != nil {
			t.Fatal(e)
		}
		temporary.Downloaders[0].Name = original.Name
		temporary.Downloaders[0].BaseURL = endpoint
		temporary.Downloaders[0].Username = original.Username
		temporary.Downloaders[0].Password = original.Password
		reviewedIYUUOwnedSettings(t, a.Store, temporary)
		m := reviewedSeedRun(t, a.pluginStore, raw)
		if _, e = a.pluginStore.ImportReviewedSeedRescue(ctx, m.ID, raw); e != nil {
			t.Fatal("actual source SDK import", e)
		}
		ownedStoppedPluginSourceHandoff(t, a.pluginStore, sr.PluginID)
		// Restore every endpoint the running owner can use before any new admission.
		tx, e = a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}

		// Keep all proven identities represented in cold settings. Extra profile
		// sites are disabled loopback entries, never orphaned active identities.
		for i, site := range temporary.Sites {
			if site.ID == target.SiteID || site.ID == peer.SiteID {
				continue
			}
			site.Kind = "hhanclub"
			site.BaseURL = "http://127.0.0.1:" + itoaSourceSite(int64(21100+i))
			site.Enabled = false
			base.Sites = append(base.Sites, site)
		}
		for _, site := range base.Sites {
			identity, e := a.Store.readNativeSiteIdentity(ctx, tx, "site_id", site.ID)
			if e != nil {
				t.Fatal(e)
			}
			identity.CanonicalOrigin = site.BaseURL
			if e = a.Store.writeNativeSiteIdentityTx(ctx, tx, identity); e != nil {
				t.Fatal(e)
			}
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
		reviewedIYUUOwnedSettings(t, a.Store, base)
		a.cfgMu.Lock()
		a.config = base
		a.cfgMu.Unlock()
		for k, v := range source.Data {
			saved, e := a.pluginStore.Data(ctx, sr.PluginID, k)
			if e != nil || !bytes.Equal(saved.Value, []byte(v)) {
				t.Fatal("pre-execution imported data changed", e)
			}
		}

		// Retain the user's actual processing limits and selection rules. Only the
		// network/path/schedule fixture values are replaced before execution.
		overrides := sr.Object{"token": cfg["token"], "rss_urls": cfg["rss_urls"], "cron": cfg["cron"], "save_path": cfg["save_path"]}
		originalConfig, e := seedRescueConfig([]byte(source.Config))
		if e != nil {
			t.Fatal(e)
		}
		for key := range cfg {
			delete(cfg, key)
		}
		for key, value := range originalConfig {
			cfg[key] = value
		}
		for key, value := range overrides {
			cfg[key] = value
		}
		cfg["target_site_id"] = int64(11)
		cfg["downloader"] = original.Name
		saved, e := a.pluginStore.Config(ctx, sr.PluginID)
		if e != nil {
			t.Fatal(e)
		}
		if _, _, e = a.pluginStore.SaveConfigAndQueue(ctx, sr.PluginID, saved.Revision, cfg); e != nil {
			t.Fatal("reviewed activation", e)
		}
		run, e := a.pluginStore.claimRunForInstance(ctx, sr.PluginID)
		if e != nil {
			t.Fatal(e)
		}
		installation, e := a.pluginStore.Installation(ctx, sr.PluginID)
		if e != nil || installation.Generation != 1 {
			t.Fatal("imported generation changed", e)
		}
		return installation, run
	})
	if instance == nil {
		t.Fatal("actual imported fixture not used")
	}
	dir := instance.Dir
	if e := instance.Close(); e != nil && e != sql.ErrConnDone {
		t.Fatal(e)
	}
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p, e := NewPluginStore(ctx, s)
	if e != nil {
		t.Fatal(e)
	}
	in, e := p.Installation(ctx, sr.PluginID)
	if e != nil || in.Generation != 1 {
		t.Fatal("SDK replaced imported generation", e)
	}
	if _, e = p.seedRescueNamespaceTx(ctx, s.DB, sr.PluginID); e != nil {
		t.Fatal("SDK/cold namespace lost", e)
	}
	if _, _, e = p.seedRescueRead(ctx, s.DB, sr.PluginID, 1); e != nil {
		t.Fatal("SDK/cold owner proof", e)
	}
	data, e := p.Data(ctx, sr.PluginID, sr.JobsKey)
	if e != nil {
		t.Fatal(e)
	}
	var jobs, originalJobs map[string]json.RawMessage
	if json.Unmarshal(data.Value, &jobs) != nil || json.Unmarshal([]byte(source.Data[sr.JobsKey]), &originalJobs) != nil {
		t.Fatal("SDK history response malformed")
	}
	// Actual source _save_jobs (lines1702-1720) prunes archived history after a
	// changed RSS feed. Import itself retained all1140; execution keeps active
	// tasks plus1000 history, rather than inventing an unlimited retention rule.
	owned, active := 0, 0
	for hash := range jobs {
		if _, ok := originalJobs[hash]; !ok {
			owned++
		}
	}
	for hash, rawJob := range originalJobs {
		var job struct {
			State string `json:"state"`
		}
		if json.Unmarshal(rawJob, &job) != nil {
			t.Fatal("invalid original job")
		}
		switch job.State {
		case "SOURCE_SUBMITTING", "SOURCE_DOWNLOADING", "TARGET_DOWNLOADING", "WAITING_IYUU", "WAITING_IYUU_RETRY", "TARGET_COMPLETE", "TARGET_PAUSED", "TARGET_ERROR", "TARGET_RECHECKING", "MATCHING", "WAITING_SOURCE_RETRY", "WAITING_CAPACITY":
			active++
			if _, ok := jobs[hash]; !ok {
				t.Fatal("SDK removed an active original task")
			}
		}
	}
	if owned != 1 || len(jobs) < 1000+active+owned || len(jobs) > len(originalJobs)+1 {
		t.Fatal("source retention or owned task identity mismatch", len(jobs), active, owned)
	}
	t.Logf("owned SDK cold history: original active=%d retained, owned task=%d, current retained total=%d; source1000-history rule applied", active, owned, len(jobs))
}

package app

import (
	"context"
	"testing"

	sr "light-media/internal/plugins/seedrescue"
)

func TestSeedRescueIndexerIncludesConfiguredTarget(t *testing.T) {
	a := newTestApp(t)
	ctx := context.Background()
	settings := a.Settings()
	settings.Sites = []SiteConfig{parallelSearchSite("owned-target", "http://127.0.0.1:21301"), parallelSearchSite("owned-source", "http://localhost:21302")}
	if e := a.saveSettings(ctx, settings); e != nil {
		t.Fatal(e)
	}
	target := identityRequire(t, a, "owned-target")
	source := identityRequire(t, a, "owned-source")
	cfg := sr.Object{"enabled": false, "target_site_id": target.SourceID, "source_site_ids": []int64{source.SourceID}}
	run := uninstallInstall(t, a, "OwnedTargetIndexer", sr.NativeManifest(), cfg, true)
	stored, err := a.pluginStore.Config(ctx, run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = seedRescueConfig(stored.Value)
	if err != nil {
		t.Fatal(err)
	}
	owner := &pluginSeedRescueOwner{a: a, instance: run.InstanceID, generation: run.Generation}
	owner.active = &seedRescueCall{o: owner, run: run, revision: run.ConfigRevision, config: cfg}
	for domain, want := range map[string]string{"127.0.0.1": target.SiteID, "localhost": source.SiteID, "not-configured.invalid": ""} {
		site, e := owner.Indexer(ctx, domain)
		if e != nil {
			t.Fatal(e)
		}
		if want == "" {
			if site != nil {
				t.Fatal("unconfigured domain resolved")
			}
			continue
		}
		if site == nil || site["_native_ref"] != want {
			t.Fatal("configured target/source indexer missing", domain)
		}
	}
}

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"light-media/internal/download"
	cf "light-media/internal/plugins/cloudflarespeedtest"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	sr "light-media/internal/plugins/seedrescue"
	"light-media/internal/sites"
)

// No App.Start, scheduler pump, scan, CLI, public network or fake business host.
// These tests enter the actual original lifecycle and retained owner machines.
func originalMachineInitCFOwner(t *testing.T, p *originalPluginInstance) *pluginCloudflareOwner {
	t.Helper()
	key := p.installation.InstanceID + ":" + strconv.FormatInt(p.installation.Generation, 10)
	r := &p.app.pluginCloudflare
	r.mu.Lock()
	o := r.owners[key]
	r.mu.Unlock()
	if o == nil || o.machine == nil {
		t.Fatalf("actual initialized owner missing at generation key %q", key)
	}
	same, err := p.app.cloudflareOwner(context.Background(), p.installation)
	if err != nil || same != o {
		t.Fatalf("actual owner lookup mismatch: %v", err)
	}
	return o
}
func originalMachineInitSeedOwner(t *testing.T, p *originalPluginInstance) *pluginSeedRescueOwner {
	t.Helper()
	key := p.installation.InstanceID + ":" + strconv.FormatInt(p.installation.Generation, 10)
	r := &p.app.pluginSeedRescue
	r.mu.Lock()
	o := r.owners[key]
	r.mu.Unlock()
	if o == nil || o.machine == nil {
		t.Fatalf("actual initialized owner missing at generation key %q", key)
	}
	same, err := p.app.seedRescueOwner(context.Background(), p.installation)
	if err != nil || same != o {
		t.Fatalf("actual owner lookup mismatch: %v", err)
	}
	return o
}
func originalMachineInitSave(t *testing.T, a *App, id string, value any) {
	t.Helper()
	c, err := a.pluginStore.Config(context.Background(), id)
	revision := int64(0)
	if err == nil {
		revision = c.Revision
	} else if !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	// Installation does not create configuration. Submit the requested fixture
	// config through the ordinary revision-zero create path, not a synthetic row.
	if _, err = a.pluginStore.SaveConfig(context.Background(), id, revision, value); err != nil {
		t.Fatal(err)
	}
}
func originalMachineInitCount(t *testing.T, a *App, id, group string) int {
	t.Helper()
	var n int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_schedules WHERE instance_id=? AND group_key=? AND active=1`, id, group).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func originalMachineInitOnlyRuns(t *testing.T, a *App, id string, want int) {
	t.Helper()
	var n, other int
	if err := a.Store.DB.QueryRow(`SELECT count(*),COALESCE(sum(CASE WHEN action<>'init' THEN 1 ELSE 0 END),0) FROM plugin_runs WHERE instance_id=?`, id).Scan(&n, &other); err != nil {
		t.Fatal(err)
	}
	if n != want || other != 0 {
		t.Fatalf("host invocation count=%d other actions=%d want=%d", n, other, want)
	}
	// This counts admitted host invocations, not internal Machine.Init calls.
}
func originalMachineInitCloudflare(t *testing.T, once bool) (*App, *originalPluginInstance) {
	t.Helper()
	t.Setenv("TZ", "UTC")
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	a.pluginCloudflare.now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	installCloneSource(t, a.pluginStore, cf.NativeManifest())
	cfg := cloudflareTestConfig("")
	cfg["onlyonce"] = once
	originalMachineInitSave(t, a, cf.PluginID, cfg)
	p := a.startOriginalPlugin(context.Background(), cf.PluginID)
	if p == nil {
		t.Fatal("actual original Cloudflare start failed")
	}
	return a, p
}
func TestPluginOriginalMachineInitCloudflarePrivatePublicSeparation(t *testing.T) {
	a, p := originalMachineInitCloudflare(t, true)
	ctx := context.Background()
	o := originalMachineInitCFOwner(t, p)
	if o == nil || o.machine == nil {
		t.Fatal("missing actual machine")
	}
	before := o.machine
	if n := originalMachineInitCount(t, a, cf.PluginID, "cloudflare"); n != 1 {
		t.Fatalf("private once=%d", n)
	}
	if n := originalMachineInitCount(t, a, cf.PluginID, "original_services"); n != 0 {
		t.Fatalf("public cron registered during init=%d", n)
	}
	var legacyPublic int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM plugin_schedules WHERE instance_id=? AND service_id='CloudflareSpeedTest' AND active=1`, cf.PluginID).Scan(&legacyPublic); err != nil || legacyPublic != 0 {
		t.Fatalf("public getter ran during init: %d %v", legacyPublic, err)
	}
	a.updateOriginalPluginJobs(ctx, cf.PluginID)
	if n := originalMachineInitCount(t, a, cf.PluginID, "original_services"); n != 1 {
		t.Fatalf("public cron=%d", n)
	}
	if n := originalMachineInitCount(t, a, cf.PluginID, "cloudflare"); n != 1 {
		t.Fatalf("common registration removed private once: %d", n)
	}
	if err := p.init(ctx); err != nil {
		t.Fatalf("direct init after public registration: %v", err)
	}
	if o.machine != before {
		t.Fatal("direct init replaced live machine")
	}
	if n := originalMachineInitCount(t, a, cf.PluginID, "cloudflare"); n != 0 {
		t.Fatalf("consumed private once survived direct init: %d", n)
	}
	a.updateOriginalPluginJobs(ctx, cf.PluginID)
	if n := originalMachineInitCount(t, a, cf.PluginID, "original_services"); n != 1 {
		t.Fatalf("public cron after second registration=%d", n)
	}
	originalMachineInitOnlyRuns(t, a, cf.PluginID, 2)
}

// Start with a real durable configuration from an earlier owner, then use an
// empty init configuration. CF's source empty-config semantics distinguish
// class defaults from an illicit warm Init of that old durable configuration.
func TestPluginOriginalMachineInitCloudflareNewObjectDoesNotWarmOldState(t *testing.T) {
	a, prior := originalMachineInitCloudflare(t, false)
	ctx := context.Background()
	old := originalMachineInitCFOwner(t, prior).machine
	originalMachineInitSave(t, a, cf.PluginID, map[string]any{})
	p := a.startOriginalPlugin(ctx, cf.PluginID)
	if p == nil {
		t.Fatal("new original object failed")
	}
	fresh := originalMachineInitCFOwner(t, p).machine
	if fresh == old {
		t.Fatal("new original object reused former machine")
	}
	if enabled, err := p.getState(ctx); err != nil || enabled {
		t.Fatalf("empty initial config inherited old warm state: %v %v", enabled, err)
	}
	originalMachineInitOnlyRuns(t, a, cf.PluginID, 2)
}

func originalMachineInitSeed(t *testing.T) (*App, *originalPluginInstance, *atomic.Int32) {
	t.Helper()
	t.Setenv("TZ", "UTC")
	withoutProxyEnv(t)
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	calls := new(atomic.Int32)
	qb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/app/webapiVersion" {
			t.Errorf("unexpected downloader action: %s", r.URL.Path)
			http.Error(w, "unexpected action", 500)
			return
		}
		calls.Add(1)
		io.WriteString(w, "2.11.3")
	}))
	t.Cleanup(qb.Close)
	// Syntax-valid synthetic HTTPS identities, matching the existing native
	// identity tests. These are configuration values only: no App.Start, scan,
	// RSS fetch or site connection is invoked. The sole network probe remains
	// the owned loopback qB webapiVersion handler above.
	const targetURL = "https://alpha.example.com"
	const sourceURL = "https://beta.example.com"
	settings := a.Settings()
	settings.Sites = []SiteConfig{{Config: sites.Config{ID: "machine-target", Kind: "nexusphp", Name: "fixture target", BaseURL: targetURL}, Enabled: true}, {Config: sites.Config{ID: "machine-source", Kind: "nexusphp", Name: "fixture source", BaseURL: sourceURL}, Enabled: true}}
	settings.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "machine-qb", Kind: "qbittorrent", BaseURL: qb.URL}, Name: "Machine fixture qB"}}
	ctx := context.Background()
	// Persist settings through the real validator and atomic identity reconciler.
	// No direct identity INSERT, guessed numeric ID, or relaxed validation.
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	target, err := a.Store.nativeSiteByAppID(ctx, "machine-target")
	if err != nil {
		t.Fatal(err)
	}
	source, err := a.Store.nativeSiteByAppID(ctx, "machine-source")
	if err != nil {
		t.Fatal(err)
	}
	// The standard getter resolves the actual primary loaded IYUU object.
	// No synthetic enabled fact, checkpoint projection or fake Host is used.
	installCloneSource(t, a.pluginStore, iyuu.Manifest())
	originalMachineInitSave(t, a, "IYUUAutoSeed", map[string]any{"enabled": true, "onlyonce": false, "clearcache": false, "cron": "0 0 1 1 *", "downloaders": []any{"Machine fixture qB"}, "sites": []any{source.publicSourceID()}, "token": "owned-fixture-token", "size": json.Number("0")})
	if a.startOriginalPlugin(ctx, "IYUUAutoSeed") == nil {
		t.Fatal("actual primary IYUU original start failed")
	}
	installCloneSource(t, a.pluginStore, sr.NativeManifest())
	cfg := sr.ParseConfig(sr.Object{}).Current()
	cfg["enabled"] = true
	cfg["onlyonce"] = true
	cfg["token"] = "owned-fixture-token"
	cfg["target_site_id"] = target.SourceID
	cfg["source_site_ids"] = []int64{source.SourceID}
	cfg["downloader"] = "Machine fixture qB"
	cfg["rss_urls"] = targetURL + "/rss"
	cfg["cron"] = "0 0 1 1 *"
	originalMachineInitSave(t, a, sr.PluginID, cfg)
	a.pluginSeedRescue.now = func() time.Time { return time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC) }
	calls.Store(0)
	p := a.startOriginalPlugin(ctx, sr.PluginID)
	if p == nil {
		t.Fatal("actual original SeedRescue start failed")
	}
	return a, p, calls
}
func TestPluginOriginalMachineInitSeedPublicServicesOnly(t *testing.T) {
	a, p, calls := originalMachineInitSeed(t)
	ctx := context.Background()
	o := originalMachineInitSeedOwner(t, p)
	if o == nil || o.machine == nil {
		t.Fatal("missing actual SeedRescue machine")
	}
	before := o.machine
	if calls.Load() != 0 {
		t.Fatalf("init performed public dependency connection: %d", calls.Load())
	}
	if n := originalMachineInitCount(t, a, sr.PluginID, "seedrescue"); n != 0 {
		t.Fatalf("init privately registered public jobs: %d", n)
	}
	if n := originalMachineInitCount(t, a, sr.PluginID, "original_services"); n != 0 {
		t.Fatalf("public jobs too early: %d", n)
	}
	a.updateOriginalPluginJobs(ctx, sr.PluginID)
	if n := originalMachineInitCount(t, a, sr.PluginID, "original_services"); n != 2 {
		t.Fatalf("expected real cron+once get_service, got %d (qB checks=%d)", n, calls.Load())
	}
	if calls.Load() == 0 {
		t.Fatal("real source dependency Service was not called")
	}
	if err := p.init(ctx); err != nil {
		t.Fatalf("direct init after public ownership: %v", err)
	}
	if o.machine != before {
		t.Fatal("direct init replaced actual SeedRescue machine")
	}
	a.updateOriginalPluginJobs(ctx, sr.PluginID)
	if n := originalMachineInitCount(t, a, sr.PluginID, "original_services"); n != 1 {
		t.Fatalf("consumed public once reappeared: %d", n)
	}
	if n := originalMachineInitCount(t, a, sr.PluginID, "seedrescue"); n != 0 {
		t.Fatalf("private jobs appeared: %d", n)
	}
	originalMachineInitOnlyRuns(t, a, sr.PluginID, 2)
}

func TestPluginOriginalMachineInitCommitFailureRetainsPartialMachine(t *testing.T) {
	for _, id := range []string{cf.PluginID, sr.PluginID} {
		t.Run(id, func(t *testing.T) {
			t.Setenv("TZ", "UTC")
			a := newTestApp(t)
			useBuiltInPluginProcess(t, a)
			ctx := context.Background()
			if id == cf.PluginID {
				installCloneSource(t, a.pluginStore, cf.NativeManifest())
				originalMachineInitSave(t, a, id, map[string]any{"ipv4": false})
			} else {
				installCloneSource(t, a.pluginStore, sr.NativeManifest())
				originalMachineInitSave(t, a, id, map[string]any{"enabled": false})
			}
			p := a.startOriginalPlugin(ctx, id)
			if p == nil {
				t.Fatal("initial start failed")
			}
			var before any
			if id == cf.PluginID {
				before = originalMachineInitCFOwner(t, p).machine
			} else {
				before = originalMachineInitSeedOwner(t, p).machine
			}
			var cfg any
			if id == cf.PluginID {
				c := cloudflareTestConfig("")
				c["onlyonce"] = true
				cfg = c
			} else {
				cfg = map[string]any{"enabled": true, "onlyonce": true}
			}
			originalMachineInitSave(t, a, id, cfg)
			// Inject at real staged-config commit, after native member mutation. This is
			// not a failure inside Machine.Init and does not claim that stronger scope.
			if _, err := a.Store.DB.Exec(`CREATE TRIGGER original_machine_owned_config_failure BEFORE UPDATE ON plugin_values WHEN NEW.kind='config' AND NEW.item_key='main' BEGIN SELECT RAISE(FAIL,'owned-machine-commit-failure'); END`); err != nil {
				t.Fatal(err)
			}
			if err := p.init(ctx); err == nil {
				t.Fatal("owned config commit fault was swallowed")
			}
			var after any
			if id == cf.PluginID {
				after = originalMachineInitCFOwner(t, p).machine
			} else {
				after = originalMachineInitSeedOwner(t, p).machine
			}
			if after != before {
				t.Fatal("failed direct init replaced actual machine")
			}
			if state, err := p.getState(ctx); err != nil || !state {
				t.Fatalf("failed direct init rolled back live member mutation: %v %v", state, err)
			}
			if got, ok := a.pluginRunningInstances.Load(id); !ok || got != p {
				t.Fatal("failed direct init removed running object")
			}
		})
	}
}

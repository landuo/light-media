package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/scrapefileclean"
	scrapehost "light-media/internal/plugins/scrapefileclean/host"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPluginScrapeCleanWorkerFixture(t *testing.T) {
	for _, arg := range os.Args {
		if arg == "--scrape-clean-owner-worker" {
			if plugins.Serve(context.Background(), os.Stdin, os.Stdout, scrapefileclean.Manifest(), scrapefileclean.Handle) != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
	}
}
func scrapeActualInstall(t *testing.T, a *App, config any) PluginRun {
	t.Helper()
	m := scrapefileclean.Manifest()
	raw, _ := json.Marshal(m)
	_, err := a.pluginStore.InstallAndQueue(context.Background(), PluginInstallation{InstanceID: m.PluginID, PluginID: m.PluginID, Version: m.Version, ManifestSchemaVersion: m.SchemaVersion, Manifest: raw}, config)
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.pluginStore.claimRunForInstance(context.Background(), m.PluginID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func scrapeActualWorker(t *testing.T, a *App, run PluginRun) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a.pluginProcess = make(chan struct{}, 1)
	a.pluginCommand = func(string) (plugins.Command, error) {
		return plugins.Command{Path: exe, Args: []string{"-test.run=^TestPluginScrapeCleanWorkerFixture$", "--", "--scrape-clean-owner-worker"}}, nil
	}
	a.executePluginRun(context.Background(), run)
	got, err := a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || got.State != "succeeded" {
		t.Fatalf("actual SDK result %s %s %v", got.State, got.ErrorCode, err)
	}
}

type scrapeActualIdleWatcher struct {
	events chan scrapehost.Event
	errs   chan error
	once   sync.Once
}

func (w *scrapeActualIdleWatcher) Events() <-chan scrapehost.Event { return w.events }
func (w *scrapeActualIdleWatcher) Errors() <-chan error            { return w.errs }
func (w *scrapeActualIdleWatcher) Close() error {
	w.once.Do(func() { close(w.events); close(w.errs) })
	return nil
}
func scrapeActualIdle(a *App) {
	a.pluginScrapeClean.watch = func(context.Context, []scrapehost.Watch, func(string) (*os.File, error)) (scrapehost.Watcher, error) {
		return &scrapeActualIdleWatcher{events: make(chan scrapehost.Event), errs: make(chan error)}, nil
	}
}
func scrapeActualFixture(t *testing.T, delayed bool, flags bool) (*App, transferHistoryItem, PluginRun, *pluginScrapeCleanOwner) {
	t.Helper()
	a, item := historyRemovalFixture(t)
	scrapeActualIdle(a)
	cfg := map[string]any{"enabled": true, "monitor_dirs": a.Settings().AllowedRoots[0] + "\n" + a.Settings().Roots[0].Path, "delayed_deletion": delayed, "delay_seconds": 3600, "delete_history": flags, "delete_torrents": flags, "notify": flags, "delete_scrap_infos": flags}
	run := scrapeActualInstall(t, a, cfg)
	scrapeActualWorker(t, a, run)
	o := a.pluginScrapeClean.owners[run.InstanceID]
	if o == nil || o.h == nil {
		t.Fatal("no native owner")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if o.h != nil {
			_ = o.h.Suspend(ctx)
		}
	})
	return a, item, run, o
}
func TestPluginScrapeCleanActualImmediateHistoryAndPrivateEvent(t *testing.T) {
	a, item, _, o := scrapeActualFixture(t, false, true)
	ctx := context.Background()
	if err := os.Remove(item.Source); err != nil {
		t.Fatal(err)
	}
	if err := o.h.Observe(ctx, scrapehost.Event{ID: "owned-immediate", Kind: "deleted", Path: item.Source, Mode: "hardlink", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(item.Target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("linked media remains", err)
	}
	if _, err := a.transferHistoryItem(ctx, item.ID); err == nil {
		t.Fatal("history remains")
	}
	if err := a.dispatchPluginEvents(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	routes := noticeRouteTestIDs(t, a, "plugin_notice_routes")
	sourceCount := 0
	for _, id := range routes {
		route, _, err := a.pluginStore.readNoticeRoute(ctx, a.Store.DB, id)
		if err != nil {
			t.Fatal(err)
		}
		if route.Kind != "scrape_clean" {
			continue
		}
		sourceCount++
		if err = a.runNoticeRoute(ctx, id); err != nil {
			t.Fatal("actual source notice router", err)
		}
		route, _, err = a.pluginStore.readNoticeRoute(ctx, a.Store.DB, id)
		if err != nil || !route.Complete {
			t.Fatal("source route not complete", route.Complete, err)
		}
	}
	if sourceCount != 2 {
		t.Fatal("source notice was not routed", sourceCount)
	}

	events := cleanupFileEvents(t, a)
	if len(events) == 0 {
		t.Fatal("private source event missing")
	}
	for _, ev := range events {
		p, err := a.pluginStore.downloadFileEvent(ctx, a.Store.DB, ev.FileEventID)
		if err != nil || p.Src == "" {
			t.Fatal("invalid private producer", err)
		}
	}
}

func TestPluginScrapeCleanActualColdStartupDiscardsPendingAndRescans(t *testing.T) {
	a, item, run, o := scrapeActualFixture(t, true, false)
	ctx := context.Background()
	removeLinkOwnerPersistSettings(t, a)
	if err := os.Remove(item.Source); err != nil {
		t.Fatal(err)
	}
	if err := o.h.Observe(ctx, scrapehost.Event{ID: "owned-before-cold", Kind: "deleted", Path: item.Source, Mode: "hardlink", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	before, err := o.j.Read(ctx)
	if err != nil || len(before.Machine.Queue) != 1 {
		t.Fatal(err)
	}
	if err = o.h.Suspend(ctx); err != nil {
		t.Fatal(err)
	}
	o.h = nil // model process interruption, without source Stop.
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	scrapeActualIdle(reopened)
	if err = reopened.restoreScrapeFileCleanOwners(ctx); err != nil {
		t.Fatal(err)
	}
	next := reopened.pluginScrapeClean.owners[run.InstanceID]
	if next == nil || next.h == nil {
		t.Fatal("cold continuous owner missing")
	}
	after, err := next.j.Read(ctx)
	if err != nil || len(after.Machine.Queue) != 0 || after.Machine.TimerDue != nil {
		t.Fatal("cold replayed old pending", err)
	}
	if _, err = os.Stat(item.Target); err != nil {
		t.Fatal("cold startup deleted old alias", err)
	}
	sourceFound, targetFound := false, false
	for _, s := range after.Machine.State {
		sourceFound = sourceFound || s.Path == item.Source
		targetFound = targetFound || s.Path == item.Target
	}
	if sourceFound || !targetFound {
		t.Fatal("startup failed to build fresh identity baseline", sourceFound, targetFound)
	}
	if err = reopened.initializeNativePlugins(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = reopened.Store.DB.QueryRow(`SELECT count(*) FROM plugin_runs WHERE instance_id=?`, run.InstanceID).Scan(&count); err != nil || count != 1 {
		t.Fatal("cold created new init", count, err)
	}
}
func TestPluginScrapeCleanActualEvidenceLossCannotMintGenesis(t *testing.T) {
	for _, mode := range []string{"machine_namespace", "owner_root", "root_and_machine", "deferred", "core_receipt"} {
		t.Run(mode, func(t *testing.T) {
			a, item, run, o := scrapeActualFixture(t, false, true)
			ctx := context.Background()
			switch mode {
			case "machine_namespace":
				_, _ = a.Store.DB.Exec(`DELETE FROM plugin_scrape_journal_meta; DELETE FROM plugin_scrape_journal_rows`)
			case "owner_root":
				_, _ = a.Store.DB.Exec(`DELETE FROM plugin_scrape_owner_roots`)
			case "root_and_machine":
				_, _ = a.Store.DB.Exec(`DELETE FROM plugin_scrape_owner_roots; DELETE FROM plugin_scrape_owner_chain; DELETE FROM plugin_scrape_journal_meta; DELETE FROM plugin_scrape_journal_rows`)
			case "deferred":
				_, _ = a.Store.DB.Exec(`DELETE FROM plugin_deferred_operations WHERE run_id=?`, run.ID)
			case "core_receipt":
				if err := os.Remove(item.Source); err != nil {
					t.Fatal(err)
				}
				if err := o.h.Observe(ctx, scrapehost.Event{ID: "owned-committed", Kind: "deleted", Path: item.Source, Mode: "hardlink", At: time.Now()}); err != nil {
					t.Fatal(err)
				}
				_, _ = a.Store.DB.Exec(`DELETE FROM plugin_scrape_core_receipts`)
			}
			if _, err := a.pluginStore.EnqueueRun(ctx, run.InstanceID, "init", nil); err == nil {
				t.Fatal("missing evidence admitted")
			}
			if err := a.pluginStore.Reset(ctx, run.InstanceID); err == nil {
				t.Fatal("missing evidence reset minted new root")
			}
			i, err := a.pluginStore.Installation(ctx, run.InstanceID)
			if err != nil || i.Generation != run.Generation {
				t.Fatal("generation changed", i.Generation, err)
			}
			if mode != "core_receipt" {
				if _, err = os.Stat(item.Target); err != nil {
					t.Fatal("unrelated media changed", err)
				}
			}
		})
	}
}

func TestPluginScrapeCleanActualKernelRenameDeleteAndTimer(t *testing.T) {
	for _, delayed := range []bool{false, true} {
		t.Run(fmt.Sprint(delayed), func(t *testing.T) {
			a, source, library := importTestApp(t)
			ctx := context.Background()
			origin, target := filepath.Join(source, "owned-kernel.mkv"), filepath.Join(library, "owned-kernel.mkv")
			if err := os.WriteFile(origin, []byte("owned actual kernel source"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(origin, target); err != nil {
				t.Fatal(err)
			}
			run := scrapeActualInstall(t, a, map[string]any{"enabled": true, "monitor_dirs": source + "\n" + library, "delayed_deletion": delayed, "delay_seconds": 10})
			scrapeActualWorker(t, a, run)
			o := a.pluginScrapeClean.owners[run.InstanceID]
			renamed := filepath.Join(library, "owned-renamed.mkv")
			if err := os.Rename(origin, renamed); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(4 * time.Second)
			tracked := false
			for time.Now().Before(deadline) {
				s, err := o.j.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for _, v := range s.Machine.State {
					if v.Path == renamed {
						tracked = true
					}
				}
				if tracked {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if !tracked {
				t.Fatal("kernel rename was not consumed")
			}
			if _, err := os.Stat(target); err != nil {
				t.Fatal("rename cleaned original media", err)
			}
			start := time.Now()
			if err := os.Remove(renamed); err != nil {
				t.Fatal(err)
			}
			if delayed {
				time.Sleep(200 * time.Millisecond)
				if _, err := os.Stat(target); err != nil {
					t.Fatal("source delay bypassed", err)
				}
			}
			deadline = start.Add(14 * time.Second)
			for time.Now().Before(deadline) {
				_, err := os.Stat(target)
				if errors.Is(err, os.ErrNotExist) {
					break
				}
				time.Sleep(30 * time.Millisecond)
			}
			if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
				s, _ := o.j.Read(ctx)
				t.Fatal("actual kernel/timer did not clean", s.Blocked, err)
			}
			if delayed && time.Since(start) < 10*time.Second {
				t.Fatal("actual source ten-second timer shortened")
			}
			page, err := a.queryLogs(ctx, map[string]string{"plugin_instance_id": run.InstanceID, "limit": "100"})
			if err != nil {
				t.Fatal(err)
			}
			moveLog, configureLog := false, false
			for _, entry := range page.Entries {
				if entry["event"] == "scrape_clean.watch_moved" {
					moveLog = entry["plugin_run_id"] == "" && entry["plugin_id"] == "ScrapeFileClean"
				}
				if entry["event"] == "scrape_clean.configured" {
					configureLog = entry["plugin_run_id"] == run.ID
				}
			}
			if !moveLog || !configureLog {
				t.Fatal("native and worker log ownership missing", moveLog, configureLog)
			}
			t.Logf("actual kernel delayed=%v completed after %v", delayed, time.Since(start))
		})
	}
}

func TestPluginScrapeCleanActualHTTPInstallFormSaveAndReopen(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	removeLinkHTTPCall(t, a, cookie, "POST", "/api/plugins/install", map[string]any{"plugin_id": "ScrapeFileClean"})
	removeLinkHTTPRun(t, a, "ScrapeFileClean")
	w := callTest(t, a.Handler(), "GET", "/api/plugins/ScrapeFileClean/form", "", cookie, "")
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("monitor_dirs")) || !bytes.Contains(w.Body.Bytes(), []byte("delay_seconds")) {
		t.Fatal("actual original form unavailable", w.Code, w.Body.String())
	}
	removeLinkHTTPCall(t, a, cookie, "PUT", "/api/plugins/ScrapeFileClean/config", map[string]any{"revision": 1, "config": map[string]any{"enabled": false, "delay_seconds": 25}})
	run := removeLinkHTTPRun(t, a, "ScrapeFileClean")
	ctx := context.Background()
	if _, err := a.pluginStore.SaveConfig(ctx, "ScrapeFileClean", run.ConfigRevision, map[string]any{"enabled": true}); err == nil {
		t.Fatal("plain config write bypassed actual owner publication")
	}
	c, err := a.pluginStore.Config(ctx, "ScrapeFileClean")
	if err != nil || c.Revision != 2 || !bytes.Contains(c.Value, []byte("25")) {
		t.Fatal("actual form save mutated", c.Revision, err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: a.Dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cold.Close() })
	if err = cold.restoreScrapeFileCleanOwners(ctx); err != nil {
		t.Fatal(err)
	}
	next, err := cold.pluginStore.Config(ctx, "ScrapeFileClean")
	if err != nil || !bytes.Equal(c.Value, next.Value) || next.Revision != c.Revision {
		t.Fatal("actual saved config did not survive cold", err)
	}
	for _, id := range []string{"ScrapeFileClean", "TorrentTransfer", "TorrentTransfermusic", "SeedRescue"} {
		if _, _, ok := builtin.Lookup(id); !ok {
			t.Fatal("existing native plugin dropped", id)
		}
	}
	TestTransferCatalogPreservesCurrentSixteenPlugins(t)
	seen := map[string]bool{}
	for _, manifest := range builtin.Catalog() {
		if seen[manifest.PluginID] {
			t.Fatal("duplicate market entry", manifest.PluginID)
		}
		seen[manifest.PluginID] = true
		if _, _, ok := builtin.Lookup(manifest.PluginID); !ok {
			t.Fatal("market entry has no executable handler", manifest.PluginID)
		}
	}
	if !seen["ScrapeFileClean"] || !seen["BrushFlowLanduo"] {
		t.Fatal("focused new plugins missing from actual market")
	}
}

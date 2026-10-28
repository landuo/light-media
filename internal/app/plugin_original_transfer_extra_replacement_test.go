package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

// Fixed TransHandler.transfer_media:432-577 makes auxiliary overwrite unconditional,
// independently of whether a preceding main-file task succeeded or even existed.
func TestOriginalExtraReplacementWithoutVideoAsset(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "main-rejected"}[grouped], func(t *testing.T) {
			a, object, first, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			main := first.Files[0].Source
			extra := strings.TrimSuffix(main, ".mkv") + ".zh.srt"
			importWrite(t, extra, []byte("old independently owned subtitle"))
			cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": "module_execute"}
			cfg["result"] = nil
			if grouped {
				cfg["transfer_results"] = map[string]any{main: map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": false, "message": "owned main rejection"}}}
			}
			save := func() {
				t.Helper()
				raw, _ := json.Marshal(cfg)
				if _, e := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); e != nil {
					t.Fatal(e)
				}
				if e := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); e != nil {
					t.Fatal(e)
				}
			}
			save()
			var plan ImportPlan
			if grouped {
				var e error
				plan, e = a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: main}}})
				if e != nil {
					t.Fatal(e)
				}
			} else {
				out, e := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{extra}, MediaType: "movie", TMDBID: 123, TargetPath: a.Settings().Roots[0].Path, Mode: "hardlink", Scraping: importFlag(false)})
				if e != nil || out.Submitted != 1 {
					t.Fatal(out, e)
				}
				if e = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); e != nil {
					t.Fatal(e)
				}
			}
			job := overwriteStart(t, a, plan)
			e := a.runImport(ctx, job, plan.ID, nil)
			if grouped != (e != nil) {
				t.Fatal("first independent task outcome", e)
			}
			a.wg.Wait()
			if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='failed' WHERE id=?`, job.ID); e != nil {
				t.Fatal(e)
			}
			var n int
			if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='asset'`).Scan(&n); e != nil || n != 0 {
				t.Fatal("no false main asset", n, e)
			}
			delete(cfg, "transfer_results")
			save()
			incoming := filepath.Join(filepath.Dir(main), "replacement", filepath.Base(extra))
			importWrite(t, incoming, []byte("new independent subtitle"))
			out, e := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{incoming}, MediaType: "movie", TMDBID: 123, TargetPath: a.Settings().Roots[0].Path, Mode: "hardlink", Scraping: importFlag(false)})
			if e != nil || out.Submitted != 1 {
				t.Fatal("resubmit", out, e)
			}
			var next ImportPlan
			if e = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &next); e != nil {
				t.Fatal(e)
			}
			var old managedFile
			if e = a.Store.Get(ctx, "managed_file", tokenHash(next.Files[0].Target), &old); e != nil {
				t.Fatal(e)
			}
			tx, e := a.Store.DB.BeginTx(ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			if e = verifyIndependentExtraOwnerTx(ctx, tx, old, a.Settings().Roots[0].Path); e != nil {
				tx.Rollback()
				t.Fatal("original proof", e)
			}
			old.SourceSnapshot.Size++
			if e = verifyIndependentExtraOwnerTx(ctx, tx, old, a.Settings().Roots[0].Path); fileCode(e) != "REVIEW_OVERWRITE_OWNER" {
				tx.Rollback()
				t.Fatal("changed source proof accepted", e)
			}
			tx.Rollback()
			if !grouped {
				if _, e = a.Store.DB.Exec(`CREATE TRIGGER owned_extra_replace_failure BEFORE INSERT ON records WHEN NEW.kind='managed_file' BEGIN SELECT RAISE(ABORT,'owned extra replacement registration'); END`); e != nil {
					t.Fatal(e)
				}
				if e = a.runImport(ctx, overwriteStart(t, a, next), next.ID, nil); e == nil {
					t.Fatal("SQL failure ignored")
				}
				published, e := snapshotPath(next.Files[0].Target)
				if e != nil {
					t.Fatal("already published replacement", e)
				}
				dir := a.Dir
				if e = a.Close(); e != nil {
					t.Fatal(e)
				}
				a, e = New(Options{DataDir: dir})
				if e != nil {
					t.Fatal(e)
				}
				t.Cleanup(func() { a.Close() })
				if e = a.initializeOriginalPlugins(ctx); e != nil {
					t.Fatal(e)
				}
				if _, e = a.Store.DB.Exec(`DROP TRIGGER owned_extra_replace_failure`); e != nil {
					t.Fatal(e)
				}
				if e = a.runImport(ctx, overwriteStart(t, a, next), next.ID, nil); e != nil {
					t.Fatal("cold register-only recovery", e)
				}
				actual, e := snapshotPath(next.Files[0].Target)
				if e != nil || actual != published {
					t.Fatal("cold republished extra", e)
				}
				instance, ok := a.pluginRunningInstances.Load(ownedModuleID)
				if !ok || ownedModuleRead(t, instance.(*originalPluginInstance)).Calls["module_execute"] != 0 {
					t.Fatal("cold replayed module transfer")
				}
			} else if e = a.runImport(ctx, overwriteStart(t, a, next), next.ID, nil); e != nil {
				t.Fatal("fixed always-overwrite extra without invented video asset", e)
			}
			var final ImportPlan
			if e = a.Store.Get(ctx, "import_plan", next.ID, &final); e != nil || final.State != "complete" {
				t.Fatal(final, e)
			}
			src, e := os.Stat(incoming)
			if e != nil {
				t.Fatal(e)
			}
			dst, e := os.Stat(final.Files[0].Target)
			if e != nil || !os.SameFile(src, dst) {
				t.Fatal("real replacement hardlink", e)
			}
			var owned managedFile
			if e = a.Store.Get(ctx, "managed_file", tokenHash(final.Files[0].Target), &owned); e != nil || owned.AssetID != final.Files[0].AssetID || owned.Kind != "subtitle" {
				t.Fatal("new independent owner", owned, e)
			}
			if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='asset'`).Scan(&n); e != nil || n != 0 {
				t.Fatal("no fabricated asset after overwrite", n, e)
			}
		})
	}
}

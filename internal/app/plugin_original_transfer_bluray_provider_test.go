package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"testing"
)

// Source transfer.py consumes the provider's returned TransferInfo and target;
// native fallback must not run after a non-None result. Native ownership checks
// additionally verify actual directory members, including copied bytes.
func TestOriginalTransferBlurayTerminalProvider(t *testing.T) {
	for _, sample := range []struct{ mode, worker, code string }{
		{"hardlink", "", ""}, {"copy", "copy", ""},
		{"copy", "corrupt", "REVIEW_COPY_CONTENT"}, {"hardlink", "copy", "REVIEW_PLUGIN_TRANSFER_METHOD"},
	} {
		t.Run(sample.mode+"_"+sample.worker, func(t *testing.T) {
			a, object, original, cfg := originalTransferModuleFixture(t, sample.mode, sample.worker)
			ctx := context.Background()
			source := filepath.Join(filepath.Dir(original.Files[0].Source), "Owned.2024.BluRay")
			importWrite(t, filepath.Join(source, "BDMV", "STREAM", "00001.m2ts"), []byte("owned stream"))
			importWrite(t, filepath.Join(source, "BDMV", "index.bdmv"), []byte("owned index"))
			target := filepath.Join(a.Settings().Roots[0].Path, "Plugin Selected Disc")
			cfg["transfer_target"] = target
			cfg["result"] = map[string]any{plugins.ModuleTransferInfoKey: map[string]any{
				"success": true, "need_scrape": false,
				"target_item":    map[string]any{"storage": "local", "type": "dir", "path": target + "/"},
				"target_diritem": map[string]any{"storage": "local", "type": "dir", "path": target + "/"},
			}}
			raw, _ := json.Marshal(cfg)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			out, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{source}, TargetPath: a.Settings().Roots[0].Path, Mode: sample.mode, MediaType: "movie", TMDBID: 123, Scraping: importFlag(false)})
			if err != nil || out.Submitted != 1 {
				t.Fatal("one directory preparation", out, err)
			}
			var plan ImportPlan
			if err = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); err != nil {
				t.Fatal(err)
			}
			before := ownedModuleRead(t, object)
			job := overwriteStart(t, a, plan)
			err = a.runImport(ctx, job, plan.ID, nil)
			if fileCode(err) != sample.code {
				t.Fatal("actual directory integrity", fileCode(err), sample.code, err)
			}
			a.wg.Wait()
			after := ownedModuleRead(t, object)
			if after.Calls["module_transfer"] != before.Calls["module_transfer"]+1 {
				t.Fatal("one terminal directory call", before.Calls, after.Calls)
			}
			if _, err := os.Stat(plan.Files[0].Target); !os.IsNotExist(err) {
				t.Fatal("native fallback ran after terminal provider", err)
			}
			if _, err := snapshotTransferPath(source); err != nil {
				t.Fatal("source preserved", err)
			}
			var assets int
			if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='asset'`).Scan(&assets); err != nil || assets != map[bool]int{true: 1, false: 0}[sample.code == ""] {
				t.Fatal("verified asset only", assets, err)
			}
			if sample.code == "" {
				var updated ImportPlan
				if err := a.Store.Get(ctx, "import_plan", plan.ID, &updated); err != nil || updated.Files[0].Target != target {
					t.Fatal("provider target adopted", updated, err)
				}
				for _, name := range []string{"BDMV/STREAM/00001.m2ts", "BDMV/index.bdmv"} {
					s, err := os.Stat(filepath.Join(source, name))
					d, other := os.Stat(filepath.Join(target, name))
					if err != nil || other != nil || os.SameFile(s, d) != (sample.mode == "hardlink") {
						t.Fatal("physical provider result", name, err, other)
					}
				}
			}
		})
	}
}

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOriginalTransferBlurayHistory(t *testing.T) {
	for _, mode := range []string{"target", "source", "both", "changed_target", "cold_source", "reorganize", "ordinary_source", "ordinary_target", "ordinary_empty_target"} {
		t.Run(mode, func(t *testing.T) {
			a, object, original, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			source := filepath.Join(filepath.Dir(original.Files[0].Source), "Owned.2024.BluRay")
			importWrite(t, filepath.Join(source, "BDMV", "STREAM", "00001.m2ts"), []byte("owned stream"))
			importWrite(t, filepath.Join(source, "BDMV", "index.bdmv"), []byte("owned index"))
			cfg["modules"].(map[string]string)["transfer"] = "module_execute"
			cfg["modules"].(map[string]string)["delete_file"] = "module_execute"
			cfg["result"] = nil
			raw, _ := json.Marshal(cfg)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			out, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{source}, TargetPath: a.Settings().Roots[0].Path, Mode: "hardlink", MediaType: "movie", TMDBID: 123, Scraping: importFlag(false)})
			if err != nil || out.Submitted != 1 {
				t.Fatal(out, err)
			}
			var plan ImportPlan
			if err = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); err != nil {
				t.Fatal(err)
			}
			job := overwriteStart(t, a, plan)
			if err = a.runImport(ctx, job, plan.ID, nil); err != nil {
				t.Fatal(err)
			}
			if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID); err != nil {
				t.Fatal(err)
			}
			a.wg.Wait()
			item, err := a.transferHistoryItem(ctx, plan.Files[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if item.Size != 12 {
				t.Fatal("source STREAM history size, independent of frozen tree total", item.Size)
			}
			ordinary := mode == "ordinary_source" || mode == "ordinary_target" || mode == "ordinary_empty_target"
			if ordinary {
				// The selected storage reports no BDMV/CERTIFICATE child. Its
				// any_files result then controls the ordinary directory branch.
				cfg["modules"] = map[string]string{"get_file_item": "module_execute"}
				if mode == "ordinary_empty_target" {
					cfg["modules"].(map[string]string)["any_files"] = "module_execute"
				}
				cfg["result"] = false
				raw, _ := json.Marshal(cfg)
				if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
					t.Fatal(err)
				}
				if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "reorganize" {
				out, err := a.manualOrganize(ctx, manualOrganizeRequest{HistoryIDs: []string{item.ID}, FromHistory: true, TargetPath: a.Settings().Roots[0].Path, Mode: "hardlink", MediaType: "movie", TMDBID: 123, Scraping: importFlag(false)})
				if err != nil || out.Submitted != 1 {
					t.Fatal("directory history reorganize preparation", out, err)
				}
				var next ImportPlan
				if err := a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &next); err != nil {
					t.Fatal(err)
				}
				nextJob := overwriteStart(t, a, next)
				if err := a.runImport(ctx, nextJob, next.ID, nil); err != nil {
					t.Fatal("directory history re-publication", err)
				}
				return
			}
			if mode == "changed_target" {
				importWrite(t, filepath.Join(item.Target, "foreign.txt"), []byte("keep me"))
			}
			if mode == "cold_source" {
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_dir_remove_commit BEFORE UPDATE ON records WHEN NEW.kind='history_source_removal' AND json_extract(NEW.payload,'$.state')='completed' BEGIN SELECT RAISE(ABORT,'owned directory removal commit'); END`); err != nil {
					t.Fatal(err)
				}
			}
			in := historyDeleteRequest{Confirm: true, DeleteSource: mode == "source" || mode == "both" || mode == "cold_source" || mode == "ordinary_source", DeleteTarget: mode == "target" || mode == "both" || mode == "changed_target" || mode == "ordinary_target" || mode == "ordinary_empty_target", FileID: item.executionFileID()}
			_, err = a.deleteTransferHistory(ctx, item, in)
			if mode == "changed_target" {
				if fileCode(err) != "FILE_SNAPSHOT_CHANGED" {
					t.Fatal("changed directory must be retained", err)
				}
				if bytes, e := os.ReadFile(filepath.Join(item.Target, "foreign.txt")); e != nil || string(bytes) != "keep me" {
					t.Fatal("unknown data preserved", e)
				}
				return
			}
			if mode == "cold_source" {
				if err == nil {
					t.Fatal("missing real SQL commit failure")
				}
				if _, e := os.Stat(source); !os.IsNotExist(e) {
					t.Fatal("delete must precede failed SQL", e, err)
				}
				if _, e := a.Store.DB.Exec(`DROP TRIGGER owned_dir_remove_commit`); e != nil {
					t.Fatal(e)
				}
				dir := a.Dir
				if e := a.Close(); e != nil {
					t.Fatal(e)
				}
				a, err = New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { a.Close() })
				if err := a.initializeOriginalPlugins(ctx); err != nil {
					t.Fatal(err)
				}
				_, err = a.deleteTransferHistory(ctx, item, in)
			}
			if err != nil {
				t.Fatal("actual owned directory history deletion", mode, err)
			}
			if mode == "ordinary_source" {
				var factID string
				if err := a.Store.DB.QueryRow(`SELECT id FROM records WHERE kind='history_download_file_event'`).Scan(&factID); err != nil {
					t.Fatal("source endpoint still publishes on retained directory", err)
				}
				dir := a.Dir
				if err := a.Close(); err != nil {
					t.Fatal(err)
				}
				a, err = New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { a.Close() })
				fact, err := a.pluginStore.historyDownloadFileFact(ctx, a.Store.DB, factID)
				if err != nil || !fact.DirectoryRetained || fact.Src != source || fact.Identity.Hash != "" {
					t.Fatal("retained directory event binding survives cold recovery", fact, err)
				}
			}
			for path, deleted := range map[string]bool{source: in.DeleteSource, item.Target: in.DeleteTarget} {
				if mode == "ordinary_source" || mode == "ordinary_target" {
					deleted = false
				}
				_, e := os.Stat(path)
				if os.IsNotExist(e) != deleted {
					t.Fatal("selected directory effects", path, deleted, e)
				}
			}
			var hidden int
			if e := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='transfer_history_hidden' AND id=?`, item.ID).Scan(&hidden); e != nil || hidden != 1 {
				t.Fatal("history hidden after actual success", hidden, e)
			}
		})
	}
}

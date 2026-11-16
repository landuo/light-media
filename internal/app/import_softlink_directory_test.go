package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins"
)

func TestImportSoftlinkDirectoryManualSDKAndColdMemberRecovery(t *testing.T) {
	for _, branch := range []string{"native", "cold-member"} {
		t.Run(branch, func(t *testing.T) {
			a, object, original, cfg := originalTransferModuleFixture(t, "softlink", "")
			ctx := t.Context()
			source := filepath.Join(filepath.Dir(original.Files[0].Source), "Owned.2024.BluRay")
			files := map[string]string{"BDMV/STREAM/00001.m2ts": "owned stream", "BDMV/index.bdmv": "owned index", "tiny.txt": "x"}
			for relative, data := range files {
				importWrite(t, filepath.Join(source, relative), []byte(data))
			}
			if err := os.Mkdir(filepath.Join(source, "CERTIFICATE"), 0750); err != nil {
				t.Fatal(err)
			}
			sourceStamp, _, err := snapshotOriginalDirectory(source)
			if err != nil {
				t.Fatal(err)
			}
			cfg["modules"].(map[string]string)["transfer"] = "module_execute"
			cfg["result"] = nil
			raw, _ := json.Marshal(cfg)
			if _, err = a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
				t.Fatal(err)
			}
			if err = a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			out, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{source}, TargetPath: a.Settings().Roots[0].Path, Mode: "softlink", MediaType: "movie", TMDBID: 123, Scraping: importFlag(false)})
			if err != nil || out.Failed != 0 || out.Submitted != 1 || len(out.Items) != 1 {
				t.Fatal("one actual manual directory task", out, err)
			}
			var plan ImportPlan
			if err = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); err != nil || len(plan.Files) != 1 {
				t.Fatal(plan, err)
			}
			key := plan.ID + "/" + plan.Files[0].ID
			job := overwriteStart(t, a, plan)
			if branch == "cold-member" {
				// Stop at an actual child publication, not an invented successful receipt.
				_, err = a.Store.DB.Exec(`CREATE TRIGGER softlink_directory_pause AFTER UPDATE ON import_receipt WHEN NEW.record_kind='import_receipt' AND NEW.id LIKE '` + key + `/directory/%' AND json_extract(NEW.payload,'$.state')='published' BEGIN UPDATE jobs SET state='cancelled',generation=generation+1 WHERE id='` + job.ID + `'; END`)
				if err != nil {
					t.Fatal(err)
				}
			}
			err = a.runImport(ctx, job, plan.ID, nil)
			var firstPath string
			var firstSnapshot importSnapshot
			if branch == "cold-member" {
				if !originalTransferInterrupted(err) {
					t.Fatal("actual child publication did not interrupt", err)
				}
				var parent importReceipt
				if err = a.Store.Get(ctx, "import_receipt", key, &parent); err != nil || parent.State != "directory_created" {
					t.Fatal("recoverable parent receipt", parent, err)
				}
				published := 0
				for _, relative := range parent.DirectoryFiles {
					var child importReceipt
					if err = a.Store.Get(ctx, "import_receipt", key+"/directory/"+tokenHash(relative), &child); err == nil && child.State == "published" {
						published++
						firstPath, firstSnapshot = child.Target, child.StageSnapshot
					}
				}
				if published != 1 {
					t.Fatal("expected exact one-member published prefix", published)
				}
				if _, err = a.Store.DB.Exec(`DROP TRIGGER softlink_directory_pause`); err != nil {
					t.Fatal(err)
				}
				a.wg.Wait()
				if events := ownedModuleRead(t, object).TransferEvents; len(events) != 0 {
					t.Fatal("interrupted directory emitted completion", events)
				}
				dir := a.Dir
				if err = a.Close(); err != nil {
					t.Fatal(err)
				}
				a, err = New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { a.Close() })
				if err = a.initializeOriginalPlugins(ctx); err != nil {
					t.Fatal(err)
				}
				object = originalBaseSDKLoaded(t, a, ownedModuleID)
				before := ownedModuleRead(t, object)
				current, e := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
				if e != nil {
					t.Fatal(e)
				}
				if _, e = a.retryOriginalScrapeJob(ctx, current); e != nil {
					t.Fatal(e)
				}
				if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID); e != nil {
					t.Fatal(e)
				}
				job, e = scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
				if e != nil {
					t.Fatal(e)
				}
				err = a.runImport(ctx, job, plan.ID, nil)
				a.wg.Wait()
				if after := ownedModuleRead(t, object); after.Calls["module_execute"] != before.Calls["module_execute"] {
					t.Fatal("cold recovery replayed provider", before.Calls, after.Calls)
				}
			}
			if err != nil {
				t.Fatal("actual directory softlink import", err)
			}
			var receipt importReceipt
			if err = a.Store.Get(ctx, "import_receipt", key, &receipt); err != nil || receipt.State != "published" || receipt.Softlink == nil || receipt.Softlink.Directory == nil || len(receipt.Softlink.Directory.Members) != len(files) {
				t.Fatal("real directory publication proof", receipt, err)
			}
			target := receipt.Target
			if info, err := os.Lstat(target); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				t.Fatal("target must be a physical directory, not a root link", info, err)
			}
			for relative, data := range files {
				path := filepath.Join(target, relative)
				if text, err := os.Readlink(path); err != nil || text != filepath.Join(source, relative) {
					t.Fatal("actual leaf softlink", path, text, err)
				}
				if got, err := os.ReadFile(path); err != nil || string(got) != data {
					t.Fatal("target member bytes", relative, err)
				}
				if member := receipt.Softlink.Directory.Members[relative]; member.Softlink == nil || member.Softlink.Directory != nil {
					t.Fatal("leaf proof", relative, member)
				}
			}
			if firstPath != "" {
				if stamp, _, err := snapshotSoftlinkEntry(firstPath); err != nil || stamp != firstSnapshot {
					t.Fatal("cold recovery replaced already-published link", err)
				}
			}
			if _, err := os.Lstat(filepath.Join(target, "CERTIFICATE")); !os.IsNotExist(err) {
				t.Fatal("empty dirs diverged from MP file-only recursion", err)
			}
			if actual, _, err := snapshotOriginalDirectory(source); err != nil || actual != sourceStamp {
				t.Fatal("directory source changed", actual, err)
			}
			if _, _, err := snapshotOriginalDirectory(target); err == nil {
				t.Fatal("generic directory snapshot followed links")
			}
			if actual, err := snapshotOwnedSoftlinkDirectory(target, receipt.StageSnapshot, receipt.Softlink, true); err != nil || actual != receipt.StageSnapshot {
				t.Fatal("exact target proof", actual, err)
			}
			var asset Asset
			if err = a.assetAtPath(ctx, target, &asset); err != nil || asset.Size != sourceStamp.Size {
				t.Fatal("single asset retains media bytes, not entry bytes", asset, err)
			}
			var owner managedFile
			if err = a.Store.Get(ctx, "managed_file", tokenHash(target), &owner); err != nil || !sameSoftlinkProof(owner.Softlink, receipt.Softlink) {
				t.Fatal("directory ownership proof", owner, err)
			}
			item, err := a.transferHistoryItem(ctx, plan.Files[0].ID)
			if err != nil || item.Size != 12 {
				t.Fatal("single directory history keeps STREAM size", item, err)
			}
			a.wg.Wait()
			events := ownedModuleRead(t, object).TransferEvents
			if len(events) != 1 {
				t.Fatal("one directory completion callback", len(events))
			}
			var event plugins.MutableEvent
			var payload struct {
				FileItem     map[string]any `json:"fileitem"`
				TransferInfo map[string]any `json:"transferinfo"`
			}
			if json.Unmarshal(events[0], &event) != nil || json.Unmarshal(event.Data, &payload) != nil || event.Type != "transfer.complete" {
				t.Fatal("actual SDK directory callback", string(events[0]))
			}
			if payload.FileItem["type"] != "dir" || payload.FileItem["size"] != float64(12) || payload.TransferInfo["file_count"] != float64(0) || payload.TransferInfo["total_size"] != float64(0) {
				t.Fatal("original root and STREAM counters", payload)
			}
			if len(payload.TransferInfo["file_list"].([]any)) != len(files) || len(payload.TransferInfo["file_list_new"].([]any)) != len(files) {
				t.Fatal("complete member mapping", payload.TransferInfo)
			}
			importWrite(t, filepath.Join(source, "unrelated.nfo"), []byte("later source metadata"))
			if _, err := snapshotOwnedSoftlinkDirectory(target, receipt.StageSnapshot, receipt.Softlink, true); err != nil {
				t.Fatal("unrelated later source file invalidated existing links", err)
			}
		})
	}
}

package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalTransferModuleActualSDKDirectorySoftlinkAdoption(t *testing.T) {
	for _, branch := range []string{"success", "asset-sql-cold", "counterfeit-copy", "existing-target"} {
		t.Run(branch, func(t *testing.T) {
			mode := "softlink"
			if branch == "counterfeit-copy" {
				mode = "copy"
			}
			a, object, original, config := originalTransferModuleFixture(t, "softlink", mode)
			ctx := t.Context()
			source := filepath.Join(filepath.Dir(original.Files[0].Source), "Owned.2024.BluRay")
			contents := map[string][]byte{"BDMV/STREAM/00001.m2ts": []byte("owned stream"), "BDMV/index.bdmv": []byte("owned index"), "tiny.txt": []byte("x")}
			for rel, data := range contents {
				importWrite(t, filepath.Join(source, rel), data)
			}
			if err := os.Mkdir(filepath.Join(source, "CERTIFICATE"), 0750); err != nil {
				t.Fatal(err)
			}
			beforeSource, entries, err := snapshotOriginalDirectory(source)
			if err != nil {
				t.Fatal(err)
			}
			out, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{source}, TargetPath: a.Settings().Roots[0].Path, Mode: "softlink", MediaType: "movie", TMDBID: 123, Scraping: importFlag(false)})
			if err != nil || out.Submitted != 1 || out.Failed != 0 {
				t.Fatal("actual directory submission", out, err)
			}
			var plan ImportPlan
			var meta importMetadata
			if err = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); err != nil || len(plan.Files) != 1 {
				t.Fatal(plan, err)
			}
			if err = a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
				t.Fatal(err)
			}
			file := plan.Files[0]
			target, key := file.Target, plan.ID+"/"+file.ID
			oldPaths, newPaths := []string{}, []string{}
			for _, entry := range entries {
				if !entry.Directory {
					oldPaths = append(oldPaths, filepath.Join(source, entry.Path))
					newPaths = append(newPaths, filepath.Join(target, entry.Path))
				}
			}
			config["transfer_target"] = target
			config["result"] = map[string]any{plugins.ModuleTransferInfoKey: map[string]any{
				"success": true, "transfer_type": "softlink", "need_scrape": false,
				"fileitem": meta.OriginalTransfer[file.ID].Item, "target_item": originalTransferPreviewItem(target, "dir", file.Size),
				"file_list": oldPaths, "file_list_new": newPaths, "file_count": 0, "total_size": 0,
			}}
			raw, _ := json.Marshal(config)
			if _, err = a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
				t.Fatal(err)
			}
			if err = a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			absentPermission := "1"
			if branch == "existing-target" {
				absentPermission = "0"
			}
			for _, event := range []string{"INSERT", "UPDATE"} {
				if _, err = a.Store.DB.Exec(`CREATE TRIGGER dir_soft_call_` + event + ` BEFORE ` + event + ` ON records WHEN NEW.kind='import_receipt' AND json_extract(NEW.payload,'$.original_module_started')=1 AND (COALESCE(json_extract(NEW.payload,'$.original_softlink_directory.permission.target_absent'),0)!=` + absentPermission + ` OR json_type(NEW.payload,'$.original_softlink_directory.entries') IS NULL) BEGIN SELECT RAISE(FAIL,'directory provider called without original inventory'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if branch == "asset-sql-cold" {
				if _, err = a.Store.DB.Exec(`CREATE TRIGGER dir_soft_asset_failure BEFORE INSERT ON records WHEN NEW.kind='asset' BEGIN SELECT RAISE(FAIL,'synthetic directory softlink asset failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			job := overwriteStart(t, a, plan)
			before := ownedModuleRead(t, object)
			if before.PID <= 0 || before.PID == os.Getpid() {
				t.Fatal("real SDK process", before.PID)
			}
			if branch == "existing-target" {
				importWrite(t, filepath.Join(target, "foreign.txt"), []byte("existing directory preserved"))
			}
			err = a.runImport(ctx, job, plan.ID, nil)
			positive := branch == "success" || branch == "asset-sql-cold" || branch == "existing-target"
			if (branch == "success" || branch == "existing-target") && err != nil || branch != "success" && branch != "asset-sql-cold" && branch != "existing-target" && err == nil {
				t.Fatal("actual outcome", branch, err)
			}
			if branch == "counterfeit-copy" && fileCode(err) != "REVIEW_PLUGIN_TRANSFER_METHOD" {
				t.Fatal("copy claimed as directory links", err)
			}
			a.wg.Wait()
			after := ownedModuleRead(t, object)
			expectedCalls := 1
			if after.Calls["module_transfer"]-before.Calls["module_transfer"] != expectedCalls {
				t.Fatal("single root SDK operation", before.Calls, after.Calls)
			}
			if actual, _, err := snapshotOriginalDirectory(source); err != nil || actual != beforeSource {
				t.Fatal("original source tree changed", actual, err)
			}
			if !positive {
				var count int
				if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='asset' OR kind='managed_file'`).Scan(&count); err != nil || count != 0 {
					t.Fatal("unverified directory claimed", count, err)
				}
				return
			}
			var receipt importReceipt
			if err = a.Store.Get(ctx, "import_receipt", key, &receipt); err != nil || receipt.State != "published" || receipt.OriginalSoftlinkDirectory == nil || !receipt.OriginalSoftlinkDirectory.Permission.Adopted || len(receipt.OriginalSoftlinkDirectory.Entries) != len(entries) || receipt.Softlink == nil || receipt.Softlink.Directory == nil || len(receipt.Softlink.Directory.Members) != len(contents) || receipt.DirectoryStreamSize == nil || *receipt.DirectoryStreamSize != 12 {
				t.Fatal("actual directory proof", receipt, err)
			}
			if branch == "existing-target" {
				found := false
				if receipt.OriginalSoftlinkDirectory.Existing != nil {
					for _, prior := range receipt.OriginalSoftlinkDirectory.Existing.Entries {
						if prior.Path == "foreign.txt" && !prior.Directory {
							found = true
						}
					}
				}
				if !found || receipt.OriginalSoftlinkDirectory.Permission.TargetAbsent {
					t.Fatal("existing target proof", receipt.OriginalSoftlinkDirectory)
				}
				if data, e := os.ReadFile(filepath.Join(target, "foreign.txt")); e != nil || string(data) != "existing directory preserved" {
					t.Fatal("preserved unrelated member", e)
				}
			}
			if info, err := os.Lstat(target); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				t.Fatal("root must be real directory", info, err)
			}
			for rel, data := range contents {
				path := filepath.Join(target, rel)
				if text, err := os.Readlink(path); err != nil || text != filepath.Join(source, rel) {
					t.Fatal("actual leaf reference", text, err)
				}
				if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, data) {
					t.Fatal("actual leaf bytes", rel, err)
				}
			}
			if _, err := os.Lstat(filepath.Join(target, "CERTIFICATE")); !os.IsNotExist(err) {
				t.Fatal("MP file-only recursion copied empty directory", err)
			}
			var children int
			if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='import_receipt' AND id LIKE ?`, key+"/directory/%").Scan(&children); err != nil || children != 0 {
				t.Fatal("external provider acquired fabricated native child receipts", children, err)
			}
			if len(after.TransferEvents) != 1 {
				t.Fatal("one directory callback", after.TransferEvents)
			}
			var event plugins.MutableEvent
			var payload struct {
				Item *plugins.ModuleFileItem `json:"fileitem"`
				Info map[string]any          `json:"transferinfo"`
			}
			if json.Unmarshal(after.TransferEvents[0], &event) != nil || json.Unmarshal(event.Data, &payload) != nil || event.Type != "transfer.complete" || !reflect.DeepEqual(payload.Item, meta.OriginalTransfer[file.ID].Item) || payload.Info["transfer_type"] != "softlink" || len(payload.Info["file_list"].([]any)) != len(contents) || len(payload.Info["file_list_new"].([]any)) != len(contents) {
				t.Fatal("original root FileItem/full mappings/STREAM", string(after.TransferEvents[0]))
			}
			// A completed publication owns these refs, not every future source addition.
			importWrite(t, filepath.Join(source, "later-unrelated.nfo"), []byte("new source metadata"))
			if branch == "asset-sql-cold" {
				if _, err = a.Store.DB.Exec(`DROP TRIGGER dir_soft_asset_failure`); err != nil {
					t.Fatal(err)
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
				cold := ownedModuleRead(t, object)
				if cold.PID == before.PID || cold.Calls["module_transfer"] != 0 {
					t.Fatal("new cold worker", cold)
				}
				if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='queued' WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
				job, err = a.claim(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err = a.runImport(ctx, job, plan.ID, nil); err != nil {
					t.Fatal("cold sealed directory recovery after source NFO addition", err)
				}
				a.wg.Wait()
				if last := ownedModuleRead(t, object); last.Calls["module_transfer"] != 0 || len(last.TransferEvents) != 0 {
					t.Fatal("cold provider/callback replay", last.Calls, last.TransferEvents)
				}
			} else {
				if _, err = a.originalTransferModulePublish(ctx, plan, file, meta, ctx.Err, func() (importReceipt, error) {
					t.Fatal("published external directory reentered native")
					return importReceipt{}, nil
				}, func(importReceipt) error { t.Fatal("published directory renamed"); return nil }); err != nil {
					t.Fatal("published original references", err)
				}
				var republished importReceipt
				if err = a.Store.Get(ctx, "import_receipt", key, &republished); err != nil {
					t.Fatal(err)
				}
				if branch == "existing-target" {
					count := 0
					if republished.OriginalSoftlinkDirectory != nil && republished.OriginalSoftlinkDirectory.Existing != nil {
						for _, prior := range republished.OriginalSoftlinkDirectory.Existing.Entries {
							if prior.Path == "foreign.txt" {
								count++
							}
						}
					}
					if count != 1 || republished.Softlink == nil || republished.Softlink.Directory == nil {
						t.Fatalf("republish refroze the existing proof %#v", republished.OriginalSoftlinkDirectory)
					}
				}
			}
			var owner managedFile
			if err = a.Store.Get(ctx, "managed_file", tokenHash(target), &owner); err != nil || owner.Snapshot != receipt.StageSnapshot || !sameSoftlinkProof(owner.Softlink, receipt.Softlink) {
				t.Fatal("full unchanged managed directory proof", owner, err)
			}
			var asset Asset
			if err = a.assetAtPath(ctx, target, &asset); err != nil || asset.Size != beforeSource.Size {
				t.Fatal("one asset/source byte size", asset, err)
			}
			history, err := a.transferHistoryItem(ctx, file.ID)
			if err != nil || history.Size != 12 {
				t.Fatal("one history/STREAM size", history, err)
			}
			for rel, data := range contents {
				if got, err := os.ReadFile(filepath.Join(source, rel)); err != nil || !bytes.Equal(got, data) {
					t.Fatal("original bytes retained", rel, err)
				}
			}
		})
	}
}

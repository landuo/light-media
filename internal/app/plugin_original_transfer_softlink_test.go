package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalTransferModuleActualSDKSoftlinkAdoption(t *testing.T) {
	for _, branch := range []string{"success", "asset-sql-cold", "counterfeit-copy", "existing-link"} {
		t.Run(branch, func(t *testing.T) {
			worker := "softlink"
			if branch == "counterfeit-copy" {
				worker = "copy"
			}
			a, object, plan, config := originalTransferModuleFixture(t, "softlink", worker)
			ctx := t.Context()
			positive := branch == "success" || branch == "asset-sql-cold"
			contents := map[string][]byte{plan.Files[0].Source: []byte("owned transfer content")}
			if positive {
				subtitle := strings.TrimSuffix(plan.Files[0].Source, ".mkv") + ".zh.srt"
				contents[subtitle] = []byte("external provider linked subtitle")
				importWrite(t, subtitle, contents[subtitle])
				var err error
				plan, err = a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "softlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: plan.Files[0].Source}}})
				if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
					t.Fatal(plan, err)
				}
			}
			var meta importMetadata
			if err := a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
				t.Fatal(err)
			}
			files := []PlanFile{plan.Files[0]}
			for index, side := range meta.Sidecars[plan.Files[0].ID] {
				files = append(files, originalTransferSideFile(plan.Files[0], side, index))
			}
			if len(files) != len(contents) {
				t.Fatal("main/subtitle plan", files)
			}
			targets, results := map[string]string{}, map[string]any{}
			frozen := map[string]importSnapshot{}
			for _, file := range files {
				targets[file.Source] = file.Target
				frozen[file.Source] = planSnapshot(file)
				results[file.Source] = map[string]any{plugins.ModuleTransferInfoKey: map[string]any{
					"success": true, "transfer_type": "softlink", "need_scrape": false,
					"fileitem":    originalTransferPreviewItem(file.Source, "file", file.Size),
					"target_item": originalTransferPreviewItem(file.Target, "file", file.Size),
					"file_list":   []string{file.Source}, "file_list_new": []string{file.Target}, "file_count": 1, "total_size": file.Size,
				}}
			}
			config["transfer_targets"], config["transfer_results"] = targets, results
			raw, _ := json.Marshal(config)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			for _, event := range []string{"INSERT", "UPDATE"} {
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER softlink_call_proof_` + event + ` BEFORE ` + event + ` ON records WHEN NEW.kind='import_receipt' AND json_extract(NEW.payload,'$.original_module_started')=1 AND (COALESCE(json_extract(NEW.payload,'$.original_softlink.target_absent'),0)!=1 OR COALESCE(json_extract(NEW.payload,'$.softlink.source'),'')!=json_extract(NEW.payload,'$.source')) BEGIN SELECT RAISE(FAIL,'softlink provider called without frozen proof'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if branch == "asset-sql-cold" {
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER softlink_asset_failure BEFORE INSERT ON records WHEN NEW.kind='asset' BEGIN SELECT RAISE(FAIL,'synthetic external softlink asset failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			job := overwriteStart(t, a, plan)
			var existing importSnapshot
			if branch == "existing-link" {
				if err := os.MkdirAll(filepath.Dir(files[0].Target), 0750); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(files[0].Source, files[0].Target); err != nil {
					t.Fatal(err)
				}
				existing, _, _ = snapshotSoftlinkEntry(files[0].Target)
			}
			before := ownedModuleRead(t, object)
			if before.PID <= 0 || before.PID == os.Getpid() {
				t.Fatal("actual separate SDK process", before.PID)
			}
			var err error
			if branch == "existing-link" {
				_, err = a.originalTransferModulePublish(ctx, plan, files[0], meta, ctx.Err, func() (importReceipt, error) {
					t.Fatal("existing target entered native fallback")
					return importReceipt{}, nil
				}, func(importReceipt) error { t.Fatal("existing target reached adoption"); return nil })
				if fileCode(err) != "REVIEW_PLUGIN_SOFTLINK_TARGET" {
					t.Fatal("exact pre-call target guard", err)
				}
			} else {
				err = a.runImport(ctx, job, plan.ID, nil)
			}
			if branch == "success" && err != nil {
				t.Fatal("external real softlink adoption", err)
			}
			if branch != "success" && err == nil {
				t.Fatal("expected physical/SQL error")
			}
			if branch == "counterfeit-copy" && fileCode(err) != "REVIEW_PLUGIN_TRANSFER_METHOD" {
				t.Fatal("copy falsely treated as link", err)
			}
			a.wg.Wait()
			after := ownedModuleRead(t, object)
			if branch == "existing-link" {
				if after.Calls["module_transfer"] != before.Calls["module_transfer"] {
					t.Fatal("pre-existing target reached provider")
				}
				if actual, _, e := snapshotSoftlinkEntry(files[0].Target); e != nil || actual != existing {
					t.Fatal("pre-existing entry changed", e)
				}
			} else if after.Calls["module_transfer"]-before.Calls["module_transfer"] != len(files) {
				t.Fatal("one SDK invocation per actual file", before.Calls, after.Calls)
			}
			stamps := map[string]importSnapshot{}
			if positive {
				for _, file := range files {
					var receipt importReceipt
					if err := a.Store.Get(ctx, "import_receipt", plan.ID+"/"+file.ID, &receipt); err != nil || receipt.State != "published" || receipt.OriginalSoftlink == nil || !receipt.OriginalSoftlink.Adopted || receipt.Softlink == nil || len(receipt.OriginalModuleResult) == 0 {
						t.Fatal("actual publication and sealed result", receipt, err)
					}
					actual, text, err := snapshotSoftlinkEntry(file.Target)
					if err != nil || actual != receipt.StageSnapshot || text != file.Source {
						t.Fatal("real external link", actual, text, err)
					}
					stamps[file.Target] = actual
				}
			} else {
				var count int
				if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='asset' OR kind='managed_file'`).Scan(&count); err != nil || count != 0 {
					t.Fatal("unverified output acquired ownership", count, err)
				}
			}
			if branch == "asset-sql-cold" {
				if _, err := a.Store.DB.Exec(`DROP TRIGGER softlink_asset_failure`); err != nil {
					t.Fatal(err)
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
				if err = a.initializeOriginalPlugins(ctx); err != nil {
					t.Fatal(err)
				}
				object = originalBaseSDKLoaded(t, a, ownedModuleID)
				cold := ownedModuleRead(t, object)
				if cold.PID == before.PID || cold.Calls["module_transfer"] != 0 {
					t.Fatal("cold SDK process", cold)
				}
				if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='queued' WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
				job, err = a.claim(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err = a.runImport(ctx, job, plan.ID, nil); err != nil {
					t.Fatal("cold sealed result recovery", err)
				}
				a.wg.Wait()
				if last := ownedModuleRead(t, object); last.Calls["module_transfer"] != 0 {
					t.Fatal("cold retry replayed provider", last.Calls)
				}
			}
			for source, data := range contents {
				if got, err := os.ReadFile(source); err != nil || !bytes.Equal(got, data) {
					t.Fatal("source bytes changed", source, err)
				}
				if actual, err := snapshotPath(source); err != nil || actual != frozen[source] {
					t.Fatal("source identity changed", source, err)
				}
			}
			if positive {
				for _, file := range files {
					var owned managedFile
					if err := a.Store.Get(ctx, "managed_file", tokenHash(file.Target), &owned); err != nil || owned.Softlink == nil || owned.Softlink.Source != file.Source || owned.Snapshot != stamps[file.Target] {
						t.Fatal("managed proof", owned, err)
					}
					if actual, _, err := snapshotSoftlinkEntry(file.Target); err != nil || actual != stamps[file.Target] {
						t.Fatal("recovery replaced real entry", err)
					}
				}
				var asset Asset
				if err := a.assetAtPath(ctx, files[0].Target, &asset); err != nil || asset.Size != files[0].Size {
					t.Fatal("asset media size", asset, err)
				}
				counts := map[string]int{}
				for _, raw := range after.TransferEvents {
					var event plugins.MutableEvent
					var payload struct {
						Info map[string]any `json:"transferinfo"`
					}
					if json.Unmarshal(raw, &event) != nil || json.Unmarshal(event.Data, &payload) != nil {
						t.Fatal("SDK event", string(raw))
					}
					if event.Type == "transfer.complete" || event.Type == "transfer.subtitle.complete" {
						if payload.Info["transfer_type"] != "softlink" || sourceText(sourceMap(payload.Info["target_item"])["storage"]) != "local" {
							t.Fatal("real target FileItem/method", payload.Info)
						}
						counts[event.Type]++
					}
				}
				if counts["transfer.complete"] != 1 || counts["transfer.subtitle.complete"] != 1 {
					t.Fatal("main/subtitle callbacks", counts)
				}
			}
		})
	}
}

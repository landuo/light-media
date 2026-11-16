package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalTransferModuleActualSDKDirectoryMoveAdoption(t *testing.T) {
	for _, branch := range []string{"success", "cold-recovery", "native-fallback", "wrong-content"} {
		t.Run(branch, func(t *testing.T) {
			mode := "move"
			native := branch == "native-fallback"
			coldRecovery := branch == "cold-recovery" || native
			providerMethod := "module_transfer"
			if native {
				providerMethod = "module_execute"
			}
			if branch == "wrong-content" {
				mode = "move_corrupt"
			}
			a, object, original, config := originalTransferModuleFixture(t, "move", mode)
			ctx := t.Context()
			source := filepath.Join(filepath.Dir(original.Files[0].Source), "Owned.2024.BluRay")
			contents := map[string][]byte{
				filepath.Join("BDMV", "STREAM", "00001.m2ts"): []byte("external directory stream bytes"),
				filepath.Join("BDMV", "index.bdmv"):           []byte("external directory index bytes"),
				"notes.txt":                                   []byte("small non-media member is also moved"),
			}
			for relative, data := range contents {
				importWrite(t, filepath.Join(source, relative), data)
			}
			if err := os.Mkdir(filepath.Join(source, "CERTIFICATE"), 0750); err != nil {
				t.Fatal(err)
			}
			beforeSource, beforeEntries, err := snapshotOriginalDirectory(source)
			if err != nil {
				t.Fatal(err)
			}
			out, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{source}, TargetPath: a.Settings().Roots[0].Path, Mode: "move", MediaType: "movie", TMDBID: 123, Scraping: importFlag(false)})
			if err != nil || out.Submitted != 1 {
				t.Fatal("actual directory move submission", out, err)
			}
			var plan ImportPlan
			var meta importMetadata
			if err = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); err != nil || len(plan.Files) != 1 || planSnapshot(plan.Files[0]) != beforeSource {
				t.Fatal("actual original directory snapshot", plan, err)
			}
			if err = a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
				t.Fatal(err)
			}
			file := plan.Files[0]
			// Exercise the existing atomic returned-target adoption as well as
			// the directory content proof; the original requested name is unused.
			target := filepath.Join(meta.RootPath, "External directory result")
			if native {
				target = file.Target
			}
			var oldPaths, newPaths []string
			for _, entry := range beforeEntries {
				if !entry.Directory {
					oldPaths = append(oldPaths, filepath.Join(source, entry.Path))
					newPaths = append(newPaths, filepath.Join(target, entry.Path))
				}
			}
			config["transfer_target"] = target
			config["result"] = map[string]any{plugins.ModuleTransferInfoKey: map[string]any{
				"success": true, "transfer_type": "move", "need_scrape": false,
				"fileitem":    meta.OriginalTransfer[file.ID].Item,
				"target_item": originalTransferPreviewItem(target, "dir", file.Size),
				"file_list":   oldPaths, "file_list_new": newPaths, "file_count": len(oldPaths), "total_size": file.Size,
			}}
			if native {
				config["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": providerMethod}
				config["result"] = nil
			}
			raw, _ := json.Marshal(config)
			if _, err = a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
				t.Fatal(err)
			}
			if err = a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			for _, statement := range []string{
				`CREATE TRIGGER directory_move_insert_proof BEFORE INSERT ON import_receipt WHEN NEW.record_kind='import_receipt' AND json_extract(NEW.payload,'$.original_module_started')=1 AND json_type(NEW.payload,'$.original_move_directory.entries') IS NULL BEGIN SELECT RAISE(FAIL,'directory move started without original member proof'); END`,
				`CREATE TRIGGER directory_move_update_proof BEFORE UPDATE ON import_receipt WHEN NEW.record_kind='import_receipt' AND json_extract(NEW.payload,'$.original_module_started')=1 AND json_type(NEW.payload,'$.original_move_directory.entries') IS NULL BEGIN SELECT RAISE(FAIL,'directory move started without original member proof'); END`,
			} {
				if _, err = a.Store.DB.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			if coldRecovery {
				if _, err = a.Store.DB.Exec(`CREATE TRIGGER directory_move_asset_failure BEFORE INSERT ON library_asset WHEN NEW.record_kind='asset' BEGIN SELECT RAISE(FAIL,'synthetic directory move asset failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			before := ownedModuleRead(t, object)
			if before.PID <= 0 || before.PID == os.Getpid() {
				t.Fatal("directory provider is not a real independent SDK process", before.PID)
			}
			job := overwriteStart(t, a, plan)
			err = a.runImport(ctx, job, plan.ID, nil)
			if branch == "success" && err != nil || branch != "success" && err == nil {
				t.Fatal("actual directory move outcome", branch, err)
			}
			if branch == "wrong-content" && fileCode(err) != "REVIEW_COPY_CONTENT" {
				t.Fatal("wrong directory bytes were not detected against pre-call proof", err)
			}
			var receipt importReceipt
			if err = a.Store.Get(ctx, "import_receipt", plan.ID+"/"+file.ID, &receipt); err != nil || receipt.OriginalMoveDirectory == nil || len(receipt.OriginalMoveDirectory.Entries) != len(beforeEntries) || receipt.SourceSnapshot != beforeSource || len(receipt.OriginalModuleResult) == 0 {
				t.Fatal("directory operation lost original member proof or sealed result", receipt, err)
			}
			for _, member := range receipt.OriginalMoveDirectory.Entries {
				if !member.Directory {
					digest := sha256.Sum256(contents[member.Path])
					if member.SHA256 != hex.EncodeToString(digest[:]) {
						t.Fatal("member proof was inferred from the post-move target", member)
					}
				}
			}
			residual, entries, err := snapshotOriginalDirectory(source)
			if err != nil || !sameInode(residual, beforeSource) {
				t.Fatal("member-by-member plugin move replaced the original container", err)
			}
			for _, entry := range entries {
				if !entry.Directory {
					t.Fatal("actual plugin move left a source file", entry)
				}
			}
			for _, dir := range []string{"BDMV", filepath.Join("BDMV", "STREAM"), "CERTIFICATE"} {
				if info, err := os.Stat(filepath.Join(source, dir)); err != nil || !info.IsDir() {
					t.Fatal("empty source directory was removed", dir, err)
				}
			}
			a.wg.Wait()
			after := ownedModuleRead(t, object)
			if after.Calls[providerMethod]-before.Calls[providerMethod] != 1 {
				t.Fatal("directory must remain one external transfer operation", before.Calls, after.Calls)
			}
			if branch == "wrong-content" {
				if receipt.State == "published" || receipt.OriginalMoveDirectory.Adopted || receipt.DirectoryMoveState == "completed" {
					t.Fatal("corrupt directory result acquired ownership", receipt)
				}
				if err = a.runImport(ctx, job, plan.ID, nil); fileCode(err) != "REVIEW_COPY_CONTENT" {
					t.Fatal("failed result retry lost its original proof", err)
				}
				a.wg.Wait()
				if last := ownedModuleRead(t, object); last.Calls[providerMethod] != after.Calls[providerMethod] {
					t.Fatal("failed directory proof replayed destructive provider")
				}
				var count int
				if err = a.Store.DB.QueryRow(`SELECT count(*) FROM library_asset WHERE 1=1`).Scan(&count); err != nil || count != 0 {
					t.Fatal("corrupt directory result registered assets", count, err)
				}
				return
			}
			if receipt.State != "published" || receipt.Target != target || receipt.OriginalMoveDirectory.Adopted == native || receipt.DirectoryMoveState != "completed" || receipt.Move != nil || receipt.MoveSourceSnapshot == nil || *receipt.MoveSourceSnapshot != residual || !native && len(receipt.SHA256) != 64 {
				t.Fatal("directory move did not persist terminal external ownership", receipt)
			}
			for relative, data := range contents {
				if got, err := os.ReadFile(filepath.Join(target, relative)); err != nil || !bytes.Equal(got, data) {
					t.Fatal("actual destination content", relative, err)
				}
			}
			var events struct {
				Events []json.RawMessage `json:"transfer_events"`
			}
			if err = object.readSession(ctx, "snapshot", &events); err != nil || len(events.Events) != 1 {
				t.Fatal("directory physical members were split into extra callbacks", events, err)
			}
			var event plugins.MutableEvent
			var payload struct {
				Item *plugins.ModuleFileItem `json:"fileitem"`
				Info map[string]any          `json:"transferinfo"`
			}
			if json.Unmarshal(events.Events[0], &event) != nil || json.Unmarshal(event.Data, &payload) != nil || event.Type != "transfer.complete" || payload.Info["transfer_type"] != "move" || !native && !reflect.DeepEqual(payload.Item, meta.OriginalTransfer[file.ID].Item) {
				t.Fatal("callback did not preserve the original directory FileItem", string(events.Events[0]))
			}
			if native {
				// Native Blu-ray results retain the root but set its source-parity
				// STREAM size; external typed results retain their supplied item.
				var nativeItem struct {
					Path string
					Size int64
				}
				raw, _ := json.Marshal(payload.Item)
				if json.Unmarshal(raw, &nativeItem) != nil || filepath.Clean(nativeItem.Path) != source || nativeItem.Size != int64(len(contents[filepath.Join("BDMV", "STREAM", "00001.m2ts")])) {
					t.Fatal("native fallback lost original root/STREAM size", nativeItem)
				}
			}
			item, err := a.transferHistoryItem(ctx, file.ID)
			if err != nil || item.Status != "success" || item.Source != source || item.ResourceSource != source || item.Target != target || historyParityReadPage(t, a, "").Total != 1 {
				t.Fatal("directory root history identity changed", item, err)
			}
			if stamp, exists, err := a.historySourceForRemoval(ctx, item); err != nil || !exists || stamp != residual {
				t.Fatal("empty residual is not bound for later explicit history deletion", stamp, exists, err)
			}
			if coldRecovery {
				if _, err = a.Store.DB.Exec(`DROP TRIGGER directory_move_asset_failure`); err != nil {
					t.Fatal(err)
				}
				// A later download is unrelated to the completed external move.
				newSource := filepath.Join(source, "BDMV", "STREAM", "00001.m2ts")
				importWrite(t, newSource, []byte("new download after completed directory move"))
				dir := a.Dir
				if err = a.Close(); err != nil {
					t.Fatal(err)
				}
				cold, err := New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = cold.Close() })
				if err = cold.initializeOriginalPlugins(ctx); err != nil {
					t.Fatal(err)
				}
				value, ok := cold.pluginRunningInstances.Load(ownedModuleID)
				if !ok {
					t.Fatal("cold SDK process missing")
				}
				instance := value.(*originalPluginInstance)
				if observed := ownedModuleRead(t, instance); observed.PID == after.PID || observed.Calls[providerMethod] != 0 {
					t.Fatal("cold SDK reused process or called transfer early", observed)
				}
				if _, err = cold.Store.DB.Exec(`UPDATE jobs SET state='queued' WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
				recovered, err := cold.claim(ctx)
				if err != nil || recovered.ID != job.ID || recovered.Generation <= job.Generation {
					t.Fatal("cold actual job claim", recovered, err)
				}
				if err = cold.runImport(ctx, recovered, plan.ID, nil); err != nil {
					t.Fatal("cold directory publication recovery", err)
				}
				cold.wg.Wait()
				if observed := ownedModuleRead(t, instance); observed.Calls[providerMethod] != 0 {
					t.Fatal("cold recovery repeated a destructive directory move", observed.Calls)
				}
				if got, err := os.ReadFile(newSource); err != nil || string(got) != "new download after completed directory move" {
					t.Fatal("cold directory recovery touched a recreated source member", err)
				}
				if err = instance.readSession(ctx, "snapshot", &events); err != nil || len(events.Events) != 0 {
					t.Fatal("cold recovery replayed committed directory callback", events, err)
				}
				var completed ImportPlan
				if err = cold.Store.Get(ctx, "import_plan", plan.ID, &completed); err != nil || completed.State != "complete" || completed.Files[0].Target != target {
					t.Fatal("cold directory result did not register the actual target", completed, err)
				}
			}
		})
	}
}

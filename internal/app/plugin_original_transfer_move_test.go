package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalTransferModuleActualSDKMoveAdoption(t *testing.T) {
	for _, branch := range []string{"success", "asset-sql-retry", "wrong-content", "source-retained"} {
		t.Run(branch, func(t *testing.T) {
			workerMode := "move"
			if branch == "wrong-content" {
				workerMode = "move_corrupt"
			}
			if branch == "source-retained" {
				workerMode = "copy"
			}
			a, object, plan, config := originalTransferModuleFixture(t, "move", workerMode)
			ctx := t.Context()
			input := plan.Files[0].Source
			contents := map[string][]byte{input: []byte("owned transfer content")}
			positive := branch == "success" || branch == "asset-sql-retry"
			if positive {
				subtitle := strings.TrimSuffix(input, ".mkv") + ".zh.srt"
				contents[subtitle] = []byte("external provider moved subtitle")
				importWrite(t, subtitle, contents[subtitle])
				var err error
				plan, err = a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "move", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
				if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
					t.Fatal("actual plugin move with subtitle preview", plan, err)
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
				t.Fatal("missing independent subtitle transfer task", files)
			}
			targets, results := map[string]string{}, map[string]any{}
			for _, file := range files {
				targets[file.Source] = file.Target
				results[file.Source] = map[string]any{plugins.ModuleTransferInfoKey: map[string]any{
					"success": true, "transfer_type": "move", "need_scrape": false,
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
			// The durable "provider started" marker may never precede the
			// destructive-call proof, including the first INSERT for a sidecar.
			for _, statement := range []string{
				`CREATE TRIGGER move_module_insert_proof BEFORE INSERT ON import_receipt WHEN NEW.record_kind='import_receipt' AND json_extract(NEW.payload,'$.original_module_started')=1 AND length(COALESCE(json_extract(NEW.payload,'$.original_move_sha256'),''))!=64 BEGIN SELECT RAISE(FAIL,'move called without frozen content proof'); END`,
				`CREATE TRIGGER move_module_update_proof BEFORE UPDATE ON import_receipt WHEN NEW.record_kind='import_receipt' AND json_extract(NEW.payload,'$.original_module_started')=1 AND length(COALESCE(json_extract(NEW.payload,'$.original_move_sha256'),''))!=64 BEGIN SELECT RAISE(FAIL,'move called without frozen content proof'); END`,
			} {
				if _, err := a.Store.DB.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			if branch == "asset-sql-retry" {
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER move_module_asset_failure BEFORE INSERT ON library_asset WHEN NEW.record_kind='asset' BEGIN SELECT RAISE(FAIL,'synthetic external move asset failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			before := ownedModuleRead(t, object)
			if before.PID <= 0 || before.PID == os.Getpid() {
				t.Fatal("move provider is not a separate SDK process", before.PID)
			}
			job := overwriteStart(t, a, plan)
			err := a.runImport(ctx, job, plan.ID, nil)
			if positive && branch == "success" && err != nil {
				t.Fatal("real external move adoption", err)
			}
			if (!positive || branch == "asset-sql-retry") && err == nil {
				t.Fatal("expected verification or asset commit failure")
			}
			if branch == "wrong-content" && fileCode(err) != "REVIEW_COPY_CONTENT" || branch == "source-retained" && fileCode(err) != "REVIEW_PLUGIN_TRANSFER_METHOD" {
				t.Fatal("wrong physical move outcome", err)
			}
			for _, file := range files {
				var receipt importReceipt
				if err := a.Store.Get(ctx, "import_receipt", plan.ID+"/"+file.ID, &receipt); err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256(contents[file.Source])
				if receipt.OriginalMoveSHA256 != hex.EncodeToString(digest[:]) || !receipt.OriginalModuleStarted || len(receipt.OriginalModuleResult) == 0 {
					t.Fatal("external move lost original content or sealed result", receipt)
				}
				if positive {
					if receipt.State != "published" || receipt.Move == nil || receipt.Move.State != "completed" || receipt.Move.Publication != "plugin" || receipt.Move.Quarantine != "" || receipt.SHA256 != receipt.OriginalMoveSHA256 {
						t.Fatal("true external move was not claimed with terminal proof", receipt)
					}
					if got, err := os.ReadFile(file.Target); err != nil || !bytes.Equal(got, contents[file.Source]) {
						t.Fatal("real SDK move target bytes", file.Target, err)
					}
				} else if receipt.State == "published" || receipt.Move != nil {
					t.Fatal("failed plugin move was falsely published", receipt)
				}
				if branch == "source-retained" {
					if got, err := os.ReadFile(file.Source); err != nil || !bytes.Equal(got, contents[file.Source]) {
						t.Fatal("host deleted a source the plugin retained", err)
					}
				} else if _, err := os.Lstat(file.Source); !os.IsNotExist(err) {
					t.Fatal("SDK provider did not actually move its original source", file.Source, err)
				}
			}
			a.wg.Wait()
			after := ownedModuleRead(t, object)
			if after.Calls["module_transfer"]-before.Calls["module_transfer"] != len(files) {
				t.Fatal("ordinary physical files did not each reach actual SDK provider once", before.Calls, after.Calls)
			}
			if branch == "asset-sql-retry" {
				if _, err := a.Store.DB.Exec(`DROP TRIGGER move_module_asset_failure`); err != nil {
					t.Fatal(err)
				}
			}
			if positive {
				for source := range contents {
					importWrite(t, source, []byte("new download after plugin move"))
				}
			}
			// Re-enter the entire frozen plan. The interrupted asset commit must
			// adopt the stored plugin publication, not repeat destructive calls.
			retryErr := a.runImport(ctx, job, plan.ID, nil)
			if (retryErr == nil) != positive {
				t.Fatal("same-plan external move recovery", retryErr)
			}
			a.wg.Wait()
			last := ownedModuleRead(t, object)
			if last.Calls["module_transfer"] != after.Calls["module_transfer"] {
				t.Fatal("same-plan recovery replayed an external move", after.Calls, last.Calls)
			}
			if positive {
				for source, target := range targets {
					if got, err := os.ReadFile(source); err != nil || string(got) != "new download after plugin move" {
						t.Fatal("published-plugin retry touched recreated source", err)
					}
					if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, contents[source]) {
						t.Fatal("published-plugin retry changed target", err)
					}
				}
				var executed ImportPlan
				if err := a.Store.Get(ctx, "import_plan", plan.ID, &executed); err != nil || executed.State != "complete" {
					t.Fatal("same-plan recovery did not commit actual assets", executed, err)
				}
				var seen struct {
					Events []json.RawMessage `json:"transfer_events"`
				}
				if err := object.readSession(ctx, "snapshot", &seen); err != nil {
					t.Fatal(err)
				}
				counts := map[string]int{}
				for _, raw := range seen.Events {
					var event plugins.MutableEvent
					var payload struct {
						Info map[string]any `json:"transferinfo"`
					}
					if json.Unmarshal(raw, &event) != nil || json.Unmarshal(event.Data, &payload) != nil {
						t.Fatal("actual SDK move callback", string(raw))
					}
					if event.Type == "transfer.complete" || event.Type == "transfer.subtitle.complete" {
						if payload.Info["transfer_type"] != "move" {
							t.Fatal("external move callback lost method", payload.Info)
						}
						counts[event.Type]++
					}
				}
				if counts["transfer.complete"] != 1 || counts["transfer.subtitle.complete"] != 1 {
					t.Fatal("actual SDK main/subtitle callbacks were lost or repeated", counts)
				}
			} else {
				var count int
				if err := a.Store.DB.QueryRow(`SELECT count(*) FROM library_asset WHERE 1=1`).Scan(&count); err != nil || count != 0 {
					t.Fatal("unverified plugin move created asset ownership", count, err)
				}
			}
		})
	}
}

func TestOriginalTransferModuleActualSDKDirectoryCopyCannotClaimMove(t *testing.T) {
	a, object, original, config := originalTransferModuleFixture(t, "move", "copy")
	ctx := t.Context()
	source := filepath.Join(filepath.Dir(original.Files[0].Source), "Owned.2024.BluRay")
	files := map[string][]byte{
		filepath.Join("BDMV", "STREAM", "00001.m2ts"): []byte("directory provider copied stream"),
		filepath.Join("BDMV", "index.bdmv"):           []byte("directory provider copied index"),
	}
	for relative, data := range files {
		importWrite(t, filepath.Join(source, relative), data)
	}
	out, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{source}, TargetPath: a.Settings().Roots[0].Path, Mode: "move", MediaType: "movie", TMDBID: 123, Scraping: importFlag(false)})
	if err != nil || out.Submitted != 1 {
		t.Fatal("actual directory move submission", out, err)
	}
	var plan ImportPlan
	if err = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); err != nil || len(plan.Files) != 1 || plan.Files[0].DirectoryDigest == "" {
		t.Fatal("missing frozen directory move plan", plan, err)
	}
	file := plan.Files[0]
	var oldPaths, newPaths []string
	for relative := range files {
		oldPaths = append(oldPaths, filepath.Join(source, relative))
		newPaths = append(newPaths, filepath.Join(file.Target, relative))
	}
	config["transfer_target"] = file.Target
	config["result"] = map[string]any{plugins.ModuleTransferInfoKey: map[string]any{
		"success": true, "transfer_type": "move", "need_scrape": false,
		"fileitem":    originalTransferPreviewItem(source, "dir", file.Size),
		"target_item": originalTransferPreviewItem(file.Target, "dir", file.Size),
		"file_list":   oldPaths, "file_list_new": newPaths, "file_count": 2, "total_size": file.Size,
	}}
	raw, _ := json.Marshal(config)
	if _, err = a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
		t.Fatal(err)
	}
	if err = a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	before := ownedModuleRead(t, object)
	if before.PID <= 0 || before.PID == os.Getpid() {
		t.Fatal("directory provider is not a separate SDK process", before.PID)
	}
	job := overwriteStart(t, a, plan)
	for attempt := 0; attempt < 2; attempt++ {
		if err = a.runImport(ctx, job, plan.ID, nil); fileCode(err) != "REVIEW_PLUGIN_TRANSFER_METHOD" {
			t.Fatal("copied directory was accepted as a real plugin move", attempt, err)
		}
		var receipt importReceipt
		if err = a.Store.Get(ctx, "import_receipt", plan.ID+"/"+file.ID, &receipt); err != nil || receipt.State == "published" || receipt.DirectoryMoveState == "completed" || len(receipt.OriginalModuleResult) == 0 {
			t.Fatal("unsupported plugin directory move acquired ownership or lost sealed result", receipt, err)
		}
		for relative, data := range files {
			for _, root := range []string{source, file.Target} {
				if got, err := os.ReadFile(filepath.Join(root, relative)); err != nil || !bytes.Equal(got, data) {
					t.Fatal("real copied source/target evidence was changed by host", root, relative, err)
				}
			}
		}
	}
	a.wg.Wait()
	if after := ownedModuleRead(t, object); after.Calls["module_transfer"]-before.Calls["module_transfer"] != 1 {
		t.Fatal("unsupported directory move recovery replayed plugin", before.Calls, after.Calls)
	}
	var assets int
	if err = a.Store.DB.QueryRow(`SELECT count(*) FROM library_asset WHERE 1=1`).Scan(&assets); err != nil || assets != 0 {
		t.Fatal("directory copied-but-declared-move created asset ownership", assets, err)
	}
	item, err := a.transferHistoryItem(ctx, file.ID)
	if err != nil || item.Status == "success" {
		t.Fatal("unsupported plugin directory move reported history success", item, err)
	}
}

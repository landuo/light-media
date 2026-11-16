package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func TestImportMoveDirectoryActualSDKSelectedStorage(t *testing.T) {
	for _, branch := range []string{"success", "cold-recovery", "directory-names-cold", "stage-sql-recovery"} {
		t.Run(branch, func(t *testing.T) {
			a, sourceRoot, library := importTestApp(t)
			ctx := t.Context()
			importFixture(t, a, "movie")
			source := filepath.Join(sourceRoot, "Owned.2024.BluRay")
			stream := filepath.Join("BDMV", "STREAM", "00001.m2ts")
			index := filepath.Join("BDMV", "index.bdmv")
			contents := map[string][]byte{stream: []byte("selected storage directory stream"), index: []byte("selected storage directory index"), "notes.txt": []byte("all actual members include non-media files")}
			for relative, data := range contents {
				importWrite(t, filepath.Join(source, relative), data)
			}
			if err := os.Mkdir(filepath.Join(source, "CERTIFICATE"), 0750); err != nil {
				t.Fatal(err)
			}
			originalRoot, originalEntries, err := snapshotOriginalDirectory(source)
			if err != nil {
				t.Fatal(err)
			}
			stamps := map[string]importSnapshot{}
			for _, entry := range originalEntries {
				if !entry.Directory {
					stamps[entry.Path] = entry.Snapshot
				}
			}
			full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"title":"Owned","original_title":"Owned","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
			if err != nil {
				t.Fatal(err)
			}
			mode := ""
			if branch == "directory-names-cold" {
				mode = "directory-names"
			}
			observer := installOwnedTransferObserver(t, a, nil, nil, map[string]any{"storage": true, "storage_root": filepath.Dir(sourceRoot), "storage_mode": mode, "mediainfo": full})
			out, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{source}, TargetPath: library, Mode: "move", MediaType: "movie", TMDBID: 123, Scraping: importFlag(false)})
			if err != nil || out.Submitted != 1 {
				t.Fatal("real storage directory move submission", out, err)
			}
			var plan ImportPlan
			if err = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); err != nil || len(plan.Files) != 1 || planSnapshot(plan.Files[0]) != originalRoot {
				t.Fatal("directory plan did not bind the original root", plan, err)
			}
			file := plan.Files[0]
			key := plan.ID + "/" + file.ID
			destinations := map[string]string{}
			for relative := range contents {
				destinations[relative] = relative
			}
			if mode == "directory-names" {
				destinations[stream] = filepath.Join("BluRay", "Streams", "feature.m2ts")
				destinations[index] = filepath.Join("BluRay", "index-custom.bdmv")
			}
			pid := func(app *App) int {
				t.Helper()
				r := ownedInputAPI(t, app, "GET", "/api/v1/plugin/"+observer+"/state", "")
				var v struct{ PID int }
				if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &v) != nil || v.PID <= 0 || v.PID == os.Getpid() {
					t.Fatal("directory storage actor is not an independent SDK process", r.Code)
				}
				return v.PID
			}
			firstPID := pid(a)
			job := overwriteStart(t, a, plan)
			cold := branch != "success"
			stageBoundary := branch == "stage-sql-recovery"
			if cold {
				trigger := `CREATE TRIGGER selected_directory_move_cancel AFTER UPDATE ON import_receipt WHEN NEW.record_kind='import_receipt' AND json_array_length(json_extract(NEW.payload,'$.directory_completed'))=1 BEGIN UPDATE jobs SET state='cancelled',generation=generation+1 WHERE kind='import'; END`
				if stageBoundary {
					// Cancel after the actual actor result commits, before the host
					// verifies source absence and durably transitions to staged.
					trigger = `CREATE TRIGGER selected_directory_move_cancel AFTER UPDATE ON import_receipt WHEN NEW.record_kind='import_receipt' AND json_extract(NEW.payload,'$.state')='planned' AND json_extract(NEW.payload,'$.storage_move.result') IS NOT NULL AND json_extract(OLD.payload,'$.storage_move.result') IS NULL BEGIN UPDATE jobs SET state='cancelled',generation=generation+1 WHERE kind='import'; END`
				}
				if _, err = a.Store.DB.Exec(trigger); err != nil {
					t.Fatal(err)
				}
			}
			err = a.runImport(ctx, job, plan.ID, nil)
			var recreated string
			if cold {
				if !originalTransferInterrupted(err) {
					t.Fatal("expected cancellation after first physical directory member move", err)
				}
				wantCompleted := 1
				if stageBoundary {
					wantCompleted = 0
				}
				var prefix importReceipt
				if err = a.Store.Get(ctx, "import_receipt", key, &prefix); err != nil || len(prefix.DirectoryCompleted) != wantCompleted || !prefix.DirectoryListed || len(prefix.DirectoryFiles) != len(contents) {
					t.Fatal("all member names must be durable before first destructive move", prefix, err)
				}
				moved := prefix.DirectoryFiles[0]
				var child importReceipt
				if err = a.Store.Get(ctx, "import_receipt", key+"/directory/"+tokenHash(moved), &child); err != nil {
					t.Fatal(err)
				}
				if stageBoundary {
					if child.State != "planned" || child.Move != nil || child.StageSnapshot.Inode != 0 || child.StorageMove == nil || len(child.StorageMove.Result) == 0 || len(child.SHA256) != 64 {
						t.Fatal("interruption missed sealed-result/before-staged boundary", child)
					}
					var result json.RawMessage
					if err = a.Store.Unseal(child.StorageMove.Result, &result); err != nil || string(result) != "true" {
						t.Fatal("actual storage result not durably sealed", string(result), err)
					}
					stage := filepath.Join(filepath.Dir(child.Target), child.Stage)
					if got, err := os.ReadFile(stage); err != nil || !bytes.Equal(got, contents[moved]) {
						t.Fatal("actual storage stage missing at recovery boundary", err)
					}
					if _, err := os.Lstat(child.Target); !os.IsNotExist(err) {
						t.Fatal("staging interruption occurred after final publication", err)
					}
				} else if child.State != "published" || child.Move == nil || child.Move.State != "completed" || child.Move.Publication != "storage" {
					t.Fatal("interruption did not follow a completed actual member move", child)
				}
				if _, err = os.Lstat(filepath.Join(source, moved)); !os.IsNotExist(err) {
					t.Fatal("storage actor did not really remove prefix source", err)
				}
				for relative, data := range contents {
					if relative != moved {
						if got, err := os.ReadFile(filepath.Join(source, relative)); err != nil || !bytes.Equal(got, data) {
							t.Fatal("cancellation moved more than the completed prefix", relative, err)
						}
					}
				}
				if !stageBoundary {
					recreated = moved
					importWrite(t, filepath.Join(source, recreated), []byte("new source after completed directory member"))
				}
				if _, err = a.Store.DB.Exec(`DROP TRIGGER selected_directory_move_cancel`); err != nil {
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
				t.Cleanup(func() { _ = a.Close() })
				if err = a.initializeOriginalPlugins(ctx); err != nil {
					t.Fatal(err)
				}
				if pid(a) == firstPID {
					t.Fatal("cold recovery did not replace SDK process")
				}
				current, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
				if err != nil {
					t.Fatal(err)
				}
				if _, err = a.retryOriginalScrapeJob(ctx, current); err != nil {
					t.Fatal("real cancelled-directory retry", err)
				}
				if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
				job, err = scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
				if err != nil {
					t.Fatal(err)
				}
				err = a.runImport(ctx, job, plan.ID, nil)
			}
			if err != nil {
				t.Fatal("actual directory storage move/recovery", err)
			}
			var receipt importReceipt
			if err = a.Store.Get(ctx, "import_receipt", key, &receipt); err != nil || receipt.State != "published" || receipt.DirectoryMoveState != "completed" || receipt.Source != source || receipt.SourceSnapshot != originalRoot || len(receipt.DirectoryCompleted) != len(contents) {
				t.Fatal("completed root publication lost original identity", receipt, err)
			}
			// Inspect the durable proof without coupling this SDK test to the
			// private Go type used for the new directory-storage state.
			var proof struct {
				Storage *struct {
					InstanceID string                   `json:"instance_id"`
					Key        string                   `json:"key"`
					Entries    []originalDirectoryEntry `json:"entries"`
				} `json:"directory_storage_move"`
			}
			raw, _ := json.Marshal(receipt)
			if json.Unmarshal(raw, &proof) != nil || proof.Storage == nil || proof.Storage.InstanceID != observer || proof.Storage.Key != "owned-local" || !reflect.DeepEqual(proof.Storage.Entries, originalEntries) {
				t.Fatal("directory storage proof lost pre-move member snapshots", string(raw))
			}
			children := map[string]importReceipt{}
			frozen := map[string]*plugins.ModuleFileItem{}
			for relative, data := range contents {
				var child importReceipt
				if err = a.Store.Get(ctx, "import_receipt", key+"/directory/"+tokenHash(relative), &child); err != nil || child.Move == nil || child.Move.Publication != "storage" || child.Move.State != "completed" || child.StorageMove == nil || child.StorageMove.InstanceID != observer || child.StorageMove.Key != "owned-local" || child.SourceSnapshot != stamps[relative] {
					t.Fatal("directory member did not use its own frozen storage move", relative, child, err)
				}
				digest := sha256.Sum256(data)
				if child.Target != filepath.Join(file.Target, destinations[relative]) || child.SHA256 != hex.EncodeToString(digest[:]) {
					t.Fatal("member target mapping/content proof changed", relative, child)
				}
				children[relative] = child
				var item plugins.ModuleFileItem
				if err = a.Store.Unseal(receipt.DirectoryMembers[relative].FileItem, &item); err != nil {
					t.Fatal("member provider FileItem was not frozen", err)
				}
				frozen[relative] = &item
				if got, err := os.ReadFile(child.Target); err != nil || !bytes.Equal(got, data) {
					t.Fatal("storage mover's actual target bytes", relative, err)
				}
				if relative == recreated {
					if got, err := os.ReadFile(child.Source); err != nil || string(got) != "new source after completed directory member" {
						t.Fatal("cold directory retry touched a recreated completed-member source", err)
					}
				} else if _, err := os.Lstat(child.Source); !os.IsNotExist(err) {
					t.Fatal("directory member source remained after move", relative, err)
				}
			}
			residual, _, err := snapshotOriginalDirectory(source)
			if err != nil || !sameInode(residual, originalRoot) {
				t.Fatal("directory move replaced the original source root", err)
			}
			for _, path := range []string{"BDMV", filepath.Join("BDMV", "STREAM"), "CERTIFICATE"} {
				if info, err := os.Stat(filepath.Join(source, path)); err != nil || !info.IsDir() {
					t.Fatal("source directory container was removed", path, err)
				}
			}
			if recreated != "" && receipt.MoveSourceSnapshot != nil || recreated == "" && (receipt.MoveSourceSnapshot == nil || *receipt.MoveSourceSnapshot != residual) {
				t.Fatal("new source content was mistaken for an owned empty residual", receipt.MoveSourceSnapshot)
			}
			item, err := a.transferHistoryItem(ctx, file.ID)
			if err != nil || item.Status != "success" || item.Mode != "move" || item.Source != source || item.ResourceSource != source || item.Target != file.Target || historyParityReadPage(t, a, "").Total != 1 {
				t.Fatal("physical members were split into histories or lost original root", item, err)
			}
			a.wg.Wait()
			response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+observer+"/observations", "")
			var calls []struct {
				Type string
				Data json.RawMessage
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &calls) != nil {
				t.Fatal("actual directory storage trace", response.Code)
			}
			moves, completions := map[string]int{}, 0
			firstMove, certificateList := -1, -1
			for position, call := range calls {
				if call.Type == "storage.copy" || call.Type == "storage.link" {
					t.Fatal("directory move was disguised as copy/link", call.Type)
				}
				if call.Type == "storage.list" {
					var invocation plugins.StorageOperationCall
					var path string
					if json.Unmarshal(call.Data, &invocation) == nil && invocation.FileItem != nil && json.Unmarshal(invocation.FileItem.Fields["path"], &path) == nil && filepath.Base(filepath.Clean(path)) == "CERTIFICATE" {
						certificateList = position
					}
				}
				if call.Type == "storage.move" {
					if firstMove < 0 {
						firstMove = position
					}
					var invocation plugins.StorageOperationCall
					var path, name string
					if json.Unmarshal(call.Data, &invocation) != nil || invocation.FileItem == nil || json.Unmarshal(invocation.FileItem.Fields["path"], &path) != nil || json.Unmarshal(invocation.FileItem.Fields["name"], &name) != nil {
						t.Fatal("actual member storage move arguments", string(call.Data))
					}
					relative, err := filepath.Rel(source, path)
					child, ok := children[relative]
					if err != nil || !ok || !storageMoveSameFileItem(invocation.FileItem, frozen[relative]) || name != filepath.Base(destinations[relative]) || invocation.Path != filepath.Dir(child.Target) || invocation.NewName == nil || *invocation.NewName != filepath.Base(child.Target) || invocation.PublicationPath != filepath.Join(filepath.Dir(child.Target), child.Stage) || !strings.HasPrefix(filepath.Base(invocation.PublicationPath), ".light-media-") {
						t.Fatal("member provider names/logical target/owned stage were not preserved", string(call.Data))
					}
					moves[relative]++
				}
				if call.Type == "transfer.subtitle.complete" || call.Type == "transfer.audio.complete" {
					t.Fatal("directory members emitted independent auxiliary completion", call.Type)
				}
				if call.Type == "transfer.complete" {
					completions++
					var payload struct {
						Item struct {
							Path, Type string
							Size       int64
						} `json:"fileitem"`
						Info struct {
							Method string `json:"transfer_type"`
							Item   struct {
								Path, Type string
								Size       int64
							} `json:"fileitem"`
							Files   []string `json:"file_list"`
							Targets []string `json:"file_list_new"`
						} `json:"transferinfo"`
					}
					if json.Unmarshal(call.Data, &payload) != nil || filepath.Clean(payload.Item.Path) != source || payload.Item.Type != "dir" || payload.Item.Size != int64(len(contents[stream])) || payload.Info.Method != "move" || payload.Info.Item != payload.Item {
						t.Fatal("directory callback lost original root/STREAM size", string(call.Data))
					}
					var wantSources, wantTargets []string
					for _, relative := range receipt.DirectoryCompleted {
						wantSources = append(wantSources, filepath.Join(source, relative))
						wantTargets = append(wantTargets, filepath.Join(file.Target, destinations[relative]))
					}
					if !reflect.DeepEqual(payload.Info.Files, wantSources) || !reflect.DeepEqual(payload.Info.Targets, wantTargets) {
						t.Fatal("directory result lost actual selected member mapping", payload.Info.Files, payload.Info.Targets)
					}
				}
			}
			if completions != 1 || certificateList < 0 || certificateList >= firstMove {
				t.Fatal("directory list was not frozen before destructive move or completion replayed", completions, certificateList, firstMove)
			}
			for relative := range contents {
				if moves[relative] != 1 {
					t.Fatal("completed member was moved twice or remaining member was skipped", relative, moves)
				}
			}
		})
	}
}

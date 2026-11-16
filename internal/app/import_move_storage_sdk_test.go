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

// TransferInfo's typed float encoding may use exponent notation while the
// top-level FileItem uses decimal notation. Compare the complete JSON values.
func storageMoveSameFileItem(left, right *plugins.ModuleFileItem) bool {
	if left == nil || right == nil {
		return false
	}
	var a, b map[string]any
	x, e1 := json.Marshal(left)
	y, e2 := json.Marshal(right)
	return e1 == nil && e2 == nil && json.Unmarshal(x, &a) == nil && json.Unmarshal(y, &b) == nil && reflect.DeepEqual(a, b)
}

func TestImportMoveActualSDKSelectedStorage(t *testing.T) {
	for _, branch := range []string{"success", "cold-recovery", "corrupt", "source-retained"} {
		t.Run(branch, func(t *testing.T) {
			a, sourceRoot, targetRoot := importTestApp(t)
			ctx := t.Context()
			observer := installOwnedTransferObserver(t, a, nil, nil, map[string]any{"storage": true, "storage_root": filepath.Dir(sourceRoot), "storage_mode": branch})
			nativeMoveFixture(t, a)
			video := filepath.Join(sourceRoot, "Moving.2024.mkv")
			contents := map[string][]byte{video: []byte("actual selected storage move video")}
			positive := branch == "success" || branch == "cold-recovery"
			if positive {
				contents[filepath.Join(sourceRoot, "Moving.2024.zh.srt")] = []byte("actual selected storage move subtitle")
			}
			for path, data := range contents {
				importWrite(t, path, data)
			}
			plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "move", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: video}}})
			if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
				t.Fatal("selected storage move preview", plan, err)
			}
			var meta importMetadata
			if err = a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
				t.Fatal(err)
			}
			files := []PlanFile{plan.Files[0]}
			frozen := map[string]*plugins.ModuleFileItem{video: meta.OriginalTransfer[plan.Files[0].ID].Item}
			for index, side := range meta.Sidecars[plan.Files[0].ID] {
				files = append(files, originalTransferSideFile(plan.Files[0], side, index))
				if side.Original == nil {
					t.Fatal("subtitle source FileItem missing")
				}
				frozen[side.Source] = side.Original.Item
			}
			if len(files) != len(contents) {
				t.Fatal("ordinary video/subtitle task binding", files)
			}
			pid := func(app *App) int {
				t.Helper()
				response := ownedInputAPI(t, app, "GET", "/api/v1/plugin/"+observer+"/state", "")
				var value struct{ PID int }
				if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &value) != nil || value.PID <= 0 || value.PID == os.Getpid() {
					t.Fatal("storage actor is not a real SDK process", response.Code, response.Body.String())
				}
				return value.PID
			}
			beforePID := pid(a)
			if branch == "cold-recovery" {
				if _, err = a.Store.DB.Exec(`CREATE TRIGGER selected_storage_move_asset_failure BEFORE INSERT ON library_asset WHEN NEW.record_kind='asset' BEGIN SELECT RAISE(FAIL,'selected storage move asset failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			job := overwriteStart(t, a, plan)
			err = a.runImport(ctx, job, plan.ID, nil)
			if branch == "success" && err != nil || branch != "success" && err == nil {
				t.Fatal("actual selected storage move result", branch, err)
			}
			if branch == "corrupt" && fileCode(err) != "REVIEW_COPY_CONTENT" || branch == "source-retained" && fileCode(err) != "REVIEW_PLUGIN_TRANSFER_METHOD" {
				t.Fatal("physical storage move verification", err)
			}
			targets, stages := map[string]string{}, map[string]string{}
			for _, file := range files {
				targets[file.Source] = file.Target
				var receipt importReceipt
				if err = a.Store.Get(ctx, "import_receipt", plan.ID+"/"+file.ID, &receipt); err != nil || receipt.StorageMove == nil {
					t.Fatal("selected storage operation lost durable call proof", receipt, err)
				}
				digest := sha256.Sum256(contents[file.Source])
				if receipt.SHA256 != hex.EncodeToString(digest[:]) || receipt.SourceSnapshot != planSnapshot(file) || receipt.StorageMove.InstanceID != observer || receipt.StorageMove.Key != "owned-local" || len(receipt.StorageMove.Result) == 0 {
					t.Fatal("storage call identity/source proof changed", receipt)
				}
				var returned json.RawMessage
				if err = a.Store.Unseal(receipt.StorageMove.Result, &returned); err != nil || string(returned) != "true" {
					t.Fatal("storage result was not sealed as actually returned", string(returned), err)
				}
				stages[file.Source] = filepath.Join(filepath.Dir(file.Target), receipt.Stage)
				if positive {
					if receipt.State != "published" || receipt.Move == nil || receipt.Move.Publication != "storage" || receipt.Move.State != "completed" || receipt.Move.Quarantine != "" {
						t.Fatal("storage move was not finalized from its verified stage", receipt)
					}
					if got, err := os.ReadFile(file.Target); err != nil || !bytes.Equal(got, contents[file.Source]) {
						t.Fatal("storage actor's real bytes did not reach final target", err)
					}
					if _, err := os.Lstat(stages[file.Source]); !os.IsNotExist(err) {
						t.Fatal("verified storage stage was not exclusively consumed", err)
					}
				} else {
					if receipt.State == "published" || receipt.Move != nil {
						t.Fatal("unverified storage effect was marked complete", receipt)
					}
					if _, err := os.Lstat(file.Target); !os.IsNotExist(err) {
						t.Fatal("unverified stage became a public target", err)
					}
					if _, err := os.Lstat(stages[file.Source]); err != nil {
						t.Fatal("failed provider's actual stage evidence was discarded", err)
					}
				}
				if branch == "source-retained" {
					if got, err := os.ReadFile(file.Source); err != nil || !bytes.Equal(got, contents[file.Source]) {
						t.Fatal("host removed a source its storage provider retained", err)
					}
				} else if _, err := os.Lstat(file.Source); !os.IsNotExist(err) {
					t.Fatal("SDK storage actor did not actually remove source", file.Source, err)
				}
			}
			observe := func(app *App) {
				t.Helper()
				app.wg.Wait()
				response := ownedInputAPI(t, app, "GET", "/api/v1/plugin/"+observer+"/observations", "")
				var calls []struct {
					Type string
					Data json.RawMessage
				}
				if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &calls) != nil {
					t.Fatal("actual storage observation read", response.Code)
				}
				moves, completions := map[string]int{}, map[string]int{}
				for _, call := range calls {
					if call.Type == "storage.copy" || call.Type == "storage.link" {
						t.Fatal("selected storage move was disguised as another operation", call.Type)
					}
					if call.Type == "storage.move" {
						var invocation plugins.StorageOperationCall
						if json.Unmarshal(call.Data, &invocation) != nil || invocation.FileItem == nil {
							t.Fatal("invalid actual storage move arguments", string(call.Data))
						}
						var source string
						if json.Unmarshal(invocation.FileItem.Fields["path"], &source) != nil || frozen[source] == nil || !storageMoveSameFileItem(invocation.FileItem, frozen[source]) || invocation.Path != filepath.Dir(targets[source]) || invocation.NewName == nil || *invocation.NewName != filepath.Base(targets[source]) || invocation.PublicationPath != stages[source] || !within(targetRoot, invocation.PublicationPath) || !strings.HasPrefix(filepath.Base(invocation.PublicationPath), ".light-media-") {
							t.Fatal("storage actor did not receive frozen source/logical destination/owned stage", string(call.Data))
						}
						moves[source]++
					}
					if call.Type == "transfer.complete" || call.Type == "transfer.subtitle.complete" {
						var payload struct {
							Item *plugins.ModuleFileItem `json:"fileitem"`
							Info struct {
								Method string                  `json:"transfer_type"`
								Item   *plugins.ModuleFileItem `json:"fileitem"`
							} `json:"transferinfo"`
						}
						if json.Unmarshal(call.Data, &payload) != nil || payload.Item == nil {
							t.Fatal("invalid storage move completion", string(call.Data))
						}
						var source string
						if json.Unmarshal(payload.Item.Fields["path"], &source) != nil || payload.Info.Method != "move" || !storageMoveSameFileItem(payload.Item, frozen[source]) || !storageMoveSameFileItem(payload.Info.Item, frozen[source]) {
							want, _ := json.Marshal(frozen[source])
							got, _ := json.Marshal(payload.Item)
							info, _ := json.Marshal(payload.Info.Item)
							t.Fatalf("storage callback method=%q expected=%s top=%s info=%s", payload.Info.Method, want, got, info)
						}
						completions[call.Type]++
					}
				}
				for source := range contents {
					if moves[source] != 1 {
						t.Fatal("actual storage move was missing or replayed", source, moves)
					}
				}
				if positive && (completions["transfer.complete"] != 1 || completions["transfer.subtitle.complete"] != 1) || !positive && len(completions) != 0 {
					t.Fatal("actual storage move completion count", completions)
				}
			}
			observe(a)
			if !positive {
				if err := a.runImport(ctx, job, plan.ID, nil); err == nil {
					t.Fatal("unverified storage stage succeeded on retry")
				}
				observe(a)
				var assets int
				if err = a.Store.DB.QueryRow(`SELECT count(*) FROM library_asset WHERE 1=1`).Scan(&assets); err != nil || assets != 0 {
					t.Fatal("unverified storage move acquired asset ownership", assets, err)
				}
				return
			}
			for source := range contents {
				importWrite(t, source, []byte("new source after completed storage move"))
			}
			current := a
			if branch == "cold-recovery" {
				if _, err = a.Store.DB.Exec(`DROP TRIGGER selected_storage_move_asset_failure`); err != nil {
					t.Fatal(err)
				}
				dir := a.Dir
				if err = a.Close(); err != nil {
					t.Fatal(err)
				}
				current, err = New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = current.Close() })
				if err = current.initializeOriginalPlugins(ctx); err != nil {
					t.Fatal(err)
				}
				if pid(current) == beforePID {
					t.Fatal("cold recovery retained the old SDK process")
				}
				if _, err = current.Store.DB.Exec(`UPDATE jobs SET state='queued' WHERE id=?`, job.ID); err != nil {
					t.Fatal(err)
				}
				previous := job
				job, err = current.claim(ctx)
				if err != nil || job.ID != previous.ID || job.Generation <= previous.Generation {
					t.Fatal("cold actual job claim", job, err)
				}
			}
			if err = current.runImport(ctx, job, plan.ID, nil); err != nil {
				t.Fatal("same-plan storage publication recovery", err)
			}
			observe(current)
			for source, target := range targets {
				if got, err := os.ReadFile(source); err != nil || string(got) != "new source after completed storage move" {
					t.Fatal("storage retry read or removed a recreated source", err)
				}
				if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, contents[source]) {
					t.Fatal("storage retry changed already published content", err)
				}
			}
			var completed ImportPlan
			if err = current.Store.Get(ctx, "import_plan", plan.ID, &completed); err != nil || completed.State != "complete" {
				t.Fatal("storage move result did not finish asset registration", completed, err)
			}
		})
	}
}

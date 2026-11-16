package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestImportMoveActualSDKCallbacksAndPlanRecovery(t *testing.T) {
	for _, branch := range []string{"success", "asset-sql-retry"} {
		t.Run(branch, func(t *testing.T) {
			a, source, _ := importTestApp(t)
			observer := installOwnedTransferObserver(t, a, nil, nil)
			nativeMoveFixture(t, a)
			ctx := t.Context()
			video, subtitle := filepath.Join(source, "Moving.2024.mkv"), filepath.Join(source, "Moving.2024.zh.srt")
			contents := map[string][]byte{video: []byte("actual SDK native move video"), subtitle: []byte("actual SDK native move subtitle")}
			for path, data := range contents {
				importWrite(t, path, data)
			}
			plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "move", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: video}}})
			if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
				t.Fatalf("actual SDK move preview: %+v %v", plan, err)
			}
			var meta importMetadata
			if err = a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil || len(meta.Sidecars[plan.Files[0].ID]) != 1 {
				t.Fatalf("move sidecar preparation: %+v %v", meta, err)
			}
			side := meta.Sidecars[plan.Files[0].ID][0]
			if side.Source != subtitle || side.Original == nil {
				t.Fatal("subtitle did not inherit source projection", side)
			}
			expectedItems := map[string]map[string]any{}
			for path, projection := range map[string]originalTransferFileContext{video: meta.OriginalTransfer[plan.Files[0].ID], subtitle: *side.Original} {
				if projection.Item == nil {
					t.Fatal("move source FileItem was not frozen before source deletion", path)
				}
				raw, err := json.Marshal(projection.Item)
				var fields map[string]any
				if err != nil || json.Unmarshal(raw, &fields) != nil || fields["path"] != path {
					t.Fatal("invalid frozen source FileItem", path, err)
				}
				expectedItems[path] = fields
			}
			if branch == "asset-sql-retry" {
				if _, err = a.Store.DB.Exec(`CREATE TRIGGER move_sdk_asset_failure BEFORE INSERT ON library_asset WHEN NEW.record_kind='asset' BEGIN SELECT RAISE(FAIL,'synthetic move SDK asset commit failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			job := overwriteStart(t, a, plan)
			err = a.runImport(ctx, job, plan.ID, nil)
			if branch == "asset-sql-retry" {
				if err == nil {
					t.Fatal("actual asset commit boundary was not reached")
				}
				for path := range contents {
					if _, e := os.Lstat(path); !os.IsNotExist(e) {
						t.Fatal("failed commit did not follow actual physical move", path, e)
					}
				}
				if _, err = a.Store.DB.Exec(`DROP TRIGGER move_sdk_asset_failure`); err != nil {
					t.Fatal(err)
				}
				// Resume the same durable execution, not a new preview of vanished
				// sources or manufactured successful history.
				err = a.runImport(ctx, job, plan.ID, nil)
			}
			if err != nil {
				t.Fatal("actual move execution/recovery", err)
			}
			if err = a.Store.Get(ctx, "import_plan", plan.ID, &plan); err != nil || plan.State != "complete" || plan.Files[0].Status != "imported" {
				t.Fatalf("committed move plan: %+v %v", plan, err)
			}
			if err = a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
				t.Fatal(err)
			}
			side = meta.Sidecars[plan.Files[0].ID][0]
			targets := map[string]string{video: plan.Files[0].Target, subtitle: side.Target}
			published := map[string]importSnapshot{}
			for path, target := range targets {
				if _, err = os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("move source remained", path, err)
				}
				got, e := os.ReadFile(target)
				if e != nil || !bytes.Equal(got, contents[path]) {
					t.Fatal("actual move target bytes", target, e)
				}
				published[target], err = snapshotPath(target)
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, key := range []string{plan.ID + "/" + plan.Files[0].ID, plan.ID + "/" + plan.Files[0].ID + "/side-0"} {
				var receipt importReceipt
				if err = a.Store.Get(ctx, "import_receipt", key, &receipt); err != nil || receipt.State != "published" || receipt.Move == nil || receipt.Move.State != "completed" || receipt.Move.Publication != "hardlink" || receipt.SHA256 != "" || receipt.StageSnapshot != receipt.SourceSnapshot {
					t.Fatalf("physical move receipt %s: %+v %v", key, receipt, err)
				}
			}
			checkCallbacks := func() int {
				t.Helper()
				a.wg.Wait()
				response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+observer+"/observations", "")
				var calls []struct {
					Type string
					Data json.RawMessage
				}
				if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &calls) != nil {
					t.Fatal("actual SDK observation response", response.Code)
				}
				complete := map[string]int{}
				for _, call := range calls {
					if call.Type != "transfer.complete" && call.Type != "transfer.subtitle.complete" {
						continue
					}
					var result struct {
						Item    map[string]any `json:"fileitem"`
						History string         `json:"transfer_history_id"`
						Info    struct {
							Success bool
							Method  string         `json:"transfer_type"`
							Item    map[string]any `json:"fileitem"`
							Files   []string       `json:"file_list"`
							Targets []string       `json:"file_list_new"`
						} `json:"transferinfo"`
					}
					if err = json.Unmarshal(call.Data, &result); err != nil {
						t.Fatal(err)
					}
					path, _ := result.Item["path"].(string)
					if !result.Info.Success || result.Info.Method != "move" || result.History == "" || expectedItems[path] == nil || !reflect.DeepEqual(result.Item, expectedItems[path]) || !reflect.DeepEqual(result.Info.Item, expectedItems[path]) || !reflect.DeepEqual(result.Info.Files, []string{path}) || !reflect.DeepEqual(result.Info.Targets, []string{targets[path]}) {
						t.Fatalf("SDK completion lost real move or frozen source identity: %s %s", call.Type, call.Data)
					}
					complete[call.Type]++
				}
				if complete["transfer.complete"] != 1 || complete["transfer.subtitle.complete"] != 1 {
					t.Fatalf("actual SDK success callbacks: %v", complete)
				}
				return len(calls)
			}
			before := checkCallbacks()
			recreated := []byte("new download after the completed move")
			for path := range contents {
				importWrite(t, path, recreated)
			}
			if err = a.runImport(ctx, job, plan.ID, nil); err != nil {
				t.Fatal("same completed plan retry", err)
			}
			if after := checkCallbacks(); after != before {
				t.Fatalf("completed-plan retry replayed SDK events: %d -> %d", before, after)
			}
			for path := range contents {
				if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, recreated) {
					t.Fatal("completed-plan retry touched recreated source", path, err)
				}
			}
			for target, stamp := range published {
				if current, err := snapshotPath(target); err != nil || current != stamp {
					t.Fatal("same-plan recovery republished target", target, current, err)
				}
			}
		})
	}
}

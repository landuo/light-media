package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOriginalTransferAuxiliaryActualSDKAndMaterialResults(t *testing.T) {
	for _, kind := range []string{"subtitle", "audio"} {
		for _, fail := range []bool{false, true} {
			name := kind + "-success"
			if fail {
				name = kind + "-failed"
			}
			t.Run(name, func(t *testing.T) {
				a, source, target := importTestApp(t)
				identity := importFixture(t, a, "movie")
				input := filepath.Join(source, "Main.2024.mkv")
				aux := filepath.Join(source, "Main.2024.zh.srt")
				if kind == "audio" {
					aux = filepath.Join(source, "Main.2024.flac")
				}
				importWrite(t, input, []byte("owned video"))
				importWrite(t, aux, []byte("owned auxiliary material"))
				full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"title":"Owned","original_title":"Owned","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
				if err != nil {
					t.Fatal(err)
				}
				mode := ""
				if fail {
					mode = "fail-aux"
				}
				id := installOwnedTransferObserver(t, a, map[string]map[string]any{"transfer.rename": {"updated": true, "updated_str": "Aux/Changed.mkv"}}, nil, map[string]any{"rename_source_extension": true, "storage": true, "storage_root": filepath.Dir(source), "storage_mode": mode, "mediainfo": full})
				plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: identity.MediaType, TMDBID: identity.ID, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
				if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
					t.Fatal("real auxiliary preview", plan, err)
				}
				var meta importMetadata
				if err := a.Store.Get(context.Background(), "import_meta", plan.ID, &meta); err != nil || len(meta.Sidecars[plan.Files[0].ID]) != 1 {
					t.Fatal("real classified auxiliary", err)
				}
				material := meta.Sidecars[plan.Files[0].ID][0]
				job := overwriteStart(t, a, plan)
				err = a.runImport(context.Background(), job, plan.ID, nil)
				var executed ImportPlan
				if e := a.Store.Get(context.Background(), "import_plan", plan.ID, &executed); e != nil || executed.Files[0].Target != filepath.Join(target, "Aux", "Changed.mkv") {
					t.Fatal("actual source native rename target", executed, e)
				}
				plan = executed
				if e := a.Store.Get(context.Background(), "import_meta", plan.ID, &meta); e != nil {
					t.Fatal(e)
				}
				material = meta.Sidecars[plan.Files[0].ID][0]
				wantAux := filepath.Join(target, "Aux", "Changed.default.chi.zh-cn.srt")
				if kind == "audio" {
					wantAux = filepath.Join(target, "Aux", "Changed.flac")
				}
				if material.Target != wantAux {
					t.Fatal("actual deferred auxiliary name", material.Target, wantAux)
				}
				a.wg.Wait()
				if (err != nil) != fail {
					t.Fatal("actual material result", err)
				}
				if bytes, err := os.ReadFile(input); err != nil || string(bytes) != "owned video" {
					t.Fatal("source video retained")
				}
				if bytes, err := os.ReadFile(aux); err != nil || string(bytes) != "owned auxiliary material" {
					t.Fatal("source auxiliary retained")
				}
				bytes, readErr := os.ReadFile(material.Target)
				if !fail && (readErr != nil || string(bytes) != "owned auxiliary material") || fail && !os.IsNotExist(readErr) {
					t.Fatal("actual auxiliary publication", readErr)
				}
				if _, err := os.Stat(plan.Files[0].Target); err != nil {
					t.Fatal("prior published main receipt must remain available", err)
				}
				response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+id+"/observations", "")
				var rows []struct {
					Type string
					Data json.RawMessage
				}
				if json.Unmarshal(response.Body.Bytes(), &rows) != nil {
					t.Fatal("actual auxiliary SDK events")
				}
				counts := map[string]int{}
				suffix := "complete"
				if fail {
					suffix = "failed"
				}
				for _, row := range rows {
					counts[row.Type]++
					if row.Type == "transfer."+kind+"."+suffix {
						var data struct {
							FileItem struct{ Path string }
							History  any                    `json:"transfer_history_id"`
							Result   struct{ Success bool } `json:"transferinfo"`
						}
						if json.Unmarshal(row.Data, &data) != nil || data.FileItem.Path != aux || data.Result.Success == fail || data.History == nil {
							t.Fatal("actual auxiliary event identity/results", string(row.Data))
						}
						historyID, ok := data.History.(string)
						history, e := a.transferHistoryItem(context.Background(), historyID)
						wantStatus := "success"
						if fail {
							wantStatus = "failed"
						}
						if !ok || e != nil || history.Source != aux || history.Status != wantStatus {
							t.Fatal("independent committed auxiliary history", history, e)
						}
					}
				}
				if counts["transfer.rename.build"] != 2 || counts["transfer.rename"] != 2 || counts["storage.operation"] != 4 || counts["transfer.intercept"] != 2 || counts["transfer."+kind+"."+suffix] != 1 {
					t.Fatal("each material needs its own ordinary callbacks", counts)
				}
				if !within(target, material.Target) {
					t.Fatal("auxiliary target authority")
				}
				mainHistory, e := a.transferHistoryItem(context.Background(), plan.Files[0].ID)
				if e != nil || mainHistory.Status != "success" || counts["transfer.complete"] != 1 || counts["transfer.failed"] != 0 {
					t.Fatal("auxiliary failure must preserve prior main callback", mainHistory, counts, e)
				}
			})
		}
	}
}

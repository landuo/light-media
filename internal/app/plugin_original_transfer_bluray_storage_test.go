package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOriginalTransferBluraySelectedStorage(t *testing.T) {
	for _, mode := range []string{"", "late-list-error", "directory-names", "directory-names-cold"} {
		t.Run(mode, func(t *testing.T) {
			a, sourceRoot, target := importTestApp(t)
			importFixture(t, a, "movie")
			path := filepath.Join(sourceRoot, "Owned.2024.BluRay")
			importWrite(t, filepath.Join(path, "BDMV", "STREAM", "00001.m2ts"), []byte("owned stream"))
			importWrite(t, filepath.Join(path, "BDMV", "index.bdmv"), []byte("owned index"))
			if err := os.Mkdir(filepath.Join(path, "CERTIFICATE"), 0750); err != nil {
				t.Fatal(err)
			}
			full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"title":"Owned","original_title":"Owned","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
			if err != nil {
				t.Fatal(err)
			}
			storageMode := mode
			if strings.HasPrefix(mode, "directory-names") {
				storageMode = "directory-names"
			}
			id := installOwnedTransferObserver(t, a, nil, nil, map[string]any{"storage": true, "storage_root": filepath.Dir(sourceRoot), "storage_mode": storageMode, "mediainfo": full})
			ctx := context.Background()
			out, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{path}, TargetPath: target, Mode: "hardlink", MediaType: "movie", TMDBID: 123, Scraping: importFlag(false)})
			if err != nil || out.Submitted != 1 {
				t.Fatal(out, err)
			}
			var plan ImportPlan
			if err := a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); err != nil {
				t.Fatal(err)
			}
			job := overwriteStart(t, a, plan)
			if mode == "directory-names-cold" {
				if _, e := a.Store.DB.Exec(`CREATE TRIGGER owned_named_directory_cancel AFTER UPDATE ON records WHEN NEW.kind='import_receipt' AND json_array_length(json_extract(NEW.payload,'$.directory_completed'))=1 BEGIN UPDATE jobs SET state='cancelled',generation=generation+1 WHERE kind='import'; END`); e != nil {
					t.Fatal(e)
				}
			}
			err = a.runImport(ctx, job, plan.ID, nil)
			if mode == "directory-names-cold" {
				if !originalTransferInterrupted(err) {
					t.Fatal("actual cancellation after first named member", err)
				}
				if _, e := a.Store.DB.Exec(`DROP TRIGGER owned_named_directory_cancel`); e != nil {
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
				if err = a.initializeOriginalPlugins(ctx); err != nil {
					t.Fatal(err)
				}
				current, e := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
				if e != nil {
					t.Fatal(e)
				}
				if _, e = a.retryOriginalScrapeJob(ctx, current); e != nil {
					t.Fatal(e)
				}
				if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID); e != nil {
					t.Fatal(e)
				}
				job, e = scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
				if e != nil {
					t.Fatal(e)
				}
				err = a.runImport(ctx, job, plan.ID, nil)
			}
			if (err == nil) != (mode != "late-list-error") {
				t.Fatal("selected directory storage", mode, err)
			}
			if mode == "late-list-error" {
				var failure *APIError
				if !errors.As(err, &failure) || failure.Code != "PLUGIN_TRANSFER_FAILED" {
					t.Fatal("caught provider exception must end the task without automatic retry", err)
				}
			}
			for _, name := range []string{"BDMV/STREAM/00001.m2ts", "BDMV/index.bdmv"} {
				s, e := os.Stat(filepath.Join(path, name))
				dest := name
				if strings.HasPrefix(mode, "directory-names") {
					dest = map[string]string{"BDMV/STREAM/00001.m2ts": "BluRay/Streams/feature.m2ts", "BDMV/index.bdmv": "BluRay/index-custom.bdmv"}[name]
				}
				d, e2 := os.Stat(filepath.Join(plan.Files[0].Target, dest))
				if e != nil || e2 != nil || !os.SameFile(s, d) {
					t.Fatal("source retains published prefix before later listing failure", name, e, e2, err)
				}
			}
			a.wg.Wait()
			response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+id+"/observations", "")
			var calls []struct {
				Type string
				Data json.RawMessage
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &calls) != nil {
				t.Fatal("SDK trace")
			}
			lastLink, certificateList := -1, -1
			failedDTO := false
			for i, call := range calls {
				if mode == "late-list-error" && call.Type == "transfer.failed" {
					var event struct {
						FileItem     map[string]any
						TransferInfo map[string]any
					}
					if json.Unmarshal(call.Data, &event) != nil {
						t.Fatal("failed event DTO")
					}
					if event.FileItem["size"] != float64(12) {
						t.Fatal("task FileItem retains changed STREAM size", event)
					}
					raw, e := os.ReadFile("../../testdata/standard-module-host/transfer-bluray-source-oracle.json")
					var oracle struct {
						Cases []struct {
							Mode   string
							Result map[string]any
						}
					}
					if e != nil || json.Unmarshal(raw, &oracle) != nil {
						t.Fatal(e)
					}
					for _, sample := range oracle.Cases {
						if sample.Mode == "late_list_failure" {
							for key, want := range sample.Result {
								if key == "message" {
									continue
								}
								if !originalBaseJSONEqual(mustCleanSeedJSON(event.TransferInfo[key]), mustCleanSeedJSON(want)) {
									t.Fatal("fresh exception TransferInfo", key, event.TransferInfo[key], want)
								}
							}
							failedDTO = true
						}
					}
				}
				if call.Type == "storage.link" {
					lastLink = i
					if strings.HasPrefix(mode, "directory-names") {
						var v struct{ FileItem struct{ Name, Path string } }
						if json.Unmarshal(call.Data, &v) != nil || v.FileItem.Name == filepath.Base(v.FileItem.Path) {
							t.Fatal("provider FileItem must reach storage publication", string(call.Data))
						}
					}
				}
				if call.Type == "storage.list" {
					var v struct{ FileItem struct{ Path string } }
					if json.Unmarshal(call.Data, &v) == nil && filepath.Base(filepath.Clean(v.FileItem.Path)) == "CERTIFICATE" {
						certificateList = i
					}
				}
			}
			if lastLink < 0 || certificateList <= lastLink {
				t.Fatal("fixed recursion publishes before listing next directory", lastLink, certificateList)
			}
			if mode == "late-list-error" && !failedDTO {
				t.Fatal("missing actual ordinary failure event")
			}
		})
	}
}

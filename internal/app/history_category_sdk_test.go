package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestOriginalHistoryFrozenCategoryActualSDKContexts(t *testing.T) {
	for _, late := range []bool{false, true} {
		name := "prepared-context"
		if late {
			name = "runtime-context"
		}
		t.Run(name, func(t *testing.T) {
			a, source, _ := importTestApp(t)
			identity := importFixture(t, a, "movie")
			path := filepath.Join(source, "Main.2024.mkv")
			importWrite(t, path, []byte("synthetic category video"))
			if !late {
				importWrite(t, filepath.Join(source, "Main.2024.zh.srt"), []byte("synthetic subtitle"))
			}
			full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"title":"Owned","original_title":"Owned","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
			if err != nil {
				t.Fatal(err)
			}
			install := func() string { return installOwnedTransferObserver(t, a, nil, nil, map[string]any{"mediainfo": full}) }
			observer := ""
			if !late {
				observer = install()
			}
			const frozen = "下载时冻结分类"
			plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: identity.MediaType, TMDBID: identity.ID, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), LibraryTypeFolder: importFlag(false), originalDownloadCategory: frozen, Files: []ImportFileRequest{{Path: path}}})
			if err != nil || plan.Category != frozen || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
				t.Fatalf("preview=%+v %v", plan, err)
			}
			if late {
				observer = install()
			}
			job := overwriteStart(t, a, plan)
			if err = a.runImport(context.Background(), job, plan.ID, nil); err != nil {
				t.Fatal(err)
			}
			a.wg.Wait()
			// Exercise the intercept's alternate-source branch without performing a
			// second publication. It must retain the frozen MediaInfo, not re-recognize.
			alternate := filepath.Join(source, "Alternate.2024.flac")
			importWrite(t, alternate, []byte("synthetic alternate material"))
			projection, err := a.originalTransferContext(context.Background(), plan.Media, plan.Files[0])
			if err != nil {
				t.Fatal(err)
			}
			if err = projection.setCategory(frozen); err != nil {
				t.Fatal(err)
			}
			ctx := context.WithValue(context.Background(), originalTransferWriteContextKey{}, &originalTransferWriteContext{File: plan.Files[0], Source: projection, Media: plan.Media})
			if err = a.originalTransferInterceptWrite(ctx, alternate, filepath.Join(a.config.Roots[0].Path, "alternate.flac"), "hardlink", false); err != nil {
				t.Fatal(err)
			}
			response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+observer+"/observations", "")
			var rows []struct {
				Type string
				Data json.RawMessage
			}
			if err = json.Unmarshal(response.Body.Bytes(), &rows); err != nil {
				t.Fatal(err)
			}
			counts := map[string]int{}
			for _, row := range rows {
				var data struct {
					Media  map[string]any `json:"mediainfo"`
					Rename map[string]any `json:"rename_dict"`
				}
				if err = json.Unmarshal(row.Data, &data); err != nil {
					t.Fatal(err)
				}
				switch row.Type {
				case "transfer.intercept", "transfer.complete", "transfer.subtitle.complete":
					counts[row.Type]++
					if data.Media["category"] != frozen {
						t.Fatalf("%s lost frozen classification: %s", row.Type, row.Data)
					}
				case "transfer.rename.build":
					counts[row.Type]++
					if data.Rename["category"] != frozen {
						t.Fatalf("naming lost frozen classification: %s", row.Data)
					}
				}
			}
			if counts["transfer.intercept"] < 2 || counts["transfer.complete"] != 1 || !late && (counts["transfer.rename.build"] < 1 || counts["transfer.subtitle.complete"] != 1) {
				t.Fatalf("missing actual SDK observations: %+v", counts)
			}
			history, err := a.transferHistoryItem(context.Background(), plan.Files[0].ID)
			if err != nil || history.Status != "success" || history.Category != frozen {
				t.Fatalf("history differs from callbacks: %+v %v", history, err)
			}
		})
	}
}

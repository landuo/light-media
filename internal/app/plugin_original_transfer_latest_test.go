package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOriginalTransferLatestOrderActualSDKFixedCaller(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-event-target/transfer-latest-oracle.json")
	var oracle struct {
		Cases []struct {
			Name           string
			Success        bool
			OldExists      bool `json:"old_exists"`
			NewExists      bool `json:"new_exists"`
			SubtitleExists bool `json:"subtitle_exists"`
			SourceExists   bool `json:"source_exists"`
			Calls          []string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 3 {
		t.Fatal("actual source latest oracle", err)
	}
	for _, test := range oracle.Cases {
		t.Run(test.Name, func(t *testing.T) {
			a, source, target := importTestApp(t)
			identity := importFixture(t, a, "movie")
			overwriteRule(a, source, target, "latest", true)
			input, old, subtitle := filepath.Join(source, "Current.mkv"), filepath.Join(target, "Latest", "Old.mkv"), filepath.Join(target, "Latest", "Subtitle.srt")
			importWrite(t, input, []byte("owned video"))
			importWrite(t, old, []byte("old version"))
			importWrite(t, subtitle, []byte("keep subtitle"))
			full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"title":"Owned","original_title":"Owned","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
			if err != nil {
				t.Fatal(err)
			}
			patches := map[string]map[string]any{"transfer.rename": {"updated": true, "updated_str": "Latest/New.mkv"}}
			if test.Name == "cancel" {
				patches["transfer.intercept"] = map[string]any{"cancel": true, "reason": "latest veto"}
			}
			id := installOwnedTransferObserver(t, a, patches, nil, map[string]any{"storage": true, "storage_root": filepath.Dir(source), "storage_mode": test.Name, "mediainfo": full})
			plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: identity.MediaType, TMDBID: identity.ID, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: input}}})
			if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
				t.Fatal("real preview", plan, err)
			}
			job := overwriteStart(t, a, plan)
			err = a.runImport(context.Background(), job, plan.ID, nil)
			var executed ImportPlan
			if e := a.Store.Get(context.Background(), "import_plan", plan.ID, &executed); e != nil || executed.Files[0].Target != filepath.Join(target, "Latest", "New.mkv") {
				t.Fatal("actual source latest task target", executed, e)
			}
			plan = executed
			a.wg.Wait()
			if (err == nil) != test.Success {
				t.Fatal("fixed source result", err, test.Success)
			}
			exists := func(path string) bool { _, err := os.Stat(path); return err == nil }
			if exists(old) != test.OldExists || exists(plan.Files[0].Target) != test.NewExists || exists(input) != test.SourceExists || exists(subtitle) != test.SubtitleExists {
				t.Fatal("actual source filesystem side effects", test.Name)
			}
			response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+id+"/observations", "")
			var rows []struct{ Type string }
			if json.Unmarshal(response.Body.Bytes(), &rows) != nil {
				t.Fatal("SDK trace")
			}
			deleteAt, interceptAt := -1, -1
			actualCalls := []string{}
			for i, row := range rows {
				if strings.HasPrefix(row.Type, "storage.") && row.Type != "storage.operation" {
					actualCalls = append(actualCalls, strings.TrimPrefix(row.Type, "storage."))
				} else if row.Type == "transfer.intercept" {
					actualCalls = append(actualCalls, row.Type)
				}
				if row.Type == "storage.delete" {
					deleteAt = i
				}
				if row.Type == "transfer.intercept" {
					interceptAt = i
				}
			}
			if deleteAt < 0 || interceptAt < 0 || deleteAt >= interceptAt {
				t.Fatal("fixed latest deletion precedes even a veto", deleteAt, interceptAt)
			}
			got, _ := json.Marshal(actualCalls)
			want, _ := json.Marshal(test.Calls)
			if string(got) != string(want) {
				t.Fatal("actual fixed complete local call order", string(got), string(want))
			}
		})
	}
}

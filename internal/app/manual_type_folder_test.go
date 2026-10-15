package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/catalog"
)

func TestManualOrganizeHTTPTypeFolderPublishesAndReorganizesSelectedDirectory(t *testing.T) {
	for _, kind := range []string{"movie", "tv"} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/type-%t", kind, enabled), func(t *testing.T) {
				a, source, automaticTarget := importTestApp(t)
				identity := importFixture(t, a, kind)
				selectedTarget := filepath.Join(filepath.Dir(automaticTarget), "selected-library")
				if err := os.Mkdir(selectedTarget, 0750); err != nil {
					t.Fatal(err)
				}
				a.config.Roots = append(a.config.Roots, Root{ID: "selected", Path: selectedTarget, Kind: "mixed"})
				// Both rules disagree with the submitted switch and method. An
				// explicit path/switch must survive source and target defaults.
				a.config.Directories = []DirectoryRule{
					{ID: "source-default", SourcePath: source, LibraryPath: automaticTarget, RootID: "root", MediaType: "mixed", Mode: "copy", LibraryTypeFolder: importFlag(!enabled)},
					{ID: "target-default", SourcePath: source, LibraryPath: selectedTarget, RootID: "selected", MediaType: "mixed", Mode: "copy", LibraryTypeFolder: importFlag(!enabled)},
				}
				name := "Original.Movie.2024.mkv"
				if kind == "tv" {
					name = "Original.Show.S02E03.mkv"
					importCache(t, a, "season:123:2", catalog.Season{ID: 20, SeriesID: 123, SeasonNumber: 2, Episodes: []catalog.Episode{{ID: 203, SeasonNumber: 2, EpisodeNumber: 3, Name: "第三集"}}})
				}
				input := filepath.Join(source, name)
				importWrite(t, input, []byte("isolated manual type-folder HTTP fixture"))
				cookie := setupTestAdmin(t, a)
				submit := func(payload map[string]any) manualOrganizeItem {
					t.Helper()
					raw, err := json.Marshal(payload)
					if err != nil {
						t.Fatal(err)
					}
					response := callTest(t, a.Handler(), "POST", "/api/files/organize", string(raw), cookie, "")
					var envelope struct {
						Data manualOrganizeOutput `json:"data"`
					}
					if err = json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || response.Code != 200 || envelope.Data.Failed != 0 || envelope.Data.Submitted != 1 || len(envelope.Data.Items) != 1 {
						t.Fatalf("organize did not submit one valid execution: status=%d body=%s err=%v", response.Code, response.Body, err)
					}
					item := envelope.Data.Items[0]
					reorgRunJob(t, a, reorgJob(t, a, "import", item.PlanID))
					return item
				}
				wantTarget := func(typeFolder bool) string {
					base := selectedTarget
					if typeFolder {
						base = filepath.Join(base, map[string]string{"movie": "华语电影", "tv": "国产剧"}[kind])
					}
					folder := fmt.Sprintf("%s (%d)", identity.Title, identity.Year)
					if kind == "tv" {
						return filepath.Join(base, folder, "Season 2", identity.Title+" S02E03.mkv")
					}
					return filepath.Join(base, folder, folder+".mkv")
				}
				first := submit(map[string]any{
					"paths": []string{input}, "target_storage": "", "target_path": selectedTarget, "mode": "hardlink",
					"media_type": kind, "tmdb_id": identity.ID, "scraping": false, "library_type_folder": enabled,
				})
				if first.Target != wantTarget(enabled) || first.Mode != "hardlink" {
					t.Fatalf("manual choices overridden: %+v want target=%s", first, wantTarget(enabled))
				}
				sourceInfo, err := os.Stat(input)
				if err != nil {
					t.Fatal(err)
				}
				targetInfo, err := os.Stat(first.Target)
				if err != nil || !os.SameFile(sourceInfo, targetInfo) {
					t.Fatal("selected hardlink was not published", err)
				}
				firstHistory := historyUpsertOnly(t, a, "")
				second := submit(map[string]any{
					"history_ids": []string{firstHistory.ID}, "from_history": true,
					"target_storage": "local", "target_path": selectedTarget, "mode": "copy",
					"scraping": false, "library_type_folder": !enabled,
				})
				reorgRunJob(t, a, reorgJob(t, a, "reorganize_cleanup", second.PlanID))
				if second.Target != wantTarget(!enabled) || second.Mode != "copy" {
					t.Fatalf("history choices overridden: %+v want target=%s", second, wantTarget(!enabled))
				}
				if data, err := os.ReadFile(second.Target); err != nil || string(data) != "isolated manual type-folder HTTP fixture" {
					t.Fatal("reorganized video missing or changed", err)
				}
				if _, err = os.Stat(first.Target); !os.IsNotExist(err) {
					t.Fatal("previous owned target was not retired", err)
				}
				if current, err := os.Stat(input); err != nil || !os.SameFile(sourceInfo, current) {
					t.Fatal("original download source changed", err)
				}
				entries, err := os.ReadDir(automaticTarget)
				if err != nil || len(entries) != 0 {
					t.Fatalf("manual target fell back to the source directory rule: entries=%v err=%v", entries, err)
				}
				latest := historyUpsertOnly(t, a, "")
				if latest.ID != firstHistory.ID || latest.Target != second.Target || latest.Status != "success" {
					t.Fatalf("history did not show the new classified target: %+v", latest)
				}
			})
		}
	}
}

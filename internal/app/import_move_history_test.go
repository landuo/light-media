package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Build real move histories through the same manual submission and job runner
// used by the ordinary frontend. No history, asset or move receipt is seeded.
func nativeMoveHistoryFixture(t *testing.T) (*App, string, map[string]transferHistoryItem, map[string][]byte) {
	t.Helper()
	a, source, target := importTestApp(t)
	nativeMoveFixture(t, a)
	video := filepath.Join(source, "History.Move.2024.mkv")
	subtitle := strings.TrimSuffix(video, ".mkv") + ".zh.srt"
	contents := map[string][]byte{video: []byte("native history move video"), subtitle: []byte("native history move subtitle")}
	for path, data := range contents {
		importWrite(t, path, data)
	}
	out, err := a.manualOrganize(t.Context(), manualOrganizeRequest{Paths: []string{video}, MediaType: "movie", TMDBID: 123, Mode: "move", TargetPath: target, Scraping: importFlag(false)})
	if err != nil || out.Failed != 0 || out.Submitted == 0 {
		t.Fatalf("initial actual move submission: %+v %v", out, err)
	}
	for _, item := range out.Items {
		if item.JobID != "" {
			_, job := historyUpsertAttempt(t, a, item.JobID)
			runEpisodeGroupJob(t, a, job)
		}
	}
	page := historyParityReadPage(t, a, "")
	if page.Total != 2 {
		t.Fatalf("actual main/subtitle histories: %+v", page)
	}
	items := map[string]transferHistoryItem{}
	for _, row := range page.Items {
		item, err := a.transferHistoryItem(t.Context(), row.ID)
		if err != nil || item.Mode != "move" || item.Status != "success" || item.ResourceSource != item.Source || item.organizeSource() != item.Target || contents[item.Source] == nil {
			t.Fatalf("actual moved history: %+v %v", item, err)
		}
		if _, err = os.Lstat(item.Source); !os.IsNotExist(err) {
			t.Fatal("initial move retained source", item.Source, err)
		}
		if got, err := os.ReadFile(item.Target); err != nil || !bytes.Equal(got, contents[item.Source]) {
			t.Fatal("initial move target bytes", item.Target, err)
		}
		items[item.Source] = item
	}
	return a, target, items, contents
}

func TestImportMoveHistorySingleAndMultiReorganize(t *testing.T) {
	t.Skip("MoviePilot single-table history keeps one row per src; physical-resource merge was removed")
	for _, selection := range []string{"main", "subtitle", "both"} {
		t.Run(selection, func(t *testing.T) {
			a, root, before, contents := nativeMoveHistoryFixture(t)
			selected := map[string]bool{}
			ids := []string{}
			// Fix main-before-subtitle order, matching the visible history batch.
			for _, ext := range []string{".mkv", ".srt"} {
				for source, item := range before {
					if !strings.HasSuffix(source, ext) || selection == "main" && ext != ".mkv" || selection == "subtitle" && ext != ".srt" {
						continue
					}
					selected[source] = true
					ids = append(ids, item.ID)
				}
			}
			nextRoot := filepath.Join(root, "Reorganized")
			if err := os.MkdirAll(nextRoot, 0750); err != nil {
				t.Fatal(err)
			}
			out, err := a.manualOrganize(t.Context(), manualOrganizeRequest{HistoryIDs: ids, FromHistory: true, Mode: "move", TargetPath: nextRoot, Scraping: importFlag(false)})
			if err != nil || out.Failed != 0 || out.Submitted != len(ids) {
				t.Fatalf("actual selected-history move submission: %+v %v", out, err)
			}
			for _, item := range out.Items {
				if item.JobID != "" {
					_, job := historyUpsertAttempt(t, a, item.JobID)
					runEpisodeGroupJob(t, a, job)
				}
			}
			if page := historyParityReadPage(t, a, ""); page.Total != 2 {
				t.Fatalf("reorganization duplicated physical-resource history: %+v", page)
			}
			for originalSource, old := range before {
				latest, err := a.transferHistoryItem(t.Context(), old.ID)
				if err != nil || latest.ID != old.ID || latest.ResourceSource != originalSource || latest.Status != "success" || latest.Mode != "move" {
					t.Fatalf("stable history or original resource lost: %+v %v", latest, err)
				}
				if selected[originalSource] {
					if latest.PlanID == old.PlanID || latest.Source != old.Target || latest.Target == old.Target || !within(nextRoot, latest.Target) || latest.organizeSource() != latest.Target {
						t.Fatalf("selected move did not use old target as its actual source: old=%+v new=%+v", old, latest)
					}
					var plan ImportPlan
					if err = a.Store.Get(t.Context(), "import_plan", latest.PlanID, &plan); err != nil {
						t.Fatal(err)
					}
					file, _, key, err := a.historyPlannedFile(t.Context(), plan, latest)
					if err != nil || file.HistorySource != originalSource || file.Source != old.Target {
						t.Fatalf("reorganized plan lost inherited resource: %+v %v", file, err)
					}
					var receipt importReceipt
					if err = a.Store.Get(t.Context(), "import_receipt", key, &receipt); err != nil || receipt.Move == nil || receipt.Move.State != "completed" || receipt.Source != old.Target || receipt.Target != latest.Target {
						t.Fatalf("actual second move receipt: %+v %v", receipt, err)
					}
					if strings.HasSuffix(originalSource, ".mkv") {
						var asset Asset
						if err = a.Store.Get(t.Context(), "asset", file.AssetID, &asset); err != nil || asset.SourcePath != originalSource || asset.Path != latest.Target {
							t.Fatalf("main asset lost original source provenance: %+v %v", asset, err)
						}
					}
					if _, err = os.Lstat(old.Target); !os.IsNotExist(err) {
						t.Fatal("selected old target remained after move", old.Target, err)
					}
				} else if latest.PlanID != old.PlanID || latest.Target != old.Target || latest.Source != old.Source {
					t.Fatalf("single-history request changed unselected companion: %+v", latest)
				}
				if got, err := os.ReadFile(latest.Target); err != nil || !bytes.Equal(got, contents[originalSource]) {
					t.Fatal("reorganization changed real target bytes", latest.Target, err)
				}
				if _, err = os.Lstat(originalSource); !os.IsNotExist(err) {
					t.Fatal("history reorganization recreated vanished original source", err)
				}
			}
		})
	}
}

func TestImportMoveHistoryFourDeletionChoices(t *testing.T) {
	for _, choice := range []struct {
		name, extension string
		source, target  bool
	}{
		{"record-only", ".mkv", false, false},
		{"record-and-missing-source", ".srt", true, false},
		{"record-and-target", ".mkv", false, true},
		{"record-and-both", ".srt", true, true},
	} {
		t.Run(choice.name, func(t *testing.T) {
			a, root, histories, contents := nativeMoveHistoryFixture(t)
			sentinel := filepath.Join(root, "unrelated.mkv")
			importWrite(t, sentinel, []byte("unrelated managed-root neighbor"))
			var selected transferHistoryItem
			for source, item := range histories {
				if strings.HasSuffix(source, choice.extension) {
					selected = item
				}
			}
			if selected.ID == "" {
				t.Fatal("missing selected actual history")
			}
			_, err := a.deleteTransferHistory(t.Context(), selected, historyDeleteRequest{Confirm: true, FileID: selected.executionFileID(), DeleteSource: choice.source, DeleteTarget: choice.target})
			if err != nil {
				t.Fatal("actual moved-history deletion", err)
			}
			for source, item := range histories {
				_, err := os.Lstat(item.Target)
				if item.ID == selected.ID && choice.target {
					if !os.IsNotExist(err) {
						t.Fatal("selected moved target was retained", err)
					}
				} else if got, err := os.ReadFile(item.Target); err != nil || !bytes.Equal(got, contents[source]) {
					t.Fatal("deletion touched retained or unselected target", item.Target, err)
				}
				if _, err = os.Lstat(source); !os.IsNotExist(err) {
					t.Fatal("deletion changed missing original source", source, err)
				}
			}
			if got, err := os.ReadFile(sentinel); err != nil || string(got) != "unrelated managed-root neighbor" {
				t.Fatal("deletion changed unrelated file", err)
			}
			page := historyParityReadPage(t, a, "")
			if page.Total != 1 || page.Items[0].ID == selected.ID {
				t.Fatalf("deletion hid the wrong physical history: %+v", page)
			}
		})
	}
}

func TestImportMoveEnabledDirectoryRulePublishesAutomatically(t *testing.T) {
	a, rule, source := directoryFileFixture(t, true)
	nativeMoveFixture(t, a)
	rule.Mode, rule.Scraping = "move", importFlag(false)
	a.config.Directories[0] = rule
	path := directoryMovieFile(t, source)
	directoryPoll(t, a, rule)
	directoryAge(t, a, rule, path)
	directoryPoll(t, a, rule)
	observation := directoryRecognize(t, a, rule, path)
	if observation.PlanID == "" || observation.JobID == "" {
		t.Fatal("enabled move rule did not queue an actual import", observation)
	}
	plan, job := historyUpsertAttempt(t, a, observation.JobID)
	if plan.Mode != "move" || plan.Files[0].Method != "move" {
		t.Fatal("automatic rule lost explicit move mode", plan)
	}
	runEpisodeGroupJob(t, a, job)
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("automatic move retained source", err)
	}
	history := historyUpsertOnly(t, a, "")
	if history.Status != "success" || history.Mode != "move" || history.PlanID != plan.ID {
		t.Fatal("automatic move did not persist successful history", history)
	}
	if got, err := os.ReadFile(history.Target); err != nil || string(got) != "synthetic monitor video" {
		t.Fatal("automatic move target bytes", err)
	}
	directoryPoll(t, a, rule)
	directoryPoll(t, a, rule)
	if latest := historyUpsertOnly(t, a, ""); latest.ID != history.ID || latest.PlanID != history.PlanID {
		t.Fatal("polling after source disappearance replaced successful move history", latest)
	}
}

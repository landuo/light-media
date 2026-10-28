package app

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func auxiliaryHistoryRemovalFixture(t *testing.T, kind string) (*App, transferHistoryItem, []string) {
	t.Helper()
	a, object, first, cfg := originalTransferModuleFixture(t, "hardlink", "")
	ctx := context.Background()
	main := first.Files[0].Source
	paths := []string{main, strings.TrimSuffix(main, ".mkv") + ".srt", strings.TrimSuffix(main, ".mkv") + ".flac"}
	for _, path := range paths[1:] {
		importWrite(t, path, []byte("owned independent history file"))
	}
	cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": "module_execute"}
	cfg["result"] = nil
	raw, _ := json.Marshal(cfg)
	if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
		t.Fatal(err)
	}
	if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: main}}})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
		t.Fatal(plan, err)
	}
	job := overwriteStart(t, a, plan)
	if err := a.runImport(ctx, job, plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID); err != nil {
		t.Fatal(err)
	}
	page, err := a.transferHistory(ctx, httptest.NewRequest("GET", "/api/history/transfers", nil))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(page)
	var out struct{ Items []transferHistoryItem }
	if json.Unmarshal(raw, &out) != nil || len(out.Items) != 3 {
		t.Fatal("three real independent histories", string(raw))
	}
	wanted := ".srt"
	if kind == "audio" {
		wanted = ".flac"
	}
	var selected transferHistoryItem
	protected := append([]string(nil), paths...)
	for _, item := range out.Items {
		if strings.HasSuffix(item.Source, wanted) {
			selected = item
		} else {
			protected = append(protected, item.Target)
		}
	}
	if selected.ID == "" || selected.Target == "" || selected.Status != "success" {
		t.Fatal("actual auxiliary history", selected)
	}
	return a, selected, protected
}

func TestAuxiliaryHistoryIndependentFileChoicesFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-history-delete-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Kind   string
			Source bool `json:"delete_source"`
			Target bool `json:"delete_target"`
			Trace  []struct{ Action, Path string }
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 8 {
		t.Fatal("fixed independent history deletion endpoint", err)
	}
	for _, sample := range oracle.Cases {
		name := sample.Kind + "/record"
		if sample.Source {
			name += "-source"
		}
		if sample.Target {
			name += "-target"
		}
		t.Run(name, func(t *testing.T) {
			a, item, protected := auxiliaryHistoryRemovalFixture(t, sample.Kind)
			ctx := context.Background()
			if _, err := a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, DeleteSource: sample.Source, DeleteTarget: sample.Target, FileID: item.executionFileID()}); err != nil {
				t.Fatal("actual independent auxiliary delete", err)
			}
			for path, deleted := range map[string]bool{item.Source: sample.Source, item.Target: sample.Target} {
				_, err := os.Stat(path)
				if os.IsNotExist(err) != deleted {
					t.Fatal("fixed selected-file effect", path, deleted, err)
				}
			}
			for _, path := range protected {
				if path == item.Source && sample.Source {
					continue
				}
				if _, err := os.Stat(path); err != nil {
					t.Fatal("another main/source/auxiliary file changed", filepath.Base(path), err)
				}
			}
			var assets, slots, histories int
			if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='asset' AND json_extract(payload,'$.state')!='removed'`).Scan(&assets); err != nil || assets != 1 {
				t.Fatal("independent deletion removed video asset", assets, err)
			}
			if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM asset_slots`).Scan(&slots); err != nil || slots != 1 {
				t.Fatal("independent deletion removed video slots", slots, err)
			}
			if err := a.Store.DB.QueryRow(transferHistoryCTE + `SELECT COUNT(*) FROM history`).Scan(&histories); err != nil || histories != 2 {
				t.Fatal("only selected history should disappear", histories, err)
			}
		})
	}
}

func TestAuxiliaryHistoryTargetChangeAndColdDeletionRecovery(t *testing.T) {
	for _, cold := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed-target", true: "commit-failure-cold"}[cold], func(t *testing.T) {
			a, item, protected := auxiliaryHistoryRemovalFixture(t, "subtitle")
			ctx := context.Background()
			if !cold {
				if err := os.Remove(item.Target); err != nil {
					t.Fatal(err)
				}
				importWrite(t, item.Target, []byte("replacement must remain"))
				_, err := a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, DeleteTarget: true, FileID: item.executionFileID()})
				if fileCode(err) != "FILE_SNAPSHOT_CHANGED" {
					t.Fatal("changed-target guard", err)
				}
				if raw, err := os.ReadFile(item.Target); err != nil || string(raw) != "replacement must remain" {
					t.Fatal("replacement changed", err)
				}
				return
			}
			if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_aux_remove_commit BEFORE UPDATE ON records WHEN NEW.kind='history_target_removal' AND json_extract(NEW.payload,'$.State')='completed' BEGIN SELECT RAISE(ABORT,'owned auxiliary removal commit'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, DeleteTarget: true, FileID: item.executionFileID()}); err == nil {
				t.Fatal("actual SQL commit failure missing")
			}
			if _, err := os.Stat(item.Target); !os.IsNotExist(err) {
				t.Fatal("actual unlink should precede failed bookkeeping", err)
			}
			if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_aux_remove_commit`); err != nil {
				t.Fatal(err)
			}
			dir := a.Dir
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			fresh, err := New(Options{DataDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { fresh.Close() })
			if _, err := fresh.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, DeleteTarget: true, FileID: item.executionFileID()}); err != nil {
				t.Fatal("cold receipt recovery", err)
			}
			for _, path := range protected {
				if _, err := os.Stat(path); err != nil {
					t.Fatal("cold recovery touched another file", path, err)
				}
			}
			var owned int
			if err := fresh.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='managed_file' AND id=?`, tokenHash(item.Target)).Scan(&owned); err != nil || owned != 0 {
				t.Fatal("cold recovery did not finish only auxiliary ownership", owned, err)
			}
		})
	}
}

func TestAuxiliaryHistorySourceUsedByAnotherActualQueuedPlan(t *testing.T) {
	for _, state := range []string{"queued", "running", "retry_wait", "waiting_paused"} {
		t.Run(state, func(t *testing.T) {
			a, item, _ := auxiliaryHistoryRemovalFixture(t, "subtitle")
			ctx := context.Background()
			other := filepath.Join(filepath.Dir(item.Source), "Other.2025.mkv")
			importWrite(t, other, []byte("another owned source"))
			full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":124,"title":"Other","original_title":"Other","release_date":"2025-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
			if err != nil {
				t.Fatal(err)
			}
			value, _ := a.pluginRunningInstances.Load(ownedModuleID)
			object := value.(*originalPluginInstance)
			extra := originalTransferPreviewItem(item.Source, "file", item.Size)
			extra["name"], extra["basename"] = "Other.2025.srt", "Other.2025"
			siblings, err := plugins.ModuleResultTuple(extra)
			if err != nil {
				t.Fatal(err)
			}
			config := map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_search_media", "transfer": "module_execute", "get_parent_item": "module_storage_parent", "list_files": "module_storage_siblings"}, "media_result": full, "result": nil, "storage_siblings": siblings}
			raw, _ := json.Marshal(config)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 124, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: other}}})
			if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
				t.Fatal("actual second preview", plan, err)
			}
			var meta importMetadata
			if err := a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil || len(meta.Sidecars[plan.Files[0].ID]) != 1 || meta.Sidecars[plan.Files[0].ID][0].Source != item.Source {
				t.Fatal("actual frozen second plan consumes selected subtitle", meta, err)
			}
			job, err := a.executeImport(ctx, plan.ID, nil)
			if err != nil {
				t.Fatal("actual second queued job", err)
			}
			// Only the scheduler phase is controlled; the plan, side association,
			// snapshot/authority and job were prepared through actual host code.
			if _, err := a.Store.DB.Exec(`UPDATE jobs SET state=? WHERE id=?`, state, job.ID); err != nil {
				t.Fatal(err)
			}
			_, err = a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, DeleteSource: true, DeleteTarget: true, FileID: item.executionFileID()})
			if fileCode(err) != "FILE_IN_USE" {
				t.Fatal("another active auxiliary consumer must retain both files", state, err)
			}
			for _, path := range []string{item.Source, item.Target, other} {
				if _, err := os.Stat(path); err != nil {
					t.Fatal("active source or target was deleted", path, err)
				}
			}
		})
	}
}

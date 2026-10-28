package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fixed history.py ignores delete_media_file's false return for the target,
// but stops on a false source deletion. StorageChain.delete_media_file calls
// the ordinary delete_file module before considering parent cleanup.
func TestOriginalHistoryDeleteStorageModuleActualSDK(t *testing.T) {
	for _, target := range []bool{false, true} {
		for _, accepted := range []bool{false, true} {
			name := map[bool]string{false: "source", true: "target"}[target] + "/" + map[bool]string{false: "false", true: "true-effect"}[accepted]
			t.Run(name, func(t *testing.T) {
				a, item, protected := auxiliaryHistoryRemovalFixture(t, "subtitle")
				ctx := context.Background()
				path := item.Source
				if target {
					path = item.Target
				}
				value, _ := a.pluginRunningInstances.Load(ownedModuleID)
				object := value.(*originalPluginInstance)
				mode := "no_effect"
				if accepted {
					mode = "effect"
				}
				cfg := map[string]any{"enabled": true, "modules": map[string]string{"delete_file": "module_storage_delete"}, "storage_root": filepath.Dir(path), "storage_mode": mode, "result": accepted}
				raw, _ := json.Marshal(cfg)
				if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
					t.Fatal(err)
				}
				if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
					t.Fatal(err)
				}
				before := ownedModuleRead(t, object)
				_, err := a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, DeleteSource: !target, DeleteTarget: target, FileID: item.executionFileID()})
				if (err == nil) != (target || accepted) {
					t.Fatal("fixed caller target return ignored / source return required", target, accepted, err)
				}
				after := ownedModuleRead(t, object)
				if after.Calls["module_storage_delete"]-before.Calls["module_storage_delete"] != 1 {
					t.Fatal("history deletion must enter ordinary delete_file", before.Calls, after.Calls)
				}
				if _, err := os.Stat(path); os.IsNotExist(err) != accepted {
					t.Fatal("module actual local effect, false must skip core", path, accepted, err)
				}
				for _, other := range protected {
					if other == path && accepted {
						continue
					}
					if _, err := os.Stat(other); err != nil {
						t.Fatal("unselected material changed", other, err)
					}
				}
				var hidden int
				if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='transfer_history_hidden' AND id=?`, item.ID).Scan(&hidden); err != nil || hidden != map[bool]int{true: 1, false: 0}[target || accepted] {
					t.Fatal("fixed record deletion branch", hidden, err)
				}
			})
		}
	}
}

func TestOriginalHistoryMainTargetDeletesOnlySelectedFile(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "false", true: "true-effect"}[accepted], func(t *testing.T) {
			a, aux, _ := auxiliaryHistoryRemovalFixture(t, "subtitle")
			ctx := context.Background()
			var plan ImportPlan
			var meta importMetadata
			if err := a.Store.Get(ctx, "import_plan", aux.PlanID, &plan); err != nil {
				t.Fatal(err)
			}
			if err := a.Store.Get(ctx, "import_meta", aux.PlanID, &meta); err != nil {
				t.Fatal(err)
			}
			item, err := a.transferHistoryItem(ctx, plan.Files[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			value, _ := a.pluginRunningInstances.Load(ownedModuleID)
			object := value.(*originalPluginInstance)
			mode := "no_effect"
			if accepted {
				mode = "effect"
			}
			cfg := map[string]any{"enabled": true, "modules": map[string]string{"delete_file": "module_storage_delete"}, "storage_root": filepath.Dir(item.Target), "storage_mode": mode, "result": accepted}
			raw, _ := json.Marshal(cfg)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			before := ownedModuleRead(t, object)
			if _, err := a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, DeleteTarget: true, FileID: item.executionFileID()}); err != nil {
				t.Fatal("fixed endpoint ignores false target result", err)
			}
			after := ownedModuleRead(t, object)
			if after.Calls["module_storage_delete"]-before.Calls["module_storage_delete"] != 1 {
				t.Fatal("one selected file is one delete_file invocation", before.Calls, after.Calls)
			}
			if _, err := os.Stat(item.Target); os.IsNotExist(err) != accepted {
				t.Fatal("selected target effect", err)
			}
			for _, side := range meta.Sidecars[plan.Files[0].ID] {
				for _, path := range []string{side.Source, side.Target, item.Source} {
					if _, err := os.Stat(path); err != nil {
						t.Fatal("pinned delete_media_file keeps other media files", path, err)
					}
				}
			}
			var slots int
			if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM asset_slots WHERE asset_id=?`, plan.Files[0].AssetID).Scan(&slots); err != nil || slots != map[bool]int{true: 0, false: 1}[accepted] {
				t.Fatal("real video removal updates only its native index", slots, err)
			}
			if accepted {
				if _, err := a.deleteTransferHistory(ctx, aux, historyDeleteRequest{Confirm: true, DeleteTarget: true, FileID: aux.executionFileID()}); err != nil {
					t.Fatal("independent auxiliary removal after main video deletion", err)
				}
				if _, err := os.Stat(aux.Target); !os.IsNotExist(err) {
					t.Fatal("selected auxiliary still exists", err)
				}
			}
		})
	}
}

func TestOriginalHistorySourceDeleteCommitFailureColdRecovery(t *testing.T) {
	a, item, protected := auxiliaryHistoryRemovalFixture(t, "subtitle")
	ctx := context.Background()
	value, _ := a.pluginRunningInstances.Load(ownedModuleID)
	object := value.(*originalPluginInstance)
	cfg := map[string]any{"enabled": true, "modules": map[string]string{"delete_file": "module_storage_delete"}, "storage_root": filepath.Dir(item.Source), "storage_mode": "effect", "result": true}
	raw, _ := json.Marshal(cfg)
	if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
		t.Fatal(err)
	}
	if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_source_remove_commit BEFORE UPDATE ON records WHEN NEW.kind='history_source_removal' AND json_extract(NEW.payload,'$.state')='completed' BEGIN SELECT RAISE(ABORT,'owned source removal commit'); END`); err != nil {
		t.Fatal(err)
	}
	before := ownedModuleRead(t, object)
	if _, err := a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, DeleteSource: true, FileID: item.executionFileID()}); err == nil {
		t.Fatal("actual completion SQL failure absent")
	}
	after := ownedModuleRead(t, object)
	if after.Calls["module_storage_delete"] != before.Calls["module_storage_delete"]+1 {
		t.Fatal("source delete did not run exactly once")
	}
	if _, err := os.Stat(item.Source); !os.IsNotExist(err) {
		t.Fatal("physical delete must precede failed completion", err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_source_remove_commit`); err != nil {
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
	if _, err := fresh.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, DeleteSource: true, FileID: item.executionFileID()}); err != nil {
		t.Fatal("cold physical deletion bookkeeping recovery", err)
	}
	for _, path := range protected {
		if path != item.Source {
			if _, err := os.Stat(path); err != nil {
				t.Fatal("recovery changed another file", path, err)
			}
		}
	}
	var receipt historySourceRemoval
	if err := fresh.Store.Get(ctx, "history_source_removal", item.executionFileID(), &receipt); err != nil || receipt.State != "completed" {
		t.Fatal("cold completion receipt", receipt.State, err)
	}
}

func TestOriginalHistoryTargetParentCleanupHiddenFailureColdRecovery(t *testing.T) {
	a, item := historyRemovalFixture(t)
	ctx := context.Background()
	if err := a.saveSettings(ctx, a.Settings()); err != nil {
		t.Fatal(err)
	}
	nfo := strings.TrimSuffix(item.Target, filepath.Ext(item.Target)) + ".nfo"
	importWrite(t, nfo, []byte("owned no-media parent cleanup"))
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_history_hide_commit BEFORE INSERT ON records WHEN NEW.kind='transfer_history_hidden' BEGIN SELECT RAISE(ABORT,'owned history hide commit'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, DeleteTarget: true, FileID: item.executionFileID()}); err == nil {
		t.Fatal("actual hide commit failure absent")
	}
	for _, path := range []string{item.Target, nfo, filepath.Dir(item.Target)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("physical file and no-media directory must be gone", path, err)
		}
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_history_hide_commit`); err != nil {
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
		t.Fatal("cold deleted-parent recovery must not reopen its absent directory", err)
	}
	if _, err := os.Stat(item.Source); err != nil {
		t.Fatal("cold target deletion changed source", err)
	}
}

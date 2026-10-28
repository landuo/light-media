package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pinned manual cleanup (transfer.py:3389) precedes transfer and deletes only
// the selected main. StorageChain:171 keeps any remaining subtitle/audio.
func TestOriginalMainHistoryReorganizeSingleFile(t *testing.T) {
	originalMainHistoryReorganize(t, originalReorganizeCase{})
}
func TestOriginalMainHistoryReorganizeSamePath(t *testing.T) {
	originalMainHistoryReorganize(t, originalReorganizeCase{sameTarget: true})
}
func TestOriginalMainHistoryReorganizeDeleteFalse(t *testing.T) {
	originalMainHistoryReorganize(t, originalReorganizeCase{reject: true})
}
func TestOriginalMainHistoryReorganizeColdBeforeTransfer(t *testing.T) {
	originalMainHistoryReorganize(t, originalReorganizeCase{cold: true})
}
func TestOriginalMainHistoryReorganizeCleanupCommitFailure(t *testing.T) {
	originalMainHistoryReorganize(t, originalReorganizeCase{fault: true})
}
func TestOriginalMainHistoryOrdinaryRedoDeleteFalse(t *testing.T) {
	originalMainHistoryReorganize(t, originalReorganizeCase{reject: true, retry: true})
}

type originalReorganizeCase struct{ sameTarget, reject, cold, fault, retry bool }

func originalMainHistoryReorganize(t *testing.T, sample originalReorganizeCase) {
	t.Helper()
	a, object, first, cfg := originalTransferModuleFixture(t, "hardlink", "")
	ctx := context.Background()
	sub := strings.TrimSuffix(first.Files[0].Source, ".mkv") + ".srt"
	importWrite(t, sub, []byte("owned retained original subtitle"))
	cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": "module_execute"}
	cfg["result"] = nil
	raw, _ := json.Marshal(cfg)
	if _, e := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); e != nil {
		t.Fatal(e)
	}
	if e := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); e != nil {
		t.Fatal(e)
	}
	first, e := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: importFlag(false), Files: []ImportFileRequest{{Path: first.Files[0].Source}}})
	if e != nil {
		t.Fatal(e)
	}
	job := overwriteStart(t, a, first)
	if e = a.runImport(ctx, job, first.ID, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='succeeded' WHERE id=?`, job.ID); e != nil {
		t.Fatal(e)
	}
	var saved ImportPlan
	if e = a.Store.Get(ctx, "import_plan", first.ID, &saved); e != nil {
		t.Fatal(e)
	}
	history, e := a.transferHistoryItem(ctx, saved.Files[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	oldMain := history.Target
	oldSub := strings.TrimSuffix(oldMain, ".mkv") + ".srt"
	oldStamp, e := snapshotPath(oldSub)
	if e != nil {
		t.Fatal(e)
	}
	dest := filepath.Join(a.Settings().Roots[0].Path, "again")
	if e = os.Mkdir(dest, 0750); e != nil {
		t.Fatal(e)
	}
	if sample.sameTarget {
		dest = a.Settings().Roots[0].Path
	}
	input := manualOrganizeRequest{HistoryIDs: []string{history.ID}, FromHistory: true, TargetPath: dest, Mode: "hardlink", Scraping: importFlag(false), Preview: true}
	out, e := a.manualOrganize(ctx, input)
	if e != nil || out.Failed != 0 {
		t.Fatal(out, e)
	}
	if _, e = os.Stat(oldMain); e != nil {
		t.Fatal("preview must not delete", e)
	}
	input.Preview = false
	if sample.reject {
		cfg["modules"].(map[string]string)["delete_file"] = "module_storage_delete"
		cfg["storage_root"], cfg["storage_mode"], cfg["result"] = a.Settings().Roots[0].Path, "no_effect", false
		raw, _ := json.Marshal(cfg)
		if _, e = a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); e != nil {
			t.Fatal(e)
		}
		if e = a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); e != nil {
			t.Fatal(e)
		}
		before := ownedModuleRead(t, object)
		rejected, e := a.manualOrganize(ctx, input)
		if e != nil || rejected.Failed != 1 || rejected.Submitted != 0 {
			t.Fatal("source delete false must abort manual", rejected, e)
		}
		after := ownedModuleRead(t, object)
		if after.Calls["module_storage_delete"]-before.Calls["module_storage_delete"] != 1 || after.Calls["module_execute"] != before.Calls["module_execute"] {
			t.Fatal("no transfer after delete false", before.Calls, after.Calls)
		}
		if _, e = os.Stat(oldMain); e != nil {
			t.Fatal("delete false must retain old main", e)
		}
		unchanged, e := a.transferHistoryItem(ctx, history.ID)
		if e != nil || unchanged.PlanID != history.PlanID || unchanged.Status != "success" {
			t.Fatal("cleanup failure must retain successful history", unchanged, e)
		}
		cfg["result"] = nil
		if sample.retry {
			input.originalRetry = true
			cfg["result"] = false
			delete(cfg["modules"].(map[string]string), "transfer")
			cfg["modules"].(map[string]string)["get_parent_item"] = "module_storage_parent"
		}
		raw, _ = json.Marshal(cfg)
		if _, e = a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); e != nil {
			t.Fatal(e)
		}
		if e = a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); e != nil {
			t.Fatal(e)
		}
	}
	if sample.fault {
		if _, e = a.Store.DB.Exec(`CREATE TRIGGER owned_reorganize_meta_failure BEFORE UPDATE ON records WHEN NEW.kind='import_meta' AND json_extract(NEW.payload,'$.reorganize.target_removed')=1 BEGIN SELECT RAISE(ABORT,'owned reorganize metadata failure'); END`); e != nil {
			t.Fatal(e)
		}
		failed, e := a.manualOrganize(ctx, input)
		if e != nil || failed.Failed != 1 || failed.Submitted != 0 {
			t.Fatal("fault was not exercised", failed, e)
		}
		if _, e = os.Stat(oldMain); !os.IsNotExist(e) {
			t.Fatal("failure follows actual cleanup", e)
		}
		if _, e = a.Store.DB.Exec(`DROP TRIGGER owned_reorganize_meta_failure`); e != nil {
			t.Fatal(e)
		}
		dir := a.Dir
		if e = a.Close(); e != nil {
			t.Fatal(e)
		}
		a, e = New(Options{DataDir: dir})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { a.Close() })
		if e = a.initializeOriginalPlugins(ctx); e != nil {
			t.Fatal(e)
		}
		instance, ok := a.pluginRunningInstances.Load(ownedModuleID)
		if !ok {
			t.Fatal("cold plugin")
		}
		object = instance.(*originalPluginInstance)
	}
	out, e = a.manualOrganize(ctx, input)
	if e != nil || out.Submitted != 1 {
		t.Fatal(out, e)
	}
	if _, e = os.Stat(oldMain); sample.retry && e != nil || !sample.retry && !os.IsNotExist(e) {
		t.Fatal("manual deletes the selected file; ordinary redo ignores false", e)
	}
	if sample.retry {
		var call struct {
			Kwargs struct{ FileItem struct{ Path string } }
		}
		state := ownedModuleRead(t, object)
		if json.Unmarshal(state.Inputs["module_storage_parent"], &call) != nil || call.Kwargs.FileItem.Path != first.Files[0].Source {
			t.Fatal("only source collection may inspect a parent; ordinary redo must not clean the old target parent", state.Inputs)
		}
	}
	if stamp, e := snapshotPath(oldSub); e != nil || stamp != oldStamp {
		t.Fatal("old subtitle stays", e)
	}
	var next ImportPlan
	if e = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &next); e != nil {
		t.Fatal(e)
	}
	if sample.cold {
		dir := a.Dir
		if e = a.Close(); e != nil {
			t.Fatal(e)
		}
		a, e = New(Options{DataDir: dir})
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { a.Close() })
		if e = a.initializeOriginalPlugins(ctx); e != nil {
			t.Fatal(e)
		}
		instance, ok := a.pluginRunningInstances.Load(ownedModuleID)
		if !ok {
			t.Fatal("cold plugin")
		}
		object = instance.(*originalPluginInstance)
		state := ownedModuleRead(t, object)
		if state.Calls["module_execute"] != 0 || state.Calls["module_storage_delete"] != 0 {
			t.Fatal("cold no cleanup replay", state.Calls)
		}
	}
	job = overwriteStart(t, a, next)
	if e = a.runImport(ctx, job, next.ID, nil); e != nil {
		t.Fatal(e)
	}
	if stamp, e := snapshotPath(oldSub); e != nil || stamp != oldStamp {
		t.Fatal("new main must not remove sibling old subtitle", e)
	}
	var groupJobs int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='reorganize_cleanup' AND json_extract(payload,'$.id')=?`, next.ID).Scan(&groupJobs); e != nil || groupJobs != 0 {
		t.Fatal("no later whole-asset cleanup", groupJobs, e)
	}
}

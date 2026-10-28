package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func verifyOwnedProductionBrowserMutations(t *testing.T, folder string, root browserRoot, source, clone string, request func(string, string, any, int) []byte, success func(string, string, any), stop, start func()) map[string]any {
	t.Helper()
	bindings := map[string]string{"create_folder": "module_storage_mkdir", "rename_file": "module_storage_rename", "delete_file": "module_storage_delete"}
	config := map[string]any{"enabled": true, "modules": bindings, "storage_root": folder, "storage_mode": "effect", "result": true}
	empty := map[string]any{"enabled": true, "modules": map[string]string{"create_folder": "module_execute", "rename_file": "module_execute", "delete_file": "module_execute"}, "result": nil}
	set := func(id string, cfg any) { success("PUT", "/api/plugins/"+id, cfg) }
	operation := func(action, name string, paths []string, want int) string {
		t.Helper()
		var out struct{ Data browserOperation }
		if json.Unmarshal(request("POST", "/api/files/operation-preview", browserOperationRequest{Action: action, RootID: root.ID, Path: folder, Name: name, Paths: paths}, 200), &out) != nil || out.Data.ID == "" {
			t.Fatal("actual mutation preview")
		}
		request("POST", "/api/files/operation-confirm", map[string]any{"plan_id": out.Data.ID, "confirm": true}, want)
		return out.Data.ID
	}
	snapshot := func(id string) ownedModuleSnapshot {
		var out ownedModuleSnapshot
		if json.Unmarshal(request("GET", "/api/v1/plugin/"+id+"/snapshot", nil, 200), &out) != nil {
			t.Fatal("actual mutation snapshot")
		}
		return out
	}
	baselineClone := snapshot(clone)
	set(source, config)
	set(clone, empty)
	operation("mkdir", "OwnedWriter", nil, 200)
	old := filepath.Join(folder, "OwnedWriter")
	newPath := filepath.Join(folder, "OwnedRenamed")
	if err := os.WriteFile(filepath.Join(old, "Synthetic.txt"), []byte("owned mutation fixture"), 0640); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(filepath.Join(old, "Synthetic.txt"))
	if err != nil {
		t.Fatal(err)
	}
	operation("rename", "OwnedRenamed", []string{old}, 200)
	if after, err := os.Stat(filepath.Join(newPath, "Synthetic.txt")); err != nil || !os.SameFile(stat, after) {
		t.Fatal("actual plugin rename inode", err)
	}
	deletion := operation("delete", "", []string{newPath}, 200)
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatal("actual plugin directory deletion", err)
	}
	request("POST", "/api/files/operation-restore", map[string]any{"plan_id": deletion, "confirm": true}, 400)
	srcBefore, cloneBefore := snapshot(source), snapshot(clone)
	if srcBefore.Calls["module_storage_mkdir"] != 1 || srcBefore.Calls["module_storage_rename"] != 1 || srcBefore.Calls["module_storage_delete"] != 1 || cloneBefore.Calls["module_execute"] != baselineClone.Calls["module_execute"] {
		t.Fatal("source terminal must not run clone", srcBefore, cloneBefore)
	}
	set(source, empty)
	set(clone, config)
	operation("mkdir", "OwnedCloneWriter", nil, 200)
	operation("rename", "OwnedCloneRenamed", []string{filepath.Join(folder, "OwnedCloneWriter")}, 200)
	operation("delete", "", []string{filepath.Join(folder, "OwnedCloneRenamed")}, 200)
	srcAfter, cloneAfter := snapshot(source), snapshot(clone)
	if srcAfter.Calls["module_execute"]-srcBefore.Calls["module_execute"] != 3 || cloneAfter.Calls["module_storage_mkdir"] != 1 || cloneAfter.Calls["module_storage_rename"] != 1 || cloneAfter.Calls["module_storage_delete"] != 1 {
		t.Fatal("none→separate real clone operations", srcAfter, cloneAfter)
	}
	set(clone, empty)
	set(source, map[string]any{"enabled": true, "modules": bindings, "storage_root": folder, "storage_mode": "no_effect", "result": true})
	operation("mkdir", "OwnedNoEffect", nil, 400)
	if _, err := os.Stat(filepath.Join(folder, "OwnedNoEffect")); !os.IsNotExist(err) {
		t.Fatal("bare success created file")
	}
	set(source, map[string]any{"enabled": true, "modules": map[string]string{"create_folder": "module_execute"}, "result": false})
	failedID := operation("mkdir", "OwnedRejected", nil, 400)
	beforeCold := snapshot(source)
	stop()
	start()
	afterCold := snapshot(source)
	if beforeCold.PID == afterCold.PID || afterCold.Calls["module_execute"] != 0 {
		t.Fatal("actual cold mutation instance")
	}
	request("POST", "/api/files/operation-confirm", map[string]any{"plan_id": failedID, "confirm": true}, 400)
	if snapshot(source).Calls["module_execute"] != 0 {
		t.Fatal("failed journal replayed after real cold start")
	}
	set(source, config)
	operation("mkdir", "OwnedCold", nil, 200)
	operation("rename", "OwnedColdRenamed", []string{filepath.Join(folder, "OwnedCold")}, 200)
	operation("delete", "", []string{filepath.Join(folder, "OwnedColdRenamed")}, 200)
	proof, _ := json.Marshal(map[string]any{"source_before": srcBefore, "clone_before": cloneBefore, "source_after_clone": srcAfter, "clone_after": cloneAfter, "before_cold": beforeCold, "after_cold": afterCold, "final_source": snapshot(source), "final_clone": snapshot(clone), "failed_plan": failedID, "directory": folder, "direct_delete_plan": deletion})
	writeOwnedEventsProcessEvidence(t, "browser-mutation-real-main.json", proof)
	t.Log("actual source/clone mkdir/rename/delete physical inode+bytes, bare true rejection, cold failed-plan no replay and post-cold writes verified")
	return config
}

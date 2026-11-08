package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/plugins"
)

func managedStorageFixture(t *testing.T) (*App, transferHistoryItem, *originalPluginInstance) {
	t.Helper()
	a, item := historyRemovalFixture(t) // actual import creates asset, history and hardlink
	t.Cleanup(a.closeOriginalSessions)
	cookie := setupTestAdmin(t, a)
	manifest := installOwnedResourceProcessWorker(t, a, cookie, os.Getenv("OWNED_HOST_SERVICES_FIXTURE_BINARY"))
	object, ok := a.pluginRunningInstances.Load(manifest.PluginID)
	if !ok {
		t.Fatal("real storage worker not loaded")
	}
	return a, item, object.(*originalPluginInstance)
}

// Fixed source LocalModule.delete178-195 and RemoveLink.unlink1246-1274 do not
// consult media registration. The file manager keeps its separate policy.
func TestOriginalPluginStorageActualSDKDeletesRegisteredTargetRetainsHistoryAndSource(t *testing.T) {
	a, item, object := managedStorageFixture(t)
	ctx := context.Background()
	before, err := os.Stat(item.Source)
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.Stat(item.Target)
	if err != nil || !os.SameFile(before, target) {
		t.Fatal("import did not produce a real hardlink", err)
	}
	if err := a.browserMutationAllowed(ctx, item.Target); fileCode(err) != "FILE_IN_USE" {
		t.Fatal("interactive registered-file protection lost", err)
	}
	var assetsBefore, managedBefore int
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='asset'`).Scan(&assetsBefore); err != nil || assetsBefore == 0 {
		t.Fatal("real imported asset missing", err)
	}
	if err := a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='managed_file'`).Scan(&managedBefore); err != nil || managedBefore == 0 {
		t.Fatal("real imported file registration missing", err)
	}
	raw, err := ownedHostServicesCall(t, object, plugins.StorageDeleteCapability, plugins.StorageFilesRequest{FileItem: hostServiceItem(t, item.Target)})
	if err != nil || string(raw) != "true" {
		t.Fatal("source storage delete was blocked by registration", string(raw), err)
	}
	if _, err := os.Lstat(item.Target); !os.IsNotExist(err) {
		t.Fatal("selected registered entry remains", err)
	}
	after, err := os.Stat(item.Source)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("source inode changed", err)
	}
	if _, err := a.transferHistoryItem(ctx, item.ID); err != nil {
		t.Fatal("storage delete silently removed history", err)
	}
	var assetsAfter, managedAfter int
	_ = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='asset'`).Scan(&assetsAfter)
	_ = a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='managed_file'`).Scan(&managedAfter)
	if assetsAfter != assetsBefore || managedAfter != managedBefore {
		t.Fatal("physical storage delete silently removed audit/index records")
	}
}

func TestOriginalPluginStorageActualSDKKeepsActiveImportProtection(t *testing.T) {
	a, item, object := managedStorageFixture(t)
	var plan ImportPlan
	if err := a.Store.Get(context.Background(), "import_plan", item.PlanID, &plan); err != nil {
		t.Fatal(err)
	}
	plan.State = "running"
	if err := a.Store.Put(context.Background(), "import_plan", plan.ID, plan); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{item.Source, item.Target, filepath.Dir(item.Target)} {
		_, err := ownedHostServicesCall(t, object, plugins.StorageDeleteCapability, plugins.StorageFilesRequest{FileItem: hostServiceItem(t, path)})
		if err == nil {
			t.Fatal("active import deletion accepted", path)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal("denied operation changed file", err)
		}
	}
}

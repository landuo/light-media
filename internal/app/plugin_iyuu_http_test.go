package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins/uninstall"
	"testing"
)

func TestIYUUActualBulkUninstallSDKRetiresOwner(t *testing.T) {
	a, in := iyuuLifecycleFixture(t)
	ctx := context.Background()
	owner, e := a.iyuuOwner(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.pluginStore.SaveData(ctx, in.InstanceID, "history", 0, []string{"synthetic-retained"}); e != nil {
		t.Fatal(e)
	}
	run := uninstallInstall(t, a, "OwnedBulkUninstall", uninstall.Manifest(), map[string]any{"plugin_ids": []string{in.InstanceID}, "clear_config": true, "clear_data": true}, true)
	run = invokeIYUUActualSDK(t, a, run)
	var result uninstall.Result
	if e = json.Unmarshal(run.Result, &result); e != nil || len(result.Removed) != 1 || result.Removed[0] != in.InstanceID {
		t.Fatal("actual uninstall SDK result", result, e)
	}
	after, e := a.pluginStore.Installation(ctx, in.InstanceID)
	if e != nil || after.Installed || after.DesiredEnabled || after.Generation != in.Generation+1 {
		t.Fatal("actual bulk uninstall Root state", after, e)
	}
	if current := iyuuLifecycleOwner(t, a, in.InstanceID); current == owner {
		t.Fatal("old owner survived committed bulk retirement")
	}
	if _, e = a.pluginStore.Data(ctx, in.InstanceID, "history"); e != nil {
		t.Fatal("bulk source data unexpectedly removed", e)
	}
	var count int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_iyuu_retirements WHERE instance_id=? AND generation=?`, in.InstanceID, in.Generation).Scan(&count); e != nil || count != 1 {
		t.Fatal("bulk retirement missing", count, e)
	}
}

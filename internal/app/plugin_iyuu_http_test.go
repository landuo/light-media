package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"light-media/internal/plugins"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	"light-media/internal/plugins/uninstall"
)

func TestIYUUActualHTTPFormConfigAndLifecycle(t *testing.T) {
	for _, operation := range []string{"reset", "uninstall"} {
		t.Run(operation, func(t *testing.T) {
			a := newTestApp(t)
			ctx := context.Background()
			instance := "OwnedIYUUHTTP"
			uninstallInstall(t, a, instance, iyuu.Manifest(), map[string]any{"enabled": false, "token": "owned-synthetic-token", "sites": []any{}, "downloaders": []any{}}, false)
			executable, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			a.pluginCommand = func(plugin string) (plugins.Command, error) {
				if plugin != "IYUUAutoSeed" {
					return plugins.Command{}, errors.New("unexpected fixture plugin")
				}
				return plugins.Command{Path: executable, Args: []string{"-test.run=^TestIYUUActualSDKWorker$", "--", "--iyuu-owned-worker"}}, nil
			}
			form, e := a.pluginAPI(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), instance+"/form")
			if e != nil {
				t.Fatal("actual form HTTP/SDK", e)
			}
			data := form.(map[string]any)
			if data["revision"] != int64(1) || data["conf"] == nil || data["model"] == nil {
				t.Fatal("actual form revision/schema", data)
			}
			body := `{"revision":1,"config":{"enabled":false,"token":"owned-synthetic-token","sites":[],"downloaders":[],"onlyonce":false}}`
			value, e := a.pluginAPI(httptest.NewRecorder(), httptest.NewRequest("PUT", "/", strings.NewReader(body)), instance+"/config")
			if e != nil {
				t.Fatal("actual configuration HTTP admission", e)
			}
			if value.(map[string]any)["revision"] != int64(2) {
				t.Fatal("config revision", value)
			}
			run, e := a.pluginStore.claimRunForInstance(ctx, instance)
			if e != nil {
				t.Fatal(e)
			}
			invokeIYUUActualSDK(t, a, run)
			before, e := a.pluginStore.Installation(ctx, instance)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = a.pluginAPI(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil), instance+"/"+operation); e != nil {
				t.Fatal("actual lifecycle HTTP", e)
			}
			after, e := a.pluginStore.Installation(ctx, instance)
			if e != nil || after.Generation != before.Generation+1 || after.DesiredEnabled || after.Installed != (operation == "reset") {
				t.Fatal("actual Root lifecycle state", after, e)
			}
			var count int
			if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_iyuu_retirements WHERE instance_id=? AND generation=?`, instance, before.Generation).Scan(&count); e != nil || count != 1 {
				t.Fatal("same transaction retirement fact absent", count, e)
			}
		})
	}
}

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

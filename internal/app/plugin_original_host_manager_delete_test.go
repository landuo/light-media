package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"light-media/internal/plugins"
)

func TestOriginalHostManagerForceDeleteAndExactScheduledJobActualSDK(t *testing.T) {
	a, cookie, object := ownedHostServicesFixture(t)
	ctx := context.Background()
	response := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedHostServices/clone", `{"suffix":"copy"}`, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	control := func(request plugins.PluginManagerRequest) plugins.PluginControlResult {
		t.Helper()
		raw, err := ownedHostServicesCall(t, object, plugins.PluginManagerControlCapability, request)
		if err != nil {
			t.Fatal(request.Action, err)
		}
		var result plugins.PluginControlResult
		if json.Unmarshal(raw, &result) != nil || !result.Accepted || !result.Completed {
			t.Fatal("control not completed", string(raw))
		}
		return result
	}
	configuration := json.RawMessage(`{"enabled":true,"services":[{"id":"one","name":"one","handler":"recognize","trigger":{"kind":"interval","interval_ms":3600000,"timezone":"UTC"}},{"id":"two|suffix","name":"two","handler":"recognize","trigger":{"kind":"interval","interval_ms":3600000,"timezone":"UTC"}}]}`)
	control(plugins.PluginManagerRequest{InstanceID: "OwnedHostServicescopy", Action: "init", Config: configuration})
	control(plugins.PluginManagerRequest{InstanceID: "OwnedHostServicescopy", Action: "services.refresh"})
	jobs := func() []string {
		state := &a.pluginOriginalDirectJobs
		state.mu.Lock()
		defer state.mu.Unlock()
		ids := []string{}
		for _, job := range state.jobs {
			ids = append(ids, job.timerID)
		}
		return ids
	}
	if len(jobs()) != 2 {
		t.Fatal("actual getter did not register two timers", jobs())
	}
	control(plugins.PluginManagerRequest{InstanceID: "OwnedHostServicescopy", Action: "services.remove", JobID: "OwnedHostServicescopy_one"})
	if ids := jobs(); len(ids) != 1 || ids[0] != "OwnedHostServicescopy_two|suffix" {
		t.Fatal("exact single-job removal", ids)
	}
	a.initializeOriginalCommonService(time.Now())
	control(plugins.PluginManagerRequest{InstanceID: "OwnedHostServicescopy", Action: "services.remove", JobID: "scheduler_job"})
	if len(jobs()) != 1 {
		t.Fatal("source permits explicit default timer removal", jobs())
	}
	folder := t.TempDir()
	settings := a.Settings()
	settings.AllowedRoots = []string{folder}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	clone, ok := a.pluginRunningInstances.Load("OwnedHostServicescopy")
	if !ok {
		t.Fatal("clone missing")
	}
	if _, err := ownedHostServicesCall(t, clone.(*originalPluginInstance), plugins.StorageExistsCapability, plugins.StorageFilesRequest{FileItem: hostServiceItem(t, folder)}); err != nil {
		t.Fatal(err)
	}
	control(plugins.PluginManagerRequest{InstanceID: "OwnedHostServicescopy", Action: "stop"})
	for _, action := range []string{"config.delete", "data.delete"} {
		if result := control(plugins.PluginManagerRequest{InstanceID: "OwnedHostServicescopy", Action: action}); string(result.Result) != "false" {
			t.Fatal("stopped missing class must return false without force", action, string(result.Result))
		}
		if result := control(plugins.PluginManagerRequest{InstanceID: "OwnedHostServicescopy", Action: action, Force: true}); string(result.Result) != "true" {
			t.Fatal("forced source deletion did not return true", action, string(result.Result))
		}
	}
	if _, err := a.pluginStore.Config(ctx, "OwnedHostServicescopy"); !missingPluginValue(err) {
		t.Fatal("forced config retained", err)
	}
	if _, err := a.pluginStore.Data(ctx, "OwnedHostServicescopy", "last"); !missingPluginValue(err) {
		t.Fatal("forced data retained", err)
	}
	if _, err := os.Stat(folder); err != nil {
		t.Fatal("data deletion touched ordinary files", err)
	}
}

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOriginalTransferBlurayIntercept(t *testing.T) {
	a, object, original, cfg := originalTransferModuleFixture(t, "hardlink", "")
	ctx := context.Background()
	source := filepath.Join(filepath.Dir(original.Files[0].Source), "Owned.2024.BluRay")
	importWrite(t, filepath.Join(source, "BDMV", "STREAM", "00001.m2ts"), []byte("owned stream"))
	cfg["modules"].(map[string]string)["transfer"] = "module_execute"
	cfg["result"] = nil
	raw, _ := json.Marshal(cfg)
	if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
		t.Fatal(err)
	}
	if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	login := callTest(t, a.Handler(), "POST", "/api/auth/login", `{"username":"admin","password":"a-strong-test-password"}`, nil, "")
	if login.Code != 200 || len(login.Result().Cookies()) == 0 {
		t.Fatal("existing fixture login", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	observer := installOrderWorker(t, a, cookie, os.Getenv("OWNED_TRANSFER_EVENTS_BINARY"))
	id := observer.PluginID
	config := `{"enabled":true,"patches":{"transfer.intercept":{"cancel":true,"reason":"owned disc veto"}}}`
	response := callTest(t, a.Handler(), "PUT", "/api/plugins/"+id, config, cookie, "")
	assertOriginalRegistrySuccess(t, response.Code, response.Body.Bytes())
	out, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{source}, TargetPath: a.Settings().Roots[0].Path, Mode: "hardlink", MediaType: "movie", TMDBID: 123, Scraping: importFlag(false)})
	if err != nil || out.Submitted != 1 {
		t.Fatal(out, err)
	}
	var plan ImportPlan
	if err := a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); err != nil {
		t.Fatal(err)
	}
	job := overwriteStart(t, a, plan)
	if err := a.runImport(ctx, job, plan.ID, nil); err == nil || err.Error() != "owned disc veto" {
		t.Fatal("directory intercept veto", err)
	}
	children, err := os.ReadDir(plan.Files[0].Target)
	if err != nil || len(children) != 0 {
		t.Fatal("fixed source creates target before directory intercept", children, err)
	}
	a.wg.Wait()
	observed := callTest(t, a.Handler(), "GET", "/api/v1/plugin/"+id+"/observations", "", cookie, "")
	var calls []struct {
		Type string
		Data map[string]json.RawMessage
	}
	if observed.Code != 200 || json.Unmarshal(observed.Body.Bytes(), &calls) != nil {
		t.Fatal("actual ordinary event", observed.Body.String())
	}
	found := 0
	for _, call := range calls {
		if call.Type == "transfer.intercept" {
			found++
			if string(call.Data["meta"]) != "null" || string(call.Data["options"]) != "{}" {
				t.Fatal("source directory intercept meta/options", call.Data)
			}
		}
	}
	if found != 1 {
		t.Fatal("one directory intercept", found)
	}
}

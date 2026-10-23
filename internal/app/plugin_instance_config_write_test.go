package app

import (
	"context"
	"encoding/json"
	bf "light-media/internal/plugins/brushflow"
	"reflect"
	"testing"
)

func TestPluginInstanceExplicitRemoteSettingsAppliesBeforeSuccess(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	p := a.pluginStore
	ctx := context.Background()
	installCloneSource(t, p, bf.NativeManifest())
	source, e := p.SaveConfig(ctx, bf.PluginID, 0, bf.DefaultConfig())
	if e != nil {
		t.Fatal(e)
	}
	in, e := p.Clone(ctx, bf.PluginID, PluginCloneRequest{Suffix: "SettingsOwned"})
	if e != nil {
		t.Fatal(e)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); a.pluginWorker(workerCtx) }()
	defer func() { cancel(); <-done }()
	before := instanceReadTables(t, a)
	invalid := callTest(t, a.Handler(), "POST", "/api/plugins/"+in.InstanceID+"/api/settings", `{"enabled":"invalid"}`, cookie, "")
	if invalid.Code != 422 {
		t.Errorf("original validation response: %d %s", invalid.Code, invalid.Body.String())
	}
	if after := instanceReadTables(t, a); !reflect.DeepEqual(before, after) {
		t.Fatal("invalid settings changed state")
	}
	response := callTest(t, a.Handler(), "POST", "/api/plugins/"+in.InstanceID+"/api/settings", `{"enabled":false,"show_sidebar_nav":false,"global_proxy_delete":false}`, cookie, "")
	if response.Code != 200 {
		t.Fatalf("cold explicit settings: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	if json.Unmarshal(response.Body.Bytes(), &body) != nil || !body.Success || body.Data["enabled"] != false || body.Data["show_sidebar_nav"] != false {
		t.Fatal("original settings result", response.Body.String())
	}
	current, e := p.Config(ctx, in.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	var conf map[string]any
	if json.Unmarshal(current.Value, &conf) != nil || conf["enabled"] != false || conf["show_sidebar_nav"] != false {
		t.Fatal("wrong stored instance config", string(current.Value))
	}
	var total, success, active int
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*),COALESCE(SUM(state='succeeded'),0) FROM plugin_runs WHERE instance_id=? AND action='init'`, in.InstanceID).Scan(&total, &success); e != nil || total != 1 || success != 1 {
		t.Fatal("success returned before applied init", total, success, e)
	}
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_schedules WHERE instance_id=? AND active=1`, in.InstanceID).Scan(&active); e != nil || active != 0 {
		t.Fatal("disabled business scheduled", active, e)
	}
	after, e := p.Config(ctx, bf.PluginID)
	if e != nil || !reflect.DeepEqual(source, after) {
		t.Fatal("changed source", e)
	}
}

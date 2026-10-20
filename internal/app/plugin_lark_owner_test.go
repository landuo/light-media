package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	lark "light-media/internal/plugins/larkmessager"
)

// Lark is deliberately not in the compiled catalog yet. This foundation test
// consumes real Store runs, owner/SQL admissions and actual source HTTP; it does
// not represent installed SDK/App registration or the unfinished core pipeline.
func prepareUnregisteredLarkOwner(t *testing.T, a *App, run PluginRun) (*pluginDeferredCall, context.Context, func()) {
	t.Helper()
	ctx := context.Background()
	in, e := a.pluginStore.Installation(ctx, run.InstanceID)
	if e != nil {
		t.Fatal(e)
	}
	method, raw, e := larkDeferredSpec(run)
	if e != nil {
		t.Fatal(e)
	}
	r := pluginDeferredRecord{Schema: 1, Plugin: in.PluginID, Instance: in.InstanceID, Version: in.Version, Run: run.ID, Action: run.Action, Method: method, Generation: run.Generation, Revision: run.ConfigRevision, InputSHA: deferredInputSHA(run.Input), Raw: raw, Ack: json.RawMessage(`{"deferred":"owned-foundation"}`), Phase: "prepared"}
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.pluginStore.writeDeferred(ctx, tx, r, nil); e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	d := &pluginDeferredCall{a: a, installation: in, run: run, record: r}
	ctx, release, e := d.lockLarkDeferred(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if e = d.changePhase(ctx, "prepared", "started", nil); e != nil {
		release()
		t.Fatal(e)
	}
	return d, context.WithValue(ctx, pluginDeferredExecutionKey{}, d), release
}
func installUnregisteredLarkConfig(t *testing.T, a *App, config any) PluginRun {
	t.Helper()
	raw, _ := json.Marshal(lark.Manifest())
	_, e := a.pluginStore.InstallAndQueue(context.Background(), PluginInstallation{InstanceID: "LarkMessager", PluginID: "LarkMessager", Version: "0.9.1", ManifestSchemaVersion: 1, Manifest: raw}, config)
	if e != nil {
		t.Fatal(e)
	}
	run, e := a.pluginStore.ClaimRun(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return run
}
func TestPluginLarkActualOwnerConfigureSyncsOriginalDisabledNotifications(t *testing.T) {
	a := newTestApp(t)
	var tokens, directory atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			tokens.Add(1)
			w.Write([]byte(`{"code":0,"tenant_access_token":"owned-tenant","expire":7200}`))
		case "/open-apis/contact/v3/users/batch_get_id":
			directory.Add(1)
			w.Write([]byte(`{"code":0,"data":{"user_list":[{"email":"admin@example.test","user_id":"ou_owned_admin"}]}}`))
		default:
			t.Errorf("ungranted endpoint %s", r.URL.Path)
			w.WriteHeader(400)
		}
	}))
	defer api.Close()
	a.pluginLark.loopbackAPI = api.URL
	a.pluginLark.client = api.Client()
	run := installUnregisteredLarkConfig(t, a, map[string]any{"enabled": true, "app_id": "owned-app", "app_secret": "owned-secret", "admin_users": "admin@example.test", "user_id": "ou_owned_user"})
	d, ctx, release := prepareUnregisteredLarkOwner(t, a, run)
	defer release()
	if _, e := a.pluginLarkHost(ctx, d.installation, &run, d.record.Method, d.record.Raw); e != nil {
		t.Fatal(e)
	}
	result, code, found, e := a.pluginStore.larkOutcome(context.Background(), run, d.record.Method)
	if e != nil || !found || code != "" || string(result) != "null" {
		t.Fatal("source receipt absent", found, code, e)
	}
	if _, e = a.pluginStore.EnqueueRun(context.Background(), run.InstanceID, "page", nil); e != nil {
		t.Fatal("confirmed native result/started deferred window blocked live queue", e)
	}
	if e = d.changePhase(ctx, "started", "confirmed", result); e != nil {
		t.Fatal(e)
	}
	channels, e := a.pluginStore.NotificationChannels(context.Background())
	if e != nil || channels.Revision != 1 || len(channels.Value) != 1 {
		t.Fatal(channels, e)
	}
	channel := channels.Value[0].(map[string]any)
	config := channel["config"].(map[string]any)
	if channel["name"] != "Lark" || channel["type"] != "larkmessager" || channel["enabled"] != false || config["FEISHU_ADMINS"] != "ou_owned_admin" || config["FEISHU_OPEN_ID"] != "ou_owned_user" {
		t.Fatal("original disabled channel merge changed", channel)
	}
	if tokens.Load() != 1 || directory.Load() != 1 {
		t.Fatal(tokens.Load(), directory.Load())
	}
	if _, e = a.pluginLarkHost(context.Background(), d.installation, &run, lark.ConfigureCapability, json.RawMessage(`{}`)); e == nil {
		t.Fatal("unleased host call accepted")
	}
}
func TestPluginLarkActualAcceptedRunBlocksConfigAndLifecycle(t *testing.T) {
	a := newTestApp(t)
	run := installUnregisteredLarkConfig(t, a, map[string]any{"enabled": false})
	d, _, release := prepareUnregisteredLarkOwner(t, a, run)
	defer func() {
		if release != nil {
			release()
		}
	}()
	if _, _, e := a.pluginStore.SaveConfigAndQueue(context.Background(), run.InstanceID, run.ConfigRevision, map[string]any{"enabled": false}); pluginErrorCodeInternal(e) != "LARK_UNCONFIRMED" {
		t.Fatal("config erased accepted owner", e)
	}
	if e := a.pluginStore.Reset(context.Background(), run.InstanceID); pluginErrorCodeInternal(e) != "LARK_UNCONFIRMED" {
		t.Fatal("reset erased accepted owner", e)
	}
	if e := a.pluginStore.Uninstall(context.Background(), run.InstanceID); pluginErrorCodeInternal(e) != "LARK_UNCONFIRMED" {
		t.Fatal("uninstall erased accepted owner", e)
	}
	release()
	release = nil
	if _, e := a.pluginStore.EnqueueRun(context.Background(), run.InstanceID, "module", json.RawMessage(`{"call_id":"arbitrary"}`)); pluginErrorCodeInternal(e) != "LARK_UNCONFIRMED" {
		t.Fatal("queue bypassed unknown", e)
	}
	_ = d
}

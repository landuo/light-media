package app

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"light-media/internal/plugins"
	source "light-media/internal/plugins/autosignin"
	"light-media/internal/plugins/autosignin/durable"
)

func TestAutoSignInDeletedSubscriptionRequiresSourceCompletion(t *testing.T) {
	scope := durable.Scope{Instance: "AutoSignIn", Generation: 1, Revision: 1}
	state := durable.State{Scope: scope, Epoch: "owned", RuntimeEpoch: "owned", Runtime: json.RawMessage(`{"enabled":true}`), Runs: map[string]*durable.Run{}}
	if ready, err := autoSignInDeletedReady(state); err != nil || ready {
		t.Fatal("config alone registered events", ready, err)
	}
	run := &durable.Run{ID: "init", Scope: scope, Action: "init", Phase: "running", Lanes: map[string][]durable.Entry{"main": {{Kind: "service.definitions", Phase: "applied"}}}}
	state.Runs[run.ID] = run
	if ready, _ := autoSignInDeletedReady(state); ready {
		t.Fatal("partial init registered events")
	}
	run.Phase = "completed"
	if ready, err := autoSignInDeletedReady(state); err != nil || !ready {
		t.Fatal("completed source init did not register", err)
	}
	state.Runtime = json.RawMessage(`{"enabled":false}`)
	if ready, _ := autoSignInDeletedReady(state); ready || !autoSignInDeletedInitialized(state) {
		t.Fatal("source stop must disable new events but preserve admitted receipts")
	}
	state.RuntimeEpoch = "revoked"
	if autoSignInDeletedInitialized(state) {
		t.Fatal("old runtime epoch accepted")
	}
}

func TestAutoSignInDeletedOriginalNumericCoercionAndAll(t *testing.T) {
	// The installed source intentionally compares int(site), including numeric
	// custom strings. The host must not convert the trusted numeric event to text.
	engine := source.New(source.Inventory{}, time.Now)
	engine.Enabled = true
	engine.Config = source.Object{"sign_sites": []any{json.Number("1"), "1", json.Number("2")}, "login_sites": []any{"all"}}
	engine.Data = source.Object{"history": []any{"preserved"}}
	if err := engine.SiteDeleted(json.Number("1")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(engine.SignSites, []any{json.Number("2")}) || !reflect.DeepEqual(engine.LoginSites, []any{"all"}) || !reflect.DeepEqual(engine.Data, source.Object{"history": []any{"preserved"}}) {
		t.Fatal("source deletion order/coercion/history changed", engine.SignSites, engine.LoginSites, engine.Data)
	}
}

func TestAutoSignInDeletedNativeBatchSDKClonesAndRevocation(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	if signinDeletedHTTP(t, a, cookie, "POST", "/api/sites/manage", identityTestSite("synthetic-signin-delete", "signin-delete.example.com")) != 200 {
		t.Fatal("create synthetic site")
	}
	site := a.Settings().Sites[0]
	identity := identityRequire(t, a, site.ID)
	if signinDeletedHTTP(t, a, cookie, "POST", "/api/sites/manage", identityTestSite("synthetic-signin-delete-two", "signin-delete-two.example.com")) != 200 {
		t.Fatal("create second synthetic site")
	}
	var second SiteConfig
	for _, candidate := range a.Settings().Sites {
		if candidate.ID != site.ID {
			second = candidate
		}
	}
	secondIdentity := identityRequire(t, a, second.ID)
	config := map[string]any{"enabled": true, "onlyonce": false, "notify": false, "cron": "0 0 * * *", "sign_sites": []any{"all"}, "login_sites": []any{"all"}}
	for _, instance := range []string{"AutoSignIn", "AutoSignIn_clone"} {
		autoSignInSDKInstall(t, a, instance, config)
	}
	if signinDeletedHTTP(t, a, cookie, "DELETE", "/api/sites/manage/"+site.ID, nil) != 200 {
		t.Fatal("delete synthetic site")
	}
	if signinDeletedHTTP(t, a, cookie, "DELETE", "/api/sites/manage/"+second.ID, nil) != 200 {
		t.Fatal("delete second synthetic site")
	}
	at := time.Now().Add(time.Second)
	consumers, err := a.pluginStore.activeEventSubscriptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(consumers) != 2 {
		t.Fatal("clone subscriptions", len(consumers))
	}
	for _, sub := range consumers {
		if err = a.pluginStore.collectPluginEvents(ctx, sub, at); err != nil {
			t.Fatal(err)
		}
		if err = a.pluginStore.collectPluginEvents(ctx, sub, at); err != nil {
			t.Fatal(err)
		}
	}
	if err = a.pluginStore.enqueueDuePluginEvents(ctx, at); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_event_deliveries WHERE handler_key='site_deleted'`).Scan(&count); err != nil || count != 4 {
		t.Fatal("delivery dedup", count, err)
	}
	seen := map[string]bool{}
	for range 4 {
		run, err := a.pluginStore.ClaimRun(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var inv plugins.Invocation
		if cleanupJournalStrict(run.Input, &inv) != nil {
			t.Fatal("invocation")
		}
		command, err := a.autoSignInTrustedSiteDeleted(ctx, run, inv)
		if err != nil || command.Action != "site_deleted" || (!reflect.DeepEqual(command.DeletedID, identity.publicSourceID()) && !reflect.DeepEqual(command.DeletedID, secondIdentity.publicSourceID())) {
			t.Fatal("native numeric provenance", command, err)
		}
		key := run.InstanceID + ":" + command.DeletedID.(json.Number).String()
		if seen[key] {
			t.Fatal("duplicate source event execution", key)
		}
		seen[key] = true
		forged := inv
		forged.Input = json.RawMessage(`{"site_id":1}`)
		if _, err = a.autoSignInTrustedSiteDeleted(ctx, run, forged); err == nil {
			t.Fatal("SDK raw ID accepted")
		}
		other := run
		other.InstanceID = "AutoSignIn_other"
		if _, err = a.autoSignInTrustedSiteDeleted(ctx, other, inv); err == nil {
			t.Fatal("foreign owner accepted")
		}
		if _, err = a.Store.DB.Exec(`UPDATE plugin_installations SET desired_enabled=0 WHERE instance_id=?`, run.InstanceID); err != nil {
			t.Fatal(err)
		}
		if _, err = a.autoSignInTrustedSiteDeleted(ctx, run, inv); err == nil {
			t.Fatal("revoked generation consumed")
		}
		if _, err = a.Store.DB.Exec(`UPDATE plugin_installations SET desired_enabled=1 WHERE instance_id=?`, run.InstanceID); err != nil {
			t.Fatal(err)
		}
		a.executePluginRun(ctx, run)
		final, err := a.pluginStore.Run(ctx, run.ID)
		if err != nil || final.State != "succeeded" {
			t.Fatal("SDK deletion", final.State, final.ErrorCode, err)
		}
		state := autoSignInSDKState(t, a, run.InstanceID)
		if state.Runs[run.ID] == nil || state.Runs[run.ID].Action != "site_deleted" {
			t.Fatal("source receipt missing")
		}
		if _, err = a.autoSignInTrustedSiteDeleted(ctx, run, inv); err == nil {
			t.Fatal("terminal invocation replay accepted")
		}
	}
}

func TestAutoSignInDeletedCursorMissingNullAndMalformed(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		want   int64
		reject bool
	}{
		{"", 9, false}, {"null", 9, false}, {" null ", 9, false}, {"{}", 9, false}, {`{"event_after":0}`, 0, false}, {`{"event_after":9}`, 9, false},
		{" ", 0, true}, {"{", 0, true}, {"[]", 0, true}, {`{"event_after":10}`, 0, true}, {`{"event_after":-1}`, 0, true}, {`{"event_after":"1"}`, 0, true},
	} {
		got, err := autoSignInDeletedCursor(json.RawMessage(tc.raw), 9)
		if (err != nil) != tc.reject || (!tc.reject && got != tc.want) {
			t.Fatalf("%q: %d %v", tc.raw, got, err)
		}
	}
}

func signinDeletedHTTP(t *testing.T, a *App, cookie *http.Cookie, method, path string, value any) int {
	t.Helper()
	var body []byte
	var err error
	if value != nil {
		body, err = json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
	}
	response := callTest(t, a.Handler(), method, path, string(body), cookie, "")
	if response.Code != 200 {
		t.Logf("synthetic site %s %s returned %d: %s", method, path, response.Code, response.Body.String())
	}
	return response.Code
}

func TestAutoSignInDeletedBackendRequiresExactPrivatelyBoundSourcePlan(t *testing.T) {
	binding := durable.Binding{Scope: durable.Scope{Instance: "AutoSignIn", Generation: 1, Revision: 1, Implementation: "synthetic"}, Epoch: "owned"}
	state, err := durable.NewState(binding.Scope, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	state.Epoch = binding.Epoch
	before, err := signinCells(state)
	if err != nil {
		t.Fatal(err)
	}
	command := durable.Command{Action: "site_deleted", DeletedID: json.Number("7")}
	plan, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	core := PluginRun{ID: "pr_synthetic", InstanceID: binding.Instance, Generation: 1, ConfigRevision: 1, Action: "event"}
	backend := &pluginAutoSignInBackend{owner: &pluginAutoSignInOwner{binding: binding}, run: &core}
	sourceRun := &durable.Run{ID: core.ID, Action: "site_deleted", Epoch: binding.Epoch, Scope: binding.Scope, Phase: "running", Plan: plan, Lanes: map[string][]durable.Entry{}}
	state.Runs[core.ID] = sourceRun
	state.ActiveRun = core.ID
	if err = backend.allowedDelta(before, state); err == nil {
		t.Fatal("unbound event became site_deleted")
	}
	backend.sourcePlan = append(json.RawMessage(nil), plan...)
	if err = backend.allowedDelta(before, state); err != nil {
		t.Fatal("exact private source plan denied", err)
	}
	command.DeletedID = json.Number("8")
	sourceRun.Plan, _ = json.Marshal(command)
	if err = backend.allowedDelta(before, state); err == nil {
		t.Fatal("replacement numeric ID accepted")
	}
	command.DeletedID = "7"
	sourceRun.Plan, _ = json.Marshal(command)
	if err = backend.allowedDelta(before, state); err == nil {
		t.Fatal("replacement string ID accepted")
	}
	sourceRun.Plan = plan
	core.Action = "api"
	if err = backend.allowedDelta(before, state); err == nil {
		t.Fatal("private event mapping expanded to unrelated action")
	}
}

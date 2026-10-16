package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"light-media/internal/plugins"
	"light-media/internal/plugins/subscribegroup"
)

func subscribeSDKExecute(t *testing.T, a *App, run PluginRun) PluginRun {
	t.Helper()
	a.executePluginRun(context.Background(), run)
	finished, err := a.pluginStore.Run(context.Background(), run.ID)
	if err != nil || finished.State != "succeeded" {
		t.Fatal("actual native worker failed", finished.State, finished.ErrorCode, err)
	}
	return finished
}

func TestSubscribeGroupAPIRealWorkerInitializesClearsAndProcessesDownload(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	settings := a.Settings()
	settings.AllowedRoots = []string{t.TempDir()}
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	if response := callTest(t, a.Handler(), "POST", "/api/plugins/install", `{"plugin_id":"SubscribeGroup"}`, cookie, ""); response.Code != 200 {
		t.Fatal("native plugin could not be installed", response.Code, response.Body.String())
	}
	subscribeSDKExecute(t, a, subscribeGroupClaim(t, a))
	form := callTest(t, a.Handler(), "GET", "/api/plugins/SubscribeGroup/form", "", cookie, "")
	var actualForm struct {
		Data struct {
			Conf  json.RawMessage `json:"conf"`
			Model json.RawMessage `json:"model"`
		} `json:"data"`
	}
	var source map[string]json.RawMessage
	json.Unmarshal(subscribegroup.Form(), &source)
	if form.Code != 200 || json.Unmarshal(form.Body.Bytes(), &actualForm) != nil {
		t.Fatal("source form unavailable", form.Code, form.Body.String())
	}
	var gotConf, wantConf any
	json.Unmarshal(actualForm.Data.Conf, &gotConf)
	json.Unmarshal(source["conf"], &wantConf)
	gotConfJSON, _ := json.Marshal(gotConf)
	wantConfJSON, _ := json.Marshal(wantConf)
	if string(gotConfJSON) != string(wantConfJSON) {
		t.Fatal("actual subprocess form differs from original")
	}
	for _, key := range []string{"history", "history_handle"} {
		if _, err := a.pluginStore.SaveData(ctx, "SubscribeGroup", key, 0, []string{"synthetic-old"}); err != nil {
			t.Fatal(err)
		}
	}
	config := subscribeDownloadConfig()
	config.Clear, config.ClearHandle = true, true
	request, _ := json.Marshal(map[string]any{"revision": 1, "config": config})
	if response := callTest(t, a.Handler(), "PUT", "/api/plugins/SubscribeGroup/config", string(request), cookie, ""); response.Code != 200 {
		t.Fatal("source config could not be saved", response.Code, response.Body.String())
	}
	configured := subscribeSDKExecute(t, a, subscribeGroupClaim(t, a))
	stored, err := a.pluginStore.Config(ctx, "SubscribeGroup")
	var saved subscribegroup.Configuration
	if err != nil || json.Unmarshal(stored.Value, &saved) != nil || stored.Revision != 4 || configured.ConfigRevision != 4 || saved.Clear || saved.ClearHandle || !saved.Enabled {
		t.Fatal("worker did not commit both original flag writes", stored.Revision, configured.ConfigRevision, err)
	}
	for _, key := range []string{"history", "history_handle"} {
		if _, err := a.pluginStore.Data(ctx, "SubscribeGroup", key); !missingPluginValue(err) {
			t.Fatal("source clear flag did not clear only instance KV", key, err)
		}
	}
	requestSub := subscriptionRequest(t)
	requestSub.DownloadPath, requestSub.RootID = settings.AllowedRoots[0], ""
	sub, err := a.saveSubscription(ctx, "", requestSub)
	if err != nil {
		t.Fatal(err)
	}
	run, _ := subscribeDownloadRun(t, a, subscribeDownloadSnapshot())
	subscribeSDKExecute(t, a, run)
	updated := subscribeGroupRead(t, a, sub.ID)
	if updated.Revision != sub.Revision+1 || updated.Policy.FilterParams == nil || updated.Policy.FilterParams.Include == nil || *updated.Policy.FilterParams.Include != "Custom.+Team" {
		t.Fatal("actual event subprocess did not update core subscription", updated)
	}
	page := callTest(t, a.Handler(), "GET", "/api/plugins/SubscribeGroup/page", "", cookie, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), `"render_mode":"vuetify"`) || !strings.Contains(page.Body.String(), "种子下载自定义配置") || !strings.Contains(page.Body.String(), "Custom.+Team") {
		t.Fatal("real subprocess history page missing", page.Code, page.Body.String())
	}
	if response := callTest(t, a.Handler(), "GET", "/api/plugins/SubscribeGroup/page", "", nil, ""); response.Code != 401 {
		t.Fatal("history page was public", response.Code)
	}
	// Source state is enabled OR category; category-only must remain enabled.
	config = subscribegroup.Configuration{Category: true}
	request, _ = json.Marshal(map[string]any{"revision": stored.Revision, "config": config})
	if response := callTest(t, a.Handler(), "PUT", "/api/plugins/SubscribeGroup/config", string(request), cookie, ""); response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	subscribeSDKExecute(t, a, subscribeGroupClaim(t, a))
	cards, err := a.pluginCards(ctx, false)
	if err != nil || len(cards) != 1 || !cards[0].State {
		t.Fatal("category-only source state was disabled", cards, err)
	}
	history, err := a.pluginStore.Data(ctx, "SubscribeGroup", "history")
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := a.pluginStore.SaveData(ctx, "SubscribeGroup", "history", history.Revision, json.RawMessage(`{"name":"legacy-object","content":"\\u4e2d"}`))
	if err != nil {
		t.Fatal(err)
	}
	page = callTest(t, a.Handler(), "GET", "/api/plugins/SubscribeGroup/page", "", cookie, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "legacy-object") || !strings.Contains(page.Body.String(), "中") {
		t.Fatal("legacy single-object history hidden", page.Code, page.Body.String())
	}
	if _, err := a.pluginStore.SaveData(ctx, "SubscribeGroup", "history", legacy.Revision, json.RawMessage(`"bad-source-history"`)); err != nil {
		t.Fatal(err)
	}
	page = callTest(t, a.Handler(), "GET", "/api/plugins/SubscribeGroup/page", "", cookie, "")
	if page.Code == 200 || strings.Contains(page.Body.String(), "暂无数据") {
		t.Fatal("original page exception was reported as empty success", page.Code, page.Body.String())
	}
	currentHistory, err := a.pluginStore.Data(ctx, "SubscribeGroup", "history")
	if err != nil {
		t.Fatal(err)
	}
	large := []map[string]string{{"name": "large-valid-history", "content": strings.Repeat("x", 1<<20), "time": "2026-10-16"}}
	largeStored, err := a.pluginStore.SaveData(ctx, "SubscribeGroup", "history", currentHistory.Revision, large)
	if err != nil {
		t.Fatal(err)
	}
	page = callTest(t, a.Handler(), "GET", "/api/plugins/SubscribeGroup/page", "", cookie, "")
	if page.Code != 200 || !strings.Contains(page.Body.String(), large[0]["content"]) || !strings.Contains(page.Body.String(), "large-valid-history") {
		t.Fatal("large full history was hidden or truncated", page.Code, page.Body.Len())
	}
	kept, err := a.pluginStore.Data(ctx, "SubscribeGroup", "history")
	if err != nil || kept.Revision != largeStored.Revision || string(kept.Value) != string(largeStored.Value) {
		t.Fatal("large-history view altered stored history", err)
	}
}

func TestSubscribeGroupRPCRejectsPatchesViewWritesAndForeignIdentity(t *testing.T) {
	a := subscriptionTestApp(t)
	ctx := context.Background()
	run := subscribeGroupInstall(t, a, subscribeDownloadConfig())
	installation, err := a.pluginStore.Installation(ctx, run.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	identity := plugins.Identity{PluginID: installation.PluginID, InstanceID: installation.InstanceID, Version: installation.Version}
	for _, request := range []struct{ method, raw string }{
		{"subscriptions.rules.configure", `{"config":{"enabled":true}}`},
		{"subscriptions.rules.apply", `{"batch_id":"pb_other","patch":{"include":"evil"}}`},
		{"subscriptions.rules.apply", `{"batch_id":"pb_other","metadata":{"team":"evil"}}`},
		{"subscriptions.rules.history", `{}`},
		{"shell.exec", `{}`},
	} {
		if _, err := a.pluginHostCall(ctx, installation, &run, identity, request.method, json.RawMessage(request.raw)); err == nil {
			t.Fatal("untrusted parameters gained authority", request.method)
		}
	}
	if _, err := a.pluginHostCall(ctx, installation, nil, identity, "subscriptions.rules.configure", json.RawMessage(`{}`)); pluginErrorCode(err) != "PLUGIN_CAPABILITY_DENIED" {
		t.Fatal("read-only config form gained write authority", err)
	}
	other := identity
	other.InstanceID = "OtherPlugin"
	if _, err := a.pluginHostCall(ctx, installation, &run, other, "subscriptions.rules.configure", json.RawMessage(`{}`)); pluginErrorCode(err) != "PLUGIN_IDENTITY_MISMATCH" {
		t.Fatal("foreign plugin identity accepted", err)
	}
	if _, err := a.pluginStore.subscribeGroupReceipt(ctx, run); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("denied calls wrote initialization receipt", err)
	}
}

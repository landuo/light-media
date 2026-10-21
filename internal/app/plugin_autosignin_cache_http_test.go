package app

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestPluginAutoSignInTjuptCacheHTTPAuthorizationAndReadOnly(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	for _, instance := range []string{"AutoSignIn", "AutoSignIn_clone"} {
		autoSignInSDKInstall(t, a, instance, map[string]any{"enabled": false, "onlyonce": false, "sign_sites": []any{}, "login_sites": []any{}})
	}
	before := autoSignInSDKState(t, a, "AutoSignIn")
	other := autoSignInSDKState(t, a, "AutoSignIn_clone")
	config, err := a.pluginStore.Config(context.Background(), "AutoSignIn")
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/plugins/AutoSignIn/tjupt-cache"
	for _, method := range []string{"GET", "POST"} {
		response := callTest(t, a.Handler(), method, path, `{}`, nil, "")
		if response.Code != 401 {
			t.Fatalf("anonymous %s: %d", method, response.Code)
		}
	}
	response := callTest(t, a.Handler(), "GET", path, "", cookie, "")
	if response.Code != 200 {
		t.Fatalf("export: %d %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(response.Body.Bytes(), &envelope) != nil || !json.Valid(envelope.Data) {
		t.Fatal("invalid export envelope")
	}
	response = callTest(t, a.Handler(), "POST", path, string(envelope.Data), cookie, "http://evil.example")
	if response.Code != 403 {
		t.Fatalf("cross origin import: %d", response.Code)
	}
	response = callTest(t, a.Handler(), "POST", path, `{}`, cookie, "")
	if response.Code == 200 {
		t.Fatal("unbound import accepted")
	}
	afterConfig, err := a.pluginStore.Config(context.Background(), "AutoSignIn")
	if err != nil || !reflect.DeepEqual(config, afterConfig) {
		t.Fatal("cache read/rejected import changed config", err)
	}
	if !reflect.DeepEqual(before, autoSignInSDKState(t, a, "AutoSignIn")) || !reflect.DeepEqual(other, autoSignInSDKState(t, a, "AutoSignIn_clone")) {
		t.Fatal("cache read/rejected import changed state or clone")
	}
	var pending int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM plugin_runs WHERE state IN ('pending','running')`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("cache read/rejected import queued execution", pending, err)
	}
}

func TestPluginAutoSignInTjuptCacheHTTPImportIsolationAndReplay(t *testing.T) {
	a := newTestApp(t)
	useBuiltInPluginProcess(t, a)
	cookie := setupTestAdmin(t, a)
	for _, instance := range []string{"AutoSignIn", "AutoSignIn_clone"} {
		autoSignInSDKInstall(t, a, instance, map[string]any{"enabled": false, "onlyonce": false, "sign_sites": []any{}, "login_sites": []any{}})
	}
	other := autoSignInSDKState(t, a, "AutoSignIn_clone")
	before := autoSignInSDKState(t, a, "AutoSignIn")
	path := "/api/plugins/AutoSignIn/tjupt-cache"
	get := callTest(t, a.Handler(), "GET", path, "", cookie, "")
	var response struct {
		Data AutoSignInTjuptCacheTransfer `json:"data"`
	}
	if get.Code != 200 || json.Unmarshal(get.Body.Bytes(), &response) != nil {
		t.Fatal(get.Code, get.Body.String())
	}
	payload := []byte(" { \"synthetic-answer\" : \"option-value\" } \n")
	response.Data.Cache = AutoSignInTjuptCachePayload{Mode: "source-bytes", SourceSHA256: AutoSignInTjuptSourceSHA256, Key: "tjupt", Present: true, ReadyPresent: true, Ready: true, Bytes: payload, SHA256: sourceSettingsDigest(payload)}
	raw, err := json.Marshal(response.Data)
	if err != nil {
		t.Fatal(err)
	}
	cross := callTest(t, a.Handler(), "POST", "/api/plugins/AutoSignIn_clone/tjupt-cache", string(raw), cookie, "")
	if cross.Code == 200 {
		t.Fatal("cross-instance cache admitted")
	}
	post := callTest(t, a.Handler(), "POST", path, string(raw), cookie, "")
	if post.Code != 200 {
		t.Fatal(post.Code, post.Body.String())
	}
	var receipt struct {
		Data AutoSignInTjuptCacheReceipt `json:"data"`
	}
	if json.Unmarshal(post.Body.Bytes(), &receipt) != nil || receipt.Data.InstanceID != "AutoSignIn" || receipt.Data.JobsQueued != 0 || receipt.Data.ExternalCalls != 0 || receipt.Data.ConfigChanged || receipt.Data.RuntimeChanged {
		t.Fatal("invalid no-execution receipt", post.Body.String())
	}
	after := autoSignInSDKState(t, a, "AutoSignIn")
	before.Cache["tjupt"] = payload
	before.CacheReady["tjupt"] = true
	if !reflect.DeepEqual(before, after) {
		t.Fatal("import changed more than owned cache")
	}
	if !reflect.DeepEqual(other, autoSignInSDKState(t, a, "AutoSignIn_clone")) {
		t.Fatal("cache import mutated clone")
	}
	replay := callTest(t, a.Handler(), "POST", path, string(raw), cookie, "")
	if replay.Code == 200 {
		t.Fatal("stale destination proof replay accepted")
	}
	if !reflect.DeepEqual(after, autoSignInSDKState(t, a, "AutoSignIn")) {
		t.Fatal("rejected replay changed state")
	}
}

package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"light-media/internal/fileextensions"
	"light-media/internal/plugins"
)

func TestOriginalHostBusinessMediaExtensionsActualSDKFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-business-20261031/host-residual-review/rmt-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		SourceCommit string `json:"source_commit"`
		Cases        []struct {
			Case                                 string
			Configuration, Environment, Expected json.RawMessage
			Error                                string
		}
	}
	if json.Unmarshal(raw, &corpus) != nil || corpus.SourceCommit != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(corpus.Cases) != 15 {
		t.Fatal("fixed extension oracle missing")
	}
	for _, row := range corpus.Cases {
		t.Run(row.Case, func(t *testing.T) {
			// Native filesystem classification is string-only, while the settings
			// API and this source-shaped readonly getter preserve every list member.

			t.Setenv(fileextensions.MediaKey, "")
			if err := os.Unsetenv(fileextensions.MediaKey); err != nil {
				t.Fatal(err)
			}
			a := newTestApp(t)
			config := a.Settings()
			config.RMTMediaExt = row.Configuration
			if len(row.Environment) > 0 {
				ownedDownloaderSave(t, a, config)
				dir := a.Dir
				if err := a.Close(); err != nil {
					t.Fatal(err)
				}
				t.Setenv(fileextensions.MediaKey, string(row.Environment))
				a, err = New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = a.Close() })
			}
			cookie, worker := hostBusinessFixture(t, a)
			config = a.Settings()
			if len(row.Environment) == 0 {
				config.RMTMediaExt = row.Configuration
			}
			if row.Case == "unsupported_native_member" {
				body, _ := json.Marshal(config)
				response := callTest(t, a.Handler(), "PUT", "/api/settings", string(body), cookie, "")
				if response.Code != 200 {
					t.Fatal("actual native API rejected arbitrary list unexpectedly", response.Code, response.Body.String())
				}
				if !originalHostSubscriptionJSONEqual(a.Settings().RMTMediaExt, row.Configuration) {
					t.Fatal("actual native save dropped source members")
				}
			} else {
				a.config = config
			}
			var got plugins.SettingsHostResultV1
			err := hostBusinessCall(t, worker, plugins.SettingsReadCapability, plugins.SettingsHostRequestV1{Key: fileextensions.MediaKey}, &got)
			if row.Error != "" {
				if err == nil {
					t.Fatal("source validation error became extensions", string(got.Value))
				}
				return
			}
			if err != nil || !originalHostSubscriptionJSONEqual(got.Value, row.Expected) {
				t.Fatal("source extension values/order/case/default differ", string(got.Value), string(row.Expected), err)
			}
			snapshot, err := a.sourceFileExtensions(context.Background())
			if row.Case != "unsupported_native_member" && (err != nil || !originalHostSubscriptionJSONEqual(mustCleanSeedJSON(snapshot.Values(fileextensions.MediaKey)), row.Expected)) {
				t.Fatal("SDK and actual native classifier diverged", err)
			}
			if err := hostBusinessCall(t, worker, plugins.SettingsWriteCapability, plugins.SettingsHostWriteV1{Key: fileextensions.MediaKey, Value: json.RawMessage(`[".forged"]`)}, &got); err == nil {
				t.Fatal("plugin changed host readonly extension list")
			}
			if err := hostBusinessCall(t, worker, plugins.SettingsReadCapability, plugins.SettingsHostRequestV1{Key: fileextensions.MediaKey}, &got); err != nil || !originalHostSubscriptionJSONEqual(got.Value, row.Expected) {
				t.Fatal("rejected write altered effective extensions", err)
			}
		})
	}
}

func TestOriginalHostBusinessMediaExtensionsActualSDKColdAndClone(t *testing.T) {
	a := newTestApp(t)
	cookie, worker := hostBusinessFixture(t, a)
	config := a.Settings()
	config.RMTMediaExt = json.RawMessage(`[".owned",".UPPER",".owned"]`)
	ownedDownloaderSave(t, a, config)
	response := callTest(t, a.Handler(), "POST", "/api/plugins/OwnedHostBusiness/clone", `{"suffix":"extensions"}`, cookie, "")
	if response.Code != 200 {
		t.Fatal("clone failed", response.Code, response.Body.String())
	}
	cloneValue, ok := a.pluginRunningInstances.Load("OwnedHostBusinessextensions")
	if !ok {
		t.Fatal("clone absent")
	}
	clone := cloneValue.(*originalPluginInstance)
	var got plugins.SettingsHostResultV1
	for _, object := range []*originalPluginInstance{worker, clone} {
		if err := hostBusinessCall(t, object, plugins.SettingsReadCapability, plugins.SettingsHostRequestV1{Key: fileextensions.MediaKey}, &got); err != nil || !originalHostSubscriptionJSONEqual(got.Value, config.RMTMediaExt) {
			t.Fatal("clone sees a different global effective extension list", err)
		}
	}
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err := cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	cold.wg.Wait()
	value, ok := cold.pluginRunningInstances.Load("OwnedHostBusiness")
	if !ok {
		t.Fatal("cold worker absent")
	}
	if err := hostBusinessCall(t, value.(*originalPluginInstance), plugins.SettingsReadCapability, plugins.SettingsHostRequestV1{Key: fileextensions.MediaKey}, &got); err != nil || !originalHostSubscriptionJSONEqual(got.Value, config.RMTMediaExt) {
		t.Fatal("cold persisted effective extensions lost", err, string(got.Value))
	}
}

func TestOriginalHostBusinessMediaExtensionsStartupSnapshotAndLiveAPI(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/plugin-host-business-20261031/host-residual-review/rmt-source-oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		Live struct{ Initial, AfterEnvironmentChange, AfterAPI, Cold json.RawMessage } `json:"live_settings"`
	}
	// Source field names are snake_case; keep the boundary explicit.
	var document map[string]json.RawMessage
	if json.Unmarshal(raw, &document) != nil {
		t.Fatal("source document")
	}
	var live map[string]json.RawMessage
	if json.Unmarshal(document["live_settings"], &live) != nil {
		t.Fatal("source live settings")
	}
	oracle.Live.Initial = live["initial"]
	oracle.Live.AfterEnvironmentChange = live["after_environment_change"]
	oracle.Live.AfterAPI = live["after_api"]
	oracle.Live.Cold = live["cold"]
	if len(oracle.Live.Initial) == 0 || len(oracle.Live.Cold) == 0 {
		t.Fatal("actual source BaseSettings lifecycle observation missing")
	}
	t.Setenv(fileextensions.MediaKey, string(oracle.Live.Initial))
	a := newTestApp(t)
	cookie, worker := hostBusinessFixture(t, a)
	check := func(object *originalPluginInstance, want json.RawMessage) {
		t.Helper()
		var got plugins.SettingsHostResultV1
		if err := hostBusinessCall(t, object, plugins.SettingsReadCapability, plugins.SettingsHostRequestV1{Key: fileextensions.MediaKey}, &got); err != nil || !originalHostSubscriptionJSONEqual(got.Value, want) {
			t.Fatal("fixed source live extension setting differs", err, string(got.Value), string(want))
		}
	}
	check(worker, oracle.Live.Initial)
	t.Setenv(fileextensions.MediaKey, string(oracle.Live.Cold))
	check(worker, oracle.Live.AfterEnvironmentChange)
	config := a.Settings()
	config.RMTMediaExt = oracle.Live.AfterAPI
	body, _ := json.Marshal(config)
	response := callTest(t, a.Handler(), "PUT", "/api/settings", string(body), cookie, "")
	if response.Code != 200 {
		t.Fatal("runtime native settings save failed", response.Code, response.Body.String())
	}
	check(worker, oracle.Live.AfterAPI)
	dir := a.Dir
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cold, err := New(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer cold.Close()
	if err := cold.initializeOriginalPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	cold.wg.Wait()
	value, ok := cold.pluginRunningInstances.Load("OwnedHostBusiness")
	if !ok {
		t.Fatal("cold worker absent")
	}
	check(value.(*originalPluginInstance), oracle.Live.Cold)
}

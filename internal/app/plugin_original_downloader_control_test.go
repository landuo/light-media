package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"light-media/internal/download"
)

// Source results/ordering come from the actual fixed wrappers, dispatcher,
// signature check and endpoint bool expression. Native side effects here are
// a counted controlled boundary; separate main/protocol gates prove delivery.
func TestOriginalDownloaderControlActualSDKFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/downloader-control-oracle.json")
	var oracle struct {
		Cases []struct {
			Name, Action, Method string
			PluginResult         json.RawMessage `json:"plugin_result"`
			PluginFailure        bool            `json:"plugin_failure"`
			CoreResult           bool            `json:"core_result"`
			CoreFailure          bool            `json:"core_failure"`
			Success              bool
			Observations         []struct {
				Kind   string
				Kwargs json.RawMessage
			}
			Roles []string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 12 {
		t.Fatal("fixed downloader control source oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, cookie, object := ownedModuleFixture(t, true)
			method := "module_execute"
			if sample.PluginFailure {
				method = "module_fail"
			}
			config, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{sample.Method: method}, "result": sample.PluginResult})
			saveOwnedModule(t, a, cookie, object, string(config))
			object = originalBaseSDKLoaded(t, a, ownedModuleID)
			before := ownedModuleRead(t, object)
			for len(a.systemMessages.get("system")) > 0 {
			}
			coreCalls := 0
			_, err := a.originalDownloaderControl(context.Background(), DownloaderConfig{Config: download.Config{ID: "owned-downloader", Kind: "qb"}, Name: "Owned downloader"}, "0123456789012345678901234567890123456789", sample.Action, func() error {
				coreCalls++
				if sample.CoreFailure {
					return errors.New("owned core control failure")
				}
				if !sample.CoreResult {
					return &download.Error{Operation: "control", Code: "rejected"}
				}
				return nil
			})
			if (err == nil) != sample.Success {
				t.Fatal("fixed caller truth changed", err, sample.Success)
			}
			wantCore := 0
			var want json.RawMessage
			for _, observation := range sample.Observations {
				if observation.Kind == "core" {
					wantCore++
				} else if observation.Kind == "plugin" {
					want = observation.Kwargs
				}
			}
			after := ownedModuleRead(t, object)
			var actual struct {
				Args   []json.RawMessage
				Kwargs json.RawMessage
			}
			if coreCalls != wantCore || after.Calls[method]-before.Calls[method] != 1 || json.Unmarshal(after.Inputs[method], &actual) != nil || actual.Args == nil || len(actual.Args) != 0 || !originalBaseJSONEqual(actual.Kwargs, want) {
				t.Fatal("actual SDK control method/kwargs/core order", coreCalls, wantCore, string(actual.Kwargs), string(want))
			}
			a.wg.Wait()
			for _, role := range sample.Roles {
				var message systemMessage
				if json.Unmarshal(a.systemMessages.get("system"), &message) != nil || message.Type != role {
					t.Fatal("source exception role", role, message.Type)
				}
			}
			if len(a.systemMessages.get("system")) != 0 {
				t.Fatal("unexpected source exception helper message")
			}
		})
	}
}

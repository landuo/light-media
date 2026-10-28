package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestOriginalTransferInfoConstructorActualSDKFallback(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-info-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name            string
			Input           json.RawMessage
			ValidationError *string `json:"validation_error"`
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bad_success_two", "bad_integer_fraction", "item_bad_path_number", "veto"} {
		t.Run(name, func(t *testing.T) {
			var input json.RawMessage
			invalid := false
			for _, c := range oracle.Cases {
				if c.Name == name {
					input = c.Input
					invalid = c.ValidationError != nil
				}
			}
			if len(input) == 0 {
				t.Fatal("fixed constructor case absent")
			}
			a, object, plan, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": "module_execute"}
			cfg["result"], cfg["build_transfer_info"] = input, true
			encoded, _ := json.Marshal(cfg)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, encoded); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			job := overwriteStart(t, a, plan)
			err := a.runImport(ctx, job, plan.ID, nil)
			if (err == nil) != invalid {
				t.Fatal("source constructor error must enter native fallback; valid typed veto stops", invalid, err)
			}
			_, statErr := os.Stat(plan.Files[0].Target)
			if (statErr == nil) != invalid {
				t.Fatal("actual constructor/native physical outcome", statErr)
			}
			a.wg.Wait()
			var observed struct {
				Trace  []string `json:"transfer_trace"`
				Events []struct {
					Type string
					Data struct {
						Info map[string]any `json:"transferinfo"`
					}
				} `json:"transfer_events"`
			}
			if err := object.readSession(ctx, "snapshot", &observed); err != nil {
				t.Fatal(err)
			}
			wantEvent := "transfer.failed"
			wantTrace := 3
			if invalid {
				wantEvent = "transfer.complete"
				wantTrace = 5
			}
			if len(observed.Trace) != wantTrace || observed.Trace[0] != "storage.operation" || observed.Trace[1] != "storage.operation" || observed.Trace[2] != "module_execute" || len(observed.Events) != 1 || observed.Events[0].Type != wantEvent {
				t.Fatal("actual safe-invoke constructor/native callback", observed)
			}
			if !invalid && observed.Events[0].Data.Info["need_scrape"] != nil {
				t.Fatal("explicit nullable flag lost", observed)
			}
			var count int
			if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='asset'`).Scan(&count); err != nil || count != map[bool]int{true: 1, false: 0}[invalid] {
				t.Fatal("actual constructor asset result", count, err)
			}
		})
	}
}

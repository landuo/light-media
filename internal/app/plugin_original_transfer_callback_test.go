package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalTransferCallbackFixedSourceFieldsAndHistory(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-callback-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Kind       string
			Success    bool
			NullTarget bool `json:"null_target"`
			Trace      []string
			History    struct {
				Status int
				Dest   *string
				Mode   *string
			}
			Events []struct {
				Type         string
				TransferInfo json.RawMessage `json:"transferinfo"`
			}
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 9 {
		t.Fatal("fixed callback prefix oracle", err)
	}
	for _, sample := range oracle.Cases {
		if sample.Kind != "main" {
			continue
		}
		name := "success"
		if sample.NullTarget {
			name += "-null"
		}
		if !sample.Success {
			name = "failed-null"
		}
		t.Run(name, func(t *testing.T) {
			if len(sample.Trace) != 2 || sample.Trace[0] != "history" || sample.Trace[1] != sample.Events[0].Type {
				t.Fatal("fixed history-before-event prefix", sample.Trace)
			}
			a, object, plan, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			var info map[string]any
			if json.Unmarshal(sample.Events[0].TransferInfo, &info) != nil {
				t.Fatal("fixed full TransferInfo")
			}
			info["fileitem"], info["file_list"] = originalTransferPreviewItem(plan.Files[0].Source, "file", 22), []string{plan.Files[0].Source}
			info["file_list_new"] = []string{plan.Files[0].Target}
			if !sample.NullTarget {
				info["target_item"] = originalTransferPreviewItem(plan.Files[0].Target, "file", 22)
			}
			cfg["result"] = map[string]any{plugins.ModuleTransferInfoKey: info}
			method := "module_transfer"
			if !sample.Success {
				method = "module_execute"
				cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": method}
			}
			encoded, _ := json.Marshal(cfg)
			if _, err = a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, encoded); err != nil {
				t.Fatal(err)
			}
			if err = a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			job := overwriteStart(t, a, plan)
			err = a.runImport(ctx, job, plan.ID, nil)
			if (err == nil) != sample.Success {
				t.Fatal("actual fixed callback branch", err)
			}
			history, err := a.transferHistoryItem(ctx, plan.Files[0].ID)
			if err != nil || (history.Status == "success") != (sample.History.Status == 1) {
				t.Fatal("actual committed source history", history, err)
			}
			if sample.History.Dest == nil && history.Target != "" || sample.History.Dest != nil && history.Target != plan.Files[0].Target {
				t.Fatal("source null/concrete returned history destination", history)
			}
			if sample.History.Mode == nil && history.Mode != "" || sample.History.Mode != nil && history.Mode != "hardlink" {
				t.Fatal("source explicit null/mode survives native history projection", history)
			}
			a.wg.Wait()
			var observed struct {
				Events []struct {
					Type string
					Data struct {
						History string         `json:"transfer_history_id"`
						Info    map[string]any `json:"transferinfo"`
					}
				} `json:"transfer_events"`
			}
			if err = object.readSession(ctx, "snapshot", &observed); err != nil || len(observed.Events) != 1 {
				t.Fatal("actual SDK callback count", len(observed.Events), err)
			}
			actual := observed.Events[0]
			if actual.Type != sample.Events[0].Type || actual.Data.History != history.ID || actual.Data.Info["file_count"] != float64(77) || actual.Data.Info["total_size"] != float64(999) || actual.Data.Info["transfer_type"] != info["transfer_type"] {
				t.Fatal("full provided callback values", actual.Type, actual.Data.History, actual.Data.Info)
			}
		})
	}
}

func TestOriginalTransferCallbackHistoryFailureCannotBroadcast(t *testing.T) {
	a, object, plan, _ := originalTransferModuleFixture(t, "hardlink", "")
	ctx := context.Background()
	if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_callback_failure BEFORE INSERT ON records WHEN NEW.kind='original_transfer_callback' BEGIN SELECT RAISE(ABORT,'owned_callback_failure'); END`); err != nil {
		t.Fatal(err)
	}
	job := overwriteStart(t, a, plan)
	if err := a.runImport(ctx, job, plan.ID, nil); err == nil {
		t.Fatal("real source history-store failure not reached")
	}
	a.wg.Wait()
	var observed struct {
		Events []json.RawMessage `json:"transfer_events"`
	}
	if err := object.readSession(ctx, "snapshot", &observed); err != nil || len(observed.Events) != 0 {
		t.Fatal("history failure must stop before ordinary success or failed event", len(observed.Events), err)
	}
	if _, err := os.Stat(plan.Files[0].Target); err != nil {
		t.Fatal("physical publication is retained after history failure", err)
	}
	if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_callback_failure`); err != nil {
		t.Fatal(err)
	}
	if err := a.runImport(ctx, job, plan.ID, nil); err != nil {
		t.Fatal("history-only recovery from published receipt", err)
	}
	a.wg.Wait()
	if err := object.readSession(ctx, "snapshot", &observed); err != nil || len(observed.Events) != 1 || ownedModuleRead(t, object).Calls["module_transfer"] != 1 {
		t.Fatal("history recovery repeats physical provider or misses callback", len(observed.Events), err)
	}
}

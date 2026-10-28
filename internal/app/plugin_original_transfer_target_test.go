package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func configureOriginalTransferTarget(t *testing.T, a *App, object *originalPluginInstance, config map[string]any, target string) {
	t.Helper()
	config["transfer_target"] = target
	config["result"] = map[string]any{plugins.ModuleTransferInfoKey: map[string]any{"success": true, "need_scrape": false, "target_item": originalTransferPreviewItem(target, "file", 22), "target_diritem": originalTransferPreviewItem(filepath.Dir(target), "dir", 0), "file_list_new": []string{target}}}
	raw, _ := json.Marshal(config)
	if _, err := a.pluginStore.saveOriginalPluginConfiguration(context.Background(), object.installation, raw); err != nil {
		t.Fatal(err)
	}
	if err := a.initOriginalLoadedPlugin(context.Background(), object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
}

func TestOriginalTransferModuleActualReturnedTarget(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-target-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name           string
			RelativeTarget string `json:"relative_target"`
			Content        string
			History        struct{ Dest string }
			Events         []struct{ Type string }
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 2 {
		t.Fatal("fixed successful history oracle", err)
	}
	for _, sample := range []struct {
		name, mode, worker string
		rollback           bool
	}{{"link", "hardlink", "", false}, {"copy", "copy", "copy", false}, {"transaction_retry", "hardlink", "", true}} {
		t.Run(sample.name, func(t *testing.T) {
			key := sample.name
			if sample.rollback {
				key = "link"
			}
			var relative, content, event string
			for _, row := range oracle.Cases {
				if row.Name == key {
					relative = strings.TrimPrefix(row.History.Dest, "{root}/library/")
					content = row.Content
					if len(row.Events) > 0 {
						event = row.Events[0].Type
					}
					if relative != row.RelativeTarget {
						t.Fatal("fixed target history trace diverged")
					}
				}
			}
			if relative == "" || event == "" {
				t.Fatal("missing fixed history/event case", key)
			}
			a, object, plan, cfg := originalTransferModuleFixture(t, sample.mode, sample.worker)
			ctx := context.Background()
			mapped := filepath.Join(a.Settings().Roots[0].Path, relative)
			configureOriginalTransferTarget(t, a, object, cfg, mapped)
			job := overwriteStart(t, a, plan)
			if sample.rollback {
				if _, err := a.Store.DB.Exec(`CREATE TRIGGER owned_target_transition_failure BEFORE UPDATE ON records WHEN NEW.kind='import_plan' AND json_extract(NEW.payload,'$.files[0].target')!=json_extract(OLD.payload,'$.files[0].target') BEGIN SELECT RAISE(ABORT,'owned_target_transition_failure'); END`); err != nil {
					t.Fatal(err)
				}
				if err := a.runImport(ctx, job, plan.ID, nil); err == nil {
					t.Fatal("actual target transaction did not fail")
				}
				var held ImportPlan
				var receipt importReceipt
				if err := a.Store.Get(ctx, "import_plan", plan.ID, &held); err != nil || held.Files[0].Target != plan.Files[0].Target {
					t.Fatal("atomic plan rollback", held, err)
				}
				if err := a.Store.Get(ctx, "import_receipt", plan.ID+"/"+plan.Files[0].ID, &receipt); err != nil || receipt.Target != plan.Files[0].Target || receipt.State != "planned" || len(receipt.OriginalModuleResult) == 0 {
					t.Fatal("frozen result retained before adoption", receipt, err)
				}
				var meta importMetadata
				if err := a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
					t.Fatal(err)
				}
				if err := a.requireNativeDirectoryAuthorizationTx(ctx, a.Store.DB, held, meta); err != nil {
					t.Fatal("old authority preserved", err)
				}
				if _, err := a.Store.DB.Exec(`DROP TRIGGER owned_target_transition_failure`); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.runImport(ctx, job, plan.ID, nil); err != nil {
				t.Fatal("adopt actual returned local target", err)
			}
			var actual ImportPlan
			if err := a.Store.Get(ctx, "import_plan", plan.ID, &actual); err != nil || actual.Files[0].Target != mapped || actual.Files[0].Status != "imported" {
				t.Fatal(actual, err)
			}
			if _, err := os.Stat(plan.Files[0].Target); !os.IsNotExist(err) {
				t.Fatal("native destination unexpectedly published", err)
			}
			source, err := os.Stat(plan.Files[0].Source)
			if err != nil {
				t.Fatal(err)
			}
			target, err := os.Stat(mapped)
			if err != nil || source.Size() != target.Size() || sample.mode == "hardlink" && !os.SameFile(source, target) || sample.mode == "copy" && os.SameFile(source, target) {
				t.Fatal("actual method and bytes", source, target, err)
			}
			b, err := os.ReadFile(mapped)
			if err != nil || string(b) != content {
				t.Fatal("actual content", string(b), err)
			}
			var receipt importReceipt
			if err := a.Store.Get(ctx, "import_receipt", plan.ID+"/"+plan.Files[0].ID, &receipt); err != nil || receipt.Target != mapped || receipt.State != "published" {
				t.Fatal("actual publication receipt", receipt, err)
			}
			var asset Asset
			if err := a.Store.Get(ctx, "asset", actual.Files[0].AssetID, &asset); err != nil || asset.Path != mapped {
				t.Fatal("actual asset consumes source result", asset, err)
			}
			history, err := a.transferHistoryExecution(ctx, plan.Files[0].ID)
			if err != nil || history.Target != mapped {
				t.Fatal("actual history consumes source target", history, err)
			}
			a.wg.Wait()
			if calls := ownedModuleRead(t, object).Calls["module_transfer"]; calls != 1 {
				t.Fatal("publication module replay", calls)
			}
			var observed struct {
				Events []struct {
					Type string
					Data struct {
						Info struct {
							Target struct{ Path string } `json:"target_item"`
						} `json:"transferinfo"`
					}
				} `json:"transfer_events"`
			}
			if err := object.readSession(ctx, "snapshot", &observed); err != nil || len(observed.Events) != 1 || observed.Events[0].Type != event || observed.Events[0].Data.Info.Target.Path != mapped {
				t.Fatal("actual returned model completion callback", observed, err)
			}
		})
	}
}

func TestOriginalTransferReturnedTargetOwnershipBoundary(t *testing.T) {
	for _, kind := range []string{"outside_root", "already_managed"} {
		t.Run(kind, func(t *testing.T) {
			a, object, plan, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			mapped := filepath.Join(a.Settings().Roots[0].Path, "Guarded.mkv")
			if kind == "outside_root" {
				mapped = filepath.Join(t.TempDir(), "Outside.mkv")
				cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "transfer": "module_execute"}
			} else {
				if err := os.Link(plan.Files[0].Source, mapped); err != nil {
					t.Fatal(err)
				}
				stamp, err := snapshotPath(mapped)
				if err != nil {
					t.Fatal(err)
				}
				if err := a.Store.Put(ctx, "managed_file", tokenHash(mapped), managedFile{Path: mapped, RootID: plan.RootID, AssetID: "foreign-owned-asset", Kind: "video", Snapshot: stamp}); err != nil {
					t.Fatal(err)
				}
				cfg["storage_mode"] = "no_effect"
			}
			configureOriginalTransferTarget(t, a, object, cfg, mapped)
			job := overwriteStart(t, a, plan)
			if err := a.runImport(ctx, job, plan.ID, nil); fileCode(err) != "REVIEW_PLUGIN_TRANSFER_TARGET" {
				t.Fatal("local target ownership guard", fileCode(err), err)
			}
			var current ImportPlan
			if err := a.Store.Get(ctx, "import_plan", plan.ID, &current); err != nil || current.Files[0].Target != plan.Files[0].Target {
				t.Fatal("rejected target adopted", current, err)
			}
			if _, err := os.Stat(plan.Files[0].Target); !os.IsNotExist(err) {
				t.Fatal("native publisher ran after terminal plugin", err)
			}
			if kind == "already_managed" {
				var actual managedFile
				if err := a.Store.Get(ctx, "managed_file", tokenHash(mapped), &actual); err != nil || actual.AssetID != "foreign-owned-asset" {
					t.Fatal("foreign owner changed", actual, err)
				}
			}
			var assets int
			if err := a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM records WHERE kind='asset'`).Scan(&assets); err != nil || assets != 0 {
				t.Fatal("unverified asset", assets, err)
			}
		})
	}
}

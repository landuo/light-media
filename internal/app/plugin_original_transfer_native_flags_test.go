package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestOriginalTransferNativeFlagsFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-native-flags-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name   string
			Kwargs map[string]any
			Output map[string]any
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 15 {
		t.Fatal("fixed FileManager native flag selections", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			info := map[string]any{}
			if err := originalTransferApplyNativeFlags(info, sample.Kwargs, true); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"need_notify", "need_scrape"} {
				if info[key] != sample.Output[key] {
					t.Fatal("fixed native flag", key, info[key], sample.Output[key])
				}
			}
		})
	}
}

func TestOriginalTransferNativeWithoutOverrideFlagsAndScrape(t *testing.T) {
	for _, mode := range []string{"unspecified", "false", "true"} {
		t.Run(mode, func(t *testing.T) {
			a, object, first, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			cfg["modules"] = map[string]string{"recognize_media": "module_search_media"}
			raw, _ := json.Marshal(cfg)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			var scrape *bool
			if mode != "unspecified" {
				scrape = importFlag(mode == "true")
			}
			plan, err := a.previewImport(ctx, ImportRequest{MediaType: "movie", TMDBID: 123, RootID: "root", Mode: "hardlink", Scraping: scrape, Files: []ImportFileRequest{{Path: first.Files[0].Source}}})
			if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
				t.Fatal(plan, err)
			}
			before := ownedModuleRead(t, object)
			job := overwriteStart(t, a, plan)
			if err := a.runImport(ctx, job, plan.ID, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := a.finishJob(ctx, job, "succeeded", "", "", 0); err != nil {
				t.Fatal(err)
			}
			a.wg.Wait()
			var observed struct {
				Calls  map[string]int
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
			transfers := observed.Events[:0]
			for _, event := range observed.Events {
				if event.Type == "transfer.complete" {
					transfers = append(transfers, event)
				}
			}
			observed.Events = transfers
			if len(observed.Events) != 1 || observed.Events[0].Data.Info["need_notify"] != false || observed.Events[0].Data.Info["need_scrape"] != (mode == "true") || observed.Calls["module_transfer"] != before.Calls["module_transfer"] {
				t.Fatal("actual native callback without a transfer override", observed)
			}
			var saved ImportPlan
			if err := a.Store.Get(ctx, "import_plan", plan.ID, &saved); err != nil {
				t.Fatal(err)
			}
			history, err := a.transferHistoryItem(ctx, saved.Files[0].ID)
			if err != nil || history.Target != saved.Files[0].Target || history.Status != "success" {
				t.Fatal("fixed native callback preserves actual target FileItem in history", history, err)
			}
			var asset Asset
			if err := a.Store.Get(ctx, "asset", saved.Files[0].AssetID, &asset); err != nil {
				t.Fatal(err)
			}
			var jobs int
			if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='scrape' AND json_extract(payload,'$.id')=?`, asset.ID).Scan(&jobs); err != nil || jobs != map[bool]int{true: 1, false: 0}[mode == "true"] {
				t.Fatal("actual native scrape flag consumption", jobs, err)
			}
			if mode != "true" && asset.ScrapeState != "skipped" {
				t.Fatal("no scrape requested must not leave a pending asset", asset.ScrapeState)
			}
		})
	}
}

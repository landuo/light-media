package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins"
	"os"
	"path/filepath"
	"testing"
)

func TestOriginalTransferBlurayUnitPreviewAndPublish(t *testing.T) {
	for _, deny := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "plugin_veto"}[deny], func(t *testing.T) {
			a, object, original, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			source := filepath.Join(filepath.Dir(original.Files[0].Source), "Owned.2024.BluRay")
			importWrite(t, filepath.Join(source, "BDMV", "STREAM", "00001.m2ts"), []byte("owned stream"))
			importWrite(t, filepath.Join(source, "BDMV", "index.bdmv"), []byte("owned index"))
			cfg["modules"].(map[string]string)["transfer"] = "module_execute"
			cfg["result"] = nil
			if deny {
				cfg["result"] = false
			}
			raw, _ := json.Marshal(cfg)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			before := ownedModuleRead(t, object)
			out, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{source}, TargetPath: a.Settings().Roots[0].Path, Mode: "hardlink", MediaType: "movie", TMDBID: 123, Scraping: importFlag(false), Preview: true})
			if err != nil || len(out.Items) != 1 || (out.Failed == 1) != deny {
				t.Fatal("source directory is one task", out, err)
			}
			after := ownedModuleRead(t, object)
			if after.Calls["module_execute"] != before.Calls["module_execute"]+1 {
				t.Fatal("one whole-directory invocation", before.Calls, after.Calls)
			}
			var call struct{ Kwargs map[string]json.RawMessage }
			if json.Unmarshal(after.Inputs["module_execute"], &call) != nil {
				t.Fatal("actual kwargs")
			}
			var item map[string]any
			if json.Unmarshal(call.Kwargs["fileitem"], &item) != nil || item["type"] != "dir" || string(call.Kwargs["preview"]) != "true" {
				t.Fatal("whole source FileItem", item, call)
			}
			if !deny {
				want := filepath.Join(a.Settings().Roots[0].Path, "Owned (2024)")
				if out.Items[0].Target != want || out.Items[0].Status != "ready" {
					t.Fatal("movie directory preview source media root", out)
				}
				if _, err := os.Stat(want); !os.IsNotExist(err) {
					t.Fatal("preview created directory", err)
				}
			}
			var jobs int
			if err := a.Store.DB.QueryRow(`SELECT count(*) FROM jobs`).Scan(&jobs); err != nil || jobs != 0 {
				t.Fatal("preview created jobs", jobs, err)
			}
			if !deny {
				out, err = a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{source}, TargetPath: a.Settings().Roots[0].Path, Mode: "hardlink", MediaType: "movie", TMDBID: 123, Scraping: importFlag(false)})
				if err != nil || out.Failed != 0 || out.Submitted != 1 {
					t.Fatal("one actual directory task", out, err)
				}
				var plan ImportPlan
				if err = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); err != nil {
					t.Fatal(err)
				}
				job := overwriteStart(t, a, plan)
				if err = a.runImport(ctx, job, plan.ID, nil); err != nil {
					t.Fatal("native directory publication", err)
				}
				for _, relative := range []string{"BDMV/STREAM/00001.m2ts", "BDMV/index.bdmv"} {
					s, e := os.Stat(filepath.Join(source, relative))
					d, e2 := os.Stat(filepath.Join(out.Items[0].Target, relative))
					if e != nil || e2 != nil || !os.SameFile(s, d) {
						t.Fatal("actual directory member hardlink", relative, e, e2)
					}
				}
				a.wg.Wait()
				events := ownedModuleRead(t, object).TransferEvents
				if len(events) != 1 {
					t.Fatal("one directory result callback", len(events))
				}
				var event plugins.MutableEvent
				var payload struct {
					FileItem     map[string]any `json:"fileitem"`
					TransferInfo map[string]any `json:"transferinfo"`
				}
				if json.Unmarshal(events[0], &event) != nil || json.Unmarshal(event.Data, &payload) != nil || event.Type != "transfer.complete" {
					t.Fatal("source directory callback", string(events[0]))
				}
				if payload.FileItem["size"] != float64(12) || payload.TransferInfo["file_count"] != float64(0) || payload.TransferInfo["total_size"] != float64(0) {
					t.Fatal("fixed directory result uses STREAM size and leaves aggregate counters zero", string(events[0]))
				}
				if len(payload.TransferInfo["file_list"].([]any)) != 2 || len(payload.TransferInfo["file_list_new"].([]any)) != 2 {
					t.Fatal("recursive file list", payload.TransferInfo)
				}
			}
		})
	}
}

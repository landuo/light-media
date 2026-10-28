package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
	"light-media/internal/plugins"
)

func TestOriginalTransferBlurayDiscNamesFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/transfer-bluray-source-oracle.json")
	var oracle struct {
		Naming []struct {
			Part          string
			EffectiveName string `json:"effective_name"`
			Output        string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Naming) != 8 {
		t.Fatal("fixed source disc names", err)
	}
	for _, sample := range oracle.Naming {
		if got := originalTransferDiscFolder(sample.Part, sample.EffectiveName); got != sample.Output {
			t.Fatal(sample, got)
		}
	}
}

func TestOriginalTransferBlurayNativeFailureAndRecovery(t *testing.T) {
	for _, mode := range []string{"copy", "empty", "partial", "cancel", "source_changed", "foreign_member"} {
		t.Run(mode, func(t *testing.T) {
			method := "hardlink"
			if mode == "copy" {
				method = "copy"
			}
			a, object, original, cfg := originalTransferModuleFixture(t, method, "")
			ctx := context.Background()
			source := filepath.Join(filepath.Dir(original.Files[0].Source), "Owned.2024.BluRay")
			if err := os.MkdirAll(filepath.Join(source, "BDMV", "STREAM"), 0750); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(source, "CERTIFICATE"), 0750); err != nil {
				t.Fatal(err)
			}
			if mode != "empty" {
				importWrite(t, filepath.Join(source, "BDMV", "STREAM", "00001.m2ts"), []byte("owned stream"))
				importWrite(t, filepath.Join(source, "BDMV", "index.bdmv"), []byte("owned index"))
			}
			cfg["modules"].(map[string]string)["transfer"] = "module_execute"
			cfg["result"] = nil
			raw, _ := json.Marshal(cfg)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			out, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{source}, TargetPath: a.Settings().Roots[0].Path, Mode: method, MediaType: "movie", TMDBID: 123, Scraping: importFlag(false)})
			if err != nil || out.Failed != 0 || out.Submitted != 1 {
				t.Fatal("actual whole-directory preparation", out, err)
			}
			var plan ImportPlan
			if err = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); err != nil {
				t.Fatal(err)
			}
			job := overwriteStart(t, a, plan)
			links := 0
			a.importSourceLink = func(fd int, old string, targetFD int, name string, flags int) error {
				links++
				if mode == "partial" && links == 2 {
					return unix.EACCES
				}
				err := unix.Linkat(fd, old, targetFD, name, flags)
				if err != nil {
					return err
				}
				if links == 1 {
					switch mode {
					case "cancel":
						_, err = a.Store.DB.Exec(`UPDATE jobs SET state='cancelled',generation=generation+1 WHERE id=?`, job.ID)
					case "source_changed":
						err = os.WriteFile(filepath.Join(source, "BDMV", "index.bdmv"), []byte("changed index"), 0640)
					case "foreign_member":
						err = os.WriteFile(filepath.Join(out.Items[0].Target, "foreign.txt"), []byte("keep foreign member"), 0640)
					}
				}
				return err
			}
			err = a.runImport(ctx, job, plan.ID, nil)
			wantSuccess := mode == "copy" || mode == "empty"
			if (err == nil) != wantSuccess {
				t.Fatal("actual directory outcome", mode, err)
			}
			a.wg.Wait()
			before := ownedModuleRead(t, object)
			if mode == "cancel" {
				if !originalTransferInterrupted(err) || len(before.TransferEvents) != 0 {
					t.Fatal("cancelled generation emitted callback", err, len(before.TransferEvents))
				}
				dir := a.Dir
				if e := a.Close(); e != nil {
					t.Fatal(e)
				}
				a, err = New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { a.Close() })
				if e := a.initializeOriginalPlugins(ctx); e != nil {
					t.Fatal(e)
				}
				object = originalBaseSDKLoaded(t, a, ownedModuleID)
				before = ownedModuleRead(t, object)
				current, e := scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
				if e != nil {
					t.Fatal(e)
				}
				if _, e = a.retryOriginalScrapeJob(ctx, current); e != nil {
					t.Fatal(e)
				}
				if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, job.ID); e != nil {
					t.Fatal(e)
				}
				job, e = scanJob(a.Store.DB.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, job.ID))
				if e != nil {
					t.Fatal(e)
				}
				a.importSourceLink = unix.Linkat
				if e = a.runImport(ctx, job, plan.ID, nil); e != nil {
					t.Fatal("resume real native member receipts", e)
				}
				a.wg.Wait()
				after := ownedModuleRead(t, object)
				if before.Calls["module_execute"] != after.Calls["module_execute"] || len(after.TransferEvents) != 1 {
					t.Fatal("resume replayed provider or lost callback", before.Calls, after.Calls)
				}
				return
			}
			if len(before.TransferEvents) != 1 {
				t.Fatal("one directory callback", len(before.TransferEvents))
			}
			var event plugins.MutableEvent
			var data struct {
				Info map[string]any `json:"transferinfo"`
			}
			if json.Unmarshal(before.TransferEvents[0], &event) != nil || json.Unmarshal(event.Data, &data) != nil {
				t.Fatal("callback")
			}
			if data.Info["success"] != wantSuccess || data.Info["file_count"] != float64(0) || data.Info["total_size"] != float64(0) {
				t.Fatal("source directory counters", data.Info)
			}
			if mode == "partial" && len(data.Info["file_list"].([]any)) != 1 {
				t.Fatal("source retains successful prefix", data.Info)
			}
			if mode == "foreign_member" {
				raw, e := os.ReadFile(filepath.Join(out.Items[0].Target, "foreign.txt"))
				if e != nil || string(raw) != "keep foreign member" {
					t.Fatal("unknown target changed", e)
				}
			}
			if _, e := os.Stat(filepath.Join(out.Items[0].Target, "CERTIFICATE")); !os.IsNotExist(e) {
				t.Fatal("source recursion does not create empty child directories", e)
			}
		})
	}
}

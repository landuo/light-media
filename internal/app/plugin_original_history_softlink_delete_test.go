package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"light-media/internal/plugins"
)

func TestOriginalHistorySoftlinkDeleteActualSDK(t *testing.T) {
	cases := []struct {
		name                               string
		directory                          bool
		mode                               string
		result                             any
		both, broken, cold, unknown, asset bool
	}{
		{name: "leaf-native-delegate", mode: "no_effect"},
		{name: "leaf-physical-broken", mode: "effect", result: true, broken: true},
		{name: "leaf-false-retained", mode: "no_effect", result: false},
		{name: "leaf-true-without-effect", mode: "no_effect", result: true},
		{name: "leaf-both", mode: "effect", result: true, both: true},
		{name: "directory-native", directory: true, mode: "no_effect"},
		{name: "directory-physical-cold", directory: true, mode: "effect", result: true, cold: true},
		{name: "leaf-unknown-result", mode: "effect", result: true, unknown: true},
		{name: "directory-asset-metadata", directory: true, mode: "effect", result: true, asset: true},
		{name: "leaf-exception-fallback", mode: "effect_then_throw", result: true},
	}
	for _, sample := range cases {
		t.Run(sample.name, func(t *testing.T) {
			a, object, plan, config := originalTransferModuleFixture(t, "softlink", "")
			ctx := t.Context()
			config["modules"].(map[string]string)["transfer"] = "module_execute"
			config["result"] = nil
			saveConfig := func() {
				raw, _ := json.Marshal(config)
				if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
					t.Fatal(err)
				}
				if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
					t.Fatal(err)
				}
			}
			saveConfig()
			contents := map[string][]byte{plan.Files[0].Source: []byte("owned transfer content")}
			if sample.directory {
				source := filepath.Join(filepath.Dir(plan.Files[0].Source), "Owned.2024.BluRay")
				contents = map[string][]byte{filepath.Join(source, "BDMV", "STREAM", "00001.m2ts"): []byte("preserved directory stream"), filepath.Join(source, "BDMV", "index.bdmv"): []byte("index"), filepath.Join(source, "tiny.txt"): []byte("x")}
				for path, data := range contents {
					importWrite(t, path, data)
				}
				out, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{source}, TargetPath: a.Settings().Roots[0].Path, Mode: "softlink", MediaType: "movie", TMDBID: 123, Scraping: importFlag(sample.asset)})
				if err != nil || out.Submitted != 1 {
					t.Fatal(out, err)
				}
				runSoftlinkDirectoryJob(t, a, out.Items[0].JobID)
				if err = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); err != nil {
					t.Fatal(err)
				}
			} else {
				job := overwriteStart(t, a, plan)
				if err := a.runImport(ctx, job, plan.ID, nil); err != nil {
					t.Fatal(err)
				}
				if changed, err := a.finishJob(ctx, job, "succeeded", "", "", 0); err != nil || !changed {
					t.Fatal("real import completion", changed, err)
				}
				if err := a.Store.Get(ctx, "import_plan", plan.ID, &plan); err != nil {
					t.Fatal(err)
				}
			}
			var asset Asset
			if err := a.Store.Get(ctx, "asset", plan.Files[0].AssetID, &asset); err != nil {
				t.Fatal(err)
			}
			if sample.asset {
				var scrapeID string
				if err := a.Store.DB.QueryRow(`SELECT id FROM jobs WHERE kind='scrape' LIMIT 1`).Scan(&scrapeID); err != nil {
					t.Fatal(err)
				}
				runSoftlinkDirectoryJob(t, a, scrapeID)
				var owned managedFile
				if err := a.Store.Get(ctx, "managed_file", tokenHash(asset.Path), &owned); err != nil {
					t.Fatal(err)
				}
				ordinary := 0
				for _, member := range owned.Softlink.Directory.Members {
					if member.Softlink == nil {
						ordinary++
					}
				}
				if ordinary == 0 {
					t.Fatal("real metadata scrape did not explicitly freeze ordinary NFO")
				}
			}
			item, err := a.transferHistoryItem(ctx, plan.Files[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			frozen, err := a.originalHistoryFrozenItem(ctx, item, false)
			if err != nil || frozen == nil {
				t.Fatal("original target FileItem", frozen, err)
			}
			if sample.asset {
				if _, err = a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true}); err != nil {
					t.Fatal("record-only removal before asset deletion", err)
				}
			}
			if sample.broken {
				if err = os.Remove(plan.Files[0].Source); err != nil {
					t.Fatal(err)
				}
			}
			config["modules"] = map[string]string{"delete_file": "module_storage_delete"}
			config["storage_root"] = filepath.Dir(a.Settings().Roots[0].Path)
			config["storage_mode"], config["result"] = sample.mode, sample.result
			saveConfig()
			before := ownedModuleRead(t, object)
			if before.PID <= 0 || before.PID == os.Getpid() {
				t.Fatal("actual independent delete SDK worker", before.PID)
			}
			var storageBefore, storageAfter struct {
				Calls []json.RawMessage `json:"storage_calls"`
			}
			if err = object.readSession(ctx, "snapshot", &storageBefore); err != nil {
				t.Fatal(err)
			}
			if sample.cold {
				if _, err = a.Store.DB.Exec(`CREATE TRIGGER soft_delete_final_fail BEFORE UPDATE ON records WHEN NEW.kind='history_target_removal' AND json_extract(NEW.payload,'$.File.deleted')=1 BEGIN SELECT RAISE(FAIL,'real returned effect final commit interrupted'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if sample.unknown {
				if _, err = a.Store.DB.Exec(`CREATE TRIGGER soft_delete_return_fail BEFORE UPDATE ON records WHEN NEW.kind='history_target_removal' AND COALESCE(json_extract(OLD.payload,'$.File.source_operation'),'')!='' BEGIN SELECT RAISE(FAIL,'provider returned but result not committed'); END`); err != nil {
					t.Fatal(err)
				}
			}
			remove := func() error {
				_, err := a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, FileID: item.executionFileID(), DeleteTarget: true, DeleteSource: sample.both})
				return err
			}
			if sample.asset {
				preview, e := a.previewAssetRemoval(ctx, asset)
				if e != nil {
					t.Fatal(e)
				}
				err = a.removeAsset(ctx, asset.ID, preview.ID, true, false)
			} else {
				err = remove()
			}
			fails := sample.cold || sample.unknown || sample.name == "leaf-true-without-effect"
			if (err != nil) != fails {
				t.Fatal("real delete outcome", sample.name, err)
			}
			a.wg.Wait()
			after := ownedModuleRead(t, object)
			primaryCalls := 0
			if err = object.readSession(ctx, "snapshot", &storageAfter); err != nil {
				t.Fatal(err)
			}
			for _, raw := range storageAfter.Calls[len(storageBefore.Calls):] {
				var call struct {
					Kwargs struct {
						Item *plugins.ModuleFileItem `json:"fileitem"`
					}
				}
				if json.Unmarshal(raw, &call) != nil || call.Kwargs.Item == nil {
					t.Fatal("actual delete FileItem", string(raw))
				}
				var path string
				_ = json.Unmarshal(call.Kwargs.Item.Fields["path"], &path)
				if filepath.Clean(path) == asset.Path {
					primaryCalls++
					if !reflect.DeepEqual(call.Kwargs.Item, frozen) {
						t.Fatal("provider lost original FileItem", call.Kwargs.Item, frozen)
					}
				}
			}
			wantCalls := 1
			if sample.directory && !sample.asset && !sample.cold {
				wantCalls = 2
			} // MP's separate missing Blu-ray self-cleanup.
			if primaryCalls != wantCalls {
				t.Fatal("target provider invocation count", primaryCalls, after.Calls)
			}
			retained := sample.name == "leaf-false-retained" || sample.name == "leaf-true-without-effect"
			if _, e := os.Lstat(asset.Path); retained && e != nil || !retained && !os.IsNotExist(e) {
				t.Fatal("physical entry disposition", retained, e)
			}
			if sample.cold || sample.unknown {
				var removal historyTargetRemoval
				if err = a.Store.Get(ctx, "history_target_removal", item.executionFileID(), &removal); err != nil {
					t.Fatal(err)
				}
				var journal historySoftlinkDeleteOperation
				sealed, e := base64.StdEncoding.DecodeString(removal.File.SourceOperation)
				if e != nil || a.Store.Unseal(sealed, &journal) != nil || journal.SoftlinkDelete == nil || journal.SoftlinkDelete.Returned != sample.cold {
					t.Fatal("actual sealed return phase", journal, e)
				}
				trigger := "soft_delete_final_fail"
				if sample.unknown {
					trigger = "soft_delete_return_fail"
				}
				if _, err = a.Store.DB.Exec(`DROP TRIGGER ` + trigger); err != nil {
					t.Fatal(err)
				}
				dir := a.Dir
				if err = a.Close(); err != nil {
					t.Fatal(err)
				}
				a, err = New(Options{DataDir: dir})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { a.Close() })
				if err = a.initializeOriginalPlugins(ctx); err != nil {
					t.Fatal(err)
				}
				object = originalBaseSDKLoaded(t, a, ownedModuleID)
				err = remove()
				if sample.unknown && fileCode(err) != "REVIEW_HISTORY_DELETE" || sample.cold && err != nil {
					t.Fatal("cold result phase recovery", err)
				}
				a.wg.Wait()
				cleanupCalls := 0
				if sample.directory && sample.cold {
					cleanupCalls = 1
				} // First execution of MP's post-delete self-cleanup, not adapter replay.
				if last := ownedModuleRead(t, object); last.Calls["module_storage_delete"] != cleanupCalls {
					t.Fatal("cold retry repeated dangerous provider", last.Calls)
				}
			}
			for path, data := range contents {
				got, e := os.ReadFile(path)
				if sample.both || sample.broken && path == plan.Files[0].Source {
					if !os.IsNotExist(e) {
						t.Fatal("explicit original source outcome", e)
					}
				} else if e != nil || !bytes.Equal(got, data) {
					t.Fatal("target deletion touched referent bytes", path, e)
				}
			}
		})
	}
}

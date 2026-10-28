package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOriginalBlurayScrapeFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/bluray-scrape-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name  string
			Files map[string]string
			Calls []struct{ Method string }
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 4 {
		t.Fatal("fixed source", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, object, _, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			cfg["modules"] = map[string]string{"metadata_nfo": "module_metadata_nfo"}
			cfg["nfo_result"] = "<movie>owned NFO</movie>"
			config, _ := json.Marshal(cfg)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, config); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			disc := filepath.Join(a.Settings().Roots[0].Path, "Owned (2024)")
			stream := filepath.Join(disc, "BDMV", "STREAM", "00001.m2ts")
			importWrite(t, stream, []byte("owned stream"))
			if strings.HasPrefix(sample.Name, "existing") {
				importWrite(t, filepath.Join(disc, "Owned (2024).nfo"), []byte("prior NFO"))
			}
			item, e := originalLocalFileItem(disc)
			if e != nil {
				t.Fatal(e)
			}
			files := []string{stream}
			if sample.Name == "empty_file_list" {
				files = []string{}
			}
			full, e := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"title":"Owned","original_title":"Owned","release_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
			if e != nil {
				t.Fatal(e)
			}
			before := ownedModuleRead(t, object).Calls["module_metadata_nfo"]
			if e = a.consumeOriginalCoreMetadataScrape(ctx, mustCleanSeedJSON(map[string]any{"fileitem": item, "file_list": files, "mediainfo": full, "overwrite": sample.Name == "existing_overwrite"})); e != nil {
				t.Fatal(e)
			}
			want := 0
			for _, call := range sample.Calls {
				if call.Method == "metadata_nfo" {
					want++
				}
			}
			if after := ownedModuleRead(t, object).Calls["module_metadata_nfo"]; after-before != want {
				t.Fatal("fixed source module count", after-before, want)
			}
			found := map[string]string{}
			if e = filepath.WalkDir(disc, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				relative, err := filepath.Rel(disc, path)
				if err != nil {
					return err
				}
				b, err := os.ReadFile(path)
				found[relative] = string(b)
				return err
			}); e != nil {
				t.Fatal(e)
			}
			if !originalBaseJSONEqual(mustCleanSeedJSON(found), mustCleanSeedJSON(sample.Files)) {
				t.Fatal("fixed source actual files", found, sample.Files)
			}
		})
	}
}

// Fixed MediaChain.scrape_metadata_event -> _handle_movie_directory ->
// _get_target_fileitem_and_path writes <disc>/<disc name>.nfo once, without
// scraping STREAM children. The managed host must retain that actual write.
func TestOriginalTransferBlurayScrapeHistory(t *testing.T) {
	for _, mode := range []string{"success", "cold_commit", "cold_stage", "cold_foreign", "cold_changed_nfo", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			a, source, target := importTestApp(t)
			ctx := context.Background()
			importFixture(t, a, "movie")
			full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"title":"Owned","original_title":"Owned","release_date":"2024-01-01","overview":"owned plot","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
			if err != nil {
				t.Fatal(err)
			}
			installOwnedTransferObserver(t, a, nil, nil, map[string]any{"mediainfo": full})
			disc := filepath.Join(source, "Owned.2024.BluRay")
			importWrite(t, filepath.Join(disc, "BDMV", "STREAM", "00001.m2ts"), []byte("owned stream"))
			importWrite(t, filepath.Join(disc, "BDMV", "index.bdmv"), []byte("owned index"))
			out, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{disc}, TargetPath: target, Mode: "hardlink", MediaType: "movie", TMDBID: 123, Scraping: importFlag(true)})
			if err != nil || out.Submitted != 1 {
				t.Fatal(out, err)
			}
			var plan ImportPlan
			if err = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); err != nil {
				t.Fatal(err)
			}
			job := overwriteStart(t, a, plan)
			if err = a.runImport(ctx, job, plan.ID, nil); err != nil {
				t.Fatal(err)
			}
			if _, err = a.finishJob(ctx, job, "succeeded", "", "", 0); err != nil {
				t.Fatal(err)
			}
			a.wg.Wait()
			scrape, err := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='scrape'`))
			if err != nil {
				t.Fatal("actual core scrape job", err)
			}
			if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, scrape.ID); err != nil {
				t.Fatal(err)
			}
			scrape.State, scrape.Generation = "running", 1
			var payload struct{ ID string }
			if err = json.Unmarshal(scrape.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(mode, "cold_") {
				if _, err = a.Store.DB.Exec(`CREATE TRIGGER owned_disc_metadata_commit BEFORE UPDATE ON records WHEN NEW.kind='managed_file' BEGIN SELECT RAISE(ABORT,'owned metadata commit'); END`); err != nil {
					t.Fatal(err)
				}
			}
			err = a.runScrape(ctx, scrape, payload.ID)
			if strings.HasPrefix(mode, "cold_") {
				if err == nil {
					t.Fatal("missing actual SQL failure")
				}
				nfo := filepath.Join(out.Items[0].Target, filepath.Base(out.Items[0].Target)+".nfo")
				if b, e := os.ReadFile(nfo); e != nil || len(b) == 0 {
					t.Fatal("NFO published before failed SQL commit", e)
				}
				if mode == "cold_foreign" {
					importWrite(t, filepath.Join(out.Items[0].Target, "foreign.txt"), []byte("keep"))
				}
				if mode == "cold_changed_nfo" {
					importWrite(t, nfo, []byte("external replacement"))
				}
				if mode == "cold_stage" {
					// Explicit crash-state injection: the published inode also
					// remains at its recorded temporary name before cleanup.
					var pending originalMetadataDirectory
					if e := a.Store.Get(ctx, "original_metadata_directory", payload.ID, &pending); e != nil {
						t.Fatal(e)
					}
					for path, write := range pending.Writes {
						if e := os.Link(filepath.Join(pending.Owned.Path, path), filepath.Join(filepath.Dir(filepath.Join(pending.Owned.Path, path)), write.Stage)); e != nil {
							t.Fatal(e)
						}
					}
				}
				if _, err = a.Store.DB.Exec(`DROP TRIGGER owned_disc_metadata_commit`); err != nil {
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
				if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, scrape.ID); err != nil {
					t.Fatal(err)
				}
				err = a.runScrape(ctx, scrape, payload.ID)
				if mode == "cold_foreign" || mode == "cold_changed_nfo" {
					if fileCode(err) != "REVIEW_METADATA_SOURCE_CHANGED" {
						t.Fatal("reject unknown content during recovery", err)
					}
					var owned managedFile
					if e := a.Store.Get(ctx, "managed_file", tokenHash(out.Items[0].Target), &owned); e != nil {
						t.Fatal(e)
					}
					actual, e := snapshotTransferPath(out.Items[0].Target)
					if e != nil || actual == owned.Snapshot {
						t.Fatal("external content must not become owned", e)
					}
					return
				}
			}
			if err != nil {
				t.Fatal("actual directory metadata consumer", err)
			}
			destination := out.Items[0].Target
			nfo := filepath.Join(destination, filepath.Base(destination)+".nfo")
			if bytes, e := os.ReadFile(nfo); e != nil || len(bytes) == 0 {
				t.Fatal("source directory NFO", e)
			}
			if _, e := os.Stat(filepath.Join(destination, "BDMV", "STREAM", "00001.nfo")); !os.IsNotExist(e) {
				t.Fatal("no child scrape", e)
			}
			item, err := a.transferHistoryItem(ctx, plan.Files[0].ID)
			if err != nil || item.Size != 12 {
				t.Fatal("history remains STREAM size", item, err)
			}
			if mode == "foreign" {
				importWrite(t, filepath.Join(destination, "foreign.txt"), []byte("keep"))
			}
			_, err = a.deleteTransferHistory(ctx, item, historyDeleteRequest{Confirm: true, DeleteTarget: true, FileID: item.executionFileID()})
			if mode == "foreign" {
				if fileCode(err) != "FILE_SNAPSHOT_CHANGED" {
					t.Fatal("foreign member must remain protected", err)
				}
				if b, e := os.ReadFile(filepath.Join(destination, "foreign.txt")); e != nil || string(b) != "keep" {
					t.Fatal(e)
				}
				return
			}
			if err != nil {
				t.Fatal("delete scraped directory", err)
			}
			if _, e := os.Stat(destination); !os.IsNotExist(e) {
				t.Fatal("target directory remains", e)
			}
			if b, e := os.ReadFile(filepath.Join(disc, "BDMV", "STREAM", "00001.m2ts")); e != nil || string(b) != "owned stream" {
				t.Fatal("source remains", e)
			}
		})
	}
}

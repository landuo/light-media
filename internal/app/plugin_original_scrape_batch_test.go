package app

import (
	"context"
	"encoding/json"
	"fmt"
	"light-media/internal/catalog"
	"os"
	"path/filepath"
	"testing"
)

func TestOriginalScrapeBatchFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/scrape-batch-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name             string
			Paths, Roots     []string
			Lists            [][]string
			Enabled, Success []bool
			Events           []json.RawMessage
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 7 {
		t.Fatal("fixed oracle", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			batch := originalScrapeBatch{}
			for i, path := range sample.Paths {
				if !sample.Success[i] || !sample.Enabled[i] || filepath.Ext(path) != ".mkv" {
					continue
				}
				title := "later"
				if i == 0 {
					title = "first"
				}
				raw, _ := json.Marshal(map[string]any{"meta": map[string]any{"title": title}, "mediainfo": map[string]any{"tmdb_id": 123}, "fileitem": map[string]any{"storage": "local", "path": sample.Roots[i], "type": "dir"}, "file_list": sample.Lists[i], "overwrite": false})
				if err := batch.add(raw, path); err != nil {
					t.Fatal(err)
				}
			}
			if len(batch.Targets) != len(sample.Events) {
				t.Fatal("source target count", len(batch.Targets), len(sample.Events))
			}
			for i, event := range sample.Events {
				if !originalBaseJSONEqual(batch.Targets[i].Event, event) {
					t.Fatal("source payload", string(batch.Targets[i].Event), string(event))
				}
			}
		})
	}
}

func TestOriginalScrapeBatchActualManualHost(t *testing.T) {
	for _, mode := range []string{"success", "failed-last", "cancelled-last", "cold-pending", "commit-failure"} {
		t.Run(mode, func(t *testing.T) { originalScrapeBatchActualManualHost(t, mode) })
	}
}
func originalScrapeBatchActualManualHost(t *testing.T, mode string) {
	a, source, target := importTestApp(t)
	ctx := context.Background()
	importFixture(t, a, "tv")
	importCache(t, a, "season:123:1", catalog.Season{ID: 1, SeriesID: 123, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 11, SeasonNumber: 1, EpisodeNumber: 1, Name: "One"}, {ID: 12, SeasonNumber: 1, EpisodeNumber: 2, Name: "Two"}}})
	full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"name":"Owned series","original_name":"Owned series","first_air_date":"2024-01-01","overview":"owned plot","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{},"seasons":[{"season_number":1,"episode_count":2}]}`), "tv", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	observer := installOwnedTransferObserver(t, a, nil, nil, map[string]any{"mediainfo": full})
	for i := 1; i <= 2; i++ {
		importWrite(t, filepath.Join(source, fmt.Sprintf("ScrapeBatchsuccess.S01E%02d.mkv", i)), []byte(fmt.Sprintf("owned episode %d", i)))
	}
	season := 1
	out, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{source}, TargetPath: target, TargetStorage: "local", MediaType: "tv", TMDBID: 123, Season: &season, Mode: "hardlink", Scraping: importFlag(true), Renaming: importFlag(false)})
	if err != nil || out.Submitted != 2 {
		t.Fatal("real manual batch", out, err)
	}
	read := func() []json.RawMessage {
		t.Helper()
		a.wg.Wait()
		response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+observer+"/observations", "")
		var rows []struct {
			Type string
			Data json.RawMessage
		}
		if json.Unmarshal(response.Body.Bytes(), &rows) != nil {
			t.Fatal("SDK observations")
		}
		var events []json.RawMessage
		for _, row := range rows {
			if row.Type == "metadata.scrape" {
				events = append(events, row.Data)
			}
		}
		return events
	}
	for i, item := range out.Items {
		if i == 1 && mode == "cold-pending" {
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
			if err = a.recoverOriginalScrapeBatches(ctx); err != nil {
				t.Fatal(err)
			}
			if len(read()) != 0 {
				t.Fatal("cold pending batch emitted")
			}
		}
		if i == 1 && mode == "cancelled-last" {
			var plan ImportPlan
			if err = a.Store.Get(ctx, "import_plan", item.PlanID, &plan); err != nil {
				t.Fatal(err)
			}
			if _, err = a.cancelOrganizeQueueFile(ctx, plan.Files[0].ID); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if i == 1 && mode == "failed-last" {
			var plan ImportPlan
			if err = a.Store.Get(ctx, "import_plan", item.PlanID, &plan); err != nil {
				t.Fatal(err)
			}
			// An actual source snapshot change fails before publication.
			importWrite(t, plan.Files[0].Source, []byte("changed source rejected by actual host"))
		}
		if _, err = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=generation+1 WHERE id=?`, item.JobID); err != nil {
			t.Fatal(err)
		}
		job, e := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id=?`, item.JobID))
		if e != nil {
			t.Fatal(e)
		}
		e = a.runImport(ctx, job, item.PlanID, nil)
		rejected := i == 1 && mode == "failed-last"
		if (e != nil) != rejected {
			t.Fatal("actual file publication", e, rejected)
		}
		if i == 0 && len(read()) != 0 {
			t.Fatal("source batch must not emit while second task is pending")
		}
		state := "succeeded"
		if rejected {
			state = "failed"
		}
		if i == 1 && mode == "commit-failure" {
			if _, e = a.Store.DB.Exec(`CREATE TRIGGER owned_scrape_batch_failure BEFORE INSERT ON jobs WHEN NEW.kind='scrape' BEGIN SELECT RAISE(ABORT,'owned scrape batch insert failure'); END`); e != nil {
				t.Fatal(e)
			}
			if _, e = a.finishJob(ctx, job, state, "", "", 0); e == nil {
				t.Fatal("real queue transaction failure missing")
			}
			if len(read()) != 0 {
				t.Fatal("failed transaction emitted aggregate")
			}
			if _, e = a.Store.DB.Exec(`DROP TRIGGER owned_scrape_batch_failure`); e != nil {
				t.Fatal(e)
			}
		}
		if _, e = a.finishJob(ctx, job, state, "", "", 0); e != nil {
			t.Fatal(e)
		}
		if i == 0 && len(read()) != 0 {
			t.Fatal("source closed batch must wait for all tasks")
		}
	}
	events := read()
	if len(events) != 1 {
		t.Fatal("one source aggregate event", len(events))
	}
	var payload struct {
		Files []string `json:"file_list"`
	}
	wantFiles := 2
	if mode == "failed-last" || mode == "cancelled-last" {
		wantFiles = 1
	}
	if json.Unmarshal(events[0], &payload) != nil || len(payload.Files) != wantFiles {
		t.Fatal("source successful unique files", string(events[0]))
	}
	var count int
	if err = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='scrape'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("one actual core consumer job", count, err)
	}

	scrape, e := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='scrape'`))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, scrape.ID); e != nil {
		t.Fatal(e)
	}
	scrape.State, scrape.Generation = "running", 1
	if e = a.runScrape(ctx, scrape, func() string { var p struct{ ID string }; _ = json.Unmarshal(scrape.Payload, &p); return p.ID }()); e != nil {
		t.Fatal("aggregate actual metadata consumer", e)
	}
	for _, path := range payload.Files {
		if raw, e := os.ReadFile(path[:len(path)-len(filepath.Ext(path))] + ".nfo"); e != nil || len(raw) == 0 {
			t.Fatal("aggregate real NFO", path, e)
		}
	}
	if e = a.Store.DB.QueryRow(`SELECT COUNT(*) FROM records WHERE kind='asset' AND json_extract(payload,'$.scrape_state')='scraped'`).Scan(&count); e != nil || count != wantFiles {
		t.Fatal("all successful assets reflect same consumer", count, e)
	}
	if e = a.recoverOriginalScrapeBatches(ctx); e != nil {
		t.Fatal(e)
	}
	if len(read()) != 1 {
		t.Fatal("completed batch replayed ordinary event")
	}
}

func TestOriginalScrapeNativeSeasonDoesNotInvokeEpisodeModule(t *testing.T) {
	a, object, _, cfg := originalTransferModuleFixture(t, "hardlink", "")
	ctx := context.Background()
	cfg["modules"] = map[string]string{"tmdb_episodes": "module_fail"}
	raw, _ := json.Marshal(cfg)
	if _, e := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); e != nil {
		t.Fatal(e)
	}
	if e := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); e != nil {
		t.Fatal(e)
	}
	full, e := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"name":"Owned","original_name":"Owned","first_air_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "tv", "image.tmdb.org", "")
	if e != nil {
		t.Fatal(e)
	}
	importCache(t, a, "season:123:1", catalog.Season{ID: 1, SeriesID: 123, SeasonNumber: 1, Episodes: []catalog.Episode{{ID: 12301, SeasonNumber: 1, EpisodeNumber: 1, Name: "Native first", AirDate: "2024-01-01"}}})
	nfo, e := a.originalMetadataNativeNFO(ctx, full, json.Number("1"), json.Number("1"))
	if e != nil || len(nfo) == 0 {
		t.Fatal("source system scraper native helper", string(nfo), e)
	}
	if n := ownedModuleRead(t, object).Calls["module_fail"]; n != 0 {
		t.Fatal("system scraper added a source-absent chain extension", n)
	}
}

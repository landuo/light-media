package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOriginalTransferQueuesSameMetadataConsumerOnceWithInheritedMedia(t *testing.T) {
	a, source, _ := importTestApp(t)
	identity := importFixture(t, a, "movie")
	input := filepath.Join(source, "Owned.2024.mkv")
	importWrite(t, input, []byte("owned movie metadata media"))
	full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"title":"Owned","original_title":"Owned","release_date":"2024-01-01","overview":"owned plot","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	id := installOwnedTransferObserver(t, a, nil, nil, map[string]any{"mediainfo": full})
	plan, err := a.previewImport(context.Background(), ImportRequest{MediaType: identity.MediaType, TMDBID: identity.ID, RootID: "root", Mode: "hardlink", Scraping: importFlag(true), Files: []ImportFileRequest{{Path: input}}})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
		t.Fatal("real metadata preview", plan, err)
	}
	job := overwriteStart(t, a, plan)
	if err := a.runImport(context.Background(), job, plan.ID, nil); err != nil {
		t.Fatal("real import", err)
	}
	if _, err := a.finishJob(context.Background(), job, "succeeded", "", "", 0); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	var actual ImportPlan
	if err := a.Store.Get(context.Background(), "import_plan", plan.ID, &actual); err != nil {
		t.Fatal(err)
	}
	assetID := actual.Files[0].AssetID
	scrape, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind='scrape' AND json_extract(payload,'$.id')=?`, assetID))
	if err != nil {
		t.Fatal("one real queued scrape", err)
	}
	var data struct {
		Event json.RawMessage `json:"original_metadata_event"`
	}
	if json.Unmarshal(scrape.Payload, &data) != nil || len(data.Event) == 0 {
		t.Fatal("actual provided source context absent")
	}
	var count int
	if err := a.Store.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind='scrape' AND json_extract(payload,'$.id')=?`, assetID).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate native scrape jobs", count, err)
	}
	nfo := actual.Files[0].Target[:len(actual.Files[0].Target)-len(filepath.Ext(actual.Files[0].Target))] + ".nfo"
	if _, err := os.Stat(nfo); !os.IsNotExist(err) {
		t.Fatal("broadcast incorrectly invoked another core writer before the native job", err)
	}
	response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+id+"/observations", "")
	var rows []struct {
		Type string
		Data json.RawMessage
	}
	if json.Unmarshal(response.Body.Bytes(), &rows) != nil {
		t.Fatal("real metadata observation")
	}
	events := 0
	for _, row := range rows {
		if row.Type == "metadata.scrape" {
			events++
			if !originalBaseJSONEqual(row.Data, data.Event) {
				t.Fatal("job and observer did not receive same event")
			}
		}
	}
	if events != 1 {
		t.Fatal("ordinary metadata event absent or duplicated", events)
	}
	if _, err := a.Store.DB.Exec(`UPDATE jobs SET state='running',generation=1 WHERE id=?`, scrape.ID); err != nil {
		t.Fatal(err)
	}
	scrape.State, scrape.Generation = "running", 1
	if a.Settings().TMDB.APIKey != "" || a.Settings().TMDB.AccessToken != "" {
		t.Fatal("test requires no provider credential")
	}
	if err := a.runScrape(context.Background(), scrape, assetID); err != nil {
		t.Fatal("same inherited core consumer", err)
	}
	if bytes, err := os.ReadFile(nfo); err != nil || len(bytes) == 0 {
		t.Fatal("actual NFO publication", err)
	}
	var asset Asset
	if err := a.Store.Get(context.Background(), "asset", assetID, &asset); err != nil || asset.ScrapeState != "scraped" {
		t.Fatal("real asset completion", asset, err)
	}
}

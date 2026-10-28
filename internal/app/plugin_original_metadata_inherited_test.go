package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
)

func TestOriginalScrapeInheritedTransferContextAvoidsRecognition(t *testing.T) {
	a, cookie, object := ownedModuleFixture(t, true)
	dir := t.TempDir()
	path := filepath.Join(dir, "Owned.Movie.2024.mkv")
	if err := os.WriteFile(path, []byte("owned inherited media bytes"), 0640); err != nil {
		t.Fatal(err)
	}
	settings := a.Settings()
	settings.Roots = []Root{{ID: "metadata-root", Path: dir, Kind: "mixed"}}
	settings.AllowedRoots = []string{dir}
	settings.TMDB.APIKey = ""
	if err := a.saveSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	asset := Asset{ID: "owned-inherited-asset", RootID: "metadata-root", Path: path, State: "imported", ScrapeState: "pending", Media: catalog.Media{ID: 900043, MediaType: "movie", Title: "Owned inherited movie"}}
	if err := a.Store.Put(context.Background(), "asset", asset.ID, asset); err != nil {
		t.Fatal(err)
	}
	root, err := originalLocalFileItem(dir)
	if err != nil {
		t.Fatal(err)
	}
	full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":900043,"title":"Owned inherited movie","original_title":"Owned inherited movie","release_date":"2024-01-01","overview":"owned","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`), "movie", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	event, _ := json.Marshal(map[string]any{"fileitem": root, "file_list": []string{path}, "mediainfo": full, "meta": map[string]any{"title": "ignored event meta for file-list branch"}, "overwrite": false})
	job := importClaim(t, a, "scrape", asset.ID)
	job.Payload, _ = json.Marshal(map[string]any{"id": asset.ID, "original_metadata_event": json.RawMessage(event)})
	if _, err = a.Store.DB.Exec(`UPDATE jobs SET payload=? WHERE id=?`, job.Payload, job.ID); err != nil {
		t.Fatal(err)
	}
	result, _ := plugins.ModuleResultBytes([]byte("inherited SDK NFO"))
	config, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"metadata_nfo": "module_execute"}, "result": result})
	saveOwnedModule(t, a, cookie, object, string(config))
	object = originalBaseSDKLoaded(t, a, ownedModuleID)
	before := ownedModuleRead(t, object)
	if err = a.runScrape(context.Background(), job, asset.ID); err != nil {
		t.Fatal("frozen provided media must not perform a.media or recognize with no configured API", err)
	}
	nfo := filepath.Join(dir, "Owned.Movie.2024.nfo")
	bytes, err := os.ReadFile(nfo)
	if err != nil || string(bytes) != "inherited SDK NFO" {
		t.Fatal("inherited actual NFO output", string(bytes), err)
	}
	after := ownedModuleRead(t, object)
	var input struct{ Kwargs map[string]json.RawMessage }
	if after.Calls["module_execute"]-before.Calls["module_execute"] != 1 || json.Unmarshal(after.Inputs["module_execute"], &input) != nil || !originalBaseJSONEqual(input.Kwargs["mediainfo"], full) {
		t.Fatal("inherited full model replaced/re-recognized", string(after.Inputs["module_execute"]))
	}
	var stored Asset
	if err = a.Store.Get(context.Background(), "asset", asset.ID, &stored); err != nil || stored.ScrapeState != "scraped" || stored.FNOSState != "disabled" {
		t.Fatal("existing native asset completion bookkeeping", stored, err)
	}
	if err = a.runScrape(context.Background(), job, asset.ID); err != nil {
		t.Fatal("native job recovery", err)
	}
	if final := ownedModuleRead(t, object); final.Calls["module_execute"] != after.Calls["module_execute"] {
		t.Fatal("existing sidecar retry called NFO again")
	}
	var invalid map[string]any
	_ = json.Unmarshal(event, &invalid)
	invalid["file_list"] = []string{filepath.Join(dir, "foreign.mkv")}
	badEvent, _ := json.Marshal(invalid)
	if err = a.validateOriginalAssetMetadataEvent(asset, badEvent); err == nil {
		t.Fatal("frozen event missing owned asset was accepted")
	}
}

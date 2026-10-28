package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/catalog"
)

func TestOriginalMetadataTVDirectoryFixedSource(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/standard-module-host/tv-directory-scrape-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Recursive bool
			Calls     []string
		}
	}
	if err != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 2 {
		t.Fatal("fixed TV directory source", err)
	}
	for _, sample := range oracle.Cases {
		t.Run(map[bool]string{true: "recursive", false: "root_only"}[sample.Recursive], func(t *testing.T) {
			a, object, _, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			cfg["modules"] = map[string]string{"list_files": "module_execute", "metadata_nfo": "module_metadata_nfo"}
			cfg["result"] = nil
			cfg["nfo_result"] = "<season>owned season</season>"
			config, _ := json.Marshal(cfg)
			if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, config); err != nil {
				t.Fatal(err)
			}
			if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(a.Settings().Roots[0].Path, "Season 2")
			if err := os.MkdirAll(filepath.Join(dir, "BDMV"), 0750); err != nil {
				t.Fatal(err)
			}
			item, err := originalLocalFileItem(dir)
			if err != nil {
				t.Fatal(err)
			}
			itemRaw, _ := json.Marshal(item)
			var fields map[string]any
			_ = json.Unmarshal(itemRaw, &fields)
			full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"name":"Owned","original_name":"Owned","first_air_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{},"seasons":[{"season_number":2,"episode_count":8}]}`), "tv", "image.tmdb.org", "")
			if err != nil {
				t.Fatal(err)
			}
			var media map[string]any
			_ = json.Unmarshal(full, &media)
			importCache(t, a, "season:123:2", catalog.Season{ID: 2, SeriesID: 123, SeasonNumber: 2})
			before := ownedModuleRead(t, object)
			if err = a.scrapeOriginalLocalMetadata(ctx, map[string]any{"fileitem": fields, "mediainfo": media, "recursive": sample.Recursive, "init_folder": true}); err != nil {
				t.Fatal(err)
			}
			after := ownedModuleRead(t, object)
			want := 0
			for _, call := range sample.Calls {
				if call == "list_files" {
					want++
				}
			}
			if got := after.Calls["module_execute"] - before.Calls["module_execute"]; got != want {
				t.Fatal("source directory listing count", got, want)
			}
			if after.Calls["module_metadata_nfo"]-before.Calls["module_metadata_nfo"] != 1 {
				t.Fatal("one season NFO")
			}
			if b, e := os.ReadFile(filepath.Join(dir, "season.nfo")); e != nil || string(b) != "<season>owned season</season>" {
				t.Fatal("actual season NFO", e)
			}
		})
	}
}

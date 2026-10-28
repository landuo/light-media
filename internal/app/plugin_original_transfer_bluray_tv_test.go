package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOriginalTransferBlurayTVDisc(t *testing.T) {
	a, object, original, cfg := originalTransferModuleFixture(t, "hardlink", "")
	ctx := context.Background()
	source := filepath.Join(filepath.Dir(original.Files[0].Source), "Owned.2024.S02.Disc02")
	importWrite(t, filepath.Join(source, "BDMV", "STREAM", "00001.m2ts"), []byte("owned tv stream"))
	settings := a.Settings()
	settings.Roots[0].Kind = "tv"
	if err := a.saveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	full, err := sourceTMDBMediaInfo(json.RawMessage(`{"id":123,"name":"Owned","original_name":"Owned","first_air_date":"2024-01-01","genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{},"seasons":[{"season_number":2,"episode_count":8}]}`), "tv", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg["media_result"] = full
	cfg["modules"].(map[string]string)["transfer"] = "module_execute"
	cfg["result"] = nil
	raw, _ := json.Marshal(cfg)
	if _, err := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); err != nil {
		t.Fatal(err)
	}
	if err := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); err != nil {
		t.Fatal(err)
	}
	season := 2
	out, err := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{source}, TargetPath: settings.Roots[0].Path, Mode: "hardlink", MediaType: "tv", TMDBID: 123, Season: &season, Scraping: importFlag(false)})
	if err != nil || out.Submitted != 1 {
		t.Fatal("one TV disc without invented episode", out, err)
	}
	var plan ImportPlan
	if err := a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 || len(plan.Files[0].Episodes) != 0 || plan.Files[0].Season != 2 || !strings.HasSuffix(plan.Files[0].Target, "/Season 2/Disc 2") {
		t.Fatal("fixed TV disc folder", plan.Files)
	}
	job := overwriteStart(t, a, plan)
	if err := a.runImport(ctx, job, plan.ID, nil); err != nil {
		t.Fatal("publish TV disc", err)
	}
	s, err := os.Stat(filepath.Join(source, "BDMV", "STREAM", "00001.m2ts"))
	d, e := os.Stat(filepath.Join(plan.Files[0].Target, "BDMV", "STREAM", "00001.m2ts"))
	if err != nil || e != nil || !os.SameFile(s, d) {
		t.Fatal("actual TV disc hardlink", err, e)
	}
}

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"light-media/internal/plugins"
)

func TestOrganizeEpisodeGroupNativeMetadata(t *testing.T) {
	testOrganizeEpisodeGroupMetadata(t, false)
}

func TestOriginalOrganizeEpisodeGroupActualSDK(t *testing.T) {
	testOrganizeEpisodeGroupMetadata(t, true)
}

func TestOrganizeEpisodeGroupCrossSeasonNativeMetadata(t *testing.T) {
	testOrganizeEpisodeGroupMetadata(t, false, 1, 2)
}

func TestOriginalOrganizeEpisodeGroupCrossSeasonActualSDK(t *testing.T) {
	testOrganizeEpisodeGroupMetadata(t, true, 1, 2)
}

func testOrganizeEpisodeGroupMetadata(t *testing.T, observe bool, episodes ...int) {
	t.Helper()
	a, source, _ := importTestApp(t)
	importFixture(t, a, "tv")
	organizeGroupFixture(t, a)
	ctx := context.Background()
	observer := ""
	if observe {
		full, err := a.originalHostMediaRecognize(ctx, plugins.MediaRecognizeRequest{Type: "tv", TMDBID: 123, Source: "themoviedb", EpisodeGroup: "merged"})
		if err != nil {
			t.Fatal(err)
		}
		observer = installOwnedTransferObserver(t, a, nil, nil, map[string]any{"mediainfo": full})
	}
	name := "Show.S01E01"
	if len(episodes) == 0 {
		episodes = []int{1}
	}
	if len(episodes) == 2 {
		name += "-E02"
	}
	path := filepath.Join(source, name+".mkv")
	importWrite(t, path, []byte("synthetic group episode for metadata"))
	importWrite(t, filepath.Join(source, name+".zh.srt"), []byte("synthetic subtitle"))
	plan, err := a.previewImport(ctx, ImportRequest{MediaType: "tv", TMDBID: 123, EpisodeGroup: "merged", RootID: "root", Mode: "hardlink", Scraping: importFlag(true), Files: []ImportFileRequest{{Path: path, Season: 1, SeasonExplicit: true, Episodes: episodes}}})
	if err != nil || len(plan.Files) != 1 || plan.Files[0].Status != "ready" {
		t.Fatalf("metadata preview=%+v %v", plan, err)
	}
	job := overwriteStart(t, a, plan)
	if err = a.runImport(ctx, job, plan.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = a.finishJob(ctx, job, "succeeded", "", "", 0); err != nil {
		t.Fatal(err)
	}
	a.wg.Wait()
	var executed ImportPlan
	if err = a.Store.Get(ctx, "import_plan", plan.ID, &executed); err != nil {
		t.Fatal(err)
	}
	file := executed.Files[0]
	scrape := reorgJob(t, a, "scrape", file.AssetID)
	reorgRunJob(t, a, scrape)
	nfo := strings.TrimSuffix(file.Target, filepath.Ext(file.Target)) + ".nfo"
	content, err := os.ReadFile(nfo)
	if err != nil {
		entries, _ := os.ReadDir(filepath.Dir(file.Target))
		names := []string{}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("NFO missing: %v; files=%v; scrape=%s", err, names, scrape.Payload)
	}
	for _, fragment := range []string{"<season>1</season>", "<episode>1</episode>", "<tmdbid>4016</tmdbid>", "Canonical sixteen"} {
		if !strings.Contains(string(content), fragment) {
			t.Fatalf("group NFO lacks %q: %s", fragment, content)
		}
	}
	var asset Asset
	if err = a.Store.Get(ctx, "asset", file.AssetID, &asset); err != nil || asset.Season != 4 || len(asset.Episodes) != 1 || asset.Episodes[0] != 16 || asset.ScrapeState != "scraped" {
		t.Fatalf("metadata rewrote canonical inventory: %+v %v", asset, err)
	}
	if len(episodes) == 2 && !sameCanonicalSlots(asset.inventorySlots(), []canonicalEpisodeSlot{{4, 16}, {0, 2}}) {
		t.Fatalf("scrape lost multi-season inventory: %+v", asset)
	}
	if !observe {
		return
	}
	a.wg.Wait()
	response := ownedInputAPI(t, a, "GET", "/api/v1/plugin/"+observer+"/observations", "")
	var rows []struct {
		Type string
		Data json.RawMessage
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &rows) != nil {
		t.Fatalf("SDK observations: %d %s", response.Code, response.Body)
	}
	counts := map[string]int{}
	for _, row := range rows {
		var data struct {
			Meta   map[string]any `json:"meta"`
			Media  map[string]any `json:"mediainfo"`
			Rename map[string]any `json:"rename_dict"`
		}
		if json.Unmarshal(row.Data, &data) != nil {
			t.Fatalf("invalid observation: %s", row.Data)
		}
		switch row.Type {
		case "transfer.intercept", "transfer.complete", "transfer.subtitle.complete", "metadata.scrape":
			counts[row.Type]++
			if len(episodes) == 2 && data.Meta["end_episode"] != float64(2) {
				t.Fatalf("%s lost presentation end episode: %s", row.Type, row.Data)
			}
			if data.Media["episode_group"] != "merged" || data.Meta["begin_season"] != float64(1) || data.Meta["begin_episode"] != float64(1) {
				t.Fatalf("%s lost group context: %s", row.Type, row.Data)
			}
		case "transfer.rename.build":
			counts[row.Type]++
			if sourceStringValue(data.Rename["season"]) != "1" {
				t.Fatalf("naming used canonical season: %s", row.Data)
			}
		}
	}
	if counts["transfer.complete"] != 1 || counts["transfer.subtitle.complete"] != 1 || counts["transfer.intercept"] != 2 || counts["metadata.scrape"] != 1 || counts["transfer.rename.build"] < 2 {
		t.Fatalf("incomplete SDK chain: %+v", counts)
	}
}

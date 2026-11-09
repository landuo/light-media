package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
)

func TestManualTVTransferRecognitionUsesMoviePilotMatcher(t *testing.T) {
	for _, sample := range []struct {
		file, name, title, premiere, seasonDate string
		id, season, episode                     int
		legacy                                  map[string]catalog.Page
		searchResults                           string
	}{
		{
			file: "[老友记 第四季].Friends.1997.S04E21.2160p.MAX.WEB-DL.DoVi.HDR10.H265.10bit.DDP5.1-UBWEB.mkv",
			name: "Friends", title: "老友记", premiere: "1994-09-22", seasonDate: "1997-09-25",
			id: 1668, season: 4, episode: 21,
			legacy: map[string]catalog.Page{
				"Friends 1997": {Page: 1, TotalPages: 1},
				"老友记 第四季":      {Page: 1, TotalPages: 1},
				"Friends":      {Page: 1, TotalPages: 6},
			},
			searchResults: `[{"id":1668,"name":"老友记","original_name":"Friends","first_air_date":"1994-09-22"}]`,
		},
		{
			file: "Fallout.S02E01.2160p.BluRay.DoVi.x265.10bit.Atmos.TrueHD7.1-WiKi.mkv",
			name: "Fallout", title: "辐射", premiere: "2024-04-10", seasonDate: "2025-12-16",
			id: 106379, season: 2, episode: 1,
			legacy: map[string]catalog.Page{"Fallout": {
				Page: 1, TotalPages: 1, TotalResults: 2, Results: []catalog.Media{
					{ID: 106379, MediaType: "tv", Title: "辐射", OriginalTitle: "Fallout", Year: 2024},
					{ID: 32366, MediaType: "tv", Title: "Fallout", OriginalTitle: "Fallout", Year: 2006},
				},
			}},
			searchResults: `[{"id":32366,"name":"Fallout","original_name":"Fallout","first_air_date":"2006-01-01"},{"id":106379,"name":"辐射","original_name":"Fallout","first_air_date":"2024-04-10"}]`,
		},
	} {
		t.Run(sample.name, func(t *testing.T) {
			a, source, target := importTestApp(t)
			for name, page := range sample.legacy {
				importCache(t, a, "directory-identify:tv:"+name, page)
			}
			detail, err := json.Marshal(map[string]any{
				"id": sample.id, "name": sample.title, "original_name": sample.name, "first_air_date": sample.premiere,
				"seasons": []map[string]any{{"season_number": sample.season, "air_date": sample.seasonDate, "episode_count": sample.episode}},
			})
			if err != nil {
				t.Fatal(err)
			}
			id := strconv.Itoa(sample.id)
			searches := 0
			originalHostMediaInstallOwnedTransport(t, a, func(r *http.Request) (int, string) {
				switch r.URL.Path {
				case "/3/search/tv":
					searches++
					if r.URL.Query().Get("query") != sample.name || r.URL.Query().Get("first_air_date_year") != "" {
						t.Errorf("expected MoviePilot clean title and season-date lookup, got %s", r.URL.RawQuery)
					}
					return http.StatusOK, `{"results":` + sample.searchResults + `}`
				case "/3/tv/" + id:
					return http.StatusOK, string(detail)
				case "/3/tv/" + id + "/images":
					return http.StatusOK, `{}`
				default:
					t.Errorf("unexpected TV recognition endpoint: %s", r.URL.Path)
					return http.StatusNotFound, `{}`
				}
			})
			path := filepath.Join(source, "电视剧", sample.file)
			importWrite(t, path, []byte("isolated TV transfer identity fixture"))
			got, err := a.prepareManualImport(context.Background(), ImportRequest{
				Manual: true, TargetPath: target, MediaType: "tv", Files: []ImportFileRequest{{Path: path}},
			})
			if err != nil || got.TMDBID != int64(sample.id) || searches != 1 {
				t.Fatalf("MoviePilot recognition picked %d after %d searches: %v", got.TMDBID, searches, err)
			}
			f := got.Files[0]
			if f.Season != sample.season || len(f.Episodes) != 1 || f.Episodes[0] != sample.episode {
				t.Fatalf("season or episode changed during recognition: %+v", f)
			}
		})
	}
}

// Expected recognition/image ordering comes from the pinned ordinary task
// prefix, not a fabricated successful import response.
func TestOriginalAutomaticManualRecognitionActualSDK(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/standard-module-host/transfer-auto-recognition-source-oracle.json")
	var oracle struct {
		Cases []struct {
			Name  string
			Trace []struct{ Method string }
		}
	}
	if e != nil || json.Unmarshal(raw, &oracle) != nil || len(oracle.Cases) != 6 {
		t.Fatal("pinned ordinary caller oracle", e)
	}
	wants := map[string]map[string]int{}
	for _, row := range oracle.Cases {
		wants[row.Name] = map[string]int{}
		for _, step := range row.Trace {
			wants[row.Name][step.Method]++
		}
	}
	for _, sample := range []struct {
		name, ext string
		missing   bool
	}{{"main", ".mkv", false}, {"subtitle", ".zh.srt", false}, {"audio", ".flac", false}, {"missing", ".mkv", true}} {
		t.Run(sample.name, func(t *testing.T) {
			a, object, original, cfg := originalTransferModuleFixture(t, "hardlink", "")
			ctx := context.Background()
			path := strings.TrimSuffix(original.Files[0].Source, ".mkv") + sample.ext
			if sample.ext != ".mkv" {
				importWrite(t, path, []byte("owned auto extra"))
			}
			cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "obtain_images": "module_images", "transfer": "module_execute"}
			cfg["image_mode"], cfg["result"] = "false", nil
			if sample.missing {
				cfg["media_result"] = false
			}
			raw, _ := json.Marshal(cfg)
			if _, e := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); e != nil {
				t.Fatal(e)
			}
			if e := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); e != nil {
				t.Fatal(e)
			}
			for _, preview := range []bool{true, false} {
				before := ownedModuleRead(t, object)
				out, e := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{path}, TargetPath: a.Settings().Roots[0].Path, Mode: "hardlink", Scraping: importFlag(false), Preview: preview})
				if e != nil || len(out.Items) != 1 {
					t.Fatal(out, e)
				}
				after := ownedModuleRead(t, object)
				key := "automatic_success"
				if sample.missing {
					key = "automatic_missing"
				}
				if after.Calls["module_search_media"]-before.Calls["module_search_media"] != wants[key]["recognize_media"] {
					t.Fatal("automatic path must ask source recognize_media exactly once", before.Calls, after.Calls, out)
				}
				if sample.missing {
					if out.Failed != 1 || out.Submitted != 0 || after.Calls["module_images"] != before.Calls["module_images"] || after.Calls["module_execute"] != before.Calls["module_execute"] {
						t.Fatal("source missing terminates before images and transfer", out, before.Calls, after.Calls)
					}
					continue
				}
				if out.Failed != 0 || after.Calls["module_images"]-before.Calls["module_images"] != wants[key]["obtain_images"] {
					t.Fatal("recognition then exactly one sync image enrichment", out, before.Calls, after.Calls)
				}
				if preview {
					if _, e = os.Stat(out.Items[0].Target); !os.IsNotExist(e) {
						t.Fatal("preview created target", e)
					}
					continue
				}
				var plan ImportPlan
				if e = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); e != nil {
					t.Fatal(e)
				}
				job := overwriteStart(t, a, plan)
				if e = a.runImport(ctx, job, plan.ID, nil); e != nil {
					t.Fatal(e)
				}
				a.wg.Wait()
				state := ownedModuleRead(t, object)
				if state.Calls["module_search_media"] != after.Calls["module_search_media"] || state.Calls["module_images"] != after.Calls["module_images"] || state.Calls["module_execute"]-before.Calls["module_execute"] != 1 {
					t.Fatal("actual uses same recognized task", before.Calls, state.Calls)
				}
				var call struct {
					Kwargs struct {
						Media map[string]any `json:"mediainfo"`
					}
				}
				if json.Unmarshal(state.Inputs["module_execute"], &call) != nil || call.Kwargs.Media["poster_path"] != "plugin-poster" {
					t.Fatal("transfer must receive mutated image input", string(state.Inputs["module_execute"]))
				}
				src, e1 := os.Stat(path)
				dst, e2 := os.Stat(plan.Files[0].Target)
				if e1 != nil || e2 != nil || !os.SameFile(src, dst) {
					t.Fatal("actual automatic hardlink", e1, e2)
				}
				if sample.ext != ".mkv" {
					if _, e = os.Stat(original.Files[0].Target); !os.IsNotExist(e) {
						t.Fatal("no sibling main publication", e)
					}
				}
			}
		})
	}
}

func TestOriginalAutomaticManualNameEventRetainsMetadata(t *testing.T) {
	a, object, original, cfg := originalTransferModuleFixture(t, "hardlink", "")
	ctx := context.Background()
	a.cfgMu.Lock()
	a.config.RecognizePluginFirst = true
	a.cfgMu.Unlock()
	calls := 0
	a.registerOriginalChainHandler("name.recognize", originalMutableEventHandler{ID: "owned-auto-name", Invoke: func(_ context.Context, event *plugins.MutableEvent) error {
		calls++
		event.Data = json.RawMessage(`{"name":"Changed.Film","year":"2025"}`)
		return nil
	}})
	cfg["modules"] = map[string]string{"recognize_media": "module_search_media", "obtain_images": "module_images", "transfer": "module_execute"}
	cfg["image_mode"], cfg["result"] = "false", nil
	raw, _ := json.Marshal(cfg)
	if _, e := a.pluginStore.saveOriginalPluginConfiguration(ctx, object.installation, raw); e != nil {
		t.Fatal(e)
	}
	if e := a.initOriginalLoadedPlugin(ctx, object.installation.InstanceID); e != nil {
		t.Fatal(e)
	}
	out, e := a.manualOrganize(ctx, manualOrganizeRequest{Paths: []string{original.Files[0].Source}, TargetPath: a.Settings().Roots[0].Path, Mode: "hardlink", Scraping: importFlag(false)})
	if e != nil || out.Submitted != 1 || calls != 1 {
		t.Fatal("ordinary name-event then recognition", out, calls, e)
	}
	var plan ImportPlan
	if e = a.Store.Get(ctx, "import_plan", out.Items[0].PlanID, &plan); e != nil {
		t.Fatal(e)
	}
	job := overwriteStart(t, a, plan)
	if e = a.runImport(ctx, job, plan.ID, nil); e != nil {
		t.Fatal(e)
	}
	var call struct{ Kwargs struct{ Meta map[string]any } }
	state := ownedModuleRead(t, object)
	if json.Unmarshal(state.Inputs["module_execute"], &call) != nil || call.Kwargs.Meta["name"] != "Changed Film" || call.Kwargs.Meta["year"] != "2025" || calls != 1 {
		t.Fatal("do not reparse away NameRecognize mutation", calls, string(state.Inputs["module_execute"]))
	}
}

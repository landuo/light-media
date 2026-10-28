package app

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/download"
)

func originalExistenceFixture(t *testing.T, result json.RawMessage) (*App, Subscription, *originalPluginInstance) {
	t.Helper()
	a, cookie, object := ownedModuleFixture(t, true)
	ctx := context.Background()
	provider := json.RawMessage(`{"id":900043,"name":"Owned existence","original_name":"Owned existence","first_air_date":"2020-01-01","number_of_seasons":1,"number_of_episodes":3,"seasons":[{"season_number":1,"episode_count":3}],"alternative_titles":{"results":[]},"genres":[],"credits":{"cast":[],"crew":[]},"external_ids":{}}`)
	full, e := sourceTMDBMediaInfo(provider, "tv", "image.tmdb.org", "")
	if e != nil {
		t.Fatal(e)
	}
	info, e := a.originalRecognitionProjection(catalog.Media{ID: 900043, MediaType: "tv"}, full)
	if e != nil {
		t.Fatal(e)
	}
	episodes := []catalog.Episode{}
	for n := 1; n <= 3; n++ {
		episodes = append(episodes, catalog.Episode{ID: int64(n), SeasonNumber: 1, EpisodeNumber: n, AirDate: "2020-01-01"})
	}
	for key, v := range map[string]any{"media:tv:900043": info.Media, "season:900043:1": catalog.Season{SeriesID: 900043, SeasonNumber: 1, Episodes: episodes}} {
		if e = a.Store.Put(ctx, "cache", key, map[string]any{"at": time.Now(), "data": v}); e != nil {
			t.Fatal(e)
		}
	}
	a.cfgMu.Lock()
	a.config.Downloaders = []DownloaderConfig{{Config: download.Config{ID: "owned-qb", Kind: "qb", BaseURL: "http://127.0.0.1:1"}}}
	a.cfgMu.Unlock()
	s := Subscription{ID: "owned-existence", Media: info.Media, Season: 1, TargetScope: "season", StartEpisode: 1, TotalEpisodes: 3, ManualTotalEpisode: true, DownloaderID: "owned-qb", State: "active", SourceState: "R", Revision: 1, EpisodeDates: map[int]string{}}
	if e = a.Store.Put(ctx, "subscription", s.ID, s); e != nil {
		t.Fatal(e)
	}
	cfg, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_page_size", "media_exists": "module_execute"}, "page_size": full, "result": result})
	saveOwnedModule(t, a, cookie, object, string(cfg))
	return a, s, originalBaseSDKLoaded(t, a, ownedModuleID)
}

func TestOriginalMediaExistsActualSubscriptionHost(t *testing.T) {
	a, s, object := originalExistenceFixture(t, json.RawMessage(`{"type":"电视剧","seasons":{"1":[1,2,3]}}`))
	before := ownedModuleRead(t, object)
	ctx := context.Background()
	check := importClaim(t, a, "subscription", s.ID)
	if e := a.checkSubscription(ctx, check, s.ID, s.Revision); e != nil {
		t.Fatal(e)
	}
	after := ownedModuleRead(t, object)
	if after.Calls["module_execute"]-before.Calls["module_execute"] != 1 {
		t.Fatal("existing subscription host bypassed fixed media_exists module", after.Calls, before.Calls)
	}
	var queued, assets, slots int
	a.Store.DB.QueryRow(`SELECT count(*) FROM jobs WHERE kind='subscription_completion_prepare'`).Scan(&queued)
	a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='asset'`).Scan(&assets)
	a.Store.DB.QueryRow(`SELECT count(*) FROM asset_slots`).Scan(&slots)
	if queued != 1 || assets != 0 || slots != 0 {
		t.Fatal("module fact did not reach completion or fabricated inventory", queued, assets, slots)
	}
}

func TestOriginalMediaExistsFixedSourceActualSDK(t *testing.T) {
	raw, e := os.ReadFile("../../testdata/standard-module-host/media-exists-source-oracle.json")
	if e != nil {
		t.Fatal(e)
	}
	var oracle struct {
		Fixed string `json:"fixed_backend"`
		Cases []struct {
			Name, Kind string
			Result     json.RawMessage
			Season     int
			Totals     map[string]int
			Satisfied  bool
			Error      *string
			Missing    map[string]map[string]struct {
				Episodes []int
				Total    int `json:"total_episode"`
				Start    int `json:"start_episode"`
			}
		}
	}
	if json.Unmarshal(raw, &oracle) != nil || oracle.Fixed != "8a83efeacd3e26b82be495e17d50146e31c96a60" || len(oracle.Cases) != 13 {
		t.Fatal("fixed source oracle required")
	}
	for _, sample := range oracle.Cases {
		t.Run(sample.Name, func(t *testing.T) {
			a, s, object := originalExistenceFixture(t, sample.Result)
			s.Media.MediaType, s.Season = sample.Kind, sample.Season
			total := sample.Totals[strconv.Itoa(s.Season)]
			if total == 0 {
				total = 3
			}
			s.TotalEpisodes = total
			meta := json.RawMessage(`{"type":"电视剧","seasons":{"1":[1,2,3]},"tmdb_id":900043}`)
			if sample.Kind == "movie" {
				meta = json.RawMessage(`{"type":"电影","tmdb_id":900043}`)
			}
			before := ownedModuleRead(t, object)
			resolved, e := a.resolveOriginalSubscriptionExistence(context.Background(), s, &standardSubscriptionRecognition{Information: sourceMediaInformation{MediaInfo: meta}})
			if sample.Error != nil {
				if e == nil {
					t.Fatal("fixed source error was accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			after := ownedModuleRead(t, object)
			var input struct {
				Args   []any
				Kwargs map[string]json.RawMessage
			}
			if json.Unmarshal(after.Inputs["module_execute"], &input) != nil || len(input.Args) != 0 || len(input.Kwargs) != 3 || string(input.Kwargs["itemid"]) != "null" || string(input.Kwargs["server"]) != "null" || !larkSameRaw(input.Kwargs["mediainfo"], meta) || after.Calls["module_execute"]-before.Calls["module_execute"] != 1 {
				t.Fatal("fixed wrapper kwargs changed", input, after.Calls)
			}
			known := []int{}
			for n := 1; n <= total; n++ {
				known = append(known, n)
			}
			coverage, e := a.subscriptionCoverageWithExistence(context.Background(), s, known, originalExistenceFromRecognition(resolved))
			if e != nil {
				t.Fatal(e)
			}
			expected := []int{}
			if sample.Kind == "movie" {
				if !sample.Satisfied {
					expected = []int{0}
				}
			} else if !sample.Satisfied {
				missing := sample.Missing["900043"][strconv.Itoa(s.Season)]
				expected = append(expected, missing.Episodes...)
				if len(expected) == 0 {
					for n := missing.Start; n < missing.Start+missing.Total; n++ {
						expected = append(expected, n)
					}
				}
			}
			slices.Sort(expected)
			slices.Sort(coverage.Missing)
			if !reflect.DeepEqual(expected, coverage.Missing) {
				t.Fatal("actual provider did not produce fixed source missing scope", coverage.Missing, expected)
			}
		})
	}
}

func TestOriginalMediaExistsCompletionUsesSealedObservation(t *testing.T) {
	a, s, object := originalExistenceFixture(t, json.RawMessage(`{"type":"电视剧","seasons":{"1":[1,2,3]}}`))
	ctx := context.Background()
	installNoticeSourceFixtures(t, a, s.Media, s.Season, s.TotalEpisodes)
	if e := a.checkSubscription(ctx, importClaim(t, a, "subscription", s.ID), s.ID, s.Revision); e != nil {
		t.Fatal(e)
	}
	before := ownedModuleRead(t, object)
	prepare := qualityRunningJob(t, a, reorgJob(t, a, "subscription_completion_prepare", s.ID))
	if e := a.safeRun(ctx, prepare); e != nil {
		t.Fatal("actual completion preparation", e)
	}
	after := ownedModuleRead(t, object)
	if after.Calls["module_execute"]-before.Calls["module_execute"] != 1 {
		t.Fatal("completion did not make one fresh existence lookup", after.Calls, before.Calls)
	}
	var remaining, history, assets, slots int
	a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='subscription'`).Scan(&remaining)
	a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='subscription_history'`).Scan(&history)
	a.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='asset'`).Scan(&assets)
	a.Store.DB.QueryRow(`SELECT count(*) FROM asset_slots`).Scan(&slots)
	if remaining != 0 || history != 1 || assets != 0 || slots != 0 {
		t.Fatal("completion failed or forged ownership", remaining, history, assets, slots)
	}
	job, e := scanJob(a.Store.DB.QueryRow(`SELECT ` + jobColumns + ` FROM jobs WHERE kind='subscription_completion'`))
	if e != nil {
		t.Fatal(e)
	}
	job = qualityRunningJob(t, a, job)
	if e = a.safeRun(ctx, job); e != nil {
		t.Fatal("actual completion notification/event", e)
	}
	var input struct{ ID string }
	if e = json.Unmarshal(job.Payload, &input); e != nil {
		t.Fatal(e)
	}
	var receipt subscriptionCompletionReceipt
	if e = a.readQualitySealed(ctx, a.Store.DB, "subscription_completion", input.ID, &receipt); e != nil || receipt.Stage != "complete" || receipt.Input.Ordinary == nil || receipt.Input.Ordinary.Version != 2 || receipt.Input.Ordinary.Existence == nil {
		t.Fatal("sealed ordinary observation missing", e, receipt.Stage)
	}
	settings, dir := a.Settings(), a.Dir
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	cold, e := New(Options{DataDir: dir})
	if e != nil {
		t.Fatal(e)
	}
	defer cold.Close()
	cold.config = settings
	job = qualityRunningJob(t, cold, job)
	if e = cold.safeRun(ctx, job); e != nil {
		t.Fatal("cold completed receipt replay", e)
	}
	var count int
	cold.Store.DB.QueryRow(`SELECT count(*) FROM records WHERE kind='source_subscribe_complete'`).Scan(&count)
	if count != 1 {
		t.Fatal("cold duplicated event", count)
	}
}

func TestOriginalMediaExistsTerminalSkipsNativeInventory(t *testing.T) {
	a, s, _ := originalExistenceFixture(t, json.RawMessage(`{"type":"电视剧","seasons":{"1":[1,3]}}`))
	// A terminal source result must bypass native inventory, not read it first
	// and overwrite its answer. This real SQL fault distinguishes those paths.
	if _, e := a.Store.DB.Exec(`ALTER TABLE asset_slots RENAME TO owned_unavailable_slots`); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if _, e := a.Store.DB.Exec(`ALTER TABLE owned_unavailable_slots RENAME TO asset_slots`); e != nil {
			t.Error(e)
		}
	}()
	if e := a.checkSubscription(context.Background(), importClaim(t, a, "subscription", s.ID), s.ID, s.Revision); e != nil {
		t.Fatal("terminal result still accessed native inventory", e)
	}
}

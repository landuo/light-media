package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
)

func ownedStandardSubscriptionMedia(t *testing.T, media catalog.Media, season, total int) json.RawMessage {
	t.Helper()
	provider := map[string]any{"id": media.ID, "name": media.Title, "original_name": media.OriginalTitle, "first_air_date": "2024-01-01", "genres": []any{}, "credits": map[string]any{"cast": []any{}, "crew": []any{}}, "external_ids": map[string]any{}, "seasons": []any{map[string]any{"season_number": season, "episode_count": total}}}
	raw, _ := json.Marshal(provider)
	full, err := sourceTMDBMediaInfo(raw, "tv", "image.tmdb.org", "")
	if err != nil {
		t.Fatal(err)
	}
	return full
}

func TestStandardSubscriptionPrecheckRecognizesWithoutEpisodesListener(t *testing.T) {
	for _, mode := range []string{"all-missing", "confirmed-highest", "lack-floor"} {
		t.Run(mode, func(t *testing.T) {
			a := subscriptionTestApp(t)
			cookie := setupTestAdmin(t, a)
			request := subscriptionRequest(t)
			request.TotalEpisodes = 12
			manual := false
			request.ManualTotalEpisode = &manual
			s, err := a.saveSubscription(context.Background(), "", request)
			if err != nil {
				t.Fatal(err)
			}
			initialLack := 12
			if mode == "confirmed-highest" {
				s.SourceNote, initialLack = []int{10}, 11
			} else if mode == "lack-floor" {
				initialLack = 1
			}
			s.LackEpisode = &initialLack
			if err = a.Store.Put(context.Background(), "subscription", s.ID, s); err != nil {
				t.Fatal(err)
			}
			manifest := installOrderWorker(t, a, cookie, os.Getenv("OWNED_MODULE_OVERRIDE_BINARY"))
			config, _ := json.Marshal(map[string]any{"enabled": true, "modules": map[string]string{"recognize_media": "module_execute"}, "result": ownedStandardSubscriptionMedia(t, s.Media, s.Season, 8)})
			r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+manifest.PluginID, string(config), cookie, "")
			assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
			a.wg.Wait()
			if a.originalEventHasHandler(plugins.EventQuery{Kind: plugins.ChainEventKind, Type: "subscribe.episodes.refresh"}) {
				t.Fatal("test artifact unexpectedly declares episodes listener")
			}
			object := originalBaseSDKLoaded(t, a, manifest.PluginID)
			before := ownedModuleRead(t, object)
			updated, err := a.refreshOrdinaryCompletionScope(context.Background(), s)
			want, lack := 8, 8
			if mode == "confirmed-highest" {
				want, lack = 10, 9
			} else if mode == "lack-floor" {
				lack = 0
			}
			after := ownedModuleRead(t, object)
			if err != nil || updated.TotalEpisodes != want || updated.LackEpisode == nil || *updated.LackEpisode != lack || after.Calls["module_execute"]-before.Calls["module_execute"] != 1 {
				t.Fatal("fixed source decrease or listener-independent recognition", updated.TotalEpisodes, want, err)
			}
			if updated.LastCheck == "" {
				t.Fatal("fixed source metadata total change did not record last_update")
			}
		})
	}
}

func TestStandardSubscriptionPreparationSingleRecognitionActualSDK(t *testing.T) {
	ordinaryMovieDownloadWorkflow(t, "tv", func(a *App, s Subscription) {
		ctx := context.Background()
		cookie := setupTestAdmin(t, a)
		manifest := installOrderWorker(t, a, cookie, os.Getenv("OWNED_EPISODES_REFRESH_FIXTURE_BINARY"))
		trace := filepath.Join(t.TempDir(), "single-recognition.jsonl")
		config, _ := json.Marshal(map[string]any{"enabled": true, "mode": "observe", "recognize_media": ownedStandardSubscriptionMedia(t, s.Media, s.Season, 1), "trace_path": trace})
		r := callTest(t, a.Handler(), "PUT", "/api/plugins/"+manifest.PluginID, string(config), cookie, "")
		assertOriginalRegistrySuccess(t, r.Code, r.Body.Bytes())
		a.wg.Wait()
		prepare, err := scanJob(a.Store.DB.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE kind='subscription_completion_prepare' AND json_extract(payload,'$.id')=? ORDER BY rowid LIMIT 1`, s.ID))
		if err != nil {
			t.Fatal(err)
		}
		prepare = qualityRunningJob(t, a, prepare)
		if err = a.prepareSubscriptionCompletion(ctx, prepare, s.ID, s.Revision); err != nil {
			t.Fatal(err)
		}
		rows := readOrderTrace(t, trace)
		calls := map[string]int{}
		for _, row := range rows {
			calls[row.Method]++
		}
		if calls["recognize_media"] != 1 || calls["chain_first"] != 1 || calls["chain_second"] != 1 {
			t.Fatal("fixed preparation repeated recognition instead of passing full snapshot", calls)
		}
		requireOwnedSubscriptionRecognizeSource(t, rows, s.Media.Title, "2024", "电视剧", s.Media.ID, s.Season)
		if err = a.Store.Get(ctx, "subscription", s.ID, &s); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("actual accepted download preparation did not commit history/delete", err)
		}
	}, func(in *SubscriptionRequest) {
		manual := false
		in.ManualTotalEpisode = &manual
	})
}

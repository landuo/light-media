package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// This is the native subscription row's source-shaped projection. Unsupported
// alternative providers/episode groups retain the original nullable columns;
// they are not inferred from a title or from a compact media search result.
func (a *App) subscriptionSourceProjection(ctx context.Context, s Subscription) (json.RawMessage, error) {
	return a.subscriptionSourceProjectionFrom(ctx, a.Store.DB, s)
}

// The caller can freeze both sides of a plugin event with the same owning
// transaction, without waiting for a second connection to the store.
func (a *App) subscriptionSourceProjectionFrom(ctx context.Context, q PluginProofReader, s Subscription) (json.RawMessage, error) {
	return a.subscriptionSourceProjectionReferences(ctx, q, s, false)
}

// Event observers can represent a missing named reference as unknown. Core
// acquisition/quality callers retain their strict dependency errors.
func (a *App) subscriptionSourceProjectionReferences(ctx context.Context, q PluginProofReader, s Subscription, missingDownloaderAsNull bool) (json.RawMessage, error) {
	state, e := a.subscriptionQualitySourceStateFrom(ctx, q, s)
	if e != nil {
		return nil, e
	}
	if state.State != "N" && state.State != "R" && state.State != "P" && state.State != "S" {
		if _, ok := s.SharedSourceFields["state"]; !ok {
			return nil, bad("SUBSCRIPTION_SOURCE_STATE_MISSING", "订阅缺少原始N/R/P/S运行状态")
		}
	}
	row := map[string]any{}
	for _, key := range []string{"tvdbid", "doubanid", "bangumiid", "anilistid", "filter", "include", "exclude", "quality", "resolution", "effect", "last_update", "username", "downloader", "custom_words", "media_category", "episode_group"} {
		row[key] = nil
	}
	flag := func(value bool) int {
		if value {
			return 1
		}
		return 0
	}
	row["id"], row["name"], row["type"], row["year"] = s.ID, s.Media.Title, state.Type, fmt.Sprint(s.Media.Year)
	if s.Media.Year == 0 {
		row["year"] = nil
	}
	row["keyword"], row["tmdbid"], row["imdbid"] = s.Keyword, s.Media.ID, s.Media.IMDbID
	if s.Media.IMDbID == "" {
		row["imdbid"] = nil
	}
	row["media_source"], row["media_id"], row["mediaid"] = "themoviedb", fmt.Sprint(s.Media.ID), nil
	row["season"], row["poster"], row["backdrop"] = s.Season, s.Media.PosterPath, s.Media.BackdropPath
	if s.Media.MediaType == "movie" {
		row["season"] = nil
	}
	row["vote"], row["description"] = s.Media.VoteAverage, s.Media.Overview
	row["total_episode"], row["start_episode"], row["lack_episode"] = s.TotalEpisodes, s.StartEpisode, s.LackEpisode
	if s.Media.MediaType == "movie" {
		row["total_episode"], row["start_episode"] = nil, nil
	}
	row["note"], row["state"], row["date"] = qualityCopyNote(s.SourceNote), s.SourceState, sourceSubscriptionDate(s.CreatedAt)
	if s.LastCheck != "" {
		row["last_update"] = sourceSubscriptionDate(s.LastCheck)
	}
	if len(state.Username) > 0 {
		row["username"] = json.RawMessage(append([]byte(nil), state.Username...))
	}
	row["sites"] = []int64{}
	sites := []int64{}
	for _, id := range s.SiteIDs {
		if sourceID, ok := sharedSubscriptionSourceID(id); ok {
			sites = append(sites, sourceID)
			continue
		}
		if strings.HasPrefix(id, "source-unrepresentable:") {
			continue
		}
		identity, e := a.Store.readNativeSiteIdentity(ctx, q, "site_id", id)
		if e != nil {
			return nil, e
		}
		sites = append(sites, identity.SourceID)
	}
	row["sites"] = sites
	if s.DownloaderID != "" && !strings.HasPrefix(s.DownloaderID, "source-downloader:") {
		d, e := a.configuredDownloader(s.DownloaderID)
		if e != nil {
			if !missingDownloaderAsNull {
				return nil, e
			}
			a.log("WARN", "plugins", "subscription_event_reference_missing", "订阅事件引用的下载器已不存在，名称保留为未知值", "", "")
		} else {
			row["downloader"] = d.Name
		}
	}
	row["best_version"], row["best_version_full"], row["current_priority"], row["episode_priority"] = flag(s.BestVersion), flag(s.BestVersionFull), s.CurrentPriority, s.EpisodePriority
	row["save_path"], row["search_imdbid"], row["manual_total_episode"] = s.DownloadPath, flag(s.SourceSearchIMDb), flag(s.ManualTotalEpisode)
	words, e := a.subscriptionWordsFrom(ctx, q, s.ID)
	if e != nil {
		return nil, e
	}
	if words != "" {
		row["custom_words"] = words
	}
	row["filter_groups"] = s.Policy.FilterGroups
	if s.Policy.FilterGroups == nil {
		row["filter_groups"] = []string{}
	}
	for _, field := range []struct{ key, value string }{{"include", s.Policy.Include}, {"exclude", s.Policy.Exclude}, {"quality", s.Policy.Source}, {"resolution", s.Policy.Resolution}, {"effect", s.Policy.HDR}} {
		if field.value != "" {
			row[field.key] = field.value
		}
	}
	if p := s.Policy.FilterParams; p != nil {
		for _, field := range []struct {
			key   string
			value *string
		}{{"include", p.Include}, {"exclude", p.Exclude}, {"quality", p.Quality}, {"resolution", p.Resolution}, {"effect", p.Effect}} {
			if field.value != nil {
				row[field.key] = *field.value
			}
		}
	}
	overlaySharedSubscription(s, row)
	return json.Marshal(row)
}
func sourceSubscriptionDate(value string) any {
	instant, e := time.Parse(time.RFC3339Nano, value)
	if e != nil {
		return nil
	}
	location, e := time.LoadLocation(identifiersTimezone())
	if e != nil {
		location = shanghai()
	}
	return instant.In(location).Format("2006-01-02 15:04:05")
}

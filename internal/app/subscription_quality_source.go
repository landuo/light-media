package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// This is the native subscription row's source-shaped projection. It emits
// only identities already bound to the saved subscription.
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
		return nil, bad("SUBSCRIPTION_SOURCE_STATE_MISSING", "订阅缺少原始N/R/P/S运行状态")
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
	if s.SourceYearText != nil {
		row["year"] = *s.SourceYearText
	}
	row["keyword"], row["tmdbid"], row["imdbid"] = s.Keyword, s.Media.ID, s.Media.IMDbID
	if s.Media.IMDbID == "" {
		row["imdbid"] = nil
	}
	row["media_source"], row["media_id"], row["mediaid"] = "themoviedb", fmt.Sprint(s.Media.ID), nil
	if providerNativeMedia(s.Media) {
		row["tmdbid"], row["media_source"], row["media_id"] = nil, s.Media.ProviderSource, s.Media.ProviderID
		switch s.Media.ProviderSource {
		case "douban":
			row["doubanid"] = s.Media.ProviderID
		case "bangumi":
			row["bangumiid"] = json.Number(s.Media.ProviderID)
		case "anilist":
			row["anilistid"] = json.Number(s.Media.ProviderID)
		}
	}
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
		identity, e := a.Store.readNativeSiteIdentity(ctx, q, "site_id", id)
		if e != nil {
			return nil, e
		}
		reader, ok := q.(interface {
			PluginProofReader
			QueryContext(context.Context, string, ...any) (*sql.Rows, error)
		})
		if !ok {
			return nil, nativeSourceSiteError()
		}
		sourceID, e := a.Store.nativeSourcePublicSiteID(ctx, reader, identity)
		if e != nil {
			return nil, e
		}
		sites = append(sites, sourceID)
	}
	row["sites"] = sites
	if s.DownloaderID != "" {
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
	for key, value := range s.SourceFlagValues {
		switch key {
		case "best_version", "best_version_full", "search_imdbid", "manual_total_episode":
			row[key] = value
		}
	}
	words, e := a.subscriptionWordsFrom(ctx, q, s.ID)
	if e != nil {
		return nil, e
	}
	if words != "" {
		row["custom_words"] = words
	}
	// Native string zero values can otherwise hide the difference between a
	// source SQL NULL and a deliberately written empty string.
	for _, key := range s.SourcePresentFields {
		switch key {
		case "include":
			row[key] = s.Policy.Include
		case "exclude":
			row[key] = s.Policy.Exclude
		case "quality":
			row[key] = s.Policy.Source
		case "resolution":
			row[key] = s.Policy.Resolution
		case "effect":
			row[key] = s.Policy.HDR
		case "custom_words":
			row[key] = words
		case "downloader":
			if s.DownloaderID == "" {
				row[key] = ""
			}
		case "imdbid":
			row[key] = s.Media.IMDbID
		case "season":
			row[key] = s.Season
		case "total_episode":
			row[key] = s.TotalEpisodes
		case "start_episode":
			row[key] = s.StartEpisode
		}
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
	// DB operator patches can explicitly store SQL/JSON null. Keep supported
	// nullable ordinary columns distinct from an omitted or empty value;
	// the native form's complete replacement save resets this metadata.
	for _, key := range s.SourceNullFields {
		switch key {
		case "sites", "filter_groups", "resolution", "quality", "effect", "include", "exclude", "save_path", "keyword", "imdbid", "year", "poster", "backdrop", "vote", "description", "season", "total_episode", "start_episode", "lack_episode", "note", "downloader", "best_version", "best_version_full", "current_priority", "episode_priority", "search_imdbid", "manual_total_episode", "custom_words":
			row[key] = nil
		}
	}
	return mustCleanSeedJSON(row), nil
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

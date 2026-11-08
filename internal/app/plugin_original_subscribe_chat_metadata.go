package app

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"light-media/internal/catalog"
)

// The chat metadata action follows SubscribeChain.check. It recognizes each
// current subscription, including paused ones, without running a search.
func (a *App) originalChatRefreshSubscriptionMetadata(ctx context.Context) error {
	rows, err := a.Store.originalSubscriptionAliases(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		sub := row.Subscription
		if sub.Media.MediaType != "movie" && sub.Media.MediaType != "tv" {
			a.log("WARN", "subscriptions", "metadata_type_invalid", "订阅类型无法识别，跳过元数据更新", "", "")
			continue
		}
		resolved, err := a.resolveStandardSubscriptionRecognition(ctx, sub, nil)
		if err != nil {
			var appError *APIError
			var providerError *catalog.Error
			if errors.As(err, &appError) && appError.Code == "MEDIA_NOT_FOUND" || errors.As(err, &providerError) && providerError.Code == "not_found" {
				a.log("WARN", "subscriptions", "metadata_not_found", "未识别到媒体信息，跳过订阅元数据更新", "", "")
				continue
			}
			return err
		}
		updated, err := a.originalChatSubscriptionMetadata(ctx, sub, resolved)
		if err != nil {
			return err
		}
		if err := a.storeOriginalChatSubscriptionMetadata(ctx, sub, updated); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) originalChatSubscriptionMetadata(ctx context.Context, sub Subscription, resolved *standardSubscriptionRecognition) (Subscription, error) {
	media := resolved.Information.Media
	value, err := decodeSourceJSON(resolved.Information.MediaInfo)
	fields := sourceMap(value)
	if err != nil || fields == nil || mediaKey(media) != mediaKey(sub.Media) {
		return sub, bad("SUBSCRIPTION_METADATA_INVALID", "订阅元数据与原媒体身份不一致")
	}
	updated := sub
	state := subscriptionQualityState(sub)
	if sub.Media.MediaType == "tv" {
		count := len(sourceRows(sourceMap(fields["seasons"])[strconv.Itoa(sub.Season)]))
		if providerNativeMedia(sub.Media) {
			if episodes := subscriptionProviderEpisodes(resolved.Information.MediaInfo, sub.Season); len(episodes) > 0 {
				updated.ProviderEpisodes = episodes
			}
		}
		total := sub.TotalEpisodes
		if !sub.ManualTotalEpisode && count > 0 {
			total, err = a.applyStandardSubscriptionEpisodes(ctx, count, sub.Season, resolved.Information.MediaInfo, sub.ID, "refresh")
			if err != nil {
				return sub, err
			}
			if total < sub.TotalEpisodes {
				total = a.resolveStandardSubscriptionTotalDecreaseResolved(ctx, sub, total, resolved)
			}
		}
		if total > 10000 {
			return sub, bad("SUBSCRIPTION_METADATA_INVALID", "订阅元数据总集数超过可处理范围")
		}
		if total != sub.TotalEpisodes {
			state = state.ChangeTotal(total)
		} else if sub.BestVersion {
			state = state.Progress(0)
		}
		setSubscriptionQualityState(&updated, state)
	}
	updated.Media.Title = media.Title
	updated.Media.Year = media.Year
	updated.SourceYearText = nil
	if fields["year"] == nil {
		updated.Media.Year = 0
	} else if year, ok := fields["year"].(string); ok && (year != strconv.Itoa(media.Year) || media.Year == 0) {
		updated.SourceYearText = &year
	}
	imageDomain := a.Settings().TMDBImageDomain
	updated.Media.PosterPath = originalChatSubscriptionImage(sourceText(fields["poster_path"]), imageDomain)
	updated.Media.BackdropPath = originalChatSubscriptionImage(sourceText(fields["backdrop_path"]), imageDomain)
	if providerNativeMedia(sub.Media) {
		updated.Media.PosterPath = catalog.ExploreImageURL(sub.Media.ProviderSource, sourceText(fields["poster_path"]))
		updated.Media.BackdropPath = catalog.ExploreImageURL(sub.Media.ProviderSource, sourceText(fields["backdrop_path"]))
	}
	updated.Media.Overview, updated.Media.IMDbID = sourceText(fields["overview"]), sourceText(fields["imdb_id"])
	updated.Media.VoteAverage = 0
	if fields["vote_average"] != nil {
		updated.Media.VoteAverage, err = strconv.ParseFloat(sourceNumberString(fields["vote_average"]), 64)
		if err != nil {
			return sub, bad("SUBSCRIPTION_METADATA_INVALID", "订阅元数据评分无效")
		}
	}
	// The source writes these fields even if recognition clears their values.
	// Keep SQL null distinct from empty text where the host model supports it.
	updated.SourceNullFields = slices.DeleteFunc(slices.Clone(sub.SourceNullFields), func(key string) bool {
		switch key {
		case "year", "vote", "poster", "backdrop", "description", "imdbid":
			return true
		case "total_episode":
			return updated.TotalEpisodes != sub.TotalEpisodes
		case "lack_episode":
			return updated.LackEpisode != nil
		case "episode_priority":
			return updated.EpisodePriority != nil
		case "current_priority":
			return updated.CurrentPriority != nil
		}
		return false
	})
	for _, field := range []struct{ column, key string }{{"description", "overview"}, {"imdbid", "imdb_id"}, {"vote", "vote_average"}, {"year", "year"}} {
		if fields[field.key] == nil {
			updated.SourceNullFields = append(updated.SourceNullFields, field.column)
		}
	}
	slices.Sort(updated.SourceNullFields)
	updated.SourcePresentFields = slices.DeleteFunc(slices.Clone(sub.SourcePresentFields), func(key string) bool { return key == "imdbid" })
	if fields["imdb_id"] != nil && updated.Media.IMDbID == "" {
		updated.SourcePresentFields = append(updated.SourcePresentFields, "imdbid")
		slices.Sort(updated.SourcePresentFields)
	}
	return updated, nil
}

// MoviePilot stores a full w500 image URL. Native subscription views use
// relative image paths through the host's existing TMDB image endpoint.
func originalChatSubscriptionImage(path, domain string) string {
	if assetImagePattern.MatchString(path) && len(path) <= 128 {
		return path
	}
	if domain == "" {
		domain = "image.tmdb.org"
	}
	address, err := url.Parse(path)
	if err != nil || address.Scheme != "https" || !strings.EqualFold(address.Host, domain) || address.User != nil || address.RawQuery != "" || address.ForceQuery || address.Fragment != "" {
		return ""
	}
	for _, prefix := range []string{"/t/p/original", "/t/p/w500"} {
		relative, ok := strings.CutPrefix(address.Path, prefix)
		if ok && len(relative) <= 128 && assetImagePattern.MatchString(relative) {
			return relative
		}
	}
	return ""
}

func (a *App) storeOriginalChatSubscriptionMetadata(ctx context.Context, old, updated Subscription) error {
	if reflect.DeepEqual(old, updated) {
		return nil
	}
	a.subMu.Lock()
	defer a.subMu.Unlock()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current Subscription
	err = qualityRecord(ctx, tx, "subscription", old.ID, &current)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !reflect.DeepEqual(current, old) {
		// Download reconciliation can update progress without advancing the
		// configuration revision. Never replace either type of newer state.
		a.log("WARN", "subscriptions", "metadata_snapshot_changed", "订阅已变化，跳过过期的元数据结果", "", "")
		return nil
	}
	if err != nil {
		return err
	}
	if err := txPut(ctx, tx, "subscription", updated.ID, updated); err != nil {
		return err
	}
	return tx.Commit()
}

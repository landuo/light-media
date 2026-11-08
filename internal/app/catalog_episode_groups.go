package app

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"time"

	"light-media/internal/catalog"
)

var catalogEpisodeGroupID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type catalogEpisodeGroupResponse struct {
	Groups []catalogEpisodeGroup `json:"groups"`
}

type catalogEpisodeGroup struct {
	Order    *int                  `json:"order"`
	Name     string                `json:"name"`
	Episodes []catalogGroupEpisode `json:"episodes"`
}

type catalogGroupEpisode struct {
	SeasonNumber  *int    `json:"season_number"`
	EpisodeNumber int     `json:"episode_number"`
	Name          string  `json:"name"`
	Overview      string  `json:"overview"`
	AirDate       string  `json:"air_date"`
	StillPath     string  `json:"still_path"`
	Runtime       int     `json:"runtime"`
	VoteAverage   float64 `json:"vote_average"`
}

func (a *App) catalogEpisodeGroup(ctx context.Context, id int64, group string) (catalogEpisodeGroupResponse, error) {
	var result catalogEpisodeGroupResponse
	if !validMedia("tv", id) || !catalogEpisodeGroupID.MatchString(group) {
		return result, bad("INVALID_EPISODE_GROUP", "请选择有效的电视剧和剧集组")
	}
	var native sourceMediaInformation
	_, err := a.cached(ctx, "source-detail:tv:"+strconv.FormatInt(id, 10), 24*time.Hour, &native, func() (any, error) {
		return a.sourceRecognizeTMDBCore(ctx, catalog.Media{ID: id, MediaType: "tv"})
	})
	if err != nil {
		return result, err
	}
	var series struct {
		ID            int64 `json:"id"`
		EpisodeGroups struct {
			Results []struct {
				ID string `json:"id"`
			} `json:"results"`
		} `json:"episode_groups"`
	}
	if json.Unmarshal(native.ProviderInfo, &series) != nil || series.ID != id {
		return result, &catalog.Error{Code: "invalid_response"}
	}
	for _, entry := range series.EpisodeGroups.Results {
		if entry.ID == group {
			return a.readCatalogEpisodeGroup(ctx, group)
		}
	}
	return result, &APIError{Code: "EPISODE_GROUP_NOT_FOUND", Message: "此电视剧没有该剧集组", Status: 404}
}

func (a *App) readCatalogEpisodeGroup(ctx context.Context, group string) (catalogEpisodeGroupResponse, error) {
	var result catalogEpisodeGroupResponse
	key := a.originalHostMediaTMDBProviderKey([]any{"group", group})
	raw, err := a.originalHostMediaTMDBResponse(ctx, "tv", key, func() (catalog.SourceRecognitionResponse, error) {
		return a.catalog().SourceRecognitionGroupResponse(ctx, group, a.originalHostMediaEffectiveLocale())
	})
	if err != nil {
		return result, err
	}
	var payload struct {
		ID string `json:"id"`
		catalogEpisodeGroupResponse
	}
	if json.Unmarshal(raw, &payload) != nil || (payload.ID != "" && payload.ID != group) || payload.Groups == nil || len(payload.Groups) > 1000 {
		return result, &catalog.Error{Code: "invalid_response"}
	}
	total := 0
	for i := range payload.Groups {
		season := &payload.Groups[i]
		total += len(season.Episodes)
		if season.Order == nil || *season.Order < 0 || total > 10000 {
			return result, &catalog.Error{Code: "invalid_response"}
		}
		if season.Episodes == nil {
			season.Episodes = []catalogGroupEpisode{}
		}
		for j := range season.Episodes {
			episode := &season.Episodes[j]
			// Group position controls presentation only. Search must use the
			// canonical TMDB season and episode numbers on the provider row.
			if episode.SeasonNumber == nil || *episode.SeasonNumber < 0 || episode.EpisodeNumber < 1 {
				return result, &catalog.Error{Code: "invalid_response"}
			}
			if len(episode.StillPath) > 128 || !assetImagePattern.MatchString(episode.StillPath) {
				episode.StillPath = ""
			}
		}
	}
	return payload.catalogEpisodeGroupResponse, nil
}

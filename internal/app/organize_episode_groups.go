package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"light-media/internal/catalog"
)

// Group numbering is presentation metadata. Season/Episodes plus optional
// cross-season EpisodeScopes own canonical TMDB inventory and reservations.
type importGroupNumbering struct {
	Season   int   `json:"season"`
	Episodes []int `json:"episodes"`
}

func (file PlanFile) presentationFile() PlanFile {
	if file.GroupNumbering != nil {
		file.Season = file.GroupNumbering.Season
		file.Episodes = append([]int(nil), file.GroupNumbering.Episodes...)
	}
	return file
}

type organizeEpisodeGroupOption struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	GroupCount   int    `json:"group_count"`
	EpisodeCount int    `json:"episode_count"`
}

func (a *App) organizeEpisodeGroupOptions(ctx context.Context, id int64) ([]organizeEpisodeGroupOption, error) {
	if !validMedia("tv", id) {
		return nil, bad("INVALID_MEDIA", "请选择明确的电视剧身份")
	}
	// Share the original recognition provider cache, rather than fetching the
	// same TV header once for the picker and again when organizing.
	info, err := a.originalHostMediaTMDBInfo(ctx, "tv", json.Number(strconv.FormatInt(id, 10)))
	if err != nil {
		return nil, err
	}
	if len(info) == 0 {
		return nil, &catalog.Error{Code: "invalid_response"}
	}
	out := []organizeEpisodeGroupOption{}
	raw, err := json.Marshal(sourceMap(info["episode_groups"])["results"])
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return out, nil
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, &catalog.Error{Code: "invalid_response"}
	}
	return out, nil
}

func (a *App) organizeEpisodeGroup(ctx context.Context, id int64, group string) (catalogEpisodeGroupResponse, error) {
	if !catalogEpisodeGroupID.MatchString(group) {
		return catalogEpisodeGroupResponse{}, bad("INVALID_EPISODE_GROUP", "请选择有效的剧集组")
	}
	options, err := a.organizeEpisodeGroupOptions(ctx, id)
	if err != nil {
		return catalogEpisodeGroupResponse{}, err
	}
	for _, option := range options {
		if option.ID == group {
			return a.readCatalogEpisodeGroup(ctx, group)
		}
	}
	return catalogEpisodeGroupResponse{}, &APIError{Code: "EPISODE_GROUP_NOT_FOUND", Message: "此电视剧没有该剧集组", Status: 404}
}

// Native scraping reads the same raw group rows directly, preserving episode
// IDs that the SDK's TmdbEpisode projection deliberately omits.
func (a *App) nativeGroupSeason(ctx context.Context, id int64, number int, group string) (catalog.Season, error) {
	view := catalog.Season{SeriesID: id, SeasonNumber: number}
	seasons, err := a.originalHostMediaGroupSeasons(ctx, group)
	if err != nil {
		return view, err
	}
	for _, season := range seasons {
		if originalCatalogIdentityString(season["order"]) != strconv.Itoa(number) {
			continue
		}
		view.Name = sourceText(season["name"])
		view.Overview = sourceText(season["overview"])
		view.AirDate = sourceText(season["air_date"])
		raw, err := json.Marshal(season["episodes"])
		if err != nil {
			return view, err
		}
		if err = json.Unmarshal(raw, &view.Episodes); err != nil {
			return view, err
		}
		return view, nil
	}
	return view, nil
}

// The synchronous transfer chain exposes the source's group-position episode
// numbers to SDK modules. Its raw season_number remains the provider value.
func (a *App) importGroupSeason(ctx context.Context, id int64, number int, group string) (catalog.Season, error) {
	view := catalog.Season{SeriesID: id, SeasonNumber: number}
	canonical, err := a.readCatalogEpisodeGroup(ctx, group)
	if err != nil {
		return view, err
	}
	rows, err := a.originalCatalogEpisodesMethod(ctx, "tmdb_episodes", id, number, group, func() (json.RawMessage, error) {
		seasons, err := a.originalHostMediaGroupSeasons(ctx, group)
		if err != nil {
			return nil, err
		}
		for _, season := range seasons {
			if originalCatalogIdentityString(season["order"]) == strconv.Itoa(number) {
				return json.Marshal(season["episodes"])
			}
		}
		return json.RawMessage(`[]`), nil
	}, nil)
	if err != nil {
		return view, err
	}
	view, err = originalCatalogSeasonProjection(view, rows)
	if err != nil {
		return view, err
	}
	for _, season := range canonical.Groups {
		if season.Order == nil || *season.Order != number {
			continue
		}
		for i := range view.Episodes {
			episode := &view.Episodes[i]
			if episode.EpisodeNumber > 0 && episode.EpisodeNumber <= len(season.Episodes) {
				episode.ID = season.Episodes[episode.EpisodeNumber-1].ID
			}
		}
	}
	return view, nil
}

func applyImportEpisodeGroup(file *PlanFile, group catalogEpisodeGroupResponse) error {
	for _, season := range group.Groups {
		if season.Order == nil || *season.Order != file.Season {
			continue
		}
		var scopes []canonicalEpisodeScope
		positions := map[int]int{}
		seen := map[canonicalEpisodeSlot]bool{}
		for _, number := range file.Episodes {
			if number < 1 || number > len(season.Episodes) {
				return bad("EPISODE_GROUP_NUMBERING", fmt.Sprintf("剧集组第%d季没有第%d集", file.Season, number))
			}
			// MoviePilot numbers the provider array in order, not by its optional
			// episode.order field. Keep the untouched canonical numbers separately.
			episode := season.Episodes[number-1]
			if episode.SeasonNumber == nil {
				return &catalog.Error{Code: "invalid_response"}
			}
			slot := canonicalEpisodeSlot{*episode.SeasonNumber, episode.EpisodeNumber}
			if seen[slot] {
				continue
			}
			seen[slot] = true
			index, exists := positions[slot.Season]
			if !exists {
				index = len(scopes)
				positions[slot.Season] = index
				scopes = append(scopes, canonicalEpisodeScope{Season: slot.Season})
			}
			scopes[index].Episodes = append(scopes[index].Episodes, slot.Episode)
		}
		if len(scopes) == 0 && file.DirectoryDigest == "" {
			return bad("EPISODE_GROUP_NUMBERING", "剧集组需要明确集号")
		}
		file.GroupNumbering = &importGroupNumbering{Season: file.Season, Episodes: append([]int(nil), file.Episodes...)}
		file.EpisodeScopes = nil
		if len(scopes) == 0 {
			// A disc without an explicit episode range has presentation metadata,
			// not evidence for every episode in this group. Preserve the existing
			// unknown-content inventory slot, rather than claiming a whole season.
			file.Season, file.Episodes = 0, nil
		} else {
			file.Season, file.Episodes = scopes[0].Season, append([]int(nil), scopes[0].Episodes...)
			if len(scopes) > 1 {
				file.EpisodeScopes = scopes
			}
		}
		return nil
	}
	return bad("EPISODE_GROUP_NUMBERING", fmt.Sprintf("剧集组没有第%d季", file.Season))
}

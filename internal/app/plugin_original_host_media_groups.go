package app

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"

	"light-media/internal/catalog"
)

var originalHostMediaGroupDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func (a *App) originalHostMediaGroupSeasons(ctx context.Context, group string) ([]map[string]any, error) {
	key := a.originalHostMediaTMDBProviderKey([]any{"group", group})
	body, err := a.originalHostMediaTMDBResponse(ctx, "tv", key, func() (catalog.SourceRecognitionResponse, error) {
		return a.catalog().SourceRecognitionGroupResponse(ctx, group, a.originalHostMediaEffectiveLocale())
	})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// Fixed TmdbApi.get_tv_group_seasons catches provider/decoding errors and
	// returns []; the Chain's eventual recognition result still carries errors
	// from its own detail lookup. No successful group lookup is fabricated.
	if err != nil {
		return []map[string]any{}, nil
	}
	value, err := decodeSourceJSON(body)
	if err != nil {
		return []map[string]any{}, nil
	}
	result := []map[string]any{}
	for _, value := range sourceRows(sourceMap(value)["groups"]) {
		row := sourceMap(value)
		if row == nil {
			return []map[string]any{}, nil
		}
		episodes := sourceRows(row["episodes"])
		for index, value := range episodes {
			episode := sourceMap(value)
			if episode == nil {
				return []map[string]any{}, nil
			}
			episode["episode_number"] = json.Number(strconv.Itoa(index + 1))
		}
		if episodes == nil {
			episodes = []any{}
		}
		row["episodes"] = episodes
		result = append(result, row)
	}
	return result, nil
}

func (a *App) originalHostMediaProcessGroups(ctx context.Context, raw json.RawMessage, group string, groups []map[string]any) (json.RawMessage, error) {
	value, err := decodeSourceJSON(raw)
	if err != nil {
		return nil, err
	}
	info := sourceMap(value)
	available := sourceRows(info["episode_groups"])
	if info["type"] != "电视剧" || len(available) == 0 {
		return raw, nil
	}
	if len(groups) > 0 {
		seasons := map[string]any{}
		seasonInfo := []any{}
		years := map[string]string{}
		for _, entry := range groups {
			episodes := sourceRows(entry["episodes"])
			if len(episodes) == 0 {
				continue
			}
			key := originalCatalogIdentityString(entry["order"])
			if key == "" || key == "null" {
				key = "null"
			}
			numbers := []any{}
			for _, episode := range episodes {
				numbers = append(numbers, sourceMap(episode)["episode_number"])
			}
			seasons[key] = numbers
			seasonInfo = append(seasonInfo, entry)
			date := sourceText(sourceMap(episodes[0])["air_date"])
			if originalHostMediaGroupDate.MatchString(date) {
				years[key] = date[:4]
			}
		}
		if len(seasons) > 0 {
			info["seasons"] = seasons
			info["number_of_seasons"] = len(seasons)
		}
		if len(seasonInfo) > 0 {
			info["season_info"] = seasonInfo
		}
		if len(years) > 0 {
			info["season_years"] = years
		}
		info["episode_group"] = group
		info["episode_groups"] = groups
		return json.Marshal(info)
	}
	years := map[string]string{}
	for _, availableGroup := range available {
		entry := sourceMap(availableGroup)
		if originalCatalogIdentityString(entry["type"]) != "6" {
			continue
		}
		id := sourceText(entry["id"])
		if id == "" {
			continue
		}
		seasons, err := a.originalHostMediaGroupSeasons(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, season := range seasons {
			episodes := sourceRows(season["episodes"])
			if len(episodes) == 0 {
				continue
			}
			date := sourceText(sourceMap(episodes[0])["air_date"])
			if !originalHostMediaGroupDate.MatchString(date) {
				continue
			}
			key := originalCatalogIdentityString(season["order"])
			if key == "" {
				key = strconv.Itoa(0)
			}
			years[key] = date[:4]
		}
	}
	if len(years) > 0 {
		info["season_years"] = years
	}
	return json.Marshal(info)
}

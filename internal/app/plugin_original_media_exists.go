package app

import (
	"context"
	"encoding/json"
	"math/big"
	"slices"
	"strconv"
)

// An ordinary module observation is an existence fact, never ownership of a
// local file. Keep its original DTO in the existing sealed completion input;
// do not manufacture assets, slots, registrations or cleanup permissions.
type originalSubscriptionExistence struct {
	MediaInfo json.RawMessage
	Result    json.RawMessage
}

func (a *App) resolveOriginalSubscriptionExistence(ctx context.Context, s Subscription, resolved *standardSubscriptionRecognition) (*standardSubscriptionRecognition, error) {
	groups, err := a.originalPluginModules(ctx, "")
	if err != nil {
		return resolved, err
	}
	if !originalModuleGroupsHaveMethod(groups, "media_exists") {
		return resolved, nil
	}
	if resolved == nil {
		resolved, err = a.resolveStandardSubscriptionRecognition(ctx, s, nil)
		if err != nil {
			return nil, err
		}
	}
	current := *resolved
	current.Existence = nil
	resolved = &current
	// Local single-user host has no media-server item index. The pinned wrapper
	// still passes both optional arguments, including their explicit nulls.
	kwargs, _ := json.Marshal(map[string]any{"mediainfo": resolved.Information.MediaInfo, "itemid": nil, "server": nil})
	result, err := a.invokeOriginalPluginModuleGroups(ctx, "media_exists", nil, json.RawMessage(`[]`), kwargs, nil, groups)
	if err != nil {
		return nil, err
	}
	if originalModuleResultEmpty(result) {
		return resolved, nil
	} // Native inventory fallback.
	var list []json.RawMessage
	if json.Unmarshal(result, &list) == nil && list != nil {
		// ChainBase invokes native providers after a list result as well. The
		// local native provider returns an existence DTO (never a list), so its
		// value cannot replace the plugin list. Its read/error effects still run
		// before DownloadChain interprets list truth or rejects a TV list.
		if _, err := a.originalNativeSubscriptionExistence(ctx, s); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			a.reportOriginalPluginExecutionError(ctx, "system", "文件整理发生了错误", map[string]any{"type": "module", "module_id": "FileManagerModule", "module_name": "文件整理", "module_method": "media_exists"}, err)
		}
	}
	observed := &originalSubscriptionExistence{MediaInfo: resolved.Information.MediaInfo, Result: result}
	if _, err = observed.episodes(s); err != nil {
		return nil, err
	}
	resolved.Existence = observed
	return resolved, nil
}

func (a *App) originalNativeSubscriptionExistence(ctx context.Context, s Subscription) (json.RawMessage, error) {
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT season,episode FROM asset_slots WHERE media_key=? ORDER BY season,episode`, mediaKey(s.Media))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seasons := map[string][]int{}
	found := false
	for rows.Next() {
		var season, episode int
		if err := rows.Scan(&season, &episode); err != nil {
			return nil, err
		}
		found = true
		key := strconv.Itoa(season)
		if !slices.Contains(seasons[key], episode) {
			seasons[key] = append(seasons[key], episode)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !found {
		return json.RawMessage(`null`), nil
	}
	kind := "电视剧"
	if s.Media.MediaType == "movie" {
		kind = "电影"
	}
	return json.Marshal(map[string]any{"type": kind, "seasons": seasons})
}

func (observation *originalSubscriptionExistence) episodes(s Subscription) ([]int, error) {
	if observation == nil {
		return nil, nil
	}
	value, err := decodeSourceJSON(observation.Result)
	if err != nil {
		return nil, originalMediaExistsInvalid()
	}
	fields := sourceMap(value)
	// ExistMediaInfo is an object even when all its optional fields are empty.
	truth := fields != nil || sourceSubscriptionTruth(value)
	if !truth {
		return []int{}, nil
	}
	if s.Media.MediaType == "movie" {
		return []int{0}, nil
	}
	if fields == nil {
		return nil, originalMediaExistsInvalid()
	}
	seasons, has := fields["seasons"]
	if !has {
		seasons = map[string]any{}
	} // Pydantic default_factory=dict.
	bySeason := sourceMap(seasons)
	if bySeason == nil {
		return nil, originalMediaExistsInvalid()
	}
	list := bySeason[strconv.Itoa(s.Season)]
	if !sourceSubscriptionTruth(list) {
		return []int{}, nil
	}
	rows, ok := list.([]any)
	if !ok {
		return nil, originalMediaExistsInvalid()
	}
	episodes := []int{}
	for _, value := range rows {
		// Python's set difference treats True and 1, or 2.0 and 2, as equal;
		// a numeric string remains distinct from an integer episode.
		var n int
		switch v := value.(type) {
		case bool:
			if v {
				n = 1
			}
		case json.Number:
			number, valid := new(big.Rat).SetString(v.String())
			if !valid || !number.IsInt() || !number.Num().IsInt64() {
				continue
			}
			integer := number.Num().Int64()
			if int64(int(integer)) != integer {
				continue
			}
			n = int(integer)
		default:
			continue
		}
		if !slices.Contains(episodes, n) {
			episodes = append(episodes, n)
		}
	}
	return episodes, nil
}

func originalMediaExistsInvalid() error {
	return bad("PLUGIN_MEDIA_EXISTS_INVALID", "插件媒体存在信息缺少有效的季集对象")
}

func originalExistenceFromRecognition(resolved *standardSubscriptionRecognition) *originalSubscriptionExistence {
	if resolved == nil {
		return nil
	}
	return resolved.Existence
}

package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"light-media/internal/plugins"
)

// Fixed SubscribeOper.update normalizes four integer flags and directly
// patches ORM columns. These mappings change existing executable host fields;
// no arbitrary JSON column or unimplemented subscription option is retained.
func (a *App) originalHostExtendedSubscriptionPatch(ctx context.Context, tx *sql.Tx, s *Subscription, p plugins.SubscriptionHostPatchV1, fields map[string]json.RawMessage) (*string, bool, error) {
	nulls := map[string]bool{}
	for _, key := range p.NullFields {
		nulls[key] = true
	}
	present := map[string]bool{}
	for _, key := range s.SourcePresentFields {
		present[key] = true
	}
	for _, key := range []string{"include", "exclude", "quality", "resolution", "effect", "custom_words", "downloader", "imdbid"} {
		if raw, ok := fields[key]; ok {
			if string(raw) == `""` {
				present[key] = true
			} else {
				delete(present, key)
			}
		}
	}
	for _, key := range []string{"season", "total_episode", "start_episode"} {
		if raw, ok := fields[key]; ok {
			if s.Media.MediaType == "movie" && string(raw) != "null" {
				present[key] = true
			} else {
				delete(present, key)
			}
		}
	}
	s.SourcePresentFields = nil
	for key := range present {
		s.SourcePresentFields = append(s.SourcePresentFields, key)
	}
	slices.Sort(s.SourcePresentFields)
	for _, field := range []struct {
		key string
		in  *string
		out *string
	}{{"name", p.Name, &s.Media.Title}, {"poster", p.Poster, &s.Media.PosterPath}, {"backdrop", p.Backdrop, &s.Media.BackdropPath}, {"description", p.Description, &s.Media.Overview}, {"imdbid", p.IMDbID, &s.Media.IMDbID}} {
		if field.in != nil {
			*field.out = *field.in
		}
		if nulls[field.key] {
			*field.out = ""
		}
	}
	if p.Year != nil {
		year := 0
		if *p.Year != "" {
			n, err := strconv.Atoi(*p.Year)
			if err != nil || n < 0 || n > 9999 {
				return nil, false, bad("INVALID_PLUGIN_VALUE", "原生年份须为0至9999的十进制文本或空串")
			}
			year = n
		}
		s.Media.Year = year
		text := *p.Year
		if text == strconv.Itoa(year) && year != 0 {
			s.SourceYearText = nil
		} else {
			s.SourceYearText = &text
		}
	}
	if nulls["year"] {
		s.Media.Year = 0
		s.SourceYearText = nil
	}
	if p.Vote != nil {
		s.Media.VoteAverage = *p.Vote
	}
	if nulls["vote"] {
		s.Media.VoteAverage = 0
	}
	if p.State != nil {
		switch *p.State {
		case "N", "R", "P":
			s.SourceState = *p.State
			s.State = "active"
		case "S":
			s.SourceState = "S"
			s.State = "paused"
		default:
			return nil, false, bad("INVALID_PLUGIN_VALUE", "原生订阅运行状态须为N/R/P/S")
		}
	}
	if p.Season != nil {
		s.Season = *p.Season
	}
	if nulls["season"] {
		if s.Media.MediaType == "tv" {
			return nil, false, bad("INVALID_PLUGIN_VALUE", "原生电视剧须有明确季号；null不能冒充第0季")
		}
		s.Season = 0
	}
	if s.Season < 0 || s.Season > 9999 {
		return nil, false, bad("INVALID_PLUGIN_VALUE", "原生季号超出范围")
	}
	for _, field := range []struct {
		key string
		in  *int
		out *int
	}{{"total_episode", p.TotalEpisode, &s.TotalEpisodes}, {"start_episode", p.StartEpisode, &s.StartEpisode}} {
		if field.in != nil {
			*field.out = *field.in
		}
		if nulls[field.key] {
			*field.out = 0
		}
		if *field.out < 0 || *field.out > 9999 {
			return nil, false, bad("INVALID_PLUGIN_VALUE", "原生集数范围须为0至9999")
		}
	}
	if p.LackEpisode != nil {
		v := *p.LackEpisode
		s.LackEpisode = &v
	}
	if nulls["lack_episode"] {
		s.LackEpisode = nil
	}
	if p.CurrentPriority != nil {
		v := *p.CurrentPriority
		s.CurrentPriority = &v
	}
	if nulls["current_priority"] {
		s.CurrentPriority = nil
	}
	if p.Note != nil {
		s.SourceNote = append([]int{}, (*p.Note)...)
	}
	if nulls["note"] {
		s.SourceNote = nil
	}
	if p.EpisodePriority != nil {
		s.EpisodePriority = map[string]int{}
		for k, v := range *p.EpisodePriority {
			s.EpisodePriority[k] = v
		}
	}
	if nulls["episode_priority"] {
		s.EpisodePriority = nil
	}
	for _, field := range []struct {
		key string
		in  *plugins.SubscriptionHostFlagV1
		out *bool
	}{{"best_version", p.BestVersion, &s.BestVersion}, {"best_version_full", p.BestVersionFull, &s.BestVersionFull}, {"search_imdbid", p.SearchIMDbID, &s.SourceSearchIMDb}, {"manual_total_episode", p.ManualTotalEpisode, &s.ManualTotalEpisode}} {
		if field.in != nil {
			normalized, truth, err := originalHostSubscriptionSQLiteFlag(ctx, tx, *field.in)
			if err != nil {
				return nil, false, err
			}
			*field.out = truth
			if normalized.Integer == nil || *normalized.Integer != 0 && *normalized.Integer != 1 {
				if s.SourceFlagValues == nil {
					s.SourceFlagValues = map[string]plugins.SubscriptionHostFlagV1{}
				}
				s.SourceFlagValues[field.key] = normalized
			} else {
				delete(s.SourceFlagValues, field.key)
			}
		}
		if nulls[field.key] {
			*field.out = false
			delete(s.SourceFlagValues, field.key)
		}
	}
	if len(s.SourceFlagValues) == 0 {
		s.SourceFlagValues = nil
	}
	if p.Downloader != nil {
		if *p.Downloader != "" {
			if _, err := a.configuredDownloader(*p.Downloader); err != nil {
				return nil, false, err
			}
		}
		s.DownloaderID = *p.Downloader
	}
	if nulls["downloader"] {
		s.DownloaderID = ""
	}
	if p.Season != nil && s.Media.MediaType == "tv" {
		for _, mapping := range s.Mappings {
			if mapping.TargetSeason != s.Season {
				return nil, false, bad("INVALID_MAPPING", "修改季号须先通过原生编辑器调整既有集号映射")
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT payload FROM records WHERE kind='subscription' AND id<>?`, s.ID)
		if err != nil {
			return nil, false, err
		}
		duplicate := false
		for rows.Next() {
			var raw []byte
			var other Subscription
			if err = rows.Scan(&raw); err != nil {
				break
			}
			if err = json.Unmarshal(raw, &other); err != nil {
				break
			}
			if other.State != "cancelled" && mediaKey(other.Media) == mediaKey(s.Media) && other.Season == s.Season {
				duplicate = true
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, false, err
		}
		if duplicate {
			return nil, false, bad("DUPLICATE_SUBSCRIPTION", "该作品及季已有订阅")
		}
	}
	var customWords *string
	if raw, ok := fields["custom_words"]; ok {
		var err error
		customWords, err = subscriptionWordsInput(raw)
		if err != nil {
			return nil, false, err
		}
	}
	wordsChanged := false
	if customWords != nil {
		old, err := a.subscriptionWordsFrom(ctx, tx, s.ID)
		if err != nil {
			return nil, false, err
		}
		wordsChanged = old != *customWords
	}
	if p.Downloader != nil && strings.ContainsAny(*p.Downloader, "\x00\r\n") {
		return nil, false, bad("INVALID_PLUGIN_VALUE", "下载器ID无效")
	}
	return customWords, wordsChanged, nil
}

func originalHostSubscriptionJSONEqual(left, right json.RawMessage) bool {
	var a, b any
	x := json.NewDecoder(bytes.NewReader(left))
	y := json.NewDecoder(bytes.NewReader(right))
	x.UseNumber()
	y.UseNumber()
	return x.Decode(&a) == nil && y.Decode(&b) == nil && reflect.DeepEqual(a, b)
}
func originalHostSubscriptionOperational(s Subscription) Subscription {
	s.SourceFlagValues = nil
	s.SourcePresentFields = nil
	s.SourceNullFields = nil
	s.SourceYearText = nil
	return s
}

// Let the host's actual SQLite INTEGER affinity normalize just the four
// source flags. This private empty temporary cell avoids approximating SQLite
// numeric-text rules and is never a persisted generic plugin database/table.
func originalHostSubscriptionSQLiteFlag(ctx context.Context, tx *sql.Tx, in plugins.SubscriptionHostFlagV1) (plugins.SubscriptionHostFlagV1, bool, error) {
	var value any
	if in.Integer != nil {
		value = *in.Integer
	} else if in.Real != nil {
		value = *in.Real
	} else if in.Text != nil {
		value = *in.Text
	} else {
		return in, false, bad("INVALID_PLUGIN_VALUE", "订阅开关缺少scalar")
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS plugin_host_subscription_flag_affinity(value INTEGER)`); err != nil {
		return in, false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM temp.plugin_host_subscription_flag_affinity`); err != nil {
		return in, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO temp.plugin_host_subscription_flag_affinity(value) VALUES(?)`, value); err != nil {
		return in, false, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT value FROM temp.plugin_host_subscription_flag_affinity`).Scan(&value); err != nil {
		return in, false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM temp.plugin_host_subscription_flag_affinity`); err != nil {
		return in, false, err
	}
	switch v := value.(type) {
	case int64:
		return plugins.SubscriptionHostFlagV1{Integer: &v}, v != 0, nil
	case float64:
		return plugins.SubscriptionHostFlagV1{Real: &v}, v != 0, nil
	case string:
		return plugins.SubscriptionHostFlagV1{Text: &v}, v != "", nil
	}
	return in, false, bad("INVALID_PLUGIN_VALUE", "订阅开关SQLite返回未知scalar")
}

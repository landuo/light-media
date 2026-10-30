package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
)

type sharedSubscriptionField struct {
	Value     json.RawMessage `json:"value"`
	Nonfinite string          `json:"nonfinite,omitempty"`
	Native    json.RawMessage `json:"native"`
}
type sharedSubscriptionFields map[string]sharedSubscriptionField

func sharedSubscriptionNative(s Subscription, key string) json.RawMessage {
	var include, exclude, quality, resolution, effect *string
	if p := s.Policy.FilterParams; p != nil {
		include, exclude, quality, resolution, effect = p.Include, p.Exclude, p.Quality, p.Resolution, p.Effect
	}

	fields := map[string]any{
		"name": s.Media.Title, "year": s.Media.Year, "type": s.Media.MediaType, "keyword": s.Keyword, "tmdbid": s.Media.ID, "imdbid": s.Media.IMDbID,
		"season": []any{s.Season, s.Media.MediaType}, "poster": s.Media.PosterPath, "backdrop": s.Media.BackdropPath, "vote": s.Media.VoteAverage, "description": s.Media.Overview,
		"include": []any{s.Policy.Include, include}, "exclude": []any{s.Policy.Exclude, exclude}, "quality": []any{s.Policy.Source, quality}, "resolution": []any{s.Policy.Resolution, resolution}, "effect": []any{s.Policy.HDR, effect},
		"total_episode": []any{s.TotalEpisodes, s.Media.MediaType}, "start_episode": []any{s.StartEpisode, s.Media.MediaType}, "lack_episode": s.LackEpisode, "note": s.SourceNote, "state": []any{s.SourceState, s.State},
		"date": s.CreatedAt, "last_update": s.LastCheck, "sites": s.SiteIDs, "downloader": s.DownloaderID, "best_version": s.BestVersion, "best_version_full": s.BestVersionFull, "current_priority": s.CurrentPriority, "episode_priority": s.EpisodePriority,
		"save_path": s.DownloadPath, "search_imdbid": s.SourceSearchIMDb, "manual_total_episode": s.ManualTotalEpisode, "filter_groups": s.Policy.FilterGroups,
	}
	return mustCleanSeedJSON(fields[key])
}
func cloneSharedSubscriptionFields(in sharedSubscriptionFields) sharedSubscriptionFields {
	if len(in) == 0 {
		return nil
	}
	out := sharedSubscriptionFields{}
	for k, v := range in {
		if _, ok := sharedSubscriptionColumnTypes[k]; ok {
			out[k] = sharedSubscriptionField{Value: copyOriginalBaseJSON(v.Value), Native: copyOriginalBaseJSON(v.Native), Nonfinite: v.Nonfinite}
		}
	}
	return out
}
func pruneSharedSubscriptionFields(s *Subscription) {
	s.SharedSourceFields = cloneSharedSubscriptionFields(s.SharedSourceFields)
	for k, v := range s.SharedSourceFields {
		if !sharedSubscriptionJSONEqual(v.Native, sharedSubscriptionNative(*s, k)) {
			delete(s.SharedSourceFields, k)
		}
	}
}
func (s Subscription) MarshalJSON() ([]byte, error) {
	pruneSharedSubscriptionFields(&s)
	type plain Subscription
	return json.Marshal(plain(s))
}
func (s *Subscription) UnmarshalJSON(raw []byte) error {
	type plain Subscription
	if e := json.Unmarshal(raw, (*plain)(s)); e != nil {
		return e
	}
	pruneSharedSubscriptionFields(s)
	return nil
}
func overlaySharedSubscription(s Subscription, row map[string]any) {
	for k, v := range s.SharedSourceFields {
		if sharedSubscriptionJSONEqual(v.Native, sharedSubscriptionNative(s, k)) {
			if v.Nonfinite != "" {
				sign := 1
				if v.Nonfinite == "-Inf" {
					sign = -1
				}
				row[k] = math.Inf(sign)
			} else {
				row[k] = json.RawMessage(v.Value)
			}
		}
	}
}

// Native persistence consults the actual previous record, so a caller reusing
// an old A snapshot cannot resurrect a source NULL after A -> B -> A.
func prepareSharedSubscriptionWrite(ctx context.Context, q PluginProofReader, value any) (any, error) {
	var sub Subscription
	switch v := value.(type) {
	case Subscription:
		sub = v
	case *Subscription:
		if v == nil {
			return value, nil
		}
		sub = *v
	default:
		return value, nil
	}
	if len(sub.SharedSourceFields) == 0 {
		return value, nil
	}
	pruneSharedSubscriptionFields(&sub)
	var raw []byte
	e := q.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='subscription' AND id=?`, sub.ID).Scan(&raw)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	if e == nil {
		var previous Subscription
		if e = json.Unmarshal(raw, &previous); e != nil {
			return nil, e
		}
		for k, v := range sub.SharedSourceFields {
			old, ok := previous.SharedSourceFields[k]
			if !ok {
				delete(sub.SharedSourceFields, k)
				continue
			}
			if old.Nonfinite != v.Nonfinite || !sharedSubscriptionJSONEqual(old.Value, v.Value) || !sharedSubscriptionJSONEqual(old.Native, v.Native) {
				// A concurrent source write may change JSON false to numeric 0
				// without changing the limited native projection. Preserve the
				// current DB fact, never restore the stale caller's raw value.
				if sharedSubscriptionJSONEqual(old.Native, sharedSubscriptionNative(sub, k)) {
					sub.SharedSourceFields[k] = old
				} else {
					delete(sub.SharedSourceFields, k)
				}
			}
		}
	}
	return sub, nil
}
func sharedSubscriptionInt(raw json.RawMessage) int {
	var n json.Number
	if json.Unmarshal(raw, &n) != nil {
		return 0
	}
	v, e := strconv.ParseInt(string(n), 10, 64)
	if e != nil {
		return 0
	}
	return int(v)
}
func sharedSubscriptionString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}
func sharedSubscriptionFlag(raw json.RawMessage) bool { return originalBaseJSONTruthy(raw) }
func sharedSubscriptionPtr(raw json.RawMessage) *int {
	if string(raw) == "null" {
		return nil
	}
	var n json.Number
	if json.Unmarshal(raw, &n) != nil {
		return nil
	}
	v, e := strconv.ParseInt(string(n), 10, 64)
	if e != nil {
		return nil
	}
	x := int(v)
	return &x
}
func sharedSubscriptionType(raw json.RawMessage) string {
	v := sharedSubscriptionString(raw)
	switch v {
	case "电影":
		return "movie"
	case "电视剧":
		return "tv"
	}
	return v
}
func sharedSubscriptionNativeDate(raw json.RawMessage) string { return sharedSubscriptionString(raw) }
func sharedSubscriptionSourceID(s string) (int64, bool) {
	if !strings.HasPrefix(s, "source-site:") {
		return 0, false
	}
	v, e := strconv.ParseInt(strings.TrimPrefix(s, "source-site:"), 10, 64)
	return v, e == nil
}

// Source JSON columns distinguish booleans from numbers. The plugin Base KV's
// Python equality (false == 0) is intentionally not appropriate here.
func sharedSubscriptionJSONEqual(a, b json.RawMessage) bool {
	av, ae := parseSharedSubscriptionJSON(a)
	bv, be := parseSharedSubscriptionJSON(b)
	return ae == nil && be == nil && av.equal(bv)
}

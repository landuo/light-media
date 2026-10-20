package app

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
)

type downloadSeasonBasis struct {
	Version                  int
	Mode, SiteID, ResourceID string
	SourceMeta               json.RawMessage
	Targets                  pluginLarkMediaTargets
}

func validateDownloadSeasonBasis(raw json.RawMessage, wanted []SearchRequest, mediaIdentity, site, resource string) error {
	var basis downloadSeasonBasis
	if cleanupJournalStrict(raw, &basis) != nil || basis.Version != 1 || basis.Mode != "selected" && basis.Mode != "whole_missing" || basis.SiteID != site || basis.ResourceID != resource || mediaIdentity != mediaKey(basis.Targets.Media) || len(wanted) < 2 {
		return seasonScopeError()
	}
	meta, e := releasemeta.DecodeMetaObjectState(basis.SourceMeta)
	if e != nil {
		return seasonScopeError()
	}
	seasons, episodes, e := larkMetaNumbers(meta)
	if e != nil || basis.Mode == "whole_missing" && len(episodes) > 0 || len(seasons) != len(wanted) {
		return seasonScopeError()
	}
	for _, request := range wanted {
		if !slices.Contains(seasons, request.Season) || !reflect.DeepEqual(request.Episodes, basis.Targets.Known[request.Season]) {
			return seasonScopeError()
		}
		found := false
		for _, r := range basis.Targets.Requests {
			if r.Season == request.Season && r.MediaType == request.MediaType && r.TMDBID == request.TMDBID && reflect.DeepEqual(r.Episodes, request.Episodes) {
				found = true
				break
			}
		}
		if !found || basis.Mode == "whole_missing" && !basis.Targets.Exists && !reflect.DeepEqual(basis.Targets.Missing[request.Season], basis.Targets.Known[request.Season]) {
			return seasonScopeError()
		}
	}
	return nil
}

// Explicitly choosing one resource authorizes that source Context's seasons.
// It does not turn the source's observed inventory into a fictional missing map.
// This read is sealed by the actual Lark source step before any P0 mutation.
func (a *App) selectedLarkSeasonBasis(ctx context.Context, core *pluginLarkCoreExecution, c Candidate, m catalog.Media, raw json.RawMessage) (downloadSeasonBasis, error) {
	return larkCoreRead(ctx, a, core, "download.selected_scope", struct {
		SiteID, ResourceID string
		Media              catalog.Media
		Meta               json.RawMessage
	}{c.Resource.SiteID, c.Resource.ID, m, raw}, func(ctx context.Context) (downloadSeasonBasis, error) {
		out := downloadSeasonBasis{Version: 1, Mode: "selected", SiteID: c.Resource.SiteID, ResourceID: c.Resource.ID, SourceMeta: raw, Targets: pluginLarkMediaTargets{Media: m, Known: map[int][]int{}, Missing: map[int][]int{}, Requests: []SearchRequest{}}}
		meta, e := releasemeta.DecodeMetaObjectState(raw)
		if e != nil {
			return out, e
		}
		seasons, _, e := larkMetaNumbers(meta)
		if e != nil || len(seasons) < 2 {
			return out, seasonScopeError()
		}
		total, exists := 0, 0
		for _, season := range seasons {
			data, e := a.season(ctx, m.ID, season)
			if e != nil {
				return out, e
			}
			known := []int{}
			for _, ep := range data.Episodes {
				known = append(known, ep.EpisodeNumber)
				total++
				var count int
				if e = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_slots WHERE media_key=? AND season=? AND episode=?`, mediaKey(m), season, ep.EpisodeNumber).Scan(&count); e != nil {
					return out, e
				}
				if count > 0 {
					exists++
				} else {
					out.Targets.Missing[season] = append(out.Targets.Missing[season], ep.EpisodeNumber)
				}
			}
			if len(known) == 0 {
				return out, seasonScopeError()
			}
			out.Targets.Known[season] = known
			out.Targets.Requests = append(out.Targets.Requests, SearchRequest{MediaType: "tv", TMDBID: m.ID, Season: season, Episodes: known})
		}
		out.Targets.Exists = total > 0 && total == exists
		return out, nil
	})
}

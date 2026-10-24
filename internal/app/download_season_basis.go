package app

import (
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

// Immutable source selection evidence, not a native execution scope.
type pluginLarkMediaTargets struct {
	Media    catalog.Media
	Requests []SearchRequest
	Known    map[int][]int
	Missing  map[int][]int
	Exists   bool
}

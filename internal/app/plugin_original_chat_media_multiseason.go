package app

import (
	"context"
	"slices"

	"light-media/internal/releasemeta"
	"light-media/internal/sites"
)

func originalChatPackParser(ctx context.Context) (*releasemeta.MetaInfoParser, error) {
	scope, ok := ctx.Value(resourceRecognitionKey{}).(resourceRecognition)
	if !ok {
		return nil, seasonScopeError()
	}
	return releasemeta.NewMetaInfoParser(releasemeta.MetaInfoEnvironment{Options: scope.options})
}

func originalChatPackMeta(parser *releasemeta.MetaInfoParser, resource sites.Resource) (*releasemeta.MetaObject, error) {
	subtitle := resource.SourceSubtitle
	if subtitle == nil && resource.Subtitle != "" {
		subtitle = &resource.Subtitle
	}
	return parser.ParseTitle(resource.Title, subtitle, nil)
}

func originalChatPackInSearch(seasons []int, scope []originalChatMediaSeason) bool {
	for _, number := range seasons {
		if !slices.ContainsFunc(scope, func(season originalChatMediaSeason) bool {
			return season.number == number && len(season.needed) != 0
		}) {
			return false
		}
	}
	return true
}

func originalChatPackListingWarnings(warnings []string) []string {
	return slices.DeleteFunc(warnings, func(warning string) bool {
		switch warning {
		case "season_pack_requires_confirmation", "episode_numbering_unresolved", "resource_season_outside_target":
			return true // The sealed multi-season proof checks these across all seasons.
		}
		return false
	})
}

// Only the authenticated chat caller supplies this private scope. A whole Add
// has no daemon file-priority selection, even though it spans multiple seasons.
func originalChatMultiSeasonBasis(parser *releasemeta.MetaInfoParser, request SearchRequest, candidate Candidate) (downloadSeasonBasis, error) {
	var basis downloadSeasonBasis
	if request.originalSearchInfo == nil || !originalChatPackInSearch(candidate.chatSeasonPack, request.chatMediaSeasons) {
		return basis, seasonScopeError()
	}
	var meta *releasemeta.MetaObject
	var err error
	if len(candidate.originalSelectionMeta) != 0 {
		meta, err = releasemeta.DecodeMetaObjectState(candidate.originalSelectionMeta)
	} else {
		meta, err = originalChatPackMeta(parser, candidate.Resource)
	}
	if err != nil {
		return basis, err
	}
	seasons, episodes, err := larkMetaNumbers(meta)
	if err != nil || len(episodes) != 0 || !slices.Equal(seasons, candidate.chatSeasonPack) {
		return basis, seasonScopeError()
	}
	raw, err := meta.EncodeState()
	if err != nil {
		return basis, err
	}
	basis = downloadSeasonBasis{
		Version: 1, Mode: "whole_missing", SiteID: candidate.Resource.SiteID, ResourceID: candidate.Resource.ID,
		SourceMeta: raw, Targets: pluginLarkMediaTargets{
			Media: request.originalSearchInfo.Media, Known: map[int][]int{}, Missing: map[int][]int{},
		},
	}
	for _, number := range seasons {
		index := slices.IndexFunc(request.chatMediaSeasons, func(season originalChatMediaSeason) bool { return season.number == number })
		season := request.chatMediaSeasons[index]
		if !season.whole {
			basis.Mode = "selected"
		}
		for _, episode := range season.all {
			if !originalSelectionAllows(candidate, episode) {
				return basis, seasonScopeError()
			}
		}
		basis.Targets.Known[number] = slices.Clone(season.all)
		basis.Targets.Missing[number] = slices.Clone(season.needed)
		basis.Targets.Requests = append(basis.Targets.Requests, SearchRequest{
			MediaType: request.MediaType, TMDBID: request.TMDBID, Season: number, Episodes: slices.Clone(season.all),
		})
	}
	return basis, nil
}

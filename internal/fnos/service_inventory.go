package fnos

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"
)

// EpisodeInventory reads the episode numbers of one TMDB series and season.
// An empty slice is a successful miss. Errors leave the inventory unknown.
// This observation gives the caller no ownership of a local file.
func (s *Service) EpisodeInventory(ctx context.Context, title string, tmdbID int64, season int) ([]int, error) {
	title = strings.TrimSpace(title)
	if ctx == nil || !serviceText(title, 1024) || tmdbID < 1 || season < 0 || season > 1000 {
		return nil, &ServiceError{Code: "invalid_input"}
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	current, err := s.session(ctx)
	if err != nil {
		return nil, err
	}
	// fnOS uses tt for series and tm for films in its trim_id namespace.
	// MoviePilot's TrimeMedia adapter decodes this field as the TMDB ID.
	var results []serviceNode
	if err := current.call(ctx, "/search/list", nil, title, &results); err != nil {
		return nil, err
	}
	episodes := []int{}
	seen := map[string]bool{}
	unidentified := false
	for _, candidate := range results {
		if candidate.Type != "TV" || (candidate.Title != title && inventorySeriesID(candidate.TrimID) != tmdbID) || seen[candidate.GUID] {
			continue
		}
		seen[candidate.GUID] = true
		root, err := current.detail(ctx, candidate.GUID)
		if err != nil {
			return nil, err
		}
		if !scopeNode(root, *root.AncestorGUID, "", "TV") {
			return nil, &ServiceError{Code: "invalid_response"}
		}
		id := inventorySeriesID(root.TrimID)
		if id == 0 || (candidate.TrimID != "" && candidate.TrimID != root.TrimID) {
			unidentified = true
			continue
		}
		if id != tmdbID {
			continue
		}
		owned, err := current.inventorySeason(ctx, root, season)
		if err != nil {
			return nil, err
		}
		episodes = append(episodes, owned...)
	}
	if unidentified && len(episodes) == 0 {
		return nil, &ServiceError{Code: "invalid_response"}
	}
	slices.Sort(episodes)
	return slices.Compact(episodes), nil
}

func inventorySeriesID(trimID string) int64 {
	number, ok := strings.CutPrefix(trimID, "tt")
	if !ok || strings.Trim(number, "0123456789") != "" {
		return 0
	}
	id, err := strconv.ParseInt(number, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func (s *serviceSession) inventorySeason(ctx context.Context, root serviceNode, number int) ([]int, error) {
	var seasons []serviceNode
	if err := s.call(ctx, "/season/list/"+root.GUID, nil, "", &seasons); err != nil {
		return nil, err
	}
	if len(seasons) > 1000 {
		return nil, &ServiceError{Code: "invalid_response"}
	}
	episodes := []int{}
	seen := map[string]bool{}
	for _, season := range seasons {
		if season.Season == nil {
			return nil, &ServiceError{Code: "invalid_response"}
		}
		if *season.Season != number || seen[season.GUID] {
			continue
		}
		if !validInventoryChild(season, root.GUID, *root.AncestorGUID, "Season") {
			return nil, &ServiceError{Code: "invalid_response"}
		}
		seen[season.GUID] = true
		var rows []serviceNode
		if err := s.call(ctx, "/episode/list/"+season.GUID, nil, "", &rows); err != nil {
			return nil, err
		}
		if len(rows) > 10000 {
			return nil, &ServiceError{Code: "invalid_response"}
		}
		for _, row := range rows {
			if !validInventoryChild(row, season.GUID, *root.AncestorGUID, "Episode") ||
				row.Season == nil || *row.Season != number || row.Episode == nil || *row.Episode < 1 || *row.Episode > 10000 {
				return nil, &ServiceError{Code: "invalid_response"}
			}
			episodes = append(episodes, *row.Episode)
		}
	}
	return episodes, nil
}

// The lists are scoped by parent GUID. Some fnOS versions omit the optional
// ancestry fields on list rows, but reject them when supplied and inconsistent.
func validInventoryChild(node serviceNode, parent, library, kind string) bool {
	return detailGUID.MatchString(node.GUID) &&
		(node.Type == "" || node.Type == kind) &&
		(node.ParentGUID == nil || *node.ParentGUID == parent) &&
		(node.AncestorGUID == nil || *node.AncestorGUID == library)
}

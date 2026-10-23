package app

import (
	"context"
	"database/sql"
	"errors"
)

type ExplorePreferences struct {
	TabOrder []string `json:"tab_order"`
}

func normalizeExplorePreferences(in ExplorePreferences) (ExplorePreferences, error) {
	defaults := []string{"themoviedb", "douban", "bangumi", "anilist"}
	allowed := map[string]bool{}
	for _, source := range defaults {
		allowed[source] = true
	}
	if len(in.TabOrder) > 32 {
		return ExplorePreferences{}, bad("INVALID_EXPLORE_ORDER", "探索来源排序无效")
	}
	result := ExplorePreferences{TabOrder: []string{}}
	seen := map[string]bool{}
	for _, source := range in.TabOrder {
		if !allowed[source] {
			return ExplorePreferences{}, bad("INVALID_EXPLORE_ORDER", "探索来源排序包含未知来源")
		}
		if !seen[source] {
			seen[source] = true
			result.TabOrder = append(result.TabOrder, source)
		}
	}
	for _, source := range defaults {
		if !seen[source] {
			result.TabOrder = append(result.TabOrder, source)
		}
	}
	return result, nil
}

func (a *App) catalogExplorePreferences(ctx context.Context) (ExplorePreferences, error) {
	var stored ExplorePreferences
	err := a.Store.Get(ctx, "ui_preferences", "catalog_explore", &stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ExplorePreferences{}, err
	}
	return normalizeExplorePreferences(stored)
}

func (a *App) saveCatalogExplorePreferences(ctx context.Context, in ExplorePreferences) (ExplorePreferences, error) {
	result, err := normalizeExplorePreferences(in)
	if err != nil {
		return ExplorePreferences{}, err
	}
	if err = a.Store.Put(ctx, "ui_preferences", "catalog_explore", result); err != nil {
		return ExplorePreferences{}, err
	}
	return result, nil
}

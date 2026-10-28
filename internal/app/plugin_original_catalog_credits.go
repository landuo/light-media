package app

import (
	"context"
	"strconv"

	"light-media/internal/catalog"
)

func (a *App) originalCatalogPersonCredits(ctx context.Context, id int64, page int, native func() (catalog.Page, error)) (catalog.ExplorePage, error) {
	result, err := a.originalCatalogFeed(ctx, "async_tmdb_person_credits", map[string]any{"person_id": id, "page": page}, page, func() (catalog.ExplorePage, error) {
		core, err := native()
		if err != nil {
			return catalog.ExplorePage{}, err
		}
		rows := make([]catalog.ExploreMedia, len(core.Results))
		for i, item := range core.Results {
			rows[i] = catalog.ExploreMedia{Media: item, Source: "themoviedb", SourceID: strconv.FormatInt(item.ID, 10)}
		}
		return catalog.ExplorePage{Results: rows, Page: core.Page, TotalPages: core.TotalPages, TotalResults: core.TotalResults, HasMore: core.Page < core.TotalPages}, nil
	})
	if err != nil {
		return result, err
	}
	models := make([]catalog.Media, len(result.Results))
	for i, item := range result.Results {
		models[i] = item.Media
	}
	if err = a.decorateMedia(ctx, models); err != nil {
		return result, err
	}
	for i, item := range models {
		result.Results[i].Media = item
	}
	return result, nil
}

package app

import (
	"context"
	"encoding/json"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
)

func (a *App) originalChatAniListMedia(ctx context.Context, meta *releasemeta.MetaObject) (catalog.ExplorePage, error) {
	result := catalog.ExplorePage{Results: []catalog.ExploreMedia{}, Page: 1}
	if meta == nil || meta.Name() == "" {
		return result, nil
	}
	name := meta.Name()
	key := string(mustCleanSeedJSON([]any{name, 20}))
	value, err := a.originalHostMediaProviderCached(ctx, "anilist", "search", key, true, func() (any, error) {
		proxy, err := a.proxyConfig()
		if err != nil {
			return nil, err
		}
		transport, err := proxy.Transport(nil)
		if err != nil {
			return nil, err
		}
		explorer := catalog.NewExplorer(a.catalog(), transport)
		defer explorer.CloseIdleConnections()
		data, err := a.originalHostAniListInvoke(ctx, explorer, false, name)
		if err != nil || !sourceSubscriptionTruth(data) {
			return []any{}, err
		}
		root, err := originalHostAniListObject(data, false)
		if err != nil {
			return nil, err
		}
		page, err := originalHostAniListMember(root, "Page")
		if err != nil {
			return nil, err
		}
		return page["media"], nil
	})
	if err != nil {
		return result, err
	}
	rows, err := originalHostAniListRows(value)
	if err != nil || len(rows) == 0 {
		return result, err
	}
	parser, err := a.originalHostMediaSourceParser(ctx)
	if err != nil {
		return result, err
	}
	for _, row := range rows {
		candidate, err := originalHostAniListObject(row, false)
		if err != nil {
			return result, err
		}
		matches, err := originalHostAniListMatches(meta, candidate)
		if err != nil {
			return result, err
		}
		if !matches {
			continue
		}
		info, err := originalHostAniListEnrich(candidate)
		if err != nil {
			return result, err
		}
		fields, err := originalHostAniListFields(info, parser)
		if err != nil {
			return result, err
		}
		view, err := originalChatAniListProjection(fields)
		if err != nil {
			return result, err
		}
		result.Results = append(result.Results, view)
	}
	return result, nil
}

func originalChatAniListProjection(fields map[string]any) (catalog.ExploreMedia, error) {
	raw, err := json.Marshal(fields)
	if err != nil {
		return catalog.ExploreMedia{}, err
	}
	public, err := originalPublicCatalogMedia(raw)
	if err != nil {
		return catalog.ExploreMedia{}, err
	}
	return originalCatalogMediaProjection(public)
}

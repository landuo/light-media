package app

import (
	"context"
	"encoding/json"
	"strings"

	"light-media/internal/catalog"
	"light-media/internal/releasemeta"
	"light-media/internal/releasemeta/legacyanime"
)

func (a *App) originalChatBangumiMedia(ctx context.Context, meta *releasemeta.MetaObject) (catalog.ExplorePage, error) {
	page := catalog.ExplorePage{Results: []catalog.ExploreMedia{}, Page: 1}
	if err := ctx.Err(); err != nil {
		return page, err
	}
	selection := a.Settings().SearchSource
	if meta == nil || meta.Name() == "" || selection == nil || *selection != "" && !strings.Contains(*selection, "bangumi") {
		return page, nil
	}
	proxy, err := a.proxyConfig()
	if err != nil {
		return page, err
	}
	transport, err := proxy.Transport(nil)
	if err != nil {
		return page, err
	}
	explorer := catalog.NewExplorer(a.catalog(), transport)
	defer explorer.CloseIdleConnections()
	name := meta.Name()
	value, err := a.originalHostMediaProviderJSON(ctx, "bangumi", "get", []any{"/search/subject/" + name}, false, func() (json.RawMessage, error) {
		return explorer.SourceBangumiSearch(ctx, name)
	})
	if err != nil {
		return page, err
	}
	search, err := originalHostMediaSourceMapping(value, true)
	if err != nil {
		return page, err
	}
	rows, err := originalHostMediaSourceSequence(search["list"])
	if err != nil || len(rows) == 0 {
		return page, err
	}
	name = legacyanime.LowerCase(name)
	var parser *releasemeta.MetaInfoParser
	results := make([]catalog.ExploreMedia, 0, len(rows))
	for _, row := range rows {
		info, err := originalHostMediaSourceMapping(row, false)
		if err != nil {
			return page, err
		}
		if !strings.Contains(legacyanime.LowerCase(originalCatalogIdentityString(info["name"])), name) &&
			!strings.Contains(legacyanime.LowerCase(originalCatalogIdentityString(info["name_cn"])), name) {
			continue
		}
		if parser == nil {
			parser, err = a.originalHostMediaSourceParser(ctx)
			if err != nil {
				return page, err
			}
		}
		fields, err := originalHostMediaBangumiFields(info, parser)
		if err != nil {
			return page, err
		}
		model, err := originalHostMediaSourceSerialize(fields)
		if err != nil {
			return page, err
		}
		media, err := originalCatalogMediaProjection(model)
		if err != nil {
			return page, err
		}
		results = append(results, media)
	}
	page.Results = results
	return page, nil
}

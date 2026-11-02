package app

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"light-media/internal/catalog"
	"light-media/internal/plugins"
)

// These are independent requests after detail recognition, as in the fixed
// MediaDetailView's PersonCardSlideView and MediaCardSlideView. Discovery stays
// outside the native detail cache and never blocks the primary detail response.
func originalCatalogRelatedModule(source, kind, id, part string) (string, map[string]any, error) {
	if !catalog.ValidExploreIdentity(source, kind, id) || (part != "credits" && part != "recommendations") {
		return "", nil, bad("INVALID_MEDIA", "影视来源或详情操作无效")
	}
	suffix := "recommend"
	if part == "credits" {
		suffix = "credits"
	}
	switch source {
	case "themoviedb":
		n, _ := strconv.ParseInt(id, 10, 64)
		args := map[string]any{"tmdbid": n}
		if part == "credits" {
			args["page"] = 1
		}
		return "async_tmdb_" + kind + "_" + suffix, args, nil
	case "douban":
		return "async_douban_" + kind + "_" + suffix, map[string]any{"doubanid": id}, nil
	case "bangumi":
		n, _ := strconv.ParseInt(id, 10, 64)
		return "async_bangumi_" + suffix, map[string]any{"bangumiid": n}, nil
	}
	return "", nil, bad("INVALID_MEDIA", "当前详情未使用该来源的关联模块")
}

func (a *App) originalCatalogRelatedNative(ctx context.Context, source, kind, id string) (catalog.Media, error) {
	if source == "themoviedb" {
		n, _ := strconv.ParseInt(id, 10, 64)
		return a.media(catalog.OriginalPluginContext(ctx), kind, n)
	}
	proxy, err := a.proxyConfig()
	if err != nil {
		return catalog.Media{}, err
	}
	explorer, err := catalog.NewOriginalPluginExplorer(a.catalog(), proxy)
	if err != nil {
		return catalog.Media{}, err
	}
	defer explorer.CloseIdleConnections()
	var view catalog.ExploreMedia
	_, err = a.cached(ctx, "explore-detail:v1:"+source+":"+kind+":"+id, 12*time.Hour, &view, func() (any, error) { return explorer.Detail(catalog.OriginalPluginContext(ctx), source, kind, id) })
	return view.Media, err
}

func (a *App) originalCatalogRelated(ctx context.Context, source, kind, id, part string) (any, error) {
	method, kwargs, err := originalCatalogRelatedModule(source, kind, id, part)
	if err != nil {
		return nil, err
	}
	if part == "credits" {
		rows, err := a.originalCatalogCast(ctx, method, kwargs, func() ([]map[string]any, error) {
			view, e := a.originalCatalogRelatedNative(ctx, source, kind, id)
			if e != nil {
				return nil, e
			}
			rows := make([]map[string]any, 0, len(view.Cast))
			for _, p := range view.Cast {
				raw, _ := json.Marshal(p)
				model, e := originalCatalogPersonProjection(raw)
				if e != nil {
					return nil, e
				}
				model["source"] = source
				rows = append(rows, model)
			}
			return rows, nil
		})
		if source == "bangumi" && len(rows) > 20 {
			rows = rows[:20]
		}
		return rows, err
	}
	page, err := a.originalCatalogFeed(ctx, method, kwargs, 1, func() (catalog.ExplorePage, error) {
		view, e := a.originalCatalogRelatedNative(ctx, source, kind, id)
		if e != nil {
			return catalog.ExplorePage{}, e
		}
		rows := make([]catalog.ExploreMedia, 0, len(view.Recommendations))
		for _, m := range view.Recommendations {
			rows = append(rows, catalog.ExploreMedia{Media: m, Source: source, SourceID: strconv.FormatInt(m.ID, 10)})
		}
		return catalog.ExplorePage{Results: rows, Page: 1, TotalPages: 1, TotalResults: len(rows)}, nil
	})
	if err != nil {
		return nil, err
	}
	if source == "bangumi" && len(page.Results) > 20 {
		page.Results = page.Results[:20]
	}
	return page.Results, nil
}

func (a *App) originalCatalogCast(ctx context.Context, method string, kwargs map[string]any, native func() ([]map[string]any, error)) ([]map[string]any, error) {
	args, _ := json.Marshal(kwargs)
	result, err := a.invokeOriginalPluginModules(ctx, method, nil, json.RawMessage(`[]`), args)
	if err != nil {
		return nil, err
	}
	core := func() ([]map[string]any, error) {
		rows, e := native()
		if e == nil {
			return rows, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		a.reportOriginalPluginExecutionError(ctx, "system", originalCatalogSystemModuleName(method)+"发生了错误", map[string]any{"type": "module", "module_id": originalCatalogSystemModuleID(method), "module_name": originalCatalogSystemModuleName(method), "module_method": method}, e)
		return nil, nil
	}
	if originalModuleResultEmpty(result) {
		rows, e := core()
		if rows == nil && method != "async_douban_movie_credits" && method != "async_douban_tv_credits" {
			rows = []map[string]any{}
		}
		if e == nil && rows == nil {
			e = originalCatalogPersonInvalid()
		}
		return rows, e
	}
	var raw []json.RawMessage
	if json.Unmarshal(result, &raw) == nil && string(result) != "null" {
		// The scalar core result would be ignored by the source list accumulator;
		// this concrete native adapter always returns a list or None on failure.
		rows, e := core()
		if e != nil {
			return nil, e
		}
		out := make([]map[string]any, 0, len(raw)+len(rows))
		for _, value := range raw {
			p, e := originalCatalogPersonProjection(value)
			if e != nil {
				return nil, e
			}
			out = append(out, p)
		}
		return append(out, rows...), nil
	}
	if tuple, ok := plugins.DecodeModuleResultTuple(result); ok {
		rows := make([]map[string]any, 0, len(tuple))
		for _, value := range tuple {
			p, e := originalCatalogPersonProjection(value)
			if e != nil {
				return nil, e
			}
			rows = append(rows, p)
		}
		return rows, nil
	}
	if !originalBaseJSONTruthy(result) && method != "async_douban_movie_credits" && method != "async_douban_tv_credits" {
		return []map[string]any{}, nil
	}
	return nil, originalCatalogPersonInvalid()
}

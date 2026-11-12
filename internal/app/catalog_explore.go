package app

import (
	"errors"
	"light-media/internal/catalog"
	"net/http"
	"strings"
	"time"
)

// catalogExploreAPI is dispatched by the catalog HTTP router for explore,
// explore/detail and explore/resolve. Source identities never enter TMDB paths.
func (a *App) catalogExploreAPI(r *http.Request, action string) (any, error) {
	q := r.URL.Query()
	var query catalog.ExploreQuery
	var err error
	source, kind, id := q.Get("source"), q.Get("kind"), q.Get("source_id")
	if action == "explore" {
		query, err = catalog.ParseExploreQuery(q)
	} else if action == "explore/detail" || action == "explore/resolve" {
		if (kind != "movie" && kind != "tv") || source == "" || id == "" || strings.ContainsAny(source+id, "\x00\r\n") {
			err = bad("INVALID_EXPLORE_IDENTITY", "请选择有效的来源条目")
		}
	} else {
		return nil, &APIError{Code: "NOT_FOUND", Message: "接口不存在", Status: 404}
	}
	if err != nil {
		return nil, catalogExploreError(err)
	}
	transport, err := a.metadataAPITransportForConfig()
	if err != nil {
		return nil, err
	}
	explorer := catalog.NewExplorer(a.catalog(), transport)
	ctx := r.Context()
	switch action {
	case "explore":
		var p catalog.ExplorePage
		method, kwargs := originalCatalogExploreModule(query, r.URL.Query())
		// Source discovery calls its module boundary on each request. Native
		// provider caching remains inside this fallback and cannot hide plugins.
		p, err = a.originalCatalogFeed(ctx, method, kwargs, query.Page, func() (catalog.ExplorePage, error) {
			var core catalog.ExplorePage
			_, e := a.cached(ctx, query.CacheKey(), time.Hour, &core, func() (any, error) { return explorer.Explore(ctx, query) })
			return core, e
		})
		cachedAt := time.Now().UTC()
		if err != nil {
			return nil, catalogExploreError(err)
		}
		if query.Source == "themoviedb" {
			media := make([]catalog.Media, len(p.Results))
			for i := range p.Results {
				media[i] = p.Results[i].Media
			}
			if err := a.decorateMedia(ctx, media); err != nil {
				return nil, err
			}
			for i := range media {
				p.Results[i].Media = media[i]
			}
		}
		return struct {
			catalog.ExplorePage
			CachedAt time.Time `json:"cached_at"`
		}{p, cachedAt}, nil
	case "explore/detail":
		m, err := a.originalCatalogExploreDetail(ctx, source, kind, id, q.Get("title"), q.Get("year"), explorer)
		return m, catalogExploreError(err)
	case "explore/resolve":
		m, err := a.originalCatalogExploreDetail(ctx, source, kind, id, q.Get("title"), q.Get("year"), explorer)
		if err != nil {
			return nil, catalogExploreError(err)
		}
		resolved, err := a.catalog().ResolveExploreMedia(ctx, m)
		if err != nil {
			return nil, catalogExploreError(err)
		}
		if err := a.decorateMedia(ctx, resolved.Candidates); err != nil {
			return nil, err
		}
		if resolved.Media != nil {
			items := []catalog.Media{*resolved.Media}
			if err := a.decorateMedia(ctx, items); err != nil {
				return nil, err
			}
			resolved.Media = &items[0]
		}
		return resolved, nil
	}
	return nil, &APIError{Code: "NOT_FOUND", Message: "接口不存在", Status: 404}
}

func catalogExploreError(err error) error {
	if err == nil {
		return nil
	}
	var source *catalog.ExploreError
	if !errors.As(err, &source) {
		return err
	}
	status := http.StatusBadGateway
	switch source.Code {
	case "invalid_input":
		status = http.StatusBadRequest
	case "not_found":
		status = http.StatusNotFound
	case "rate_limited":
		status = http.StatusTooManyRequests
	case "canceled":
		status = http.StatusGatewayTimeout
	}
	return &APIError{Code: "EXPLORE_" + source.Code, Message: source.Error(), Status: status}
}

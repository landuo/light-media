package app

import (
	"errors"
	"light-media/internal/catalog"
	"net/http"
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
		if !catalog.ValidExploreIdentity(source, kind, id) {
			err = bad("INVALID_EXPLORE_IDENTITY", "请选择有效的来源条目")
		}
	} else {
		return nil, &APIError{Code: "NOT_FOUND", Message: "接口不存在", Status: 404}
	}
	if err != nil {
		return nil, catalogExploreError(err)
	}
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
	ctx := r.Context()
	switch action {
	case "explore":
		var p catalog.ExplorePage
		cachedAt, err := a.cached(ctx, query.CacheKey(), time.Hour, &p, func() (any, error) { return explorer.Explore(ctx, query) })
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
		var m catalog.ExploreMedia
		_, err := a.cached(ctx, "explore-detail:v1:"+source+":"+kind+":"+id, 12*time.Hour, &m, func() (any, error) { return explorer.Detail(ctx, source, kind, id) })
		return m, catalogExploreError(err)
	case "explore/resolve":
		var m catalog.ExploreMedia
		_, err := a.cached(ctx, "explore-detail:v1:"+source+":"+kind+":"+id, 12*time.Hour, &m, func() (any, error) { return explorer.Detail(ctx, source, kind, id) })
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

package app

import (
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"light-media/internal/catalog"
)

// Raw source alias retains the fixed MediaInfo response and its empty/default
// result. The native catalog route keeps its existing view envelope and 404.
func (a *App) serveOriginalCatalogMedia(w http.ResponseWriter, r *http.Request, path string) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if strings.HasPrefix(path, "v1/") || strings.HasPrefix(path, "v2/") {
		path = path[3:]
	} else {
		return false
	}
	if path == "media/search" {
		query := r.URL.Query()
		if !query.Has("title") {
			a.writeError(w, r, &APIError{"INVALID_INPUT", "缺少搜索标题", 422})
			return true
		}
		if query.Has("source") {
			switch query.Get("source") {
			case "themoviedb", "douban", "bangumi", "anilist":
			default:
				a.writeError(w, r, &APIError{"INVALID_INPUT", "媒体搜索来源无效", 422})
				return true
			}
		}
		if kind := query.Get("type"); query.Has("type") && kind != "media" {
			a.writeError(w, r, &APIError{"HOST_UNAVAILABLE", "当前宿主未提供该类搜索", 501})
			return true
		}
		page, count := 1, 8
		for key, target := range map[string]*int{"page": &page, "count": &count} {
			if r.URL.Query().Has(key) {
				number, err := originalCatalogPersonInteger(r.URL.Query().Get(key))
				if err != nil {
					a.writeError(w, r, &APIError{"INVALID_INPUT", "分页参数无效", 422})
					return true
				}
				n, ok := new(big.Int).SetString(number.String(), 10)
				if !ok {
					a.writeError(w, r, &APIError{"INVALID_INPUT", "分页参数无效", 422})
					return true
				}
				if !n.IsInt64() {
					if n.Sign() < 0 {
						*target = -int(^uint(0) >> 1)
					} else {
						*target = int(^uint(0) >> 1)
					}
				} else {
					*target = int(n.Int64())
				}
			}
		}
		result, err := a.originalCatalogSourceSearch(r.Context(), r.URL.Query().Get("title"), "", r.URL.Query().Get("source"), page, count)
		if err != nil {
			a.writeError(w, r, err)
			return true
		}
		rows := make([]json.RawMessage, 0, len(result.Results))
		for _, item := range result.Results {
			rows = append(rows, item.OriginalMedia)
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(rows)
		return true
	}
	if strings.HasPrefix(path, "tmdb/") {
		parts := strings.Split(strings.TrimPrefix(path, "tmdb/"), "/")
		if len(parts) == 2 && parts[0] == "person" {
			id, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				a.writeError(w, r, &APIError{"INVALID_INPUT", "人物编号无效", 422})
				return true
			}
			result, err := a.originalCatalogPerson(r.Context(), id, func() (json.RawMessage, error) {
				var raw json.RawMessage
				_, e := a.cached(r.Context(), "source-person:"+parts[1], 7*24*time.Hour, &raw, func() (any, error) { return a.catalog().SourcePersonInformation(r.Context(), id) })
				return raw, e
			})
			if err != nil {
				a.writeError(w, r, err)
				return true
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(result)
			return true
		}
		if len(parts) != 2 {
			return false
		}
		id, e := strconv.ParseInt(parts[0], 10, 64)
		if e != nil {
			return false
		}
		season, e2 := strconv.Atoi(parts[1])
		if e != nil || e2 != nil {
			a.writeError(w, r, bad("INVALID_MEDIA", "剧集编号或季号无效"))
			return true
		}
		var group any
		if r.URL.Query().Has("episode_group") {
			group = r.URL.Query().Get("episode_group")
		}
		rows, err := a.originalCatalogEpisodes(r.Context(), id, season, group, func() (json.RawMessage, error) {
			return a.originalCatalogNativeEpisodeRows(r.Context(), id, season, group, nil)
		})
		if err != nil {
			a.writeError(w, r, err)
			return true
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(rows)
		return true
	}
	if !strings.HasPrefix(path, "media/") {
		return false
	}
	key := strings.TrimPrefix(path, "media/")
	if key == "" || strings.Contains(key, "/") {
		return false
	}
	switch key {
	case "search", "recognize", "recognize2", "recognize_file", "recognize_file2":
		return false
	}
	kind := ""
	switch r.URL.Query().Get("type_name") {
	case "电影":
		kind = "movie"
	case "电视剧":
		kind = "tv"
	default:
		a.writeError(w, r, originalCatalogMediaInvalid())
		return true
	}
	parts := strings.SplitN(key, ":", 2)
	source, id := "", ""
	if len(parts) == 2 {
		source, id = parts[0], parts[1]
	}
	proxy, err := a.proxyConfig()
	if err != nil {
		a.writeError(w, r, err)
		return true
	}
	transport, err := proxy.Transport(nil)
	if err != nil {
		a.writeError(w, r, err)
		return true
	}
	explorer := catalog.NewExplorer(a.catalog(), transport)
	defer explorer.CloseIdleConnections()
	// Preserve a key without a colon for the legacy conversion event. The
	// native query helper normally reconstructs a key from two distinct fields.
	if len(parts) != 2 {
		source = key
	}
	view, err := a.originalCatalogExploreDetail(r.Context(), source, kind, id, r.URL.Query().Get("title"), r.URL.Query().Get("year"), explorer, key)
	var output map[string]any
	if err != nil {
		var api *APIError
		if !errors.As(err, &api) || api.Code != originalRecognitionMissing().(*APIError).Code {
			a.writeError(w, r, err)
			return true
		}
		output, err = originalCatalogMediaResponse(json.RawMessage(`{}`))
	} else {
		output, err = originalCatalogMediaResponse(view.OriginalMedia)
	}
	if err != nil {
		a.writeError(w, r, err)
		return true
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(output)
	return true
}

package catalog

import (
	"context"
	"net/url"
	"sort"
	"strings"
)

// SearchSourceMedia is the original TheMovieDb search_medias branch consumed
// by native MessageChain. Ordinary P0 Search keeps its existing mixed search.
// Unknown type plus a year performs two queries, retains title containment,
// then stably orders the combined results by their original release dates.
func (c *Client) SearchSourceMedia(ctx context.Context, name, kind, year string) ([]Media, error) {
	if !queryOK(name) || len(year) > 32 || strings.ContainsAny(year, "\r\n\x00") {
		return nil, &Error{Code: "invalid_input"}
	}
	query := func(kind string) ([]Media, error) {
		params := url.Values{"query": {name}, "page": {"1"}}
		if year != "" && kind != "multi" {
			key := "year"
			if kind == "tv" {
				key = "first_air_date_year"
			}
			params.Set(key, year)
		}
		var page wirePage
		if e := c.get(ctx, "search/"+kind, params, &page); e != nil {
			return nil, e
		}
		if page.Results == nil {
			return []Media{}, nil
		}
		result := []Media{}
		for _, entry := range page.Results {
			if kind == "multi" && entry.MediaType != "movie" && entry.MediaType != "tv" {
				continue
			}
			if kind == "movie" && !strings.Contains(entry.Title, name) || kind == "tv" && !strings.Contains(entry.Name, name) {
				continue
			}
			m, e := c.media(entry, kind, false)
			if e != nil {
				return nil, e
			}
			result = append(result, m)
		}
		return result, nil
	}
	switch kind {
	case "电影", "movie":
		return query("movie")
	case "电视剧", "tv":
		return query("tv")
	case "未知", "":
		if year == "" {
			return query("multi")
		}
		movies, e := query("movie")
		if e != nil {
			return nil, e
		}
		tvs, e := query("tv")
		if e != nil {
			return nil, e
		}
		result := append(movies, tvs...)
		sort.SliceStable(result, func(i, j int) bool { return result[i].ReleaseDate > result[j].ReleaseDate })
		return result, nil
	}
	return nil, &Error{Code: "invalid_input"}
}

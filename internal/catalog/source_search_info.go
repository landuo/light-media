package catalog

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

// SourceSearchRows is the first-page TmdbApi search used by search_medias.
// Its substring filtering and mixed-year date sort precede MediaInfo creation.
func (c *Client) SourceSearchRows(ctx context.Context, name, kind, year string) ([]json.RawMessage, error) {
	if name == "" {
		return []json.RawMessage{}, nil
	}
	fetch := func(kind string) ([]map[string]json.RawMessage, error) {
		params := url.Values{"query": {name}, "page": {"1"}}
		if year != "" {
			if kind == "tv" {
				params.Set("first_air_date_year", year)
			} else {
				params.Set("year", year)
			}
		}
		var page struct{ Results []map[string]json.RawMessage }
		if err := c.get(ctx, "search/"+kind, params, &page); err != nil {
			return nil, err
		}
		rows := []map[string]json.RawMessage{}
		for _, row := range page.Results {
			actual := kind
			if kind == "multi" {
				var raw string
				_ = json.Unmarshal(row["media_type"], &raw)
				if raw != "movie" && raw != "tv" {
					continue
				}
				actual = raw
			} else {
				key := "title"
				if kind == "tv" {
					key = "name"
				}
				var title string
				if json.Unmarshal(row[key], &title) != nil {
					return nil, &Error{Code: "invalid_response"}
				}
				if !strings.Contains(title, name) {
					continue
				}
			}
			if actual == "movie" {
				row["media_type"] = json.RawMessage(`"电影"`)
			} else {
				row["media_type"] = json.RawMessage(`"电视剧"`)
			}
			rows = append(rows, row)
		}
		return rows, nil
	}
	var rows []map[string]json.RawMessage
	if kind == "" && year == "" {
		var err error
		rows, err = fetch("multi")
		if err != nil {
			return nil, err
		}
	} else if kind != "" {
		var err error
		rows, err = fetch(kind)
		if err != nil {
			return nil, err
		}
	} else {
		movies, err := fetch("movie")
		if err != nil {
			return nil, err
		}
		tv, err := fetch("tv")
		if err != nil {
			return nil, err
		}
		rows = append(movies, tv...)
		date := func(row map[string]json.RawMessage) string {
			var value string
			_ = json.Unmarshal(row["release_date"], &value)
			if value == "" {
				_ = json.Unmarshal(row["first_air_date"], &value)
			}
			if value == "" {
				value = "0000-00-00"
			}
			return value
		}
		sort.SliceStable(rows, func(i, j int) bool { return date(rows[i]) > date(rows[j]) })
	}
	out := make([]json.RawMessage, 0, len(rows))
	for _, row := range rows {
		raw, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, nil
}

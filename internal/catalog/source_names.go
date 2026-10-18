package catalog

import (
	"context"
	"net/url"
	"strconv"
)

// SourceNames reads the complete names consumed by MoviePilot's
// TmdbApi.__get_names, preserving provider order and exact case. It intentionally
// does not apply the P0 search-keyword country/rank/16-alias projection.
func (c *Client) SourceNames(ctx context.Context, kind string, id int64) ([]string, error) {
	if !kindOK(kind) || !validID(id) {
		return nil, &Error{Code: "invalid_input"}
	}
	appendFields := "images,credits,alternative_titles,translations,release_dates,external_ids"
	if kind == "tv" {
		appendFields = "images,credits,alternative_titles,translations,content_ratings,external_ids,episode_groups"
	}
	var raw struct {
		ID                int64 `json:"id"`
		AlternativeTitles struct {
			Titles []struct {
				Title string `json:"title"`
			} `json:"titles"`
			Results []struct {
				Title string `json:"title"`
			} `json:"results"`
		} `json:"alternative_titles"`
		Translations struct {
			Translations []struct {
				Data struct {
					Title string `json:"title"`
					Name  string `json:"name"`
				} `json:"data"`
			} `json:"translations"`
		} `json:"translations"`
	}
	if e := c.get(ctx, kind+"/"+strconv.FormatInt(id, 10), url.Values{"append_to_response": {appendFields}}, &raw); e != nil {
		return nil, e
	}
	if raw.ID != id {
		return nil, &Error{Code: "invalid_response"}
	}
	names, seen := []string{}, map[string]bool{}
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			names = append(names, v)
		}
	}
	if kind == "movie" {
		for _, row := range raw.AlternativeTitles.Titles {
			add(row.Title)
		}
	} else {
		for _, row := range raw.AlternativeTitles.Results {
			add(row.Title)
		}
	}
	for _, row := range raw.Translations.Translations {
		if kind == "movie" {
			add(row.Data.Title)
		} else {
			add(row.Data.Name)
		}
	}
	return names, nil
}

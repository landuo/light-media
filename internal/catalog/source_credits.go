package catalog

import (
	"context"
	"sort"
	"strconv"
)

// SourcePersonCredits keeps the pinned TmdbApi cast-only movie+TV ordering and
// 24-row page slice. The existing compact UI decoder remains the native shape;
// a crew list, dedupe or popularity sort would change module list accumulation.
func (c *Client) SourcePersonCredits(ctx context.Context, id int64, page int) (Page, error) {
	empty := Page{Results: []Media{}, Page: page, TotalPages: page}
	if !validID(id) || !validPage(page) {
		return empty, &Error{Code: "invalid_input"}
	}
	var movie, television struct {
		Cast []wireMedia `json:"cast"`
	}
	if err := c.get(ctx, "person/"+strconv.FormatInt(id, 10)+"/movie_credits", nil, &movie); err != nil {
		return empty, err
	}
	if err := c.get(ctx, "person/"+strconv.FormatInt(id, 10)+"/tv_credits", nil, &television); err != nil {
		return empty, err
	}
	type entry struct {
		value      wireMedia
		kind, date string
	}
	all := []entry{}
	for _, group := range []struct {
		rows []wireMedia
		kind string
	}{{movie.Cast, "movie"}, {television.Cast, "tv"}} {
		for _, value := range group.rows {
			date := value.ReleaseDate
			if date == "" {
				date = value.FirstAirDate
			}
			if date == "" {
				date = "1900-01-01"
			}
			all = append(all, entry{value, group.kind, date})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].date > all[j].date })
	start := min(len(all), (page-1)*24)
	end := min(len(all), page*24)
	for _, item := range all[start:end] {
		media, err := c.media(item.value, item.kind, false)
		if err != nil {
			return empty, err
		}
		empty.Results = append(empty.Results, media)
	}
	empty.TotalResults = len(all)
	empty.TotalPages = (len(all) + 23) / 24
	return empty, nil
}

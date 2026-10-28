package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// ExploreMedia keeps provider identity outside the TMDB identity namespace.
// A Douban or Bangumi result always has Media.ID == 0 until explicitly resolved.
type ExploreMedia struct {
	Media
	Source        string          `json:"source"`
	SourceID      string          `json:"source_id"`
	OriginalMedia json.RawMessage `json:"original_media,omitempty"`
}

type ExplorePage struct {
	Results      []ExploreMedia `json:"results"`
	Page         int            `json:"page"`
	TotalPages   int            `json:"total_pages"`
	TotalResults int            `json:"total_results"`
	HasMore      bool           `json:"has_more"`
}

type ExploreQuery struct {
	Source string
	Kind   string
	Page   int
	Count  int
	Params url.Values
}

var exploreIDs = regexp.MustCompile(`^[1-9][0-9]*(?:[,|][1-9][0-9]*)*$`)
var exploreLanguage = regexp.MustCompile(`^[a-z]{2,3}$`)

// ParseExploreQuery accepts the original discover view parameters and only
// forwards supported filters. Pagination uses a stable count for each source.
func ParseExploreQuery(values url.Values) (ExploreQuery, error) {
	q := ExploreQuery{Source: values.Get("source"), Page: 1, Count: 30, Params: url.Values{}}
	invalid := func() (ExploreQuery, error) {
		return ExploreQuery{}, &ExploreError{Source: q.Source, Code: "invalid_input"}
	}
	for _, values := range values {
		if len(values) != 1 || len(values[0]) > 1024 || strings.ContainsAny(values[0], "\r\n\x00") {
			return invalid()
		}
	}
	if raw := values.Get("page"); raw != "" {
		page, err := strconv.Atoi(raw)
		if err != nil || !validPage(page) {
			return invalid()
		}
		q.Page = page
	}
	if raw := values.Get("count"); raw != "" {
		count, err := strconv.Atoi(raw)
		if err != nil || count < 1 || count > 100 {
			return invalid()
		}
		q.Count = count
	}
	if q.Source == "anilist" {
		return parseAniListExplore(values, q)
	}
	if q.Source == "bangumi" {
		if kind := values.Get("type"); kind != "" && kind != "2" {
			return invalid()
		}
		q.Kind = "2"
		q.Params.Set("type", "2")
		sort := values.Get("sort")
		// FastAPI's original default only applies when sort is absent. An
		// explicit empty value comes from clearing the Bangumi sort chip.
		if !values.Has("sort") {
			sort = "rank"
		}
		if sort != "" && sort != "rank" && sort != "date" {
			return invalid()
		}
		q.Params.Set("sort", sort)
		if cat := values.Get("cat"); cat != "" {
			if cat != "0" && cat != "1" && cat != "2" && cat != "3" && cat != "5" {
				return invalid()
			}
			q.Params.Set("cat", cat)
		}
		if year := values.Get("year"); year != "" {
			n, err := strconv.Atoi(year)
			if err != nil || len(year) != 4 || n < 1800 || n > 2199 {
				return invalid()
			}
			q.Params.Set("year", year)
		}
		q.Params.Set("limit", strconv.Itoa(q.Count))
		q.Params.Set("offset", strconv.Itoa((q.Page-1)*q.Count))
		return q, nil
	}
	switch values.Get("type") {
	case "", "movies", "movie":
		q.Kind = "movie"
	case "tvs", "tv":
		q.Kind = "tv"
	default:
		return invalid()
	}
	if q.Source == "douban" {
		sort := values.Get("sort")
		if sort == "" {
			sort = "U"
		}
		if sort != "U" && sort != "R" && sort != "T" && sort != "S" {
			return invalid()
		}
		tags := strings.TrimSpace(values.Get("tags"))
		if len(tags) > 480 {
			return invalid()
		}
		q.Params = url.Values{"sort": {sort}, "tags": {tags}, "count": {strconv.Itoa(q.Count)}, "start": {strconv.Itoa((q.Page - 1) * q.Count)}}
		return q, nil
	}
	if q.Source != "themoviedb" {
		return invalid()
	}
	q.Count = 20
	sort := values.Get("sort_by")
	if sort == "" {
		sort = "popularity.desc"
	}
	dateSort := "release_date"
	if q.Kind == "tv" {
		dateSort = "first_air_date"
	}
	validSort := false
	for _, field := range []string{"popularity", "vote_average", dateSort} {
		validSort = validSort || sort == field+".asc" || sort == field+".desc"
	}
	if !validSort {
		return invalid()
	}
	q.Params = url.Values{"sort_by": {sort}, "page": {strconv.Itoa(q.Page)}, "include_adult": {"false"}}
	for _, key := range []string{"with_genres", "with_keywords", "with_watch_providers"} {
		v := values.Get(key)
		if v != "" && (len(v) > 256 || !exploreIDs.MatchString(v)) {
			return invalid()
		}
		if v != "" {
			q.Params.Set(key, v)
		}
	}
	if v := values.Get("with_original_language"); v != "" {
		if !exploreLanguage.MatchString(v) {
			return invalid()
		}
		q.Params.Set("with_original_language", v)
	}
	for _, key := range []string{"vote_average", "vote_count"} {
		v := values.Get(key)
		if v == "" {
			v = "0"
		}
		n, err := strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 100_000_000 || key == "vote_average" && n > 10 || key == "vote_count" && math.Trunc(n) != n {
			return invalid()
		}
		q.Params.Set(key+".gte", strconv.FormatFloat(n, 'f', -1, 64))
	}
	if v := values.Get("release_date"); v != "" {
		if _, _, ok := date(v); !ok {
			return invalid()
		}
		q.Params.Set(dateSort+".gte", v)
	}
	return q, nil
}

func (q ExploreQuery) CacheKey() string {
	return fmt.Sprintf("explore:v1:%s:%s:%d:%s", q.Source, q.Kind, q.Page, q.Params.Encode())
}

func (c *Client) Explore(ctx context.Context, q ExploreQuery) (ExplorePage, error) {
	if q.Source != "themoviedb" || !kindOK(q.Kind) || !validPage(q.Page) {
		return ExplorePage{}, &Error{Code: "invalid_input"}
	}
	var raw wirePage
	if err := c.get(ctx, "discover/"+q.Kind, q.Params, &raw); err != nil {
		return ExplorePage{}, err
	}
	p, err := c.page(raw, q.Kind, q.Page)
	if err != nil {
		return ExplorePage{}, err
	}
	result := ExplorePage{Results: []ExploreMedia{}, Page: p.Page, TotalPages: p.TotalPages, TotalResults: p.TotalResults, HasMore: p.Page < p.TotalPages}
	for _, m := range p.Results {
		result.Results = append(result.Results, ExploreMedia{Media: m, Source: "themoviedb", SourceID: strconv.FormatInt(m.ID, 10)})
	}
	return result, nil
}

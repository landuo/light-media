package catalog

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

type ExploreResolution struct {
	Media       *Media  `json:"media,omitempty"`
	Candidates  []Media `json:"candidates"`
	MatchMethod string  `json:"match_method,omitempty"`
}

// Resolve reads source details itself. Browser-supplied titles or provider IDs
// can never create a TMDB identity. Ambiguous results remain explicit choices.
func (e *Explorer) Resolve(ctx context.Context, source, kind, id string) (ExploreResolution, error) {
	m, err := e.Detail(ctx, source, kind, id)
	if err != nil {
		return ExploreResolution{}, err
	}
	return e.tmdb.ResolveExploreMedia(ctx, m)
}

func (c *Client) ResolveExploreMedia(ctx context.Context, source ExploreMedia) (ExploreResolution, error) {
	result := ExploreResolution{Candidates: []Media{}}
	if source.Source == "themoviedb" && validID(source.ID) && kindOK(source.MediaType) {
		m := source.Media
		result.Media = &m
		result.MatchMethod = "tmdb"
		return result, nil
	}
	if !kindOK(source.MediaType) {
		return result, &ExploreError{Source: source.Source, Code: "invalid_response"}
	}
	if source.IMDbID != "" {
		if !imdbTitleID.MatchString(source.IMDbID) {
			return result, &ExploreError{Source: source.Source, Code: "invalid_response"}
		}
		var raw struct {
			Movies []wireMedia `json:"movie_results"`
			TVs    []wireMedia `json:"tv_results"`
		}
		if err := c.get(ctx, "find/"+source.IMDbID, url.Values{"external_source": {"imdb_id"}}, &raw); err != nil {
			return result, err
		}
		items := raw.Movies
		if source.MediaType == "tv" {
			items = raw.TVs
		}
		if items == nil || len(items) > 100 {
			return result, &Error{Code: "invalid_response"}
		}
		for _, entry := range items {
			m, err := c.media(entry, source.MediaType, false)
			if err != nil {
				return result, err
			}
			result.Candidates = append(result.Candidates, m)
		}
		if len(result.Candidates) == 1 {
			m := result.Candidates[0]
			m.IMDbID = source.IMDbID
			result.Media = &m
			result.MatchMethod = "imdb"
			return result, nil
		}
		if len(result.Candidates) > 1 {
			return result, nil
		}
	}
	// Search two primary names and at most one source alias. Searching is not
	// proof: only a unique exact title/type/year match can resolve automatically.
	names := []string{}
	normalized := map[string]bool{}
	for _, name := range append([]string{source.Title, source.OriginalTitle}, source.Aliases...) {
		key := exploreTitle(name)
		if key != "" && !normalized[key] && queryOK(name) {
			normalized[key] = true
			names = append(names, name)
		}
	}
	seen := map[string]bool{}
	complete := true
	for _, name := range names[:min(3, len(names))] {
		p, err := c.Search(ctx, name, source.MediaType, 1)
		if err != nil {
			return result, err
		}
		complete = complete && p.TotalPages <= 1
		for _, m := range p.Results {
			key := m.MediaType + ":" + strconv.FormatInt(m.ID, 10)
			if !seen[key] {
				seen[key] = true
				result.Candidates = append(result.Candidates, m)
			}
		}
	}
	exact := []Media{}
	for _, m := range result.Candidates {
		if source.Year > 0 && m.Year == source.Year && m.MediaType == source.MediaType && (normalized[exploreTitle(m.Title)] || normalized[exploreTitle(m.OriginalTitle)]) {
			exact = append(exact, m)
		}
	}
	if complete && len(exact) == 1 {
		m := exact[0]
		result.Media = &m
		result.MatchMethod = "title_year"
	}
	return result, nil
}

func exploreTitle(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

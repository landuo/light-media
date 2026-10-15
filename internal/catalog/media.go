package catalog

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type wirePage struct {
	Results      []wireMedia `json:"results"`
	Page         int         `json:"page"`
	TotalPages   int         `json:"total_pages"`
	TotalResults int         `json:"total_results"`
}

type wireMedia struct {
	ID                int64                  `json:"id"`
	MediaType         string                 `json:"media_type"`
	Title             string                 `json:"title"`
	Name              string                 `json:"name"`
	OriginalTitle     string                 `json:"original_title"`
	OriginalName      string                 `json:"original_name"`
	Overview          string                 `json:"overview"`
	PosterPath        string                 `json:"poster_path"`
	BackdropPath      string                 `json:"backdrop_path"`
	ReleaseDate       string                 `json:"release_date"`
	FirstAirDate      string                 `json:"first_air_date"`
	VoteAverage       float64                `json:"vote_average"`
	VoteCount         int                    `json:"vote_count"`
	Popularity        float64                `json:"popularity"`
	Genres            []Genre                `json:"genres"`
	GenreIDs          []int64                `json:"genre_ids"`
	Credits           *wireCredits           `json:"credits"`
	Recommendations   *wirePage              `json:"recommendations"`
	Seasons           []SeasonSummary        `json:"seasons"`
	NumberOfEpisodes  int                    `json:"number_of_episodes"`
	NumberOfSeasons   int                    `json:"number_of_seasons"`
	Status            string                 `json:"status"`
	Runtime           int                    `json:"runtime"`
	Character         string                 `json:"character"`
	Job               string                 `json:"job"`
	Adult             bool                   `json:"adult"`
	IMDbID            string                 `json:"imdb_id"`
	ExternalIDs       *wireExternalIDs       `json:"external_ids"`
	AlternativeTitles *wireAlternativeTitles `json:"alternative_titles"`
}

type wireCredits struct {
	Cast []CastMember `json:"cast"`
}

// Search searches titles. kind is movie, tv or all (empty also means all).
// Multi-search's person results are excluded; actor pages use Person instead.
func (c *Client) Search(ctx context.Context, query, kind string, page int) (Page, error) {
	if kind == "" || kind == "all" {
		kind = "multi"
	}
	if !queryOK(query) || !validPage(page) || (!kindOK(kind) && kind != "multi") {
		return Page{}, &Error{Code: "invalid_input"}
	}
	params := url.Values{"query": {strings.TrimSpace(query)}, "page": {strconv.Itoa(page)}, "include_adult": {"false"}}
	var response wirePage
	if err := c.get(ctx, "search/"+kind, params, &response); err != nil {
		return Page{}, err
	}
	return c.page(response, kind, page)
}

// Browse lists movie/TV rankings. The allowed lists are popular, top_rated,
// trending_day, trending_week, plus now_playing/upcoming for movies and
// on_the_air/airing_today for TV. all is allowed for trending lists only.
func (c *Client) Browse(ctx context.Context, kind, list string, page int) (Page, error) {
	if !validPage(page) || (!kindOK(kind) && kind != "all") {
		return Page{}, &Error{Code: "invalid_input"}
	}
	path := ""
	if list == "trending_day" || list == "trending_week" {
		path = "trending/" + kind + "/" + strings.TrimPrefix(list, "trending_")
	} else if kindOK(kind) && (list == "popular" || list == "top_rated" || (kind == "movie" && (list == "now_playing" || list == "upcoming")) || (kind == "tv" && (list == "on_the_air" || list == "airing_today"))) {
		path = kind + "/" + list
	} else {
		return Page{}, &Error{Code: "invalid_input"}
	}
	var response wirePage
	if err := c.get(ctx, path, url.Values{"page": {strconv.Itoa(page)}}, &response); err != nil {
		return Page{}, err
	}
	return c.page(response, kind, page)
}

// Detail includes cast roles, seasons and the first recommendation page.
// Empty Chinese titles/overviews use English when available; failure of this
// optional fallback does not discard otherwise valid Chinese metadata.
func (c *Client) Detail(ctx context.Context, kind string, id int64) (Media, error) {
	if !kindOK(kind) || !validID(id) {
		return Media{}, &Error{Code: "invalid_input"}
	}
	path := kind + "/" + strconv.FormatInt(id, 10)
	var response wireMedia
	if err := c.get(ctx, path, url.Values{"append_to_response": {"credits,recommendations,external_ids,alternative_titles"}}, &response); err != nil {
		return Media{}, err
	}
	if response.ID != id || (response.MediaType != "" && response.MediaType != kind) {
		return Media{}, &Error{Code: "invalid_response"}
	}
	if response.Overview == "" || (kind == "movie" && response.Title == "") || (kind == "tv" && response.Name == "") {
		var fallback wireMedia
		if c.get(ctx, path, url.Values{"language": {"en-US"}}, &fallback) == nil && fallback.ID == id {
			if response.Overview == "" {
				response.Overview = fallback.Overview
			}
			if response.Title == "" {
				response.Title = fallback.Title
			}
			if response.Name == "" {
				response.Name = fallback.Name
			}
		}
	}
	return c.media(response, kind, true)
}

func (c *Client) page(response wirePage, kind string, requested int) (Page, error) {
	if response.Page != requested || response.TotalPages < 0 || response.TotalResults < 0 || response.TotalPages > 10_000_000 || response.Results == nil || len(response.Results) > 100 {
		return Page{}, &Error{Code: "invalid_response"}
	}
	result := Page{Results: []Media{}, Page: response.Page, TotalPages: min(response.TotalPages, 500), TotalResults: response.TotalResults}
	for _, entry := range response.Results {
		if entry.Adult || entry.MediaType == "person" {
			continue
		}
		media, err := c.media(entry, kind, false)
		if err != nil {
			return Page{}, err
		}
		result.Results = append(result.Results, media)
	}
	return result, nil
}

func (c *Client) media(raw wireMedia, kind string, includeRelated bool) (Media, error) {
	if kind == "multi" || kind == "all" {
		kind = raw.MediaType
	}
	if !validID(raw.ID) || !kindOK(kind) || (raw.MediaType != "" && raw.MediaType != kind) || raw.VoteAverage < 0 || raw.VoteAverage > 10 || raw.VoteCount < 0 || raw.Popularity < 0 || raw.NumberOfEpisodes < 0 || raw.NumberOfSeasons < 0 || len(raw.Genres) > 100 || len(raw.GenreIDs) > 100 || len(raw.Seasons) > 1000 {
		return Media{}, &Error{Code: "invalid_response"}
	}
	title, original, release := raw.Title, raw.OriginalTitle, raw.ReleaseDate
	if kind == "tv" {
		title, original, release = raw.Name, raw.OriginalName, raw.FirstAirDate
	}
	if title == "" {
		title = original
	}
	release, year, valid := date(release)
	if !valid || c.text(title, 512) == "" {
		return Media{}, &Error{Code: "invalid_response"}
	}
	result := Media{
		ID: raw.ID, MediaType: kind, Title: c.text(title, 512), OriginalTitle: c.text(original, 512),
		Year: year, Overview: c.text(raw.Overview, 32000), PosterPath: c.image(raw.PosterPath), BackdropPath: c.image(raw.BackdropPath),
		ReleaseDate: release, VoteAverage: raw.VoteAverage, VoteCount: raw.VoteCount, Popularity: raw.Popularity,
		Genres: []Genre{}, GenreIDs: []int64{}, Cast: []CastMember{}, Recommendations: []Media{}, Seasons: []SeasonSummary{}, Aliases: []string{},
		NumberOfEpisodes: raw.NumberOfEpisodes, NumberOfSeasons: raw.NumberOfSeasons, Status: c.text(raw.Status, 100),
		Runtime: raw.Runtime, Character: c.text(raw.Character, 1024), Job: c.text(raw.Job, 256),
	}
	for _, genre := range raw.Genres {
		if !validID(genre.ID) {
			return Media{}, &Error{Code: "invalid_response"}
		}
		result.Genres = append(result.Genres, Genre{ID: genre.ID, Name: c.text(genre.Name, 256)})
	}
	for _, id := range raw.GenreIDs {
		if !validID(id) {
			return Media{}, &Error{Code: "invalid_response"}
		}
		result.GenreIDs = append(result.GenreIDs, id)
	}
	if !includeRelated {
		return result, nil
	}
	imdbID, aliases, err := c.searchIdentities(raw, kind, result.Title, result.OriginalTitle)
	if err != nil {
		return Media{}, err
	}
	result.IMDbID, result.Aliases = imdbID, aliases
	if raw.Credits == nil || raw.Credits.Cast == nil || raw.Recommendations == nil || raw.Recommendations.Results == nil {
		return Media{}, &Error{Code: "invalid_response"}
	}
	result.Cast, err = c.cast(raw.Credits.Cast)
	if err != nil {
		return Media{}, err
	}
	for _, season := range raw.Seasons {
		if !validID(season.ID) || season.SeasonNumber < 0 || season.SeasonNumber > 1000 || season.EpisodeCount < 0 {
			return Media{}, &Error{Code: "invalid_response"}
		}
		if _, _, valid := date(season.AirDate); !valid {
			return Media{}, &Error{Code: "invalid_response"}
		}
		season.Name, season.Overview, season.PosterPath = c.text(season.Name, 512), c.text(season.Overview, 32000), c.image(season.PosterPath)
		result.Seasons = append(result.Seasons, season)
	}
	if len(raw.Recommendations.Results) > 100 {
		return Media{}, &Error{Code: "invalid_response"}
	}
	for _, recommendation := range raw.Recommendations.Results {
		if recommendation.Adult {
			continue
		}
		entry, err := c.media(recommendation, kind, false)
		if err != nil {
			return Media{}, err
		}
		result.Recommendations = append(result.Recommendations, entry)
	}
	return result, nil
}

func (c *Client) cast(raw []CastMember) ([]CastMember, error) {
	if len(raw) > 2000 {
		return nil, &Error{Code: "invalid_response"}
	}
	result := make([]CastMember, 0, min(len(raw), 300))
	for _, member := range raw {
		if !validID(member.ID) || member.Order < 0 {
			return nil, &Error{Code: "invalid_response"}
		}
		member.Name, member.OriginalName = c.text(member.Name, 512), c.text(member.OriginalName, 512)
		if member.Name == "" {
			member.Name = member.OriginalName
		}
		member.Character, member.ProfilePath = c.text(member.Character, 1024), c.image(member.ProfilePath)
		result = append(result, member)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Order < result[j].Order })
	if len(result) > 300 {
		result = result[:300]
	}
	return result, nil
}

func (c *Client) image(value string) string {
	for _, secret := range []string{c.credentials.APIKey, c.credentials.AccessToken} {
		if secret != "" && strings.Contains(value, secret) {
			return ""
		}
	}
	return image(value)
}

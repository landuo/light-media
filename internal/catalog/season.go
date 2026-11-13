package catalog

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type wireSeason struct {
	ID           int64     `json:"id"`
	SeasonNumber int       `json:"season_number"`
	Name         string    `json:"name"`
	Overview     string    `json:"overview"`
	AirDate      string    `json:"air_date"`
	PosterPath   string    `json:"poster_path"`
	Episodes     []Episode `json:"episodes"`
}

// Season preserves each episode's own TMDB ID. SeriesID comes from the fixed
// API request route, never from an episode ID or an inferred string prefix.
// Season zero is supported for specials.
func (c *Client) Season(ctx context.Context, seriesID int64, season int) (Season, error) {
	if !validID(seriesID) || season < 0 || season > 1000 {
		return Season{}, &Error{Code: "invalid_input"}
	}
	path := "tv/" + strconv.FormatInt(seriesID, 10) + "/season/" + strconv.Itoa(season)
	var response wireSeason
	if err := c.get(ctx, path, nil, &response); err != nil {
		return Season{}, err
	}
	if !validID(response.ID) || response.SeasonNumber != season || response.Episodes == nil || len(response.Episodes) > 2000 {
		return Season{}, &Error{Code: "invalid_response"}
	}
	needsFallback := response.Name == "" || response.Overview == ""
	for _, episode := range response.Episodes {
		needsFallback = needsFallback || episode.Name == "" || episode.Overview == ""
	}
	if needsFallback {
		var fallback wireSeason
		if c.get(ctx, path, url.Values{"language": {"en-US"}}, &fallback) == nil && fallback.ID == response.ID && fallback.SeasonNumber == season {
			if response.Name == "" {
				response.Name = fallback.Name
			}
			if response.Overview == "" {
				response.Overview = fallback.Overview
			}
			byID := map[int64]Episode{}
			for _, episode := range fallback.Episodes {
				byID[episode.ID] = episode
			}
			for index := range response.Episodes {
				episode := &response.Episodes[index]
				if other, exists := byID[episode.ID]; exists && other.SeasonNumber == episode.SeasonNumber && other.EpisodeNumber == episode.EpisodeNumber {
					if episode.Name == "" {
						episode.Name = other.Name
					}
					if episode.Overview == "" {
						episode.Overview = other.Overview
					}
				}
			}
		}
	}
	if _, _, valid := date(response.AirDate); !valid {
		return Season{}, &Error{Code: "invalid_response"}
	}
	result := Season{ID: response.ID, SeriesID: seriesID, SeasonNumber: season, Name: c.text(response.Name, 512), Overview: c.text(response.Overview, 32000), AirDate: response.AirDate, PosterPath: c.image(response.PosterPath), Episodes: []Episode{}}
	ids, numbers := map[int64]bool{}, map[int]bool{}
	for _, episode := range response.Episodes {
		if !validID(episode.ID) || ids[episode.ID] || numbers[episode.EpisodeNumber] || episode.SeasonNumber != season || episode.EpisodeNumber < 1 || episode.EpisodeNumber > 10000 || episode.VoteAverage < 0 || episode.VoteAverage > 10 {
			return Season{}, &Error{Code: "invalid_response"}
		}
		ids[episode.ID], numbers[episode.EpisodeNumber] = true, true
		if _, _, valid := date(episode.AirDate); !valid {
			return Season{}, &Error{Code: "invalid_response"}
		}
		episode.Name, episode.Overview, episode.StillPath = c.text(episode.Name, 512), c.text(episode.Overview, 32000), c.image(episode.StillPath)
		if strings.TrimSpace(episode.Name) == "" {
			episode.Name = "第 " + strconv.Itoa(episode.EpisodeNumber) + " 集"
		}
		var err error
		episode.GuestStars, err = c.cast(episode.GuestStars)
		if err != nil {
			return Season{}, err
		}
		result.Episodes = append(result.Episodes, episode)
	}
	sort.Slice(result.Episodes, func(i, j int) bool { return result.Episodes[i].EpisodeNumber < result.Episodes[j].EpisodeNumber })
	return result, nil
}

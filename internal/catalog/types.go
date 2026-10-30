// Package catalog is the application's read-only TMDB metadata client.
package catalog

import (
	"encoding/json"
	"time"
)

// Credentials are held in memory only. Configure exactly one authentication
// method; the client never chooses a credential or a different API host itself.
type Credentials struct {
	APIKey      string `json:"-"`
	AccessToken string `json:"-"`
}

type Page struct {
	Results      []Media `json:"results"`
	Page         int     `json:"page"`
	TotalPages   int     `json:"total_pages"`
	TotalResults int     `json:"total_results"`
}

type Media struct {
	LibraryState        string    `json:"library_state,omitempty"`
	SubscriptionID      string    `json:"subscription_id,omitempty"`
	DownloadState       string    `json:"download_state,omitempty"`
	ID                  int64     `json:"id"`
	MediaType           string    `json:"media_type"`
	Title               string    `json:"title"`
	OriginalTitle       string    `json:"original_title"`
	OriginalLanguage    string    `json:"original_language"`
	OriginCountry       []string  `json:"origin_country"`
	ProductionCountries []Country `json:"production_countries"`
	// CategoryMetadataKnown records a valid detail fetch by this decoder, even
	// when TMDB leaves classification fields empty. Older cache entries lack it.
	CategoryMetadataKnown bool                     `json:"category_metadata_known,omitempty"`
	CategoryFields        map[string]CategoryField `json:"category_fields,omitempty"`
	TVDBID                *int64                   `json:"tvdb_id,omitempty"`
	IMDbID                string                   `json:"imdb_id"`
	Aliases               []string                 `json:"aliases"`
	Year                  int                      `json:"year"`
	Overview              string                   `json:"overview"`
	PosterPath            string                   `json:"poster_path"`
	BackdropPath          string                   `json:"backdrop_path"`
	ReleaseDate           string                   `json:"release_date"`
	VoteAverage           float64                  `json:"vote_average"`
	VoteCount             int                      `json:"vote_count"`
	Popularity            float64                  `json:"popularity"`
	Genres                []Genre                  `json:"genres"`
	GenreIDs              []int64                  `json:"genre_ids,omitempty"`
	Cast                  []CastMember             `json:"cast"`
	Recommendations       []Media                  `json:"recommendations"`
	Seasons               []SeasonSummary          `json:"seasons"`
	NumberOfEpisodes      int                      `json:"number_of_episodes"`
	NumberOfSeasons       int                      `json:"number_of_seasons"`
	Status                string                   `json:"status"`
	Runtime               int                      `json:"runtime"`
	Character             string                   `json:"character,omitempty"`
	Job                   string                   `json:"job,omitempty"`
}

type Genre struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Country struct {
	ISO3166_1 string `json:"iso_3166_1"`
	Name      string `json:"name"`
}

type CastMember struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	OriginalName string `json:"original_name"`
	Character    string `json:"character"`
	ProfilePath  string `json:"profile_path"`
	Order        int    `json:"order"`
}

type SeasonSummary struct {
	ID           int64  `json:"id"`
	SeasonNumber int    `json:"season_number"`
	Name         string `json:"name"`
	Overview     string `json:"overview"`
	AirDate      string `json:"air_date"`
	PosterPath   string `json:"poster_path"`
	EpisodeCount int    `json:"episode_count"`
}

type Season struct {
	ID           int64     `json:"id"`
	SeriesID     int64     `json:"series_id"`
	SeasonNumber int       `json:"season_number"`
	Name         string    `json:"name"`
	Overview     string    `json:"overview"`
	AirDate      string    `json:"air_date"`
	PosterPath   string    `json:"poster_path"`
	Episodes     []Episode `json:"episodes"`
}

type Episode struct {
	OriginalEpisode json.RawMessage `json:"original_episode,omitempty"`
	ID              int64           `json:"id"`
	SeasonNumber    int             `json:"season_number"`
	EpisodeNumber   int             `json:"episode_number"`
	Name            string          `json:"name"`
	Overview        string          `json:"overview"`
	AirDate         string          `json:"air_date"`
	StillPath       string          `json:"still_path"`
	VoteAverage     float64         `json:"vote_average"`
	Runtime         int             `json:"runtime"`
	GuestStars      []CastMember    `json:"guest_stars"`
}

type Person struct {
	ID                 int64    `json:"id"`
	Name               string   `json:"name"`
	Biography          string   `json:"biography"`
	Birthday           string   `json:"birthday"`
	Deathday           string   `json:"deathday"`
	PlaceOfBirth       string   `json:"place_of_birth"`
	ProfilePath        string   `json:"profile_path"`
	KnownForDepartment string   `json:"known_for_department"`
	AlsoKnownAs        []string `json:"also_known_as"`
}

// Error is safe to display and log. It never includes a provider response,
// request URL, authentication header or the underlying transport error.
type Error struct {
	Code       string        `json:"code"`
	StatusCode int           `json:"status_code,omitempty"`
	RetryAfter time.Duration `json:"-"`
}

func (e *Error) Error() string {
	switch e.Code {
	case "not_configured":
		return "TMDB 凭据尚未配置"
	case "invalid_input":
		return "TMDB 请求参数或凭据格式无效"
	case "unauthorized":
		return "TMDB 认证失败，请检查凭据"
	case "not_found":
		return "TMDB 未找到该条目"
	case "rate_limited":
		return "TMDB 请求过于频繁，请稍后重试"
	case "canceled":
		return "TMDB 请求已取消或超时"
	case "invalid_response":
		return "TMDB 返回了无法处理的数据"
	case "redirect_blocked":
		return "TMDB 返回了不允许的重定向"
	default:
		return "TMDB 暂时不可用，请稍后重试"
	}
}

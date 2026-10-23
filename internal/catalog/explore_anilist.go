package catalog

import (
	"context"
	"fmt"
	"html"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

const aniListMediaFields = `
  id title { romaji english native } format status description(asHtml: false)
  startDate { year month day } seasonYear episodes duration countryOfOrigin
  coverImage { extraLarge large } bannerImage genres synonyms averageScore popularity isAdult
`

const aniListExploreQuery = `query (
  $page: Int!, $count: Int!, $search: String, $genre: String, $format: MediaFormat,
  $season: MediaSeason, $seasonYear: Int, $status: MediaStatus, $country: CountryCode, $sort: [MediaSort]
) {
  Page(page: $page, perPage: $count) {
    pageInfo { currentPage perPage lastPage total hasNextPage }
    media(search: $search, type: ANIME, genre: $genre, format: $format, season: $season,
      seasonYear: $seasonYear, status: $status, countryOfOrigin: $country, isAdult: false, sort: $sort) {
` + aniListMediaFields + `
    }
  }
}`

const aniListDetailQuery = `query ($id: Int!) {
  Media(id: $id, type: ANIME) {
` + aniListMediaFields + `
    characters(perPage: 25, sort: [ROLE, RELEVANCE]) {
      edges {
        node { name { full native } }
        voiceActors(language: JAPANESE, sort: [RELEVANCE]) { name { full native } image { large } }
      }
    }
  }
}`

type aniListName struct {
	Full   string `json:"full"`
	Native string `json:"native"`
}

type aniListMedia struct {
	ID    int64 `json:"id"`
	Title struct {
		Chinese string `json:"chinese"`
		Native  string `json:"native"`
		Romaji  string `json:"romaji"`
		English string `json:"english"`
	} `json:"title"`
	Format      string `json:"format"`
	Status      string `json:"status"`
	Description string `json:"description"`
	StartDate   struct {
		Year  int `json:"year"`
		Month int `json:"month"`
		Day   int `json:"day"`
	} `json:"startDate"`
	SeasonYear      int    `json:"seasonYear"`
	Episodes        int    `json:"episodes"`
	Duration        int    `json:"duration"`
	CountryOfOrigin string `json:"countryOfOrigin"`
	CoverImage      struct {
		ExtraLarge string `json:"extraLarge"`
		Large      string `json:"large"`
	} `json:"coverImage"`
	BannerImage  string   `json:"bannerImage"`
	Genres       []string `json:"genres"`
	Synonyms     []string `json:"synonyms"`
	AverageScore float64  `json:"averageScore"`
	Popularity   float64  `json:"popularity"`
	IsAdult      bool     `json:"isAdult"`
	Characters   struct {
		Edges []struct {
			Node struct {
				Name aniListName `json:"name"`
			} `json:"node"`
			VoiceActors []struct {
				Name  aniListName `json:"name"`
				Image struct {
					Large string `json:"large"`
				} `json:"image"`
			} `json:"voiceActors"`
		} `json:"edges"`
	} `json:"characters"`
}

type aniListResponse struct {
	Errors []struct {
		Status int `json:"status"`
	} `json:"errors"`
	Data *struct {
		Media *aniListMedia `json:"Media"`
		Page  *struct {
			PageInfo struct {
				CurrentPage int  `json:"currentPage"`
				PerPage     int  `json:"perPage"`
				LastPage    int  `json:"lastPage"`
				Total       int  `json:"total"`
				HasNextPage bool `json:"hasNextPage"`
			} `json:"pageInfo"`
			Media []aniListMedia `json:"media"`
		} `json:"Page"`
	} `json:"data"`
}

func parseAniListExplore(values url.Values, q ExploreQuery) (ExploreQuery, error) {
	invalid := func() (ExploreQuery, error) {
		return ExploreQuery{}, &ExploreError{Source: "anilist", Code: "invalid_input"}
	}
	q.Kind = "anime"
	if values.Get("count") == "" {
		q.Count = 20
	}
	if q.Count < 1 || q.Count > 50 {
		return invalid()
	}
	q.Params = url.Values{"page": {strconv.Itoa(q.Page)}, "count": {strconv.Itoa(q.Count)}}
	for key, allowed := range map[string]string{
		"format":  "TV TV_SHORT MOVIE SPECIAL OVA ONA MUSIC",
		"season":  "WINTER SPRING SUMMER FALL",
		"status":  "FINISHED RELEASING NOT_YET_RELEASED CANCELLED HIATUS",
		"country": "JP CN KR TW",
		"sort":    "POPULARITY_DESC TRENDING_DESC SCORE_DESC START_DATE_DESC FAVOURITES_DESC ID_DESC SEARCH_MATCH POPULARITY TRENDING SCORE START_DATE FAVOURITES ID TITLE_ROMAJI TITLE_ROMAJI_DESC TITLE_ENGLISH TITLE_ENGLISH_DESC TITLE_NATIVE TITLE_NATIVE_DESC UPDATED_AT UPDATED_AT_DESC END_DATE END_DATE_DESC EPISODES EPISODES_DESC DURATION DURATION_DESC STATUS STATUS_DESC FORMAT FORMAT_DESC TYPE TYPE_DESC",
	} {
		value := values.Get(key)
		if key == "sort" && value == "" {
			value = "POPULARITY_DESC"
		}
		if value != "" {
			if !slices.Contains(strings.Fields(allowed), value) {
				return invalid()
			}
			q.Params.Set(key, value)
		}
	}
	for _, key := range []string{"search", "genre"} {
		if value := strings.TrimSpace(values.Get(key)); value != "" {
			if !queryOK(value) {
				return invalid()
			}
			q.Params.Set(key, value)
		}
	}
	if year := values.Get("season_year"); year != "" {
		n, err := strconv.Atoi(year)
		if err != nil || len(year) != 4 || n < 1800 || n > 2199 {
			return invalid()
		}
		q.Params.Set("seasonYear", year)
	}
	return q, nil
}

func (e *Explorer) aniListGraphQL(ctx context.Context, source, path, query string, variables map[string]any) (aniListResponse, error) {
	var raw aniListResponse
	if err := e.request(ctx, source, http.MethodPost, path, nil, map[string]any{"query": query, "variables": variables}, &raw); err != nil {
		return raw, err
	}
	if len(raw.Errors) > 0 {
		code := "upstream"
		switch raw.Errors[0].Status {
		case http.StatusNotFound:
			code = "not_found"
		case http.StatusTooManyRequests:
			code = "rate_limited"
		}
		return raw, &ExploreError{Source: "anilist", Code: code}
	}
	if raw.Data == nil {
		return raw, &ExploreError{Source: "anilist", Code: "invalid_response"}
	}
	return raw, nil
}

type aniListTranslation struct {
	ID       int64    `json:"id"`
	Title    string   `json:"title"`
	Synonyms []string `json:"synonyms"`
}

// Explorer instances are request-scoped. Keep one bounded public translation
// dataset across them; neither credentials nor media responses enter this cache.
var aniListCache = struct {
	sync.Mutex
	Translations      map[int64]aniListTranslation
	Expires           time.Time
	ProxyDisabledTill time.Time
}{}

func (e *Explorer) aniListRequest(ctx context.Context, query string, variables map[string]any) (aniListResponse, error) {
	if ctx == nil {
		return aniListResponse{}, &ExploreError{Source: "anilist", Code: "invalid_input"}
	}
	aniListCache.Lock()
	useProxy := !time.Now().Before(aniListCache.ProxyDisabledTill)
	aniListCache.Unlock()
	var raw aniListResponse
	var err error
	if useProxy {
		raw, err = e.aniListGraphQL(ctx, "anilist_proxy", "/anilist/", query, variables)
		if err != nil && ctx.Err() == nil {
			aniListCache.Lock()
			aniListCache.ProxyDisabledTill = time.Now().Add(5 * time.Minute)
			aniListCache.Unlock()
		}
	}
	if !useProxy || err != nil {
		if ctx.Err() != nil {
			return raw, &ExploreError{Source: "anilist", Code: "canceled"}
		}
		raw, err = e.aniListGraphQL(ctx, "anilist", "/", query, variables)
	}
	if err != nil {
		return raw, err
	}
	translations := e.aniListTranslations(ctx)
	inject := func(media *aniListMedia) {
		if translation, ok := translations[media.ID]; ok {
			media.Title.Chinese = translation.Title
			media.Synonyms = append(media.Synonyms, translation.Synonyms...)
		}
	}
	if raw.Data.Media != nil {
		inject(raw.Data.Media)
	}
	if raw.Data.Page != nil {
		for i := range raw.Data.Page.Media {
			inject(&raw.Data.Page.Media[i])
		}
	}
	return raw, nil
}

func (e *Explorer) aniListTranslations(ctx context.Context) map[int64]aniListTranslation {
	aniListCache.Lock()
	defer aniListCache.Unlock()
	if time.Now().Before(aniListCache.Expires) {
		return aniListCache.Translations
	}
	var raw []aniListTranslation
	// A failed refresh preserves the previous public dataset and retries later.
	aniListCache.Expires = time.Now().Add(5 * time.Minute)
	if err := e.request(ctx, "anilist_translations", http.MethodGet, "/soruly/anilist-chinese/master/anilist-chinese.json", nil, nil, &raw); err != nil || len(raw) == 0 || len(raw) > 50_000 {
		return aniListCache.Translations
	}
	translations := make(map[int64]aniListTranslation, len(raw))
	clean := (&Client{}).text
	for _, entry := range raw {
		if !validID(entry.ID) {
			continue
		}
		entry.Title = clean(entry.Title, 512)
		if len(entry.Synonyms) > 32 {
			entry.Synonyms = entry.Synonyms[:32]
		}
		for i := range entry.Synonyms {
			entry.Synonyms[i] = clean(entry.Synonyms[i], 512)
		}
		translations[entry.ID] = entry
	}
	aniListCache.Translations = translations
	aniListCache.Expires = time.Now().Add(24 * time.Hour)
	return translations
}

func (e *Explorer) exploreAniList(ctx context.Context, q ExploreQuery) (ExplorePage, error) {
	variables := map[string]any{"page": q.Page, "count": q.Count, "sort": []string{q.Params.Get("sort")}}
	for _, key := range []string{"search", "genre", "format", "season", "status", "country"} {
		if value := q.Params.Get(key); value != "" {
			variables[key] = value
		}
	}
	if year := q.Params.Get("seasonYear"); year != "" {
		variables["seasonYear"], _ = strconv.Atoi(year)
	}
	raw, err := e.aniListRequest(ctx, aniListExploreQuery, variables)
	if err != nil {
		return ExplorePage{}, err
	}
	page := raw.Data.Page
	if page == nil || page.Media == nil || len(page.Media) > q.Count || page.PageInfo.CurrentPage != q.Page || page.PageInfo.PerPage != q.Count || page.PageInfo.Total < 0 || page.PageInfo.LastPage < 0 {
		return ExplorePage{}, &ExploreError{Source: "anilist", Code: "invalid_response"}
	}
	result := ExplorePage{Results: []ExploreMedia{}, Page: q.Page, TotalPages: min(page.PageInfo.LastPage, 500), TotalResults: page.PageInfo.Total, HasMore: page.PageInfo.HasNextPage && q.Page < 500}
	seen := map[int64]bool{}
	for _, item := range page.Media {
		if item.IsAdult || seen[item.ID] {
			continue
		}
		media, err := normalizeAniList(item)
		if err != nil {
			return ExplorePage{}, err
		}
		seen[item.ID] = true
		result.Results = append(result.Results, media)
	}
	return result, nil
}

func (e *Explorer) detailAniList(ctx context.Context, id string) (ExploreMedia, error) {
	mediaID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || !validID(mediaID) || strconv.FormatInt(mediaID, 10) != id {
		return ExploreMedia{}, &ExploreError{Source: "anilist", Code: "invalid_input"}
	}
	raw, err := e.aniListRequest(ctx, aniListDetailQuery, map[string]any{"id": mediaID})
	if err != nil {
		return ExploreMedia{}, err
	}
	if raw.Data.Media == nil || raw.Data.Media.IsAdult {
		return ExploreMedia{}, &ExploreError{Source: "anilist", Code: "not_found"}
	}
	if raw.Data.Media.ID != mediaID {
		return ExploreMedia{}, &ExploreError{Source: "anilist", Code: "invalid_response"}
	}
	return normalizeAniList(*raw.Data.Media)
}

var aniListTags = regexp.MustCompile(`<[^>]+>`)
var aniListBreaks = regexp.MustCompile(`(?i)<br\s*/?>`)

func normalizeAniList(raw aniListMedia) (ExploreMedia, error) {
	invalid := func() (ExploreMedia, error) {
		return ExploreMedia{}, &ExploreError{Source: "anilist", Code: "invalid_response"}
	}
	if !validID(raw.ID) || raw.AverageScore < 0 || raw.AverageScore > 100 || math.IsNaN(raw.AverageScore) || math.IsInf(raw.AverageScore, 0) || raw.Popularity < 0 || math.IsNaN(raw.Popularity) || math.IsInf(raw.Popularity, 0) || raw.Episodes < 0 || raw.Episodes > 100_000 || raw.Duration < 0 || raw.Duration > 100_000 || len(raw.Genres) > 100 || len(raw.Synonyms) > 1000 || len(raw.Characters.Edges) > 100 {
		return invalid()
	}
	// This text helper carries no TMDB credentials or network state.
	clean := (&Client{}).text
	first := func(values ...string) string {
		for _, value := range values {
			if value = clean(value, 512); value != "" {
				return value
			}
		}
		return ""
	}
	title := first(aniListChineseTitle(raw), raw.Title.Native, raw.Title.Romaji, raw.Title.English)
	if title == "" {
		return invalid()
	}
	kind := "tv"
	if raw.Format == "MOVIE" {
		kind = "movie"
	}
	year := raw.StartDate.Year
	if year == 0 {
		year = raw.SeasonYear
	}
	if year < 1800 || year > 2199 {
		year = 0
	}
	overview := aniListTags.ReplaceAllString(aniListBreaks.ReplaceAllString(raw.Description, "\n"), "")
	media := Media{
		ID: 0, MediaType: kind, Title: title, OriginalTitle: first(raw.Title.Native, raw.Title.Romaji),
		Year: year, ReleaseDate: aniListDate(raw.StartDate.Year, raw.StartDate.Month, raw.StartDate.Day),
		Overview:     clean(html.UnescapeString(overview), 32000),
		BackdropPath: ExploreImageURL("anilist", raw.BannerImage),
		VoteAverage:  math.Round(raw.AverageScore) / 10, Popularity: raw.Popularity,
		Runtime: raw.Duration, Status: clean(raw.Status, 64),
		Aliases: []string{}, Genres: []Genre{}, Cast: []CastMember{},
	}
	for _, image := range []string{raw.CoverImage.ExtraLarge, raw.CoverImage.Large} {
		if media.PosterPath == "" {
			media.PosterPath = ExploreImageURL("anilist", image)
		}
	}
	if raw.CountryOfOrigin != "" {
		media.OriginCountry = []string{clean(raw.CountryOfOrigin, 2)}
	}
	if raw.CountryOfOrigin == "JP" {
		media.OriginalLanguage = "ja"
	}
	aliases := map[string]bool{title: true}
	for _, value := range append([]string{raw.Title.English, raw.Title.Romaji, raw.Title.Native}, raw.Synonyms...) {
		if value = clean(value, 512); value != "" && !aliases[value] {
			media.Aliases = append(media.Aliases, value)
			aliases[value] = true
		}
	}
	for _, value := range raw.Genres {
		if value = clean(value, 256); value != "" {
			media.Genres = append(media.Genres, Genre{Name: value})
		}
	}
	if kind == "tv" && raw.Episodes > 0 {
		media.NumberOfEpisodes, media.NumberOfSeasons = raw.Episodes, 1
	}
	for _, edge := range raw.Characters.Edges {
		if len(edge.VoiceActors) == 0 {
			continue
		}
		actor := edge.VoiceActors[0]
		name := first(actor.Name.Native, actor.Name.Full)
		if name == "" {
			continue
		}
		// Person IDs from AniList cannot be passed to TMDB person endpoints.
		media.Cast = append(media.Cast, CastMember{Name: name, OriginalName: first(actor.Name.Full), Character: first(edge.Node.Name.Native, edge.Node.Name.Full), ProfilePath: ExploreImageURL("anilist", actor.Image.Large), Order: len(media.Cast)})
	}
	return ExploreMedia{Media: media, Source: "anilist", SourceID: strconv.FormatInt(raw.ID, 10)}, nil
}

func aniListDate(year, month, day int) string {
	if year < 1800 || year > 2199 || month < 0 || month > 12 || day < 0 || day > 31 {
		return ""
	}
	if month == 0 {
		return strconv.Itoa(year)
	}
	if day == 0 {
		return fmt.Sprintf("%04d-%02d", year, month)
	}
	value := fmt.Sprintf("%04d-%02d-%02d", year, month, day)
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return ""
	}
	return value
}

func aniListChineseTitle(raw aniListMedia) string {
	if raw.Title.Chinese == "" {
		return ""
	}
	isChinese := func(value string) bool {
		han := false
		for _, r := range value {
			if unicode.In(r, unicode.Hiragana, unicode.Katakana) {
				return false
			}
			han = han || unicode.Is(unicode.Han, r)
		}
		return han
	}
	if isChinese(raw.Title.Chinese) {
		return raw.Title.Chinese
	}
	for i := len(raw.Synonyms) - 1; i >= 0; i-- {
		if isChinese(raw.Synonyms[i]) {
			return raw.Synonyms[i]
		}
	}
	return raw.Title.Chinese
}

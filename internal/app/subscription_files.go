package app

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"light-media/internal/catalog"
)

// The files dialog mirrors MoviePilot's subscribe_files_info: every episode of
// the subscribed season with the downloads and library files attached to it.
// Facts come from the reservation/asset slots so a partially imported pack and
// a finished episode are visible at the same time.

type subscriptionFileEntry struct {
	Storage  string `json:"storage,omitempty"`
	Path     string `json:"path"`
	Size     int64  `json:"size,omitempty"`
	State    string `json:"state,omitempty"`
	FNOSGUID string `json:"fnos_guid,omitempty"`
}

type subscriptionDownloadEntry struct {
	Title        string  `json:"title,omitempty"`
	ResourceID   string  `json:"resource_id,omitempty"`
	SiteID       string  `json:"site_id,omitempty"`
	DownloaderID string  `json:"downloader_id,omitempty"`
	Hash         string  `json:"hash,omitempty"`
	State        string  `json:"state,omitempty"`
	Progress     float64 `json:"progress,omitempty"`
	ImportPlanID string  `json:"import_plan_id,omitempty"`
}

type subscriptionEpisodeFiles struct {
	Episode   int                         `json:"episode"`
	Title     string                      `json:"title,omitempty"`
	AirDate   string                      `json:"air_date,omitempty"`
	Status    string                      `json:"status"`
	Excluded  bool                        `json:"excluded,omitempty"`
	Files     []subscriptionFileEntry     `json:"files"`
	Downloads []subscriptionDownloadEntry `json:"downloads"`
}

type subscriptionFilesResult struct {
	SubscriptionID string                     `json:"subscription_id"`
	Media          catalog.Media              `json:"media"`
	Season         int                        `json:"season"`
	Episodes       []subscriptionEpisodeFiles `json:"episodes"`
	Summary        subscriptionFilesSummary   `json:"summary"`
}

type subscriptionFilesSummary struct {
	Total       int `json:"total"`
	Library     int `json:"library"`
	Downloading int `json:"downloading"`
	Missing     int `json:"missing"`
	Unaired     int `json:"unaired"`
	Excluded    int `json:"excluded"`
}

type subscriptionSeasonEntry struct {
	Season       int    `json:"season"`
	Name         string `json:"name,omitempty"`
	AirDate      string `json:"air_date,omitempty"`
	Episodes     int    `json:"episodes"`
	Library      int    `json:"library"`
	Downloading  int    `json:"downloading"`
	Missing      int    `json:"missing"`
	Subscribed   bool   `json:"subscribed"`
	StartEpisode int    `json:"start_episode,omitempty"`
	TotalEpisode int    `json:"total_episode,omitempty"`
}

type subscriptionSeasonsResult struct {
	SubscriptionID string                    `json:"subscription_id"`
	Media          catalog.Media             `json:"media"`
	CurrentSeason  int                       `json:"current_season"`
	Seasons        []subscriptionSeasonEntry `json:"seasons"`
}

type subscriptionEpisodeFacts struct {
	files     map[int][]subscriptionFileEntry
	downloads map[int][]subscriptionDownloadEntry
	known     []int
	titles    map[int]string
	dates     map[int]string
}

func (a *App) subscriptionEpisodeFacts(ctx context.Context, s Subscription, season int) (subscriptionEpisodeFacts, error) {
	facts := subscriptionEpisodeFacts{
		files:     map[int][]subscriptionFileEntry{},
		downloads: map[int][]subscriptionDownloadEntry{},
		titles:    map[int]string{},
		dates:     map[int]string{},
	}
	key := mediaKey(s.Media)
	rows, err := a.Store.reader().QueryContext(ctx, `SELECT a.episode,la.payload FROM asset_slots a JOIN library_asset la ON la.id=a.asset_id WHERE a.media_key=? AND a.season=? ORDER BY a.episode`, key, season)
	if err != nil {
		return facts, err
	}
	for rows.Next() {
		var episode int
		var b []byte
		if err = rows.Scan(&episode, &b); err != nil {
			rows.Close()
			return facts, err
		}
		var asset Asset
		if json.Unmarshal(b, &asset) == nil && asset.State != "removed" {
			facts.files[episode] = append(facts.files[episode], subscriptionFileEntry{Path: asset.Path, Size: asset.Size, State: asset.State, FNOSGUID: asset.FNOSGUID})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return facts, err
	}
	rows, err = a.Store.reader().QueryContext(ctx, `SELECT r.episode,i.payload FROM reservations r JOIN downloadhistory i ON i.uid=r.intent_id WHERE r.media_key=? AND r.season=? ORDER BY r.episode`, key, season)
	if err != nil {
		return facts, err
	}
	for rows.Next() {
		var episode int
		var b []byte
		if err = rows.Scan(&episode, &b); err != nil {
			rows.Close()
			return facts, err
		}
		var intent Intent
		if json.Unmarshal(b, &intent) == nil && intent.State != "cancelled" {
			facts.downloads[episode] = append(facts.downloads[episode], subscriptionDownloadEntry{Title: intent.ResourceTitle, ResourceID: intent.ResourceID, SiteID: intent.SiteID, DownloaderID: intent.DownloaderID, Hash: intent.Hash, State: intent.State, Progress: intent.Progress, ImportPlanID: intent.ImportPlanID})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return facts, err
	}
	if s.Media.MediaType == "tv" && !providerNativeMedia(s.Media) {
		var b []byte
		cacheKey := "season:" + strconv.FormatInt(s.Media.ID, 10) + ":" + strconv.Itoa(season)
		if err = a.Store.reader().QueryRowContext(ctx, `SELECT payload FROM cache_entry WHERE id=?`, cacheKey).Scan(&b); err == nil {
			var cache struct {
				Data catalog.Season `json:"data"`
			}
			if json.Unmarshal(b, &cache) == nil {
				for _, ep := range cache.Data.Episodes {
					facts.known = append(facts.known, ep.EpisodeNumber)
					facts.titles[ep.EpisodeNumber] = ep.Name
					facts.dates[ep.EpisodeNumber] = ep.AirDate
				}
				sort.Ints(facts.known)
			}
		}
	}
	for ep, date := range s.EpisodeDates {
		if date != "" {
			facts.dates[ep] = date
		}
	}
	return facts, nil
}

func (a *App) subscriptionTargetEpisodeNumbers(s Subscription, season int) []int {
	if s.Media.MediaType != "tv" {
		return []int{0}
	}
	seen := map[int]bool{}
	out := []int{}
	current := season == s.Season
	add := func(ep int) {
		if ep < subscriptionStartEpisode(s) || current && s.TotalEpisodes > 0 && ep > s.TotalEpisodes || seen[ep] {
			return
		}
		seen[ep] = true
		out = append(out, ep)
	}
	if current && longSubscriptionEpisodes(s) > 0 {
		for _, ep := range s.Episodes {
			add(ep)
		}
		if len(out) == 0 {
			last := s.TotalEpisodes
			if last == 0 {
				last = len(s.ProviderEpisodes)
			}
			for ep := 1; ep <= last; ep++ {
				add(ep)
			}
		}
	} else {
		for i := range s.Media.Seasons {
			if s.Media.Seasons[i].SeasonNumber == season {
				for ep := 1; ep <= s.Media.Seasons[i].EpisodeCount; ep++ {
					add(ep)
				}
				break
			}
		}
	}
	sort.Ints(out)
	return out
}

func longSubscriptionEpisodes(s Subscription) int {
	if s.TotalEpisodes > 0 {
		return s.TotalEpisodes
	}
	if len(s.ProviderEpisodes) > 0 {
		return len(s.ProviderEpisodes)
	}
	if len(s.Episodes) > 0 {
		return len(s.Episodes)
	}
	return 0
}

func (a *App) subscriptionFiles(ctx context.Context, key string, season int) (any, error) {
	var s Subscription
	if err := a.Store.Get(ctx, "subscription", key, &s); err != nil {
		return nil, err
	}
	if season == 0 {
		season = s.Season
	}
	if s.Media.MediaType != "tv" {
		season = 0
	}
	facts, err := a.subscriptionEpisodeFacts(ctx, s, season)
	if err != nil {
		return nil, err
	}
	episodes := a.subscriptionTargetEpisodeNumbers(s, season)
	if s.Media.MediaType != "tv" {
		episodes = []int{0}
	}
	excluded := map[int]bool{}
	for _, ep := range s.Excluded {
		excluded[ep] = true
	}
	today := time.Now().In(shanghai()).Format("2006-01-02")
	out := subscriptionFilesResult{SubscriptionID: s.ID, Media: s.Media, Season: season, Episodes: []subscriptionEpisodeFiles{}}
	for _, ep := range episodes {
		item := subscriptionEpisodeFiles{Episode: ep, Status: "missing", Files: []subscriptionFileEntry{}, Downloads: []subscriptionDownloadEntry{}}
		if s.Media.MediaType != "tv" {
			item.Title = s.Media.Title
		} else {
			item.Title = facts.titles[ep]
			item.AirDate = facts.dates[ep]
		}
		item.Excluded = excluded[ep]
		item.Files = append(item.Files, facts.files[ep]...)
		item.Downloads = append(item.Downloads, facts.downloads[ep]...)
		switch {
		case len(item.Files) > 0:
			item.Status = "library"
		case len(item.Downloads) > 0:
			item.Status = "downloading"
		case item.Excluded:
			item.Status = "excluded"
		case item.AirDate != "" && item.AirDate > today:
			item.Status = "unaired"
		}
		switch item.Status {
		case "library":
			out.Summary.Library++
		case "downloading":
			out.Summary.Downloading++
		case "excluded":
			out.Summary.Excluded++
		case "unaired":
			out.Summary.Unaired++
		default:
			out.Summary.Missing++
		}
		out.Summary.Total++
		out.Episodes = append(out.Episodes, item)
	}
	return out, nil
}

func (a *App) subscriptionSeasons(ctx context.Context, key string) (any, error) {
	var s Subscription
	if err := a.Store.Get(ctx, "subscription", key, &s); err != nil {
		return nil, err
	}
	out := subscriptionSeasonsResult{SubscriptionID: s.ID, Media: s.Media, CurrentSeason: s.Season, Seasons: []subscriptionSeasonEntry{}}
	seen := map[int]bool{}
	library := map[int]int{}
	downloading := map[int]int{}
	if rows, err := a.Store.reader().QueryContext(ctx, `SELECT a.season,COUNT(DISTINCT a.episode) FROM asset_slots a JOIN library_asset la ON la.id=a.asset_id WHERE a.media_key=? GROUP BY a.season`, mediaKey(s.Media)); err == nil {
		for rows.Next() {
			var season, n int
			if rows.Scan(&season, &n) == nil {
				library[season] = n
			}
		}
		rows.Close()
	}
	if rows, err := a.Store.reader().QueryContext(ctx, `SELECT r.season,COUNT(DISTINCT r.episode) FROM reservations r WHERE r.media_key=? GROUP BY r.season`, mediaKey(s.Media)); err == nil {
		for rows.Next() {
			var season, n int
			if rows.Scan(&season, &n) == nil {
				downloading[season] = n
			}
		}
		rows.Close()
	}
	appendSeason := func(season int, name string, airDate string, total int) {
		if seen[season] {
			return
		}
		seen[season] = true
		entry := subscriptionSeasonEntry{Season: season, Name: name, AirDate: airDate, Episodes: total, Library: library[season], Downloading: downloading[season]}
		entry.Subscribed = season == s.Season
		if entry.Subscribed {
			// The subscribed season follows the subscription's own scope, which
			// can differ from the season's TMDB episode count.
			targets := a.subscriptionTargetEpisodeNumbers(s, season)
			unaired := 0
			today := time.Now().In(shanghai()).Format("2006-01-02")
			if facts, err := a.subscriptionEpisodeFacts(ctx, s, season); err == nil {
				entry.Episodes, entry.Library, entry.Downloading = len(targets), 0, 0
				for _, ep := range targets {
					switch {
					case len(facts.files[ep]) > 0:
						entry.Library++
					case len(facts.downloads[ep]) > 0:
						entry.Downloading++
					case facts.dates[ep] > today:
						unaired++
					}
				}
			} else {
				entry.Episodes = len(targets)
			}
			entry.StartEpisode, entry.TotalEpisode = subscriptionStartEpisode(s), s.TotalEpisodes
			entry.Missing = entry.Episodes - entry.Library - entry.Downloading - unaired
			if entry.Missing < 0 {
				entry.Missing = 0
			}
		}
		out.Seasons = append(out.Seasons, entry)
	}
	for _, summary := range s.Media.Seasons {
		appendSeason(summary.SeasonNumber, summary.Name, summary.AirDate, summary.EpisodeCount)
	}
	for season := range library {
		if !seen[season] {
			appendSeason(season, "", "", library[season])
		}
	}
	for season := range downloading {
		if !seen[season] {
			appendSeason(season, "", "", downloading[season])
		}
	}
	sort.SliceStable(out.Seasons, func(i, j int) bool { return out.Seasons[i].Season < out.Seasons[j].Season })
	return out, nil
}

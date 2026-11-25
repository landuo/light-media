package app

import (
	"context"
	"sort"
	"time"
)

type CalendarEntry struct {
	SubscriptionID    string `json:"subscription_id"`
	Title             string `json:"title"`
	PosterPath        string `json:"poster_path"`
	MediaType         string `json:"media_type"`
	TMDBID            int64  `json:"tmdb_id"`
	Date              string `json:"date"`
	Source            string `json:"source"`
	Season            int    `json:"season"`
	Episode           int    `json:"episode"`
	Confirmed         bool   `json:"confirmed"`
	State             string `json:"state"`
	OriginalDate      string `json:"original_date,omitempty"`
	SubscriptionState string `json:"subscription_state"`
}

func (a *App) calendar(ctx context.Context, from, to string) (any, error) {
	loc := shanghai()
	today := dateOnly(time.Now().In(loc))
	if from == "" {
		from = today.AddDate(0, 0, -7).Format("2006-01-02")
	}
	if to == "" {
		to = today.AddDate(0, 0, 30).Format("2006-01-02")
	}
	start, e := parseDay(from, loc)
	if e != nil {
		return nil, e
	}
	end, e := parseDay(to, loc)
	if e != nil {
		return nil, e
	}
	if end.Before(start) || end.Sub(start) > 93*24*time.Hour {
		return nil, bad("INVALID_CALENDAR_RANGE", "日历范围须为连续93天以内")
	}
	snapshot, e := a.loadSubscriptionDisplay(ctx)
	if e != nil {
		return nil, e
	}
	entries := []CalendarEntry{}
	for _, s := range snapshot.subscriptions {
		base := CalendarEntry{SubscriptionID: s.ID, Title: s.Media.Title, PosterPath: s.Media.PosterPath, MediaType: s.Media.MediaType, TMDBID: s.Media.ID, Season: s.Season, SubscriptionState: s.State}
		if s.Media.MediaType == "movie" {
			if date := s.Media.ReleaseDate; date >= from && date <= to {
				row := base
				row.Date = date
				row.Source = "metadata"
				row.Confirmed = true
				row.State = "upcoming"
				if date <= today.Format("2006-01-02") {
					row.State = "available"
				}
				entries = append(entries, row)
			}
			continue
		}
		// Calendar is read-only and uses metadata already cached by save/check.
		// It does not issue one remote call per subscription during page viewing.
		data := snapshot.coverage[subscriptionDisplayKey(s)]
		coverage, e := a.subscriptionCoverageLoadedAt(ctx, s, data.known, nil, data, today)
		if e != nil {
			return nil, e
		}
		for _, ep := range data.known {
			airDate := data.dates[ep]
			if ep < subscriptionStartEpisode(s) {
				continue
			}
			if len(s.Episodes) > 0 && !containsInt(s.Episodes, ep) {
				continue
			}
			date, skip := effectiveEpisodeDate(s, ep, airDate)
			if date < from || date > to {
				continue
			}
			row := base
			row.Episode = ep
			row.Date = date
			row.OriginalDate = airDate
			row.Source = "metadata"
			row.Confirmed = true
			row.State = "upcoming"
			if s.EpisodeDates[ep] != "" || date != airDate {
				row.Source = "manual_episode"
			}
			if date <= today.Format("2006-01-02") {
				row.State = "available"
			}
			if containsInt(coverage.Library, ep) {
				row.State = "in_library"
			}
			if containsInt(coverage.Reserved, ep) {
				row.State = "reserved"
			}
			if containsInt(coverage.Excluded, ep) {
				row.State = "excluded"
			}
			if skip {
				row.State = "skipped"
			}
			entries = append(entries, row)
		}
		if s.Schedule.Source == "custom" {
			zone, _ := time.LoadLocation(s.Schedule.Timezone)
			if zone == nil {
				zone = loc
			}
			zoneStart, _ := parseDay(from, zone)
			zoneEnd, _ := parseDay(to, zone)
			for _, o := range customOccurrences(s.Schedule, zoneStart, zoneEnd) {
				row := base
				row.Date = o.Date
				row.OriginalDate = o.OriginalDate
				row.Source = "prediction"
				row.Confirmed = false
				row.Episode = 0
				row.State = "pending_confirmation"
				if o.State == "skipped" {
					row.State = "skipped"
				}
				entries = append(entries, row)
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Date != entries[j].Date {
			return entries[i].Date < entries[j].Date
		}
		if entries[i].SubscriptionID != entries[j].SubscriptionID {
			return entries[i].SubscriptionID < entries[j].SubscriptionID
		}
		return entries[i].Episode < entries[j].Episode
	})
	return map[string]any{"entries": entries, "from": from, "to": to, "timezone": "Asia/Shanghai"}, nil
}

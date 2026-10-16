package app

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sort"

	"light-media/internal/catalog"
	"light-media/internal/priorityrules"
)

type prioritySorter struct {
	ctx        context.Context
	media      *catalog.Media
	priorities []string
	orders     map[string]int
	names      map[string]string
	uploads    map[string]int64
}

func (a *App) preparePrioritySorter(ctx context.Context, in SearchRequest, config Settings) (prioritySorter, error) {
	s := prioritySorter{ctx: ctx, media: in.priorityMedia, orders: map[string]int{}, names: map[string]string{}, uploads: map[string]int64{}}
	if s.media == nil {
		return s, nil
	}
	if config.PriorityRules != nil {
		s.priorities = append([]string{}, config.PriorityRules.TorrentPriority...)
	}
	for _, site := range config.Sites {
		s.orders[site.ID] = site.Priority
		s.names[site.ID] = site.Name
		var snapshot siteSnapshot
		err := a.Store.Get(ctx, "site_stats_latest", site.ID, &snapshot)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return s, err
		}
		if snapshot.ConfigKey == a.managedSiteKey(site) && snapshot.Stats != nil && snapshot.Stats.Uploaded != nil {
			name := snapshot.Name
			if name == "" {
				name = site.Name
			}
			s.uploads[name] = *snapshot.Stats.Uploaded
		}
	}
	return s, nil
}
func (s prioritySorter) sort(a *App, candidates []Candidate, policy Policy) {
	if s.media == nil {
		sortSearchCandidates(candidates, policy)
		return
	}
	type keyed struct {
		candidate Candidate
		key       string
	}
	values := make([]keyed, len(candidates))
	for i, candidate := range candidates {
		name := candidate.Resource.Title
		if filepath.Ext(name) != ".mkv" {
			name += ".mkv"
		}
		ctx := s.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		parsed := a.parseMediaNameContext(ctx, name)
		seasons, episodes := 0, 0
		if s.media.MediaType == "tv" {
			seasons = 1
		}
		if parsed.Original != nil {
			if parsed.Original.Season != nil {
				seasons = 1
			}
			episodes = len(parsed.Original.Episodes)
		}
		values[i] = keyed{candidate, priorityrules.SortKey(priorityrules.SortInput{Title: s.media.Title, SiteOrder: s.orders[candidate.Resource.SiteID], Upload: s.uploads[s.names[candidate.Resource.SiteID]], Priority: candidate.Resource.PriorityOrder, Seeders: candidate.Resource.Seeders, Seasons: seasons, Episodes: episodes}, s.priorities)}
	}
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].candidate.Accepted != values[j].candidate.Accepted {
			return values[i].candidate.Accepted
		}
		return values[i].key > values[j].key
	})
	for i, value := range values {
		candidates[i] = value.candidate
	}
}

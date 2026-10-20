// Package acquisitionbatch ports MoviePilot DownloadChain.batch_download's
// ordered resource selection. Native host callbacks provide the real material
// and acknowledged downloader effects; a planned intent is not an Add success.
package acquisitionbatch

import "context"

type Candidate struct {
	ID, Kind, MediaKey, TitleYear, Fingerprint string
	Seasons, Episodes                          []int
	AllowedEpisodes                            *[]int
	ConfirmedFullCoverage                      bool
}
type Need struct {
	Season                     int
	Episodes                   []int
	TotalEpisode, StartEpisode int
	RequireCompleteCoverage    bool
}
type MediaNeed struct {
	MediaKey string
	Seasons  []Need
}
type Material struct {
	Content  []byte
	Magnet   bool
	Episodes []int
}
type Download struct {
	Candidate *Candidate
	Content   []byte
	Episodes  *[]int // nil means original None; an explicit set selects files.
}
type Dependencies struct {
	Fingerprint    func(*Candidate) string
	Sort           func(context.Context, []*Candidate) ([]*Candidate, error)
	ActiveFailures func(context.Context, []*Candidate) (map[string]bool, error)
	Stopped        func() bool
	Torrent        func(context.Context, *Candidate) (Material, error)
	Single         func(context.Context, Download) (bool, error)
	RecordFailure  func(context.Context, *Candidate, string) error
}
type Result struct {
	Downloaded []*Candidate
	Remaining  []MediaNeed
}

func subset(a, b []int) bool {
	for _, n := range a {
		if !contains(b, n) {
			return false
		}
	}
	return true
}
func contains(a []int, n int) bool {
	for _, v := range a {
		if v == n {
			return true
		}
	}
	return false
}
func intersection(a, b []int) []int {
	out := []int{}
	for _, n := range a {
		if contains(b, n) && !contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}
func difference(a, b []int) []int {
	out := []int{}
	for _, n := range a {
		if !contains(b, n) && !contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}
func required(n Need) []int {
	out := []int{}
	start := n.StartEpisode
	if start == 0 {
		start = 1
	}
	for ep := start; ep <= n.TotalEpisode; ep++ {
		out = append(out, ep)
	}
	return out
}
func allowed(n []int, c *Candidate) []int {
	if c.AllowedEpisodes == nil {
		return append([]int{}, n...)
	}
	return intersection(n, *c.AllowedEpisodes)
}
func setEpisodes(c *Candidate, episodes []int) {
	if len(episodes) == 0 {
		return
	}
	first, last := episodes[0], episodes[0]
	for _, n := range episodes[1:] {
		if n < first {
			first = n
		}
		if n > last {
			last = n
		}
	}
	c.Episodes = nil
	for n := first; n <= last; n++ {
		c.Episodes = append(c.Episodes, n)
	}
}

// Run retains source order for media/season dictionaries. Callers must pass
// immutable source IDs on candidate pointers, and must not count a queued write
// as Single=true. Callback errors propagate without inventing a False result.
func Run(ctx context.Context, candidates []*Candidate, needs []MediaNeed, d Dependencies) (Result, error) {
	out := Result{Downloaded: []*Candidate{}, Remaining: cloneNeeds(needs)}
	var e error
	if d.Sort != nil {
		candidates, e = d.Sort(ctx, candidates)
		if e != nil {
			return out, e
		}
	}
	failures := map[string]bool{}
	if d.ActiveFailures != nil {
		failures, e = d.ActiveFailures(ctx, candidates)
		if e != nil {
			return out, e
		}
		if failures == nil {
			failures = map[string]bool{}
		}
	}
	stopped := func() bool { return ctx.Err() != nil || d.Stopped != nil && d.Stopped() }
	downloaded := map[*Candidate]bool{}
	fingerprint := func(c *Candidate) string {
		if d.Fingerprint != nil {
			return d.Fingerprint(c)
		}
		return c.Fingerprint
	}
	cooldown := func(c *Candidate) bool { key := fingerprint(c); return key != "" && failures[key] }
	remember := func(c *Candidate) {
		if key := fingerprint(c); key != "" {
			failures[key] = true
		}
	}
	accept := func(c *Candidate) { out.Downloaded = append(out.Downloaded, c); downloaded[c] = true }
	single := func(c *Candidate, material []byte, episodes *[]int) (bool, error) {
		ok, e := d.Single(ctx, Download{c, material, episodes})
		if e == nil && !ok {
			remember(c)
		}
		return ok, e
	}
	torrent := func(c *Candidate) (Material, error) {
		m, e := d.Torrent(ctx, c)
		if e != nil {
			return m, e
		}
		if len(m.Content) == 0 && !m.Magnet {
			if d.RecordFailure != nil {
				if e = d.RecordFailure(ctx, c, "下载种子内容为空"); e != nil {
					return m, e
				}
			}
			remember(c)
		}
		return m, nil
	}
	movies := map[string]bool{}
	for _, c := range candidates {
		if stopped() {
			break
		}
		if c.Kind != "movie" || cooldown(c) || movies[c.TitleYear] {
			continue
		}
		ok, e := single(c, nil, nil)
		if e != nil {
			return out, e
		}
		if ok {
			accept(c)
			movies[c.TitleYear] = true
		}
	}
	// 1. Whole missing seasons: a one-season pack is inspected before download;
	// multiple-season packs retain the original uninspected source branch.
	for _, media := range cloneNeeds(out.Remaining) {
		whole := []int{}
		for _, n := range media.Seasons {
			if len(n.Episodes) == 0 {
				whole = append(whole, n.Season)
			}
		}
		for _, c := range candidates {
			if stopped() || len(whole) == 0 {
				break
			}
			seasons := c.Seasons
			if len(seasons) == 0 {
				seasons = []int{1}
			}
			if c.Kind != "tv" || c.MediaKey != media.MediaKey || len(c.Episodes) > 0 || downloaded[c] || cooldown(c) || !subset(seasons, whole) {
				continue
			}
			complete := false
			var content []byte
			if len(seasons) == 1 {
				m, e := torrent(c)
				if e != nil {
					return out, e
				}
				if m.Magnet || len(m.Content) == 0 || len(m.Episodes) == 0 {
					continue
				}
				setEpisodes(c, m.Episodes)
				n := findNeed(out.Remaining, media.MediaKey, seasons[0])
				if n == nil {
					continue
				}
				requiredEpisodes := []int{}
				if n.RequireCompleteCoverage {
					requiredEpisodes = required(*n)
				}
				complete = len(requiredEpisodes) > 0 && subset(requiredEpisodes, m.Episodes)
				if len(requiredEpisodes) > 0 && !complete || len(requiredEpisodes) == 0 && n.TotalEpisode != 0 && len(m.Episodes) < n.TotalEpisode {
					continue
				}
				content = m.Content
			}
			ok, e := single(c, content, nil)
			if e != nil {
				return out, e
			}
			if ok {
				c.ConfirmedFullCoverage = complete
				accept(c)
				whole = difference(whole, seasons)
				// __update_seasons retains only still-whole-missing seasons.
				// This also drops that media's partially missing seasons; preserve
				// the original behavior rather than silently repairing its policy.
				out.Remaining = retainSeasons(out.Remaining, media.MediaKey, whole)
			}
		}
	}
	// 2. Complete episode groups, requiring the resource itself to be a subset
	// of ordinary missing episodes, or a superset for full-coverage subscriptions.
	for _, media := range cloneNeeds(out.Remaining) {
		for _, n := range media.Seasons {
			need := n.Episodes
			if len(need) == 0 {
				need = required(n)
			}
			for _, c := range candidates {
				if stopped() {
					break
				}
				if c.Kind != "tv" || c.MediaKey != media.MediaKey || downloaded[c] || cooldown(c) || len(c.Seasons) != 1 || c.Seasons[0] != n.Season || len(c.Episodes) == 0 {
					continue
				}
				effective := allowed(need, c)
				if len(effective) == 0 {
					continue
				}
				match := subset(c.Episodes, effective)
				if n.RequireCompleteCoverage {
					requiredEpisodes := required(n)
					match = len(requiredEpisodes) > 0 && subset(requiredEpisodes, c.Episodes)
				}
				if !match {
					continue
				}
				ok, e := single(c, nil, nil)
				if e != nil {
					return out, e
				}
				if ok {
					if n.RequireCompleteCoverage {
						c.ConfirmedFullCoverage = true
					}
					accept(c)
					need = difference(need, c.Episodes)
					out.Remaining = updateEpisodes(out.Remaining, media.MediaKey, n.Season, need)
				}
			}
		}
	}
	// 3. Inspect remaining overlapping packs, and download only the actual
	// material files covering the effective missing episode intersection.
	for _, media := range cloneNeeds(out.Remaining) {
		for _, n := range media.Seasons {
			need := append([]int{}, n.Episodes...)
			if n.RequireCompleteCoverage || len(need) == 0 {
				continue
			}
			for _, c := range candidates {
				if stopped() || len(need) == 0 {
					break
				}
				if c.Kind != "tv" || c.MediaKey != media.MediaKey || downloaded[c] || cooldown(c) || len(c.Seasons) != 1 || c.Seasons[0] != n.Season {
					continue
				}
				effective := allowed(need, c)
				if len(effective) == 0 || len(c.Episodes) > 0 && len(intersection(c.Episodes, effective)) == 0 {
					continue
				}
				m, e := torrent(c)
				if e != nil {
					return out, e
				}
				if m.Magnet || len(m.Content) == 0 {
					continue
				}
				selected := intersection(m.Episodes, effective)
				if len(selected) == 0 {
					continue
				}
				ok, e := single(c, m.Content, &selected)
				if e != nil {
					return out, e
				}
				if ok {
					accept(c)
					setEpisodes(c, m.Episodes)
					need = difference(need, selected)
					out.Remaining = updateEpisodes(out.Remaining, media.MediaKey, n.Season, need)
				}
			}
		}
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	return out, nil
}

func cloneNeeds(values []MediaNeed) []MediaNeed {
	out := make([]MediaNeed, len(values))
	for i, m := range values {
		out[i].MediaKey = m.MediaKey
		for _, n := range m.Seasons {
			n.Episodes = append([]int{}, n.Episodes...)
			out[i].Seasons = append(out[i].Seasons, n)
		}
	}
	return out
}
func findNeed(values []MediaNeed, key string, season int) *Need {
	for _, m := range values {
		if m.MediaKey == key {
			for _, n := range m.Seasons {
				if n.Season == season {
					copy := n
					return &copy
				}
			}
		}
	}
	return nil
}
func removeSeasons(values []MediaNeed, key string, seasons []int) []MediaNeed {
	out := []MediaNeed{}
	for _, m := range values {
		if m.MediaKey == key {
			filtered := []Need{}
			for _, n := range m.Seasons {
				if !contains(seasons, n.Season) {
					filtered = append(filtered, n)
				}
			}
			m.Seasons = filtered
		}
		if len(m.Seasons) > 0 {
			out = append(out, m)
		}
	}
	return out
}
func retainSeasons(values []MediaNeed, key string, seasons []int) []MediaNeed {
	out := []MediaNeed{}
	for _, m := range values {
		if m.MediaKey == key {
			filtered := []Need{}
			for _, n := range m.Seasons {
				if contains(seasons, n.Season) {
					filtered = append(filtered, n)
				}
			}
			m.Seasons = filtered
		}
		if len(m.Seasons) > 0 {
			out = append(out, m)
		}
	}
	return out
}
func updateEpisodes(values []MediaNeed, key string, season int, episodes []int) []MediaNeed {
	if len(episodes) == 0 {
		return removeSeasons(values, key, []int{season})
	}
	for i := range values {
		if values[i].MediaKey == key {
			for j := range values[i].Seasons {
				if values[i].Seasons[j].Season == season {
					values[i].Seasons[j].Episodes = append([]int{}, episodes...)
				}
			}
		}
	}
	return values
}

// Package subscriptionquality holds SubscribeChain's quality facts. A queued
// intent is not an accepted download, and a quality target is not a library slot.
package subscriptionquality

import (
	"encoding/json"
	"sort"
	"strconv"
)

const (
	Movie = "电影"
	TV    = "电视剧"
)

// Note is the shared source note, not a second progress ledger. App adapters
// map it to Subscription.SourceNote. Username and State keep source values;
// policy defaults continue to come from subscriptiondefaults.Provider.Apply.
type State struct {
	Type            string          `json:"type"`
	Season          *int            `json:"season"`
	StartEpisode    int             `json:"start_episode"`
	TotalEpisode    int             `json:"total_episode"`
	BestVersion     bool            `json:"best_version"`
	BestVersionFull bool            `json:"best_version_full"`
	CurrentPriority *int            `json:"current_priority"`
	EpisodePriority map[string]int  `json:"episode_priority"`
	Note            []int           `json:"note"`
	LackEpisode     *int            `json:"lack_episode"`
	State           string          `json:"state"`
	Username        json.RawMessage `json:"username"`
}

type Resource struct {
	Type                  string `json:"type"`
	Seasons               []int  `json:"seasons"`
	Episodes              []int  `json:"episodes"`
	SelectedEpisodes      []int  `json:"selected_episodes"`
	Priority              int    `json:"priority"`
	ConfirmedFullCoverage bool   `json:"confirmed_full_coverage"`
}

type Selection struct {
	Accepted        bool  `json:"accepted"`
	AllowedEpisodes []int `json:"allowed_episodes"`
	PreferFullPack  bool  `json:"prefer_full_pack"`
}

func number(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
func ptr(n int) *int { return &n }
func cloneInts(xs []int) []int {
	if xs == nil {
		return nil
	}
	return append([]int{}, xs...)
}
func (s State) clone() State {
	s.Note = cloneInts(s.Note)
	s.Username = append(json.RawMessage(nil), s.Username...)
	if s.CurrentPriority != nil {
		s.CurrentPriority = ptr(*s.CurrentPriority)
	}
	if s.LackEpisode != nil {
		s.LackEpisode = ptr(*s.LackEpisode)
	}
	if s.EpisodePriority != nil {
		priorities := map[string]int{}
		for k, v := range s.EpisodePriority {
			priorities[k] = v
		}
		s.EpisodePriority = priorities
	}
	return s
}
func (s State) Full() bool { return s.Type == TV && s.BestVersion && s.BestVersionFull }
func (s State) Targets() []int {
	out := []int{}
	if s.Type != TV {
		return out
	}
	start := s.StartEpisode
	if start == 0 {
		start = 1
	}
	for episode := start; episode <= s.TotalEpisode; episode++ {
		out = append(out, episode)
	}
	return out
}
func (s State) Priorities() map[string]int {
	out := map[string]int{}
	for k, v := range s.EpisodePriority {
		if episode, e := strconv.Atoi(k); e == nil {
			out[strconv.Itoa(episode)] = v
		}
	}
	if len(out) == 0 && s.BestVersion && !s.Full() && s.Type == TV && s.CurrentPriority != nil {
		for _, episode := range s.Targets() {
			out[strconv.Itoa(episode)] = *s.CurrentPriority
		}
	}
	return out
}
func (s State) Downloaded() []int {
	set := map[int]bool{}
	for _, episode := range s.Note {
		set[episode] = true
	}
	for key, priority := range s.Priorities() {
		if episode, e := strconv.Atoi(key); e == nil && priority > 0 {
			set[episode] = true
		}
	}
	out := []int{}
	for _, episode := range s.Targets() {
		if set[episode] {
			out = append(out, episode)
		}
	}
	return out
}
func (s State) Pending() []int {
	priorities := s.Priorities()
	out := []int{}
	for _, episode := range s.Targets() {
		if priorities[strconv.Itoa(episode)] != 100 {
			out = append(out, episode)
		}
	}
	return out
}
func (s State) Current() int {
	if !s.BestVersion || s.Type != TV || s.Full() || len(s.Targets()) == 0 {
		return number(s.CurrentPriority)
	}
	priorities := s.Priorities()
	minimum := priorities[strconv.Itoa(s.Targets()[0])]
	for _, episode := range s.Targets()[1:] {
		minimum = min(minimum, priorities[strconv.Itoa(episode)])
	}
	return minimum
}
func (s State) Complete() bool {
	if !s.BestVersion {
		return false
	}
	if s.Type != TV || s.Full() || len(s.Targets()) == 0 {
		return number(s.CurrentPriority) == 100
	}
	return len(s.Pending()) == 0
}
func (s State) Interested(r Resource) []int {
	priorities := s.Priorities()
	selected := r.SelectedEpisodes
	if selected == nil {
		selected = r.Episodes
	}
	set := map[int]bool{}
	for _, episode := range selected {
		set[episode] = true
	}
	out := []int{}
	for _, episode := range s.Targets() {
		priority, exists := priorities[strconv.Itoa(episode)]
		if (len(selected) == 0 || set[episode]) && (!exists || r.Priority > priority) {
			out = append(out, episode)
		}
	}
	return out
}
func (s State) FullSeason(r Resource) bool {
	seasons := r.Seasons
	if len(seasons) == 0 {
		seasons = []int{1}
	}
	if len(seasons) != 1 || s.Season != nil && seasons[0] != *s.Season {
		return false
	}
	if len(r.Episodes) == 0 {
		return true // The real torrent still has to confirm full coverage.
	}
	set := map[int]bool{}
	for _, episode := range r.Episodes {
		set[episode] = true
	}
	if len(s.Targets()) == 0 {
		return false
	}
	for _, episode := range s.Targets() {
		if !set[episode] {
			return false
		}
	}
	return true
}
func (s State) Select(r Resource) Selection {
	if !s.BestVersion {
		return Selection{Accepted: true}
	}
	if s.Type != TV {
		return Selection{Accepted: number(s.CurrentPriority) == 0 || r.Priority > number(s.CurrentPriority)}
	}
	if s.Full() {
		return Selection{Accepted: s.FullSeason(r) && r.Priority > number(s.CurrentPriority)}
	}
	interested := s.Interested(r)
	prefer := len(s.Targets()) > 0
	priorities := s.Priorities()
	for _, episode := range s.Targets() {
		prefer = prefer && r.Priority > priorities[strconv.Itoa(episode)]
	}
	if len(interested) == 0 {
		return Selection{PreferFullPack: prefer}
	}
	return Selection{Accepted: len(interested) > 0, AllowedEpisodes: interested, PreferFullPack: prefer}
}
func (s State) Progress(ordinaryMissing int) State {
	s = s.clone()
	if s.Type == TV {
		lack := ordinaryMissing
		if s.BestVersion {
			lack = len(s.Targets()) - len(s.Downloaded())
		}
		s.LackEpisode = ptr(lack)
		if s.BestVersion && !s.Full() {
			s.CurrentPriority = ptr(s.Current())
		}
	}
	return s
}

// Accepted consumes only actual downloader-accepted resources; callers must
// persist that fact with the accepted intent/receipt in one SQL transaction.
func (s State) Accepted(resources []Resource, ordinaryMissing int) State {
	s = s.clone()
	if len(resources) == 0 {
		return s.Progress(ordinaryMissing)
	}
	priorities := s.Priorities()
	notes := map[int]bool{}
	for _, episode := range s.Note {
		if episode >= 0 {
			notes[episode] = true
		}
	}
	wrote := false
	moviePriority := resources[0].Priority
	for _, r := range resources {
		moviePriority = max(moviePriority, r.Priority)
		if r.Type != s.Type {
			continue
		}
		if s.Type == Movie {
			notes[1], wrote = true, true
			continue
		}
		episodes := r.SelectedEpisodes
		if len(episodes) == 0 {
			episodes = r.Episodes
		}
		if len(episodes) == 0 && r.ConfirmedFullCoverage {
			episodes = s.Targets()
		}
		selected := map[int]bool{}
		for _, episode := range episodes {
			selected[episode] = true
		}
		valid := false
		for _, episode := range s.Targets() {
			if !selected[episode] {
				continue
			}
			valid, wrote, notes[episode] = true, true, true
			key := strconv.Itoa(episode)
			if old, ok := priorities[key]; !ok || r.Priority > old {
				priorities[key] = r.Priority
			}
		}
		if valid && s.Full() && r.ConfirmedFullCoverage && r.Priority > number(s.CurrentPriority) {
			s.CurrentPriority = ptr(r.Priority)
		}
	}
	if wrote {
		s.Note = []int{}
		for episode := range notes {
			s.Note = append(s.Note, episode)
		}
		sort.Ints(s.Note)
		if s.Type == TV {
			s.EpisodePriority = priorities
		}
	}
	if s.Type == Movie {
		s.CurrentPriority = ptr(moviePriority)
	}
	return s.Progress(ordinaryMissing)
}

func (s State) ChangeTotal(total int) State {
	oldTotal := s.TotalEpisode
	priorities := s.Priorities()
	s = s.clone()
	s.TotalEpisode = total
	if !s.BestVersion || s.Type != TV {
		s.LackEpisode = ptr(max(number(s.LackEpisode)+(total-oldTotal), 0))
		return s
	}
	filtered := map[string]int{}
	for _, episode := range s.Targets() {
		if priority, ok := priorities[strconv.Itoa(episode)]; ok {
			filtered[strconv.Itoa(episode)] = priority
		}
	}
	s.EpisodePriority = filtered
	if s.Full() {
		if total > oldTotal {
			s.CurrentPriority = ptr(0)
		}
	} else {
		current := 0
		if targets := s.Targets(); len(targets) > 0 {
			current = filtered[strconv.Itoa(targets[0])]
			for _, episode := range targets[1:] {
				current = min(current, filtered[strconv.Itoa(episode)])
			}
		}
		s.CurrentPriority = ptr(current)
	}
	return s.Progress(0)
}

// ExpandTotal is the source's metadata-expansion path. It preserves the old
// per-episode facts before deriving progress for the enlarged target range.
func (s State) ExpandTotal(total int) State {
	oldTotal := s.TotalEpisode
	s = s.clone()
	s.TotalEpisode = total
	if s.BestVersion && s.Type == TV {
		basis := s
		if oldTotal != 0 {
			basis.TotalEpisode = oldTotal
		}
		s.EpisodePriority = basis.Priorities()
		if s.Full() {
			s.CurrentPriority = ptr(0)
		}
	}
	return s.Progress(0)
}

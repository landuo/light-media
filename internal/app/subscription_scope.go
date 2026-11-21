package app

import (
	"os"
	"strconv"
	"strings"
)

// A season subscription follows provider-confirmed episodes as the season grows.
// The start threshold is not a fabricated episode or a snapshot of today's list.
func normalizeSubscriptionScope(in *SubscriptionRequest, old Subscription, kind string) error {
	if kind == "movie" {
		if in.TargetScope != "" && in.TargetScope != "movie" || in.StartEpisode < 0 || in.StartEpisode > 1 {
			return bad("INVALID_SUBSCRIPTION_SCOPE", "电影订阅以整部影片为单位")
		}
		in.TargetScope, in.StartEpisode = "movie", 0
		return nil
	}
	if in.TargetScope == "" {
		in.TargetScope = "season"
		if len(in.Episodes) > 0 {
			in.TargetScope = "episodes" // Preserve existing explicit-range records.
		}
	}
	if in.TargetScope != "season" && in.TargetScope != "episodes" {
		return bad("INVALID_SUBSCRIPTION_SCOPE", "电视剧订阅请选择整季范围")
	}
	if in.TargetScope == "season" && len(in.Episodes) > 0 {
		return bad("INVALID_SUBSCRIPTION_SCOPE", "按季订阅只需设置开始集数，无需逐集选择")
	}
	if in.StartEpisode == 0 {
		in.StartEpisode = 1
		if old.ID != "" && old.Season == in.Season && old.StartEpisode > 0 {
			in.StartEpisode = old.StartEpisode
		}
	}
	if in.StartEpisode < 1 || in.StartEpisode > 9999 {
		return bad("INVALID_START_EPISODE", "开始集数须为1至9999")
	}
	return nil
}

func subscriptionStartEpisode(s Subscription) int {
	if s.StartEpisode > 0 && s.Media.MediaType == "tv" {
		return s.StartEpisode
	}
	return 1
}

func subscriptionTargetEpisodes(s Subscription, episodes []int) []int {
	if s.Media.MediaType != "tv" {
		return episodes
	}
	start := subscriptionStartEpisode(s)
	out := make([]int, 0, len(episodes))
	for _, n := range episodes {
		if n >= start && (s.TotalEpisodes == 0 || n <= s.TotalEpisodes) {
			out = append(out, n)
		}
	}
	return out
}

// subscriptionSearchQueries lists the names a subscription is searched under,
// in MoviePilot's order: its own keyword first when set, then the title, the
// original title and any alias.
func subscriptionSearchQueries(s Subscription) []string {
	if keyword := strings.TrimSpace(s.Keyword); keyword != "" {
		return []string{keyword}
	}
	out := []string{}
	seen := map[string]bool{}
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	add(s.Media.Title)
	add(s.Media.OriginalTitle)
	for _, alias := range s.Media.Aliases {
		add(alias)
	}
	// MoviePilot searches at most MAX_SEARCH_NAME_LIMIT names, default 3: title,
	// original title and the English one. Searching every alias multiplies the
	// site requests by the alias count and is what made one subscription check
	// hold the queue for minutes.
	if limit := subscriptionSearchNameLimit(); limit > 0 && len(out) > limit {
		out = out[:limit]
	}

	return out
}

// subscriptionSearchNameLimit mirrors MoviePilot's MAX_SEARCH_NAME_LIMIT setting,
// whose default is 3. Zero or a negative value leaves the list uncapped.
func subscriptionSearchNameLimit() int {
	if raw := strings.TrimSpace(os.Getenv("MAX_SEARCH_NAME_LIMIT")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			return n
		}
	}
	return 3
}

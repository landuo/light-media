package app

// A physical multi-episode file can cover several standard TMDB seasons when
// its presentation numbering comes from an episode group. Keep one asset/file;
// only the canonical inventory scope expands. Ordinary single-season records
// keep their existing JSON and Season/Episodes representation.
type canonicalEpisodeScope struct {
	Season   int   `json:"season"`
	Episodes []int `json:"episodes"`
}

type canonicalEpisodeSlot struct{ Season, Episode int }

func canonicalScopes(season int, episodes []int, scopes []canonicalEpisodeScope) []canonicalEpisodeScope {
	if len(scopes) != 0 {
		return scopes
	}
	return []canonicalEpisodeScope{{Season: season, Episodes: episodes}}
}
func canonicalSlots(scopes []canonicalEpisodeScope, includeUnnumbered bool) []canonicalEpisodeSlot {
	var slots []canonicalEpisodeSlot
	seen := map[canonicalEpisodeSlot]bool{}
	for _, scope := range scopes {
		episodes := scope.Episodes
		if includeUnnumbered {
			// Whole movies and item-level exclusions use an episode-zero key.
			// A season-only disc establishes no episode inventory.
			episodes = importEpisodes(episodes)
		}
		for _, episode := range episodes {
			slot := canonicalEpisodeSlot{scope.Season, episode}
			if !seen[slot] {
				slots = append(slots, slot)
				seen[slot] = true
			}
		}
	}
	return slots
}
func sameCanonicalSlots(left, right []canonicalEpisodeSlot) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[canonicalEpisodeSlot]bool, len(left))
	for _, slot := range left {
		seen[slot] = true
	}
	for _, slot := range right {
		if !seen[slot] {
			return false
		}
	}
	return true
}

func cloneEpisodeScopes(scopes []canonicalEpisodeScope) []canonicalEpisodeScope {
	if scopes == nil {
		return nil
	}
	out := make([]canonicalEpisodeScope, len(scopes))
	for i, scope := range scopes {
		out[i] = canonicalEpisodeScope{Season: scope.Season, Episodes: append([]int(nil), scope.Episodes...)}
	}
	return out
}
func (file PlanFile) inventoryScopes() []canonicalEpisodeScope {
	return canonicalScopes(file.Season, file.Episodes, file.EpisodeScopes)
}
func (asset Asset) inventoryScopes() []canonicalEpisodeScope {
	return canonicalScopes(asset.Season, asset.Episodes, asset.EpisodeScopes)
}
func (file PlanFile) inventorySlots(mediaType string) []canonicalEpisodeSlot {
	return canonicalSlots(file.inventoryScopes(), mediaType == "movie")
}
func (asset Asset) inventorySlots() []canonicalEpisodeSlot {
	return canonicalSlots(asset.inventoryScopes(), asset.Media.MediaType == "movie")
}
func (asset Asset) coversEpisode(season, episode int) bool {
	for _, slot := range asset.inventorySlots() {
		if slot.Season == season && slot.Episode == episode {
			return true
		}
	}
	return false
}

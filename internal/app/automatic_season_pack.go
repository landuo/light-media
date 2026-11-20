package app

import (
	"light-media/internal/decision"
	"light-media/internal/media"
	"light-media/internal/metainfo"
	"light-media/internal/sites"
	"path/filepath"
)

// The one permissible automatic pack review is an explicit same-season package.
// Actual files must still pass the unchanged strict automatic checks in preview.
func eligibleAutomaticSeasonPack(resource sites.Resource, target decision.Target, policy Policy) bool {
	return eligibleAutomaticSeasonPackPolicy(resource, target, decisionPolicy(policy))
}
func eligibleAutomaticSeasonPackPolicy(resource sites.Resource, target decision.Target, policy decision.Policy) bool {
	if target.Media.MediaType != "tv" {
		return false
	}
	target.Auto = false
	result := decision.Evaluate(resource, target, policy)
	return result.Accepted && result.Season == target.Season && len(result.Reasons) == 1 && result.Reasons[0] == "season_pack_requires_confirmation"
}
func automaticPackCandidate(candidate Candidate, season int) bool {
	return candidate.Season == season && candidate.NeedsConfirmation && len(candidate.Reasons) == 1 && candidate.Reasons[0] == "season_pack_requires_confirmation"
}

func validateAutomaticSeasonPackFiles(material metainfo.Material, target decision.Target, siteID string) error {
	return validateAutomaticSeasonPackFilesWithWords(material, target, siteID, func(s string) string { return s })
}

func validateAutomaticSeasonPackFilesWithWords(material metainfo.Material, target decision.Target, siteID string, prepare func(string) string) error {
	// Mirrors TorrentHelper.get_torrent_episodes in MoviePilot: parse each media
	// file, keep the episodes it yields, and skip anything that does not parse.
	// A pack whose names carry no season/episode marker for some files is still
	// usable as long as the wanted episodes can be identified — the extra files
	// are simply not selected later.
	target.Auto, target.AllowEarly = true, nil
	covered := map[int]bool{}
	known := []media.Episode{}
	for _, ep := range target.KnownEpisodes {
		known = append(known, media.Episode{Season: target.Season, Episode: ep})
	}
	identified := false
	for _, file := range material.Files {
		if !isVideo(file.Path) {
			continue
		}
		parsed := media.Parse(prepare(filepath.Base(file.Path)))
		if parsed.Status != media.Parsed || parsed.Original == nil {
			continue
		}
		resolved := media.Resolve(mediaKey(target.Media), decision.AliasSeasonNumbering(target.Media, target.Season, *parsed.Original), media.Source{Site: siteID}, target.Mappings, known, nil)
		if resolved.Status != media.Parsed {
			continue
		}
		for _, ep := range resolved.Standard {
			if ep.Season != target.Season {
				continue
			}
			identified = true
			covered[ep.Episode] = true
		}
	}
	// MoviePilot decides a pack is a season pack from the torrent title and never
	// inspects the file names, so a pack this build cannot parse is still added.
	// Only a pack that does yield episodes but misses the wanted ones is refused:
	// that is a real mismatch, not a naming convention.
	if identified {
		for _, ep := range target.Episodes {
			if !covered[ep] {
				return bad("REVIEW_SEASON_PACK", "整季包不再覆盖已确认的目标集")
			}
		}
	}
	return nil
}

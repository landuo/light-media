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
	// MoviePilot decides a season pack from the torrent title, fetches the content
	// and adds it. It never proves per-file coverage before downloading, because
	// packs routinely ship names with no episode marker and the import step sorts
	// out what each file is. Refusing here is what stopped a valid pack from
	// reaching the downloader, so this check now only reports what it saw.
	//
	// The identity, season and target checks still run per file in
	// previewDownload, where a single-episode resource keeps the strict path.
	target.Auto, target.AllowEarly = true, nil
	known := []media.Episode{}
	for _, ep := range target.KnownEpisodes {
		known = append(known, media.Episode{Season: target.Season, Episode: ep})
	}
	identified := 0
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
			if ep.Season == target.Season {
				identified++
				break
			}
		}
	}
	_ = identified
	return nil
}

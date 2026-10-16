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
	if target.Media.MediaType != "tv" {
		return false
	}
	target.Auto = false
	result := decision.Evaluate(resource, target, decisionPolicy(policy))
	return result.Accepted && result.Season == target.Season && len(result.Reasons) == 1 && result.Reasons[0] == "season_pack_requires_confirmation"
}
func automaticPackCandidate(candidate Candidate, season int) bool {
	return candidate.Season == season && candidate.NeedsConfirmation && len(candidate.Reasons) == 1 && candidate.Reasons[0] == "season_pack_requires_confirmation"
}

func validateAutomaticSeasonPackFiles(material metainfo.Material, target decision.Target, siteID string) error {
	return validateAutomaticSeasonPackFilesWithWords(material, target, siteID, func(s string) string { return s })
}

func validateAutomaticSeasonPackFilesWithWords(material metainfo.Material, target decision.Target, siteID string, prepare func(string) string) error {
	target.Auto, target.AllowEarly = true, nil
	covered := map[int]bool{}
	known := []media.Episode{}
	for _, ep := range target.KnownEpisodes {
		known = append(known, media.Episode{Season: target.Season, Episode: ep})
	}
	for _, file := range material.Files {
		if !isVideo(file.Path) {
			continue
		}
		resource := sites.Resource{Title: prepare(filepath.Base(file.Path)), Size: file.Size, Seeders: 0, SiteID: siteID}
		choice := decision.Evaluate(resource, target, decision.Policy{})
		if !choice.Accepted || choice.NeedsConfirmation {
			return bad("REVIEW_SEASON_PACK", "整季包实际文件的影视身份、已播日期或目标范围已变化，请人工核对")
		}
		parsed := media.Parse(prepare(filepath.Base(file.Path)))
		if parsed.Status != media.Parsed || parsed.Original == nil {
			return bad("REVIEW_SEASON_PACK", "整季包含未知文件，无法自动获取")
		}
		resolved := media.Resolve(mediaKey(target.Media), *parsed.Original, media.Source{Site: siteID}, target.Mappings, known, nil)
		if resolved.Status != media.Parsed {
			return bad("REVIEW_SEASON_PACK", "整季包季集映射无法确认")
		}
		for _, ep := range resolved.Standard {
			if ep.Season != target.Season || !containsInt(target.Episodes, ep.Episode) {
				return bad("REVIEW_SEASON_PACK", "整季包包含当前缺集范围之外的剧集")
			}
			if covered[ep.Episode] {
				return bad("REVIEW_SEASON_PACK", "整季包同一集包含多个视频版本")
			}
			covered[ep.Episode] = true
		}
	}
	for _, ep := range target.Episodes {
		if !covered[ep] {
			return bad("REVIEW_SEASON_PACK", "整季包不再覆盖已确认的目标集")
		}
	}
	if len(covered) == 0 {
		return bad("REVIEW_SEASON_PACK", "整季包没有可确认的剧集文件")
	}
	return nil
}

package app

import (
	"context"
	"slices"
)

type originalChatSeasonBatch struct {
	number    int
	remaining []int
	whole     bool
}

func originalChatBatchResourceKey(candidate Candidate) [2]string {
	return [2]string{candidate.Resource.SiteID, candidate.Resource.ID}
}

func originalChatBatchStop(ctx context.Context, err error) error {
	if cause := ctx.Err(); cause != nil {
		return cause
	}
	switch pluginErrorCodeInternal(err) {
	case "CHAT_DOWNLOAD_UNCONFIRMED", "CHAT_DOWNLOAD_CANCELLED", "PLUGIN_CAPABILITY_DENIED", "AUTOMATION_PAUSED":
		return err
	}
	return nil
}

func originalChatBatchIncomplete() error {
	return bad("CHAT_DOWNLOAD_INCOMPLETE", "未取得覆盖所选影视范围的全部资源")
}

func originalChatBatchPack(candidate Candidate) bool {
	return len(candidate.Episodes) == 0 || automaticPackCandidate(candidate, candidate.Season)
}

func originalChatBatchAllowed(season *originalChatSeasonBatch, candidate Candidate) []int {
	return slices.DeleteFunc(slices.Clone(season.remaining), func(episode int) bool {
		return !originalSelectionAllows(candidate, episode)
	})
}

// Follow the source order: inspect whole missing seasons, acquire complete
// listed episode groups, then split a torrent for only its actual missing files.
func (a *App) originalChatAutoDownloadTV(ctx context.Context, request SearchRequest, candidates []Candidate, directory originalChatMediaDirectory) ([]Candidate, error) {
	candidates = slices.Clone(candidates)
	var seasons []*originalChatSeasonBatch
	byNumber := map[int]*originalChatSeasonBatch{}
	for _, season := range request.chatMediaSeasons {
		if len(season.needed) != 0 {
			batch := &originalChatSeasonBatch{number: season.number, remaining: slices.Clone(season.needed), whole: season.whole}
			seasons = append(seasons, batch)
			byNumber[season.number] = batch
		}
	}
	var accepted []Candidate
	attempted := map[[2]string]bool{}
	measuredPack := make([]bool, len(candidates))
	remainingSeasons := len(seasons)
	accept := func(season *originalChatSeasonBatch, candidate Candidate, eligible []int) error {
		if candidate.Season != season.number || len(candidate.Episodes) == 0 || slices.ContainsFunc(candidate.Episodes, func(episode int) bool { return !slices.Contains(eligible, episode) }) {
			return bad("CHAT_DOWNLOAD_UNCONFIRMED", "无法核对下载器实际接收的季集范围")
		}
		accepted = append(accepted, candidate)
		season.whole = false
		season.remaining = slices.DeleteFunc(season.remaining, func(episode int) bool { return slices.Contains(candidate.Episodes, episode) })
		if len(season.remaining) == 0 {
			remainingSeasons--
		}
		return nil
	}
	for i, candidate := range candidates {
		key := originalChatBatchResourceKey(candidate)
		if !candidate.Accepted || attempted[key] {
			continue
		}
		if len(candidate.chatSeasonPack) > 1 {
			if slices.ContainsFunc(candidate.chatSeasonPack, func(number int) bool {
				season := byNumber[number]
				return season == nil || !season.whole || len(season.remaining) == 0
			}) {
				continue
			}
			attempted[key] = true
			err := a.originalChatDownloadMedia(ctx, request, candidate, directory)
			if stop := originalChatBatchStop(ctx, err); stop != nil {
				return accepted, stop
			}
			if err == nil {
				accepted = append(accepted, candidate)
				for _, number := range candidate.chatSeasonPack {
					season := byNumber[number]
					season.remaining, season.whole = nil, false
					remainingSeasons--
				}
				if remainingSeasons == 0 {
					return accepted, nil
				}
			}
			continue
		}
		season := byNumber[candidate.Season]
		if season == nil || !season.whole || !originalChatBatchPack(candidate) {
			continue
		}
		eligible := originalChatBatchAllowed(season, candidate)
		if len(eligible) == 0 || len(eligible) != len(season.remaining) {
			continue
		}
		result, err := a.originalChatDownloadEpisodeBatch(ctx, request, candidate, directory, eligible, "whole_pack")
		if stop := originalChatBatchStop(ctx, err); stop != nil {
			return accepted, stop
		}
		if pluginErrorCodeInternal(err) == "CHAT_DOWNLOAD_INCOMPLETE_PACK" {
			// Source enriches the pack's Meta from its files before it checks
			// completeness. A safely measured subset is usable in the next pass.
			candidates[i], measuredPack[i] = result, true
			continue
		}
		attempted[key] = true
		if err == nil {
			if err = accept(season, result, eligible); err != nil || remainingSeasons == 0 {
				return accepted, err
			}
		}
	}
	for _, season := range seasons {
		for i, candidate := range candidates {
			if len(season.remaining) == 0 {
				break
			}
			key := originalChatBatchResourceKey(candidate)
			if !candidate.Accepted || attempted[key] || len(candidate.chatSeasonPack) > 1 || candidate.Season != season.number ||
				len(candidate.Episodes) == 0 || originalChatBatchPack(candidate) && !measuredPack[i] {
				continue
			}
			eligible := originalChatBatchAllowed(season, candidate)
			if slices.ContainsFunc(candidate.Episodes, func(episode int) bool { return !slices.Contains(eligible, episode) }) {
				continue
			}
			attempted[key] = true
			err := a.originalChatDownloadMedia(ctx, request, candidate, directory)
			if stop := originalChatBatchStop(ctx, err); stop != nil {
				return accepted, stop
			}
			if err == nil {
				if err = accept(season, candidate, eligible); err != nil || remainingSeasons == 0 {
					return accepted, err
				}
			}
		}
	}
	for _, season := range seasons {
		if season.whole {
			continue // No explicit missing-episode list yet, as in the source.
		}
		for i, candidate := range candidates {
			if len(season.remaining) == 0 {
				break
			}
			key := originalChatBatchResourceKey(candidate)
			if !candidate.Accepted || attempted[key] || len(candidate.chatSeasonPack) > 1 || candidate.Season != season.number {
				continue
			}
			eligible := originalChatBatchAllowed(season, candidate)
			pack := originalChatBatchPack(candidate) && !measuredPack[i]
			if len(eligible) == 0 || !pack && !slices.ContainsFunc(candidate.Episodes, func(episode int) bool { return slices.Contains(eligible, episode) }) {
				continue
			}
			attempted[key] = true
			result, err := a.originalChatDownloadEpisodeBatch(ctx, request, candidate, directory, eligible, "split")
			if stop := originalChatBatchStop(ctx, err); stop != nil {
				return accepted, stop
			}
			if err == nil {
				if err = accept(season, result, eligible); err != nil || remainingSeasons == 0 {
					return accepted, err
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return accepted, err
	}
	return accepted, originalChatBatchIncomplete()
}

package app

import (
	"context"
	"errors"
	"strconv"

	"light-media/internal/catalog"
)

// From-history restores known identity fields before file collection, as MP's
// manual endpoint does. Unknown history fields leave the current form value.
func (in *manualOrganizeRequest) inheritHistoryIdentity(history transferHistoryItem) {
	if history.Media.MediaType != "" {
		in.MediaType = history.Media.MediaType
	}
	if providerNativeMedia(history.Media) || history.Media.ID > 0 {
		in.MediaSource, in.MediaID = "", ""
	}
	if providerNativeMedia(history.Media) {
		// A source ID is not a TMDB ID. Keep this history's complete source
		// identity; ordinary web retries resolve that exact ID through the same
		// recognition chain before preview/submission.
		in.TMDBID, in.providerIdentity = 0, history.Media
		in.EpisodeGroup = ""
	} else if history.Media.ID > 0 {
		in.TMDBID = history.Media.ID
		in.providerIdentity = catalog.Media{}
	}
	if history.Media.MediaType == "tv" || history.Season > 0 {
		season := history.Season
		in.Season = &season
	}
	if history.EpisodeGroup != "" {
		in.EpisodeGroup = history.EpisodeGroup
	}
	if len(history.Episodes) > 0 {
		in.EpisodeDetail = strconv.Itoa(history.Episodes[0])
		if len(history.Episodes) > 1 {
			in.EpisodeDetail += "," + strconv.Itoa(history.Episodes[len(history.Episodes)-1])
		}
	}
}

// MoviePilot submits selected history rows independently. An unavailable row
// must not discard successful peers or prevent later selections from running.
// The single-row path remains the authority for recognition and ownership.
func (a *App) manualOrganizeHistoryBatch(ctx context.Context, in manualOrganizeRequest) (manualOrganizeOutput, error) {
	out := manualOrganizeOutput{Items: []manualOrganizeItem{}}
	histories, err := a.transferHistorySelections(ctx, in.HistoryIDs)
	if err != nil {
		return out, err
	}
	seen := map[string]bool{}
	for i, history := range histories {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if history.ID == "" {
			out.Items = append(out.Items, manualOrganizeItem{Status: "failed", ErrorCode: "HISTORY_NOT_FOUND", Error: "整理记录不存在，ID：" + in.HistoryIDs[i]})
			out.Failed++
			continue
		}
		if seen[history.ResourceSource] {
			continue
		}
		seen[history.ResourceSource] = true
		request := in
		request.HistoryIDs = []string{history.ID}
		result, err := a.manualOrganize(ctx, request)
		out.Items = append(out.Items, result.Items...)
		out.Submitted += result.Submitted
		out.Failed += result.Failed
		if err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			failure := manualOrganizeItem{Source: history.organizeSource(), Status: "failed", Error: a.clean(err.Error())}
			var api *APIError
			if errors.As(err, &api) {
				failure.ErrorCode = api.Code
			}
			out.Items = append(out.Items, failure)
			out.Failed++
		}
	}
	out.Total = len(out.Items)
	return out, nil
}

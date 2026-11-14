package app

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"slices"
	"strconv"

	"light-media/internal/decision"
	"light-media/internal/download"
	"light-media/internal/media"
	"light-media/internal/metainfo"
	"light-media/internal/plugins"
	"light-media/internal/sites"
)

const completedUnscopedKind = "download_unscoped_completion"

type completedUnscopedScope struct {
	InputSHA string
	Files    []downloadScopedFile
}

func unscopedCompletionReview() error {
	return bad("REVIEW_EPISODES", "此资源按整包下载，缺少可核对的文件身份或来源季集，请核对后再整理")
}

// TransferChain recognizes the exact saved download identity again at
// completion. Keep its newer coverage separate from the accepted Add.
func (a *App) recoverCompletedUnscopedMedia(ctx context.Context, in Intent) (json.RawMessage, []media.Episode, error) {
	frozen, err := a.completedProviderDownloadMedia(ctx, in)
	if err != nil {
		return nil, nil, err
	}
	if len(frozen) == 0 {
		return nil, nil, unscopedCompletionReview()
	}
	group := sourceText(originalChatLatestMediaFields(frozen).Get("episode_group"))
	// Bypass the native lookup warmed during selection. The module sees the
	// original default cache argument, just as it does for transfer recognition.
	ctx = context.WithValue(ctx, originalHostMediaFreshContextKey{}, true)
	fresh, err := a.originalHostMediaRecognize(ctx, plugins.MediaRecognizeRequest{
		Type: in.Media.MediaType, Source: in.Media.ProviderSource, MediaID: in.Media.ProviderID, EpisodeGroup: group,
	})
	if err != nil {
		return nil, nil, err
	}
	provider := originalChatMediaProvider{Source: in.Media.ProviderSource, ID: in.Media.ProviderID}
	if _, err = originalChatProviderSearchInformation(fresh, &provider, in.Media.MediaType); err != nil {
		return nil, nil, unscopedCompletionReview()
	}
	fresh, err = a.originalTransferObtainImages(ctx, fresh)
	if err != nil {
		return nil, nil, err
	}
	fields := originalChatLatestMediaFields(fresh)
	var known []media.Episode
	for key := range sourceMap(fields.Get("seasons")) {
		season, err := strconv.Atoi(key)
		if err != nil || season < 1 || season > 1000 || strconv.Itoa(season) != key {
			continue
		}
		for _, episode := range originalChatProviderEpisodes(fields, season) {
			known = append(known, media.Episode{Season: season, Episode: episode})
		}
	}
	if len(known) == 0 {
		return nil, nil, unscopedCompletionReview()
	}
	return fresh, known, nil
}

func (a *App) completedUnscopedFiles(ctx context.Context, in Intent, raw json.RawMessage, known []media.Episode, files []download.File) (map[string]downloadScopedFile, error) {
	target, err := providerDownloadTarget(SearchRequest{MediaType: in.Media.MediaType, chatUnscoped: true}, in.Media, raw, false)
	if err != nil {
		return nil, err
	}
	// Verify the title independently of season, since a single package can
	// contain multiple source seasons and saved mappings can move an episode.
	target.Season = 0
	var archived map[string]int64
	if !in.ChatMagnet {
		data, err := a.resolveDownloadMaterial(ctx, in.MaterialRef, materialPurpose, false)
		if err != nil {
			return nil, err
		}
		material, err := metainfo.ParseMaterial(data)
		if err != nil || !sameHash(material.Hash, in.Hash) {
			return nil, unscopedCompletionReview()
		}
		archived = map[string]int64{}
		for _, file := range material.Files {
			if isVideo(file.Path) {
				archived[file.Path] = file.Size
			}
		}
	}
	numbering := map[string]downloadScopedFile{}
	used := map[media.Episode]bool{}
	var size int64
	fields := originalChatLatestMediaFields(raw)
	for _, file := range files {
		if file.Size < 0 || file.Size > math.MaxInt64-size {
			return nil, unscopedCompletionReview()
		}
		size += file.Size
		if !isVideo(file.Path) {
			continue
		}
		if file.Size == 0 || archived != nil && archived[file.Path] != file.Size {
			return nil, unscopedCompletionReview()
		}
		name := filepath.Base(file.Path)
		parsed := a.parseMediaNameContext(ctx, name)
		if parsed.Status != media.Parsed || parsed.Original == nil {
			return nil, unscopedCompletionReview()
		}
		resolved := media.Resolve(mediaKey(in.Media), decision.AliasSeasonNumbering(in.Media, in.Season, *parsed.Original), media.Source{Site: in.SiteID}, in.Request.Mappings, known, nil)
		if resolved.Status != media.Parsed || len(resolved.Standard) == 0 {
			return nil, unscopedCompletionReview()
		}
		scoped := downloadScopedFile{Path: file.Path, Size: file.Size, Season: resolved.Standard[0].Season}
		for _, slot := range resolved.Standard {
			if slot.Season != scoped.Season || used[slot] {
				return nil, unscopedCompletionReview()
			}
			used[slot] = true
			scoped.Episodes = append(scoped.Episodes, slot.Episode)
		}
		slices.Sort(scoped.Episodes)
		if scoped.Episodes[len(scoped.Episodes)-1]-scoped.Episodes[0]+1 != len(scoped.Episodes) {
			return nil, unscopedCompletionReview()
		}
		year, _ := originalResourceInteger(sourceMap(fields.Get("season_years"))[strconv.Itoa(scoped.Season)])
		target.SeasonYear = int(year)
		choice := a.evaluateResourceContext(ctx, sites.Resource{SiteID: in.SiteID, Title: name, Size: file.Size}, target, decision.Policy{})
		if !choice.Accepted || choice.NeedsConfirmation {
			return nil, unscopedCompletionReview()
		}
		numbering[file.Path] = scoped
	}
	if len(numbering) == 0 || archived != nil && len(numbering) != len(archived) {
		return nil, unscopedCompletionReview()
	}
	if err := actualSizePolicy(size, in.Request.Policy); err != nil {
		return nil, err
	}
	return numbering, nil
}

func (a *App) saveCompletedUnscopedPlan(ctx context.Context, in *Intent, plan ImportPlan, files []downloadScopedFile) error {
	scope := completedUnscopedScope{InputSHA: sourceEpisodeInputSHA(*in), Files: files}
	if !scope.matches(plan) || len(mustCleanSeedJSON(scope)) > pluginDataValueLimit-(8<<10) {
		return unscopedCompletionReview()
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := importIntentTx(ctx, tx, plan)
	if err != nil {
		return err
	}
	if current.State != "downloaded" || current.ImportPlanID != "" || scope.InputSHA != sourceEpisodeInputSHA(current) {
		return unscopedCompletionReview()
	}
	if err = a.writeQualitySealedTx(ctx, tx, completedUnscopedKind, plan.ID, scope); err != nil {
		return err
	}
	current.ImportPlanID = plan.ID
	if err = txPut(ctx, tx, "intent", current.ID, current); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	*in = current
	return nil
}

func (s completedUnscopedScope) matches(plan ImportPlan) bool {
	if len(s.Files) == 0 || len(s.Files) != len(plan.Files) {
		return false
	}
	for i, f := range plan.Files {
		scoped := s.Files[i]
		if f.Source != scoped.Path || f.Size != scoped.Size || f.Season != scoped.Season || !slices.Equal(f.Episodes, scoped.Episodes) {
			return false
		}
	}
	return true
}

func (a *App) readCompletedUnscopedPlan(ctx context.Context, q qualityReader, in Intent, plan ImportPlan) (*completedUnscopedScope, error) {
	var reviewed bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM records WHERE kind='download_review' AND id=?
		AND json_extract(payload,'$.intent_id')=? AND json_extract(payload,'$.plan.id')=?
		AND json_extract(payload,'$.confirmed')=1)`, plan.ID, in.ID, plan.ID).Scan(&reviewed); err != nil {
		return nil, err
	}
	if reviewed && in.ImportPlanID == plan.ID {
		// Explicit linked-download correction has its own confirmation and
		// can use a different identity from the immutable Add.
		return nil, nil
	}
	var scope completedUnscopedScope
	if err := a.readQualitySealed(ctx, q, completedUnscopedKind, plan.ID, &scope); err != nil ||
		in.ImportPlanID != plan.ID || scope.InputSHA != sourceEpisodeInputSHA(in) || !scope.matches(plan) {
		return nil, unscopedCompletionReview()
	}
	return &scope, nil
}

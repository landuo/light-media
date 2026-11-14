package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"

	"light-media/internal/decision"
	"light-media/internal/media"
	"light-media/internal/metainfo"
	"light-media/internal/sites"
	"light-media/internal/subscriptionquality"
)

func qualityEvaluationTarget(target decision.Target) decision.Target {
	if target.Media.MediaType == "tv" {
		// Selection is separate from identity: every package file must belong
		// to known episodes in this season, while only the selected aired subset
		// receives wanted priority and is later eligible for import.
		target.Episodes = qualityCopyNote(target.KnownEpisodes)
		target.AiredEpisodes = qualityCopyNote(target.KnownEpisodes)
		target.AllowEarly = nil
	}
	return target
}
func (a *App) validateQualityOrAutomaticFiles(ctx context.Context, p *qualityDownloadSnapshot, material metainfo.Material, target decision.Target, site string) error {
	if p == nil || !p.BestVersion {
		return a.validateAutomaticSeasonPackFilesContext(ctx, material, target, site)
	}
	identity := qualityEvaluationTarget(target)
	known := []media.Episode{}
	for _, ep := range identity.KnownEpisodes {
		known = append(known, media.Episode{Season: target.Season, Episode: ep})
	}
	covered := map[int]bool{}
	for _, file := range material.Files {
		if !isVideo(file.Path) {
			continue
		}
		choice := a.evaluateResourceContext(ctx, sites.Resource{Title: filepath.Base(file.Path), Size: file.Size, SiteID: site}, identity, decision.Policy{})
		if !choice.Accepted || choice.NeedsConfirmation {
			return bad("QUALITY_FILE_IDENTITY_CHANGED", "实际洗版文件的影视或季集身份无法确认")
		}
		parsed := a.parseMediaNameContext(ctx, filepath.Base(file.Path))
		if parsed.Original == nil {
			return bad("QUALITY_FILE_IDENTITY_CHANGED", "实际洗版文件缺少可确认集号")
		}
		resolved := media.Resolve(mediaKey(target.Media), decision.AliasSeasonNumbering(target.Media, target.Season, *parsed.Original), media.Source{Site: site}, target.Mappings, known, nil)
		if resolved.Status != media.Parsed {
			return bad("QUALITY_FILE_IDENTITY_CHANGED", "洗版文件季集映射无法确认")
		}
		for _, ep := range resolved.Standard {
			if ep.Season != target.Season {
				return bad("QUALITY_FILE_IDENTITY_CHANGED", "洗版文件包含其他季")
			}
			if covered[ep.Episode] {
				return bad("QUALITY_FILE_IDENTITY_CHANGED", "洗版文件同集包含多个视频版本")
			}
			covered[ep.Episode] = true
		}
	}
	for _, ep := range p.Episodes {
		if !covered[ep] {
			return bad("QUALITY_FILE_COVERAGE_CHANGED", "洗版文件不再覆盖已确认选集")
		}
	}
	return nil
}

type subscriptionQualityAccepted struct {
	Version                                              int
	Intent, Hash, Subscription, SnapshotSHA, Disposition string
	Revision                                             int
	Resource                                             subscriptionquality.Resource
}

// The accepted fact and runtime progress commit with the actual intent/event.
// A late subscription edit keeps the old accepted fact, without overwriting its
// new rule revision. It never retries an uncertain external Add/selection.
func (a *App) acceptSubscriptionQualityTx(ctx context.Context, tx *sql.Tx, in Intent) error {
	p, err := a.qualitySnapshot(ctx, tx, "intent", in.ID, in.Hash)
	if err != nil || p == nil {
		return err
	}
	if p.MediaKey != mediaKey(in.Media) || p.Season != in.Season || !reflect.DeepEqual(p.Episodes, in.Episodes) || p.SubscriptionID != in.SubscriptionID || p.Revision != in.SubscriptionRevision {
		return bad("QUALITY_SNAPSHOT_CHANGED", "实际接收的下载与洗版快照不符")
	}
	fact := subscriptionQualityAccepted{Version: 1, Intent: in.ID, Hash: in.Hash, Subscription: p.SubscriptionID, Revision: p.Revision, SnapshotSHA: tokenHash(string(mustCleanSeedJSON(p))), Resource: p.Resource, Disposition: "accepted"}
	post, hasPost := ctx.Value(sourcePostAddKey{}).(sourcePostAddReceipt)
	if in.SourcePostAddVersion != 1 || !hasPost || !sourcePostAddReceiptValid(post, in) || !post.SourceReturned || post.SourceError != "" {
		return bad("QUALITY_ACCEPTANCE_UNCONFIRMED", "洗版进度缺少实际下载接受收据")
	}
	if post.Duplicate {
		fact.Disposition = "accepted_duplicate"
	}
	if post.Mode == "existing_hash" {
		fact.Disposition = "existing_hash"
	}
	var existing []byte
	err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='subscription_quality_accepted' AND id=?`, in.ID).Scan(&existing)
	if err == nil {
		raw, e := a.pluginStore.unseal("subscription_quality_accepted", "system", in.ID, existing)
		if e != nil {
			return e
		}
		var prior subscriptionQualityAccepted
		if cleanupJournalStrict(raw, &prior) != nil || prior.Intent != fact.Intent || prior.Hash != fact.Hash || prior.SnapshotSHA != fact.SnapshotSHA || prior.Revision != fact.Revision || prior.Subscription != fact.Subscription {
			return bad("QUALITY_ACCEPTANCE_CHANGED", "原洗版接受事实无法核对")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var current Subscription
	err = qualityRecord(ctx, tx, "subscription", p.SubscriptionID, &current)
	if errors.Is(err, sql.ErrNoRows) {
		fact.Disposition = "subscription_removed"
	} else if err != nil {
		return err
	} else if current.Revision != p.Revision || mediaKey(current.Media) != p.MediaKey || current.Season != p.Season || current.SourceContextSHA != p.SourceContextSHA || current.BestVersion != p.BestVersion {
		fact.Disposition = "subscription_changed"
	} else if post.Mode != "existing_hash" {
		state := subscriptionQualityState(current)
		oldTotal := state.TotalEpisode
		// Native ordinary subscriptions can follow an evolving season (total0).
		// Record real accepted episode facts without freezing that open scope.
		if state.Type == subscriptionquality.TV && state.TotalEpisode == 0 {
			for _, ep := range p.Episodes {
				state.TotalEpisode = max(state.TotalEpisode, ep)
			}
		}
		missing := 0
		present := map[int]bool{}
		for _, ep := range state.Targets() {
			var count int
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_slots WHERE media_key=? AND season=? AND episode=?`, p.MediaKey, p.Season, ep).Scan(&count); err != nil {
				return err
			}
			if count == 0 {
				missing++
			}
			present[ep] = count > 0
		}
		state = state.Accepted([]subscriptionquality.Resource{p.Resource}, missing)
		if !state.BestVersion && state.Type == subscriptionquality.TV {
			missing = 0
			for _, ep := range state.Targets() {
				if !present[ep] && !slices.Contains(state.Note, ep) {
					missing++
				}
			}
			state = state.Progress(missing)
		}
		if !state.BestVersion && oldTotal == 0 {
			state.TotalEpisode = 0
		}
		setSubscriptionQualityState(&current, state)
		if current.State == "active" && current.SourceState == "N" {
			current.SourceState = "R"
		}
		if err = txPut(ctx, tx, "subscription", current.ID, current); err != nil {
			return err
		}
	}
	sealed, err := a.pluginStore.seal("subscription_quality_accepted", "system", in.ID, mustCleanSeedJSON(fact))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES('subscription_quality_accepted',?,?,?)`, in.ID, sealed, now())
	return err
}

func (a *App) guardAcceptedDownloadProgressTx(ctx context.Context, tx *sql.Tx, in Intent) error {
	if in.SourcePostAddVersion == 0 {
		return nil
	}
	if in.SourcePostAddVersion != 1 {
		return bad("REVIEW_POST_ADD_UNCONFIRMED", "下载后的原操作版本无法核对")
	}
	post, e := a.sourcePostAddReceipt(ctx, tx, in)
	if e != nil || !post.SourceReturned || post.SourceError != "" {
		return bad("REVIEW_POST_ADD_UNCONFIRMED", "实际下载接受收据缺失，未凭列表观察推进进度")
	}
	if len(in.SelectedEpisodes) > 0 {
		var selection sourceEpisodeSelectionReceipt
		if e = a.readQualitySealed(ctx, tx, "download_episode_selection", in.ID, &selection); e != nil || !sourceEpisodeReceiptValid(selection, in) {
			return bad("REVIEW_FILE_SELECTION_UNCONFIRMED", "实际文件选择收据缺失，未推进进度")
		}
	}
	var fact downloadAddedSnapshot
	if e = a.readQualitySealed(ctx, tx, "download_added", domainEventID("download.accepted", in.ID), &fact); e != nil || fact.DownloadID != in.ID || fact.DownloadKind != "intent" || !sameHash(fact.Hash, in.Hash) || fact.SiteID != in.SiteID || fact.MediaType != in.Media.MediaType || fact.TMDBID != in.Media.ID {
		return bad("REVIEW_POST_ADD_UNCONFIRMED", "实际下载接受事务尚未完成，未推进进度")
	}
	if providerNativeMedia(in.Media) {
		if key, err := fact.mediaIdentity(); err != nil || key != mediaKey(in.Media) {
			return bad("REVIEW_POST_ADD_UNCONFIRMED", "实际下载接受事务的媒体来源不符，未推进进度")
		}
	}
	return nil
}

func (a *App) qualityCompletedFileSelected(ctx context.Context, in Intent, path string, providerMedia json.RawMessage) (bool, error) {
	parsed := a.parseMediaNameContext(ctx, filepath.Base(path))
	if parsed.Status != media.Parsed || parsed.Original == nil {
		return false, bad("QUALITY_FILE_IDENTITY_CHANGED", "下载器文件缺少已确认的季集身份")
	}
	season, e := a.importSeason(ctx, in.Media, providerMedia, in.Season)
	if e != nil {
		return false, e
	}
	known := []media.Episode{}
	for _, ep := range season.Episodes {
		known = append(known, media.Episode{Season: in.Season, Episode: ep.EpisodeNumber})
	}
	resolved := media.Resolve(mediaKey(in.Media), decision.AliasSeasonNumbering(in.Media, in.Season, *parsed.Original), media.Source{Site: in.SiteID}, in.Request.Mappings, known, nil)
	if resolved.Status != media.Parsed {
		return false, bad("QUALITY_FILE_IDENTITY_CHANGED", "下载器文件的季集映射无法确认")
	}
	for _, ep := range resolved.Standard {
		if ep.Season != in.Season || !containsInt(in.Episodes, ep.Episode) {
			return false, nil
		}
	}
	return len(resolved.Standard) > 0, nil
}

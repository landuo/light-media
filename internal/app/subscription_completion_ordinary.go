package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"time"

	"light-media/internal/media"
	"light-media/internal/subscriptionquality"
)

// This certificate records the real missing-media resolution made before
// history/deletion. Later library changes cannot revoke a historical message.
// Its creation still rechecks live SQL ownership and file snapshots in the
// same transaction that removes the subscription.
type subscriptionCompletionOrdinaryBasis struct {
	Version                      int
	SubscriptionSHA, AcceptedSHA string
	MediaKey                     string
	Season                       int
	Targets, AcceptedEpisodes    []int
	Inventory                    []subscriptionCompletionInventory
	Existence                    *originalSubscriptionExistence `json:",omitempty"`
}
type subscriptionCompletionInventory struct {
	Asset        Asset
	Video        *managedFile         `json:",omitempty"`
	Registration *libraryIdentity     `json:",omitempty"`
	NFO          *importInferredMedia `json:",omitempty"`
	Numbering    *media.Numbering     `json:",omitempty"`
	Snapshot     importSnapshot
	RootPath     string
	Episodes     []int
}

func ordinaryCompletionTargets(s Subscription) []int {
	if s.Media.MediaType == "movie" {
		return []int{0}
	}
	return subscriptionQualityState(s).Targets()
}

func (a *App) refreshOrdinaryCompletionScope(ctx context.Context, s Subscription) (Subscription, error) {
	updated, _, err := a.refreshOrdinaryCompletionScopeResolved(ctx, s)
	return updated, err
}

func (a *App) refreshOrdinaryCompletionScopeResolved(ctx context.Context, s Subscription) (Subscription, *standardSubscriptionRecognition, error) {
	if s.BestVersion {
		return s, nil, nil
	}
	if s.Media.MediaType != "tv" {
		resolved, err := a.resolveOriginalSubscriptionExistence(ctx, s, nil)
		return s, resolved, err
	}
	resolved, e := a.resolveStandardSubscriptionRecognition(ctx, s, nil)
	if e != nil {
		return s, nil, e
	}
	state := subscriptionQualityState(s)
	if !s.ManualTotalEpisode {
		current, e := a.refreshStandardSubscriptionEpisodesResolved(ctx, s, 0, resolved)
		if e != nil {
			return s, nil, e
		}
		if current != 0 {
			state = state.ChangeTotal(a.resolveStandardSubscriptionTotalDecreaseResolved(ctx, s, current, resolved))
		}
	}
	resolved, e = a.resolveOriginalSubscriptionExistence(ctx, s, resolved)
	if e != nil {
		return s, nil, e
	}
	metadataUpdatedAt := ""
	if state.TotalEpisode != s.TotalEpisodes {
		// Fixed __prepare_total_episode_change_fields uses the old lack plus
		// the total delta, clamped at zero; it does not replace it with a new
		// inventory-derived count while applying the metadata change.
		metadataUpdatedAt = now()
	} else {
		confirmed, err := resolved.Existence.episodes(s)
		if err != nil {
			return s, nil, err
		}
		missing := 0
		for _, episode := range state.Targets() {
			var count int
			if resolved.Existence != nil {
				if slices.Contains(confirmed, episode) {
					count = 1
				}
			} else if e = a.Store.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_slots WHERE media_key=? AND season=? AND episode=?`, mediaKey(s.Media), s.Season, episode).Scan(&count); e != nil {
				return s, nil, e
			}
			if count == 0 && !slices.Contains(state.Note, episode) {
				missing++
			}
		}
		state = state.Progress(missing)
	}
	if !reflect.DeepEqual(state, subscriptionQualityState(s)) {
		if e = a.storeQualityProgress(ctx, s, state, metadataUpdatedAt); e != nil {
			return s, nil, e
		}
		if e = a.Store.Get(ctx, "subscription", s.ID, &s); e != nil {
			return s, nil, e
		}
	}
	return s, resolved, nil
}

func ordinaryAcceptedEpisodes(s Subscription, accepted []subscriptionCompletionAccepted) ([]int, error) {
	state := subscriptionQualityState(s)
	state.Note, state.EpisodePriority, state.CurrentPriority = nil, nil, nil
	resources := []subscriptionquality.Resource{}
	for _, input := range accepted {
		var fact subscriptionQualityAccepted
		if cleanupJournalStrict(input.Raw, &fact) != nil || fact.Intent != input.ID || fact.Subscription != s.ID || fact.Disposition != "accepted" && fact.Disposition != "accepted_duplicate" {
			return nil, completionEvidenceError()
		}
		resources = append(resources, fact.Resource)
	}
	// Reuse the original record_subscribe_download_facts model: both None and
	// empty selected lists fall back to actual metadata, then to authenticated
	// full coverage. The ordinary completion must not reinterpret that path.
	note := state.Accepted(resources, 0).Note
	if s.Media.MediaType == "movie" && slices.Contains(note, 1) {
		return []int{0}, nil
	}
	if note == nil {
		return []int{}, nil
	}
	return qualityCopyNote(note), nil
}

func (a *App) ordinaryCompletionBasis(ctx context.Context, q qualityReader, s Subscription, accepted []subscriptionCompletionAccepted) (*subscriptionCompletionOrdinaryBasis, bool, error) {
	return a.ordinaryCompletionBasisWithExistence(ctx, q, s, accepted, nil)
}

func (a *App) ordinaryCompletionBasisWithExistence(ctx context.Context, q qualityReader, s Subscription, accepted []subscriptionCompletionAccepted, existence *originalSubscriptionExistence) (*subscriptionCompletionOrdinaryBasis, bool, error) {
	if s.BestVersion || s.State != "active" || s.SourceState == "P" {
		return nil, false, nil
	}
	targets := ordinaryCompletionTargets(s)
	if len(targets) == 0 {
		return nil, false, nil
	}
	downloaded, e := ordinaryAcceptedEpisodes(s, accepted)
	if e != nil {
		return nil, false, e
	}
	basis := &subscriptionCompletionOrdinaryBasis{Version: 1, SubscriptionSHA: tokenHash(string(mustCleanSeedJSON(s))), AcceptedSHA: tokenHash(string(mustCleanSeedJSON(accepted))), MediaKey: mediaKey(s.Media), Season: s.Season, Targets: targets, AcceptedEpisodes: downloaded, Inventory: []subscriptionCompletionInventory{}}
	if existence != nil {
		confirmed, err := existence.episodes(s)
		if err != nil {
			return nil, false, err
		}
		basis.Version, basis.Existence = 2, existence
		for _, ep := range targets {
			if !slices.Contains(downloaded, ep) && !slices.Contains(confirmed, ep) {
				return nil, false, nil
			}
		}
		return basis, true, nil
	}
	seen := map[string]int{}
	for _, ep := range targets {
		if slices.Contains(downloaded, ep) {
			continue
		}
		rows, e := q.QueryContext(ctx, `SELECT asset_id FROM asset_slots WHERE media_key=? AND season=? AND episode=? ORDER BY asset_id`, mediaKey(s.Media), s.Season, ep)
		if e != nil {
			return nil, false, e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return nil, false, e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, false, e
		}
		if len(ids) == 0 {
			return nil, false, nil
		}
		id := ids[0]
		if index, ok := seen[id]; ok {
			basis.Inventory[index].Episodes = append(basis.Inventory[index].Episodes, ep)
			continue
		}
		var entry subscriptionCompletionInventory
		if e = qualityRecord(ctx, q, "asset", id, &entry.Asset); e != nil {
			return nil, false, e
		}
		asset := entry.Asset
		if (asset.State != "present" && asset.State != "external") || mediaKey(asset.Media) != mediaKey(s.Media) || asset.Season != s.Season || !slices.Contains(importEpisodes(asset.Episodes), ep) {
			return nil, false, bad("SUBSCRIPTION_INVENTORY_UNCONFIRMED", "订阅库存缺少对应的真实季集资产")
		}
		root, e := a.importRoot(asset.RootID)
		if e != nil || !within(root.Path, asset.Path) {
			return nil, false, bad("SUBSCRIPTION_INVENTORY_UNCONFIRMED", "订阅库存媒体库目录无法核对")
		}
		entry.RootPath = root.Path
		stamp, e := snapshotPath(asset.Path)
		if e != nil {
			return nil, false, bad("SUBSCRIPTION_INVENTORY_UNCONFIRMED", "订阅库存文件已变化，未完成订阅")
		}
		entry.Snapshot = stamp
		if asset.State == "present" {
			var video managedFile
			if e = qualityRecord(ctx, q, "managed_file", tokenHash(asset.Path), &video); e != nil || video.Kind != "video" || video.Path != asset.Path || video.AssetID != asset.ID || video.RootID != asset.RootID || video.MediaKey != mediaKey(s.Media) || video.Snapshot != stamp {
				return nil, false, bad("SUBSCRIPTION_INVENTORY_UNCONFIRMED", "订阅库存缺少真实文件受管归属")
			}
			entry.Video = &video
		} else if asset.IdentitySource == "manual" {
			var proof libraryIdentity
			if e = qualityRecord(ctx, q, "asset_identity", asset.ID, &proof); e != nil || proof.AssetID != asset.ID || proof.Path != asset.Path || proof.RootPath != root.Path || proof.RootID != root.ID || proof.Snapshot != stamp || proof.MediaType != s.Media.MediaType || proof.TMDBID != s.Media.ID || proof.Season != s.Season || !equalQualityEpisodes(importEpisodes(proof.Episodes), importEpisodes(asset.Episodes)) || proof.Source != "manual" || proof.ConfirmedAt != asset.IdentityConfirmedAt {
				return nil, false, bad("SUBSCRIPTION_INVENTORY_UNCONFIRMED", "人工登记的库存身份或文件指纹已变化")
			}
			entry.Registration = &proof
		} else {
			identity, numbering, _ := a.importNFOEvidence(asset.Path)
			if identity == nil || identity.ID != s.Media.ID || identity.MediaType != s.Media.MediaType || s.Media.MediaType == "tv" && (numbering == nil || numbering.Season == nil || *numbering.Season != s.Season || !equalQualityEpisodes(numbering.Episodes, asset.Episodes)) {
				return nil, false, bad("SUBSCRIPTION_INVENTORY_UNCONFIRMED", "外部库存的 NFO 影视身份或季集已变化")
			}
			entry.NFO, entry.Numbering = identity, numbering
		}
		entry.Episodes = []int{ep}
		seen[id] = len(basis.Inventory)
		basis.Inventory = append(basis.Inventory, entry)
	}
	return basis, true, nil
}

func (p *PluginStore) validateOrdinaryCompletionInput(ctx context.Context, q subscriptionCompletionReader, r subscriptionCompletionReceipt) error {
	var s Subscription
	if cleanupJournalStrict(r.Input.Source.Subscription, &s) != nil || s.ID != r.Identity.SubscriptionID || s.Revision != r.Identity.Revision || s.BestVersion || s.SourceState == "P" || s.State != "active" {
		return completionEvidenceError()
	}
	basis := r.Input.Ordinary
	if r.Input.Version != 2 || basis == nil || (basis.Version != 1 && basis.Version != 2) || basis.SubscriptionSHA != tokenHash(string(mustCleanSeedJSON(s))) || basis.AcceptedSHA != r.Identity.AcceptedSHA || basis.MediaKey != mediaKey(s.Media) || basis.Season != s.Season || !reflect.DeepEqual(basis.Targets, ordinaryCompletionTargets(s)) || len(basis.Targets) == 0 {
		return completionEvidenceError()
	}
	sealed, e := p.readSubscriptionCompletionSealed(ctx, q, "subscription_completion_ordinary", r.Identity.ID)
	if e != nil || !bytes.Equal(sealed, mustCleanSeedJSON(basis)) {
		return completionEvidenceError()
	}
	downloaded, e := ordinaryAcceptedEpisodes(s, r.Input.Accepted)
	if e != nil || !reflect.DeepEqual(downloaded, basis.AcceptedEpisodes) {
		return completionEvidenceError()
	}
	if basis.Version == 2 {
		if basis.Existence == nil || len(basis.Inventory) != 0 {
			return completionEvidenceError()
		}
		confirmed, err := basis.Existence.episodes(s)
		if err != nil {
			return completionEvidenceError()
		}
		for _, ep := range basis.Targets {
			if !slices.Contains(downloaded, ep) && !slices.Contains(confirmed, ep) {
				return completionEvidenceError()
			}
		}
		return nil
	}
	if basis.Existence != nil {
		return completionEvidenceError()
	}
	covered := append([]int{}, downloaded...)
	seen := map[string]bool{}
	for _, entry := range basis.Inventory {
		asset, video := entry.Asset, entry.Video
		if seen[asset.ID] || asset.ID == "" || (asset.State != "present" && asset.State != "external") || mediaKey(asset.Media) != basis.MediaKey || asset.Season != basis.Season || !within(entry.RootPath, asset.Path) || len(entry.Episodes) == 0 {
			return completionEvidenceError()
		}
		if asset.State == "present" {
			if video == nil || video.Kind != "video" || video.Path != asset.Path || video.AssetID != asset.ID || video.RootID != asset.RootID || video.MediaKey != basis.MediaKey || video.Snapshot != entry.Snapshot || entry.Registration != nil || entry.NFO != nil {
				return completionEvidenceError()
			}
		} else if asset.IdentitySource == "manual" {
			proof := entry.Registration
			if video != nil || entry.NFO != nil || proof == nil || proof.AssetID != asset.ID || proof.Path != asset.Path || proof.RootPath != entry.RootPath || proof.RootID != asset.RootID || proof.Snapshot != entry.Snapshot || proof.MediaType != s.Media.MediaType || proof.TMDBID != s.Media.ID || proof.Season != s.Season || !equalQualityEpisodes(importEpisodes(proof.Episodes), importEpisodes(asset.Episodes)) || proof.Source != "manual" || proof.ConfirmedAt != asset.IdentityConfirmedAt {
				return completionEvidenceError()
			}
		} else if video != nil || entry.Registration != nil || entry.NFO == nil || entry.NFO.ID != s.Media.ID || entry.NFO.MediaType != s.Media.MediaType || s.Media.MediaType == "tv" && (entry.Numbering == nil || entry.Numbering.Season == nil || *entry.Numbering.Season != s.Season || !equalQualityEpisodes(entry.Numbering.Episodes, asset.Episodes)) {
			return completionEvidenceError()
		}
		seen[asset.ID] = true
		for _, ep := range entry.Episodes {
			if !slices.Contains(basis.Targets, ep) || !slices.Contains(importEpisodes(asset.Episodes), ep) || slices.Contains(covered, ep) {
				return completionEvidenceError()
			}
			covered = append(covered, ep)
		}
	}
	if !equalQualityEpisodes(covered, basis.Targets) {
		return completionEvidenceError()
	}
	return nil
}

func enqueueSubscriptionCompletionPreparationForTx(ctx context.Context, tx *sql.Tx, s Subscription, workflow string) error {
	// The generic queue deduplicates kind+object, while a completion is bound
	// to one exact subscription revision. Revoke only obsolete preparations;
	// a running one also loses its generation before its final source check.
	if _, e := tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,lease_until=0,updated_at=? WHERE kind='subscription_completion_prepare' AND json_extract(payload,'$.id')=? AND COALESCE(json_extract(payload,'$.revision'),0)!=? AND state IN ('queued','running','retry_wait','waiting_paused')`, now(), s.ID, s.Revision); e != nil {
		return e
	}
	var count int
	if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind='subscription_completion_prepare' AND json_extract(payload,'$.id')=? AND json_extract(payload,'$.revision')=? AND state IN ('queued','running','retry_wait','waiting_paused')`, s.ID, s.Revision).Scan(&count); e != nil {
		return e
	}
	if count > 0 {
		return nil
	}
	_, e := enqueueTx(ctx, tx, "subscription_completion_prepare", map[string]any{"id": s.ID, "revision": s.Revision}, workflow)
	return e
}

func requireSubscriptionPreparationJob(ctx context.Context, q subscriptionCompletionReader, j Job, key string, revision int) error {
	var kind, state, workflow string
	var generation int
	var raw []byte
	if e := q.QueryRowContext(ctx, `SELECT kind,state,generation,payload,workflow_id FROM jobs WHERE id=?`, j.ID).Scan(&kind, &state, &generation, &raw, &workflow); e != nil {
		return e
	}
	var payload struct {
		ID       string `json:"id"`
		Revision int    `json:"revision"`
	}
	if kind != "subscription_completion_prepare" || state != "running" || generation != j.Generation || workflow != j.WorkflowID || json.Unmarshal(raw, &payload) != nil || payload.ID != key || payload.Revision != revision {
		return completionEvidenceError()
	}
	return nil
}

func (a *App) requestOrdinarySubscriptionCompletion(ctx context.Context, j Job, s Subscription) error {
	a.subMu.Lock()
	defer a.subMu.Unlock()
	tx, e := a.Store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var current Subscription
	if e = qualityRecord(ctx, tx, "subscription", s.ID, &current); errors.Is(e, sql.ErrNoRows) {
		return nil
	} else if e != nil {
		return e
	}
	if current.Revision != s.Revision || current.State != "active" || current.SourceState == "P" || current.BestVersion {
		return nil
	}
	if e = enqueueSubscriptionCompletionPreparationForTx(ctx, tx, current, j.WorkflowID); e != nil {
		return e
	}
	current.LastReason = "目标已满足，等待原订阅完成判定、历史与通知流程"
	current.LastCheck = now()
	current.NextCheck = ""
	if next := nextScheduleCheck(current.Schedule, time.Now(), nil); !next.IsZero() {
		current.NextCheck = next.UTC().Format(time.RFC3339Nano)
	}
	if e = txPut(ctx, tx, "subscription", current.ID, current); e != nil {
		return e
	}
	return tx.Commit()
}

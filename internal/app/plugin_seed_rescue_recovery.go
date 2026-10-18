package app

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	native "light-media/internal/plugins/iyuuautoseed/native"
)

const seedRescueObserved = "SEED_RESCUE_SUBMISSION_OBSERVED"

// Only an exact existing torrent at the originally bound downloader can close
// a lost add response. Absence, an unavailable daemon, changed credentials or
// any other pending effect leave the old timeline unknown. This never adds,
// resumes, tags, relocates, or replays the interrupted source operation.
func (o *pluginSeedRescueOwner) reconcileAdds(ctx context.Context) error {
	state, _, e := o.a.pluginStore.seedRescueRead(ctx, o.a.Store.DB, o.instance, o.generation)
	if e != nil {
		return e
	}
	for _, action := range state.Runs {
		if action.Phase != "started" {
			continue
		}
		ordinal := -1
		for i, effect := range action.Effects {
			if effect.Phase == "confirmed" {
				continue
			}
			if effect.Kind != "add_torrent" || ordinal != -1 {
				ordinal = -2
				break
			}
			ordinal = i
		}
		if ordinal < 0 {
			continue
		}
		var proof struct {
			Hash          string   `json:"hash"`
			Downloader    string   `json:"downloader_id"`
			DownloaderSHA string   `json:"downloader_sha"`
			TorrentSHA    string   `json:"torrent_sha"`
			SavePath      *string  `json:"save_path"`
			Category      string   `json:"category"`
			Tags          []string `json:"tags"`
			Paused        bool     `json:"paused"`
			Ignore        bool     `json:"ignore_category_check"`
		}
		if cleanupJournalStrict(action.Effects[ordinal].Args, &proof) != nil || len(proof.Hash) != 40 || !pluginStorageDigest.MatchString(proof.DownloaderSHA) {
			return seedRescueEvidenceError()
		}
		var selected DownloaderConfig
		found := false
		for _, d := range o.a.Settings().Downloaders {
			if d.ID == proof.Downloader && downloaderEnabled(d) && seedRescueDownloaderSHA(d) == proof.DownloaderSHA {
				selected = d
				found = true
			}
		}
		if !found {
			continue
		}
		guard := func(ctx context.Context) error {
			in, e := o.a.pluginStore.Installation(ctx, o.instance)
			if e != nil || !in.Installed || !in.DesiredEnabled || in.Generation != o.generation {
				return seedRescueEvidenceError()
			}
			cfg, e := o.a.pluginStore.Config(ctx, o.instance)
			if e != nil || cfg.Revision != action.EffectiveRevision {
				return seedRescueEvidenceError()
			}
			for _, d := range o.a.Settings().Downloaders {
				if d.ID == proof.Downloader && downloaderEnabled(d) && seedRescueDownloaderSHA(d) == proof.DownloaderSHA {
					return nil
				}
			}
			return seedRescueEvidenceError()
		}
		client, e := native.NewIYUUClient(seedRescueNativeConfig(selected), native.IYUUOptions{APIKey: selected.APIKey, AutoCategory: selected.AutoCategory, Sequential: selected.Sequential, FirstLast: selected.FirstLast, Guard: guard})
		if e != nil {
			continue
		}
		raw, e := client.Torrents(ctx, []string{proof.Hash}, false)
		client.Close()
		if e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		var rows []map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&rows) != nil {
			continue
		}
		observed := false
		for _, row := range rows {
			hash, _ := row["hash"].(string)
			if strings.EqualFold(hash, proof.Hash) {
				observed = true
			}
		}
		if !observed {
			continue
		}
		tx, e := o.a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		s, blob, e := o.a.pluginStore.seedRescueRead(ctx, tx, o.instance, o.generation)
		if e != nil {
			tx.Rollback()
			return e
		}
		current, ok := s.Runs[action.RunID]
		if !ok || current.Phase != "started" || ordinal >= len(current.Effects) || current.Effects[ordinal].Phase != "started" || !bytes.Equal(current.Effects[ordinal].Args, action.Effects[ordinal].Args) {
			tx.Rollback()
			return seedRescueEvidenceError()
		}
		var configRevision int64
		if e = tx.QueryRowContext(ctx, `SELECT revision FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, o.instance).Scan(&configRevision); e != nil || configRevision != action.EffectiveRevision {
			tx.Rollback()
			return seedRescueEvidenceError()
		}
		run, e := o.a.pluginStore.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, action.RunID))
		if e != nil {
			tx.Rollback()
			return e
		}
		deferred, prior, e := o.a.pluginStore.readDeferred(ctx, tx, run)
		if e != nil || (deferred.Phase != "started" && deferred.Phase != "uncertain") {
			tx.Rollback()
			return seedRescueEvidenceError()
		}
		current.Effects[ordinal].Phase = "confirmed"
		current.Effects[ordinal].Result = mustCleanSeedJSON(map[string]any{"submission_observed": true, "hash": proof.Hash, "downloader_sha": proof.DownloaderSHA})
		current.Phase = "failed"
		current.ErrorCode = seedRescueObserved
		current.Result = mustCleanSeedJSON(map[string]any{"submission_observed": true, "source_not_replayed": true, "hash": proof.Hash})
		s.Runs[action.RunID] = current
		if e = o.a.pluginStore.seedRescueSave(ctx, tx, s, blob); e != nil {
			tx.Rollback()
			return e
		}
		deferred.Phase = "terminal_failed"
		deferred.ErrorCode = current.ErrorCode
		deferred.Result = current.Result
		if e = o.a.pluginStore.writeDeferred(ctx, tx, deferred, prior); e != nil {
			tx.Rollback()
			return e
		}
		sealed, e := o.a.pluginStore.seal("run_result", o.instance, run.ID, current.Result)
		if e != nil {
			tx.Rollback()
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE plugin_runs SET state='failed',error_code=?,result=?,updated_at=? WHERE id=? AND state IN ('running','reconcile_pending','failed')`, seedRescueObserved, sealed, now(), run.ID); e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
	}
	return nil
}

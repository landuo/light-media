package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	scrapehost "light-media/internal/plugins/scrapefileclean/host"
)

type scrapeAcceptedRun struct {
	Run, Version, Action, InputSHA, Method string
	Generation, Revision                   int64
}
type scrapeAdmissionKey struct{}

func scrapeAccepted(run PluginRun, method string) scrapeAcceptedRun {
	return scrapeAcceptedRun{run.ID, run.Version, run.Action, deferredInputSHA(run.Input), method, run.Generation, run.ConfigRevision}
}
func scrapeUnknown() error {
	return bad("SCRAPE_CLEAN_UNCONFIRMED", "源文件清理存在未确认操作，已保留原始凭据，不能重复执行")
}
func scrapeQueueRejected(err error) bool {
	var api *APIError
	return errors.As(err, &api) && api.Code == "SCRAPE_CLEAN_UNCONFIRMED"
}
func (p *PluginStore) scrapeCleanQueueAdmissionTx(ctx context.Context, tx *sql.Tx, instance, plugin string) error {
	if plugin != "ScrapeFileClean" {
		return nil
	}
	var g int64
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM plugin_installations WHERE instance_id=? AND plugin_id='ScrapeFileClean' AND version='1.0.7'`, instance).Scan(&g); err != nil {
		return err
	}
	j := &pluginScrapeCleanJournal{p: p, instance: instance, generation: g}
	r, _, err := j.root(ctx, tx)
	if err != nil {
		return err
	}
	return p.scrapeCleanValidateAcceptedTx(ctx, tx, j, r)
}
func (p *PluginStore) scrapeCleanValidateAcceptedTx(ctx context.Context, tx *sql.Tx, j *pluginScrapeCleanJournal, root pluginScrapeCleanRoot) error {
	state, _, _, err := j.read(ctx, tx)
	if err != nil {
		return err
	}
	unknown := state.Blocked != "" || state.Active != nil
	for _, x := range state.Effects {
		if x.State != "confirmed" {
			unknown = true
		}
	}
	for _, ev := range state.Events {
		if ev.State != "confirmed" && ev.State != "pending" && ev.State != "cancelled" {
			unknown = true
		}
	}
	if err = p.scrapeCleanVerifyEffectEvidence(ctx, tx, j, state); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM plugin_runs WHERE instance_id=? AND generation=? ORDER BY id`, j.instance, j.generation)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, id := range ids {
		run, err := p.scanPluginRun(tx.QueryRowContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE id=?`, id))
		if err != nil {
			return scrapeJournalCorrupt()
		}
		fact, exists := root.Runs[id]
		d, _, err := p.readDeferred(ctx, tx, run)
		if errors.Is(err, sql.ErrNoRows) {
			if exists || run.State == "succeeded" || run.State == "reconcile_pending" {
				return scrapeJournalCorrupt()
			}
			continue
		}
		if err != nil {
			return err
		}
		in := PluginInstallation{PluginID: "ScrapeFileClean", Version: "1.0.7"}
		method, raw, err := pluginDeferredSpec(in, run)
		want := scrapeAccepted(run, method)
		if err != nil || d.Plugin != "ScrapeFileClean" || d.Instance != j.instance || d.Generation != j.generation || d.Run != id || d.Action != run.Action || d.Version != run.Version || d.InputSHA != deferredInputSHA(run.Input) || d.Method != method || !deferredPayloadEqual(raw, d.Raw) {
			return scrapeJournalCorrupt()
		}
		active, owned := ctx.Value(scrapeAdmissionKey{}).(scrapeAcceptedRun)
		owned = owned && active == want && run.State == "running"
		if !exists {
			if d.Phase == "prepared" && run.State != "succeeded" {
				continue
			}
			if d.Phase == "started" && owned {
				continue
			}
			return scrapeJournalCorrupt()
		}
		seen[id] = true
		if fact != want {
			return scrapeJournalCorrupt()
		}
		if d.Phase == "started" && owned {
			continue
		}
		outcome, found, err := p.scrapeCleanOutcomeFromState(ctx, tx, run, method, state)
		if err != nil {
			return err
		}
		if d.Phase == "started" || d.Phase == "uncertain" {
			if !found {
				unknown = true
			}
			continue
		}
		if d.Phase != "confirmed" || !found || !bytes.Equal(d.Result, outcome) || d.ErrorCode != "" {
			return scrapeJournalCorrupt()
		}
	}
	for id := range root.Runs {
		if !seen[id] {
			return scrapeJournalCorrupt()
		}
	}
	if unknown {
		return scrapeUnknown()
	}
	return nil
}
func (p *PluginStore) scrapeCleanAcceptTx(ctx context.Context, tx *sql.Tx, run PluginRun, method string) error {
	j := &pluginScrapeCleanJournal{p: p, instance: run.InstanceID, generation: run.Generation}
	r, digest, err := j.root(ctx, tx)
	if err != nil {
		return err
	}
	ctx = context.WithValue(ctx, scrapeAdmissionKey{}, scrapeAccepted(run, method))
	if err = p.scrapeCleanValidateAcceptedTx(ctx, tx, j, r); err != nil {
		return err
	}
	want := scrapeAccepted(run, method)
	if old, exists := r.Runs[run.ID]; exists {
		if old != want {
			return scrapeJournalCorrupt()
		}
		return nil
	}
	if r.Runs == nil {
		r.Runs = map[string]scrapeAcceptedRun{}
	}
	r.Runs[run.ID] = want
	r.Sequence++
	r.Previous = digest
	return j.saveRootTx(ctx, tx, r, digest)
}

func (p *PluginStore) scrapeCleanVerifyEffectEvidence(ctx context.Context, q pluginRemoveLinkJournalReader, j *pluginScrapeCleanJournal, s scrapehost.Durable) error {
	e := &pluginScrapeCleanEffects{j: j}
	for _, x := range s.Effects {
		if x.State != "confirmed" {
			continue
		}
		switch x.Kind {
		case "history_src", "history_dest", "notification", "download_file_deleted":
			r, err := e.receipt(ctx, q, x)
			if err != nil {
				return err
			}
			if r.Result != x.Result {
				return scrapeJournalCorrupt()
			}
			if x.Kind != "notification" && x.Kind != "download_file_deleted" {
				continue
			}
			token := e.token(x.ID)
			var sealed []byte
			var created string
			if err = q.QueryRowContext(ctx, `SELECT sealed,created_at FROM plugin_scrape_private_outbox WHERE instance_id=? AND generation=? AND token=?`, j.instance, j.generation, token).Scan(&sealed, &created); err != nil {
				return err
			}
			raw, err := p.unseal("scrape_journal", j.instance, e.aad("outbox", token), sealed)
			if err != nil {
				return err
			}
			var box pluginScrapeCleanPrivateOutbox
			_, original, err := e.identity(x)
			if err != nil || scrapeJournalStrict(raw, &box) != nil || box.Version != 1 || box.CreatedAt != created || box.Timezone == "" || !bytes.Equal(box.Effect, original) {
				return scrapeJournalCorrupt()
			}
			if box.State == "pending" {
				var n int
				if err = q.QueryRowContext(ctx, `SELECT count(*) FROM plugin_scrape_outbox_handoffs WHERE instance_id=? AND generation=? AND token=?`, j.instance, j.generation, token).Scan(&n); err != nil {
					return err
				}
				if n != 0 {
					return scrapeJournalCorrupt()
				}
				continue
			}
			if x.Kind == "download_file_deleted" {
				id := "sfe_" + hex.EncodeToString(j.mac("file-event", []byte(token)))
				if _, err = p.scrapeDownloadFileEvent(ctx, q, id); err != nil {
					return err
				}
			} else {
				var rs []byte
				var h pluginScrapeHandoff
				if err = q.QueryRowContext(ctx, `SELECT sealed FROM plugin_scrape_outbox_handoffs WHERE instance_id=? AND generation=? AND token=?`, j.instance, j.generation, token).Scan(&rs); err != nil {
					return err
				}
				hr, err := p.unseal("scrape_journal", j.instance, e.aad("handoff", token), rs)
				if err != nil {
					return err
				}
				if scrapeJournalStrict(hr, &h) != nil || h.Notice == nil {
					return scrapeJournalCorrupt()
				}
				if _, _, err = p.readNoticeRoute(ctx, q, h.Notice.RouteID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

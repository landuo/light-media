package app

import (
	"context"
	"database/sql"
	"encoding/json"
)

type scrapeConfigWriteKey struct{}
type scrapeConfigWrite struct {
	Prepared *pluginScrapeCleanPrepared
	RawSHA   string
}

func (p *PluginStore) scrapeCleanConfigWriteTx(ctx context.Context, tx *sql.Tx, instance string, revision int64, raw json.RawMessage) error {
	var g int64
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM plugin_installations WHERE instance_id=? AND plugin_id='ScrapeFileClean' AND version='1.0.7'`, instance).Scan(&g); err != nil {
		return err
	}
	j := &pluginScrapeCleanJournal{p: p, instance: instance, generation: g}
	r, _, err := j.root(ctx, tx)
	if err != nil {
		return err
	}
	if err = p.scrapeCleanValidateAcceptedTx(ctx, tx, j, r); err != nil {
		return err
	}
	s, _, _, err := j.read(ctx, tx)
	if err != nil {
		return err
	}
	// Real install/import genesis may receive its initial data before any worker
	// or observer has been admitted. A later ordinary write cannot look initial
	// merely because its machine namespace disappeared.
	if len(r.Runs) == 0 && s.Binding.InstanceID == "" && s.Transition == nil && scrapeCleanIdle(s) {
		return nil
	}
	grant, ok := ctx.Value(scrapeConfigWriteKey{}).(scrapeConfigWrite)
	pr := grant.Prepared
	if !ok || pr == nil || pr.o == nil || pr.a == nil || pr.a.pluginStore != p || pr.o.j.p != p || pr.from.InstanceID != instance || pr.from.Generation != g || pr.from.Revision != revision || grant.RawSHA != deferredInputSHA(raw) || s.Binding != pr.from || s.Transition == nil || s.Transition.ID != pr.ticket.ID || s.Transition.From != pr.from || s.Transition.Phase != "prepared" || s.Transition.Kind != "init" {
		return bad("SCRAPE_CLEAN_OWNER_REQUIRED", "源文件清理配置必须通过已预留的实际宿主发布")
	}
	return nil
}

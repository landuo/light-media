package app

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"strconv"
)

// This anchor lives independently of the mutable machine namespace. Only a
// real installation/generation publication may create genesis; ordinary open,
// recovery, retry and Journal.Update never recreate missing evidence.
const pluginScrapeCleanRootSchema = `CREATE TABLE IF NOT EXISTS plugin_scrape_owner_roots(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sequence INTEGER NOT NULL,digest BLOB NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation));
CREATE TABLE IF NOT EXISTS plugin_scrape_owner_chain(instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sequence INTEGER NOT NULL,digest BLOB NOT NULL,sealed BLOB NOT NULL,PRIMARY KEY(instance_id,generation,sequence));`

type pluginScrapeCleanRoot struct {
	Version                            int
	Instance, Plugin, VersionID, Event string
	Generation, Sequence               int64
	Previous, Parent, State            []byte
	Runs                               map[string]scrapeAcceptedRun
}

func (j *pluginScrapeCleanJournal) rootAAD() string { return j.aad("owner-root", "head") }
func (j *pluginScrapeCleanJournal) root(ctx context.Context, q pluginScrapeCleanJournalReader) (pluginScrapeCleanRoot, []byte, error) {
	var out pluginScrapeCleanRoot
	var sequence int64
	var digest, sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT sequence,digest,sealed FROM plugin_scrape_owner_roots WHERE instance_id=? AND generation=?`, j.instance, j.generation).Scan(&sequence, &digest, &sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, nil, scrapeJournalCorrupt()
		}
		return out, nil, err
	}
	raw, err := j.p.unseal("scrape_owner_root", j.instance, j.rootAAD(), sealed)
	if err != nil {
		return out, nil, err
	}
	if scrapeJournalStrict(raw, &out) != nil || out.Version != 1 || out.Instance != j.instance || out.Plugin != "ScrapeFileClean" || out.VersionID != "1.0.7" || out.Generation != j.generation || out.Sequence != sequence || sequence < 0 || !hmac.Equal(digest, j.mac("owner-root", raw)) {
		return out, nil, scrapeJournalCorrupt()
	}
	rows, err := q.QueryContext(ctx, `SELECT sequence,digest,sealed FROM plugin_scrape_owner_chain WHERE instance_id=? AND generation=? ORDER BY sequence`, j.instance, j.generation)
	if err != nil {
		return out, nil, err
	}
	var prior []byte
	var ordinal int64
	for rows.Next() {
		var n int64
		var d, s []byte
		if err = rows.Scan(&n, &d, &s); err != nil {
			rows.Close()
			return out, nil, err
		}
		var r pluginScrapeCleanRoot
		v, e := j.p.unseal("scrape_owner_root", j.instance, j.aad("owner-chain", stringInt64(n)), s)
		if e != nil || scrapeJournalStrict(v, &r) != nil || n != ordinal || r.Sequence != n || r.Version != 1 || r.Instance != j.instance || r.Generation != j.generation || r.Plugin != out.Plugin || r.VersionID != out.VersionID || r.Event != out.Event || !hmac.Equal(r.Parent, out.Parent) || !hmac.Equal(r.Previous, prior) || !hmac.Equal(d, j.mac("owner-root", v)) {
			rows.Close()
			return out, nil, scrapeJournalCorrupt()
		}
		prior = append([]byte(nil), d...)
		ordinal++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, nil, err
	}
	if ordinal != sequence+1 || !hmac.Equal(prior, digest) {
		return out, nil, scrapeJournalCorrupt()
	}
	if j.generation == 1 {
		if len(out.Parent) != 0 {
			return out, nil, scrapeJournalCorrupt()
		}
	} else {
		old := &pluginScrapeCleanJournal{p: j.p, instance: j.instance, generation: j.generation - 1}
		_, _, e := old.root(ctx, q)
		if e != nil {
			return out, nil, e
		}
		var n int
		if e = q.QueryRowContext(ctx, `SELECT count(*) FROM plugin_scrape_owner_chain WHERE instance_id=? AND generation=? AND digest=?`, j.instance, j.generation-1, out.Parent).Scan(&n); e != nil {
			return out, nil, e
		}
		if n != 1 {
			return out, nil, scrapeJournalCorrupt()
		}
	}
	return out, append([]byte(nil), digest...), nil
}
func (j *pluginScrapeCleanJournal) saveRootTx(ctx context.Context, tx *sql.Tx, r pluginScrapeCleanRoot, previous []byte) error {
	raw, err := scrapeJournalJSON(r)
	if err != nil {
		return err
	}
	digest := j.mac("owner-root", raw)
	sealed, err := j.p.seal("scrape_owner_root", j.instance, j.rootAAD(), raw)
	if err != nil {
		return err
	}
	chain, err := j.p.seal("scrape_owner_root", j.instance, j.aad("owner-chain", stringInt64(r.Sequence)), raw)
	if err != nil {
		return err
	}
	if r.Sequence == 0 {
		_, err = tx.ExecContext(ctx, `INSERT INTO plugin_scrape_owner_roots(instance_id,generation,sequence,digest,sealed) VALUES(?,?,?,?,?)`, j.instance, j.generation, r.Sequence, digest, sealed)
	} else {
		var res sql.Result
		res, err = tx.ExecContext(ctx, `UPDATE plugin_scrape_owner_roots SET sequence=?,digest=?,sealed=? WHERE instance_id=? AND generation=? AND sequence=? AND digest=?`, r.Sequence, digest, sealed, j.instance, j.generation, r.Sequence-1, previous)
		if err == nil {
			var n int64
			n, err = res.RowsAffected()
			if err == nil && n != 1 {
				err = scrapeJournalCorrupt()
			}
		}
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_scrape_owner_chain(instance_id,generation,sequence,digest,sealed) VALUES(?,?,?,?,?)`, j.instance, j.generation, r.Sequence, digest, chain)
	return err
}
func (p *PluginStore) scrapeCleanGenesisTx(ctx context.Context, tx *sql.Tx, instance, event string) error {
	if event != "install" && event != "reset" && event != "uninstall" && event != "bulk_uninstall" {
		return scrapeJournalCorrupt()
	}
	if _, err := tx.ExecContext(ctx, pluginScrapeCleanRootSchema+pluginScrapeCleanJournalSchema); err != nil {
		return err
	}
	var plugin, version string
	var g int64
	if err := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&plugin, &version, &g); err != nil {
		return err
	}
	if plugin != "ScrapeFileClean" || version != "1.0.7" || g < 1 {
		return scrapeJournalScope()
	}
	j := &pluginScrapeCleanJournal{p: p, instance: instance, generation: g}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM plugin_scrape_owner_roots WHERE instance_id=? AND generation=?`, instance, g).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return scrapeJournalCorrupt()
	}
	var parent []byte
	if g > 1 {
		old := &pluginScrapeCleanJournal{p: p, instance: instance, generation: g - 1}
		previousRoot, d, err := old.root(ctx, tx)
		if err != nil {
			return err
		}
		if err = p.scrapeCleanValidateAcceptedTx(ctx, tx, old, previousRoot); err != nil {
			return err
		}
		parent = d
	}
	return j.saveRootTx(ctx, tx, pluginScrapeCleanRoot{Version: 1, Instance: instance, Plugin: plugin, VersionID: version, Event: event, Generation: g, Parent: parent}, nil)
}

// json.Number preserves the exact decimal generation/sequence in AAD.
func stringInt64(v int64) string { return strconv.FormatInt(v, 10) }

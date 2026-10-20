package app

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Only SubscribeGroup's guarded core transaction calls this. The source keeps
// already-selected downloads at its internal revision advance, while ordinary
// user edits still revoke their old scope. Their selected policy stays frozen.
func (a *App) advancePlannedQualityRevisionTx(ctx context.Context, tx *sql.Tx, sub Subscription) error {
	rows, e := tx.QueryContext(ctx, `SELECT payload FROM records WHERE kind='intent' AND json_extract(payload,'$.subscription_id')=? AND json_extract(payload,'$.subscription_revision')=? AND json_extract(payload,'$.state') IN ('planned','submitting','reconcile_pending')`, sub.ID, sub.Revision)
	if e != nil {
		return e
	}
	intents := []Intent{}
	for rows.Next() {
		var raw []byte
		var in Intent
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return e
		}
		if json.Unmarshal(raw, &in) != nil {
			rows.Close()
			return bad("QUALITY_SNAPSHOT_CHANGED", "原已选下载无法读取")
		}
		intents = append(intents, in)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, in := range intents {
		p, e := a.qualitySnapshot(ctx, tx, "intent", in.ID, in.Hash)
		if e != nil {
			return e
		}
		if p == nil {
			continue
		}
		if p.SubscriptionID != sub.ID || p.Revision != sub.Revision || p.BestVersion != sub.BestVersion || p.SourceContextSHA != sub.SourceContextSHA || p.MediaKey != mediaKey(sub.Media) || p.Season != sub.Season {
			return bad("QUALITY_SNAPSHOT_CHANGED", "原已选下载的订阅版本无法核对")
		}
		p.Revision++
		if e = a.writeQualitySealedTx(ctx, tx, "subscription_quality_download", "intent:"+in.ID, p); e != nil {
			return e
		}
	}
	return nil
}

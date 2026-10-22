package app

import (
	"bytes"
	"context"
	"database/sql"
	"time"

	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

// This row is captured only after the intent transaction has fully verified
// the original route and business input. It authorizes recording the result
// of that single send, never a new send or a later business publication.
type pluginNoticeQueueRow struct {
	entry  pluginNoticeQueueEntry
	sealed []byte
}

func (row pluginNoticeQueueRow) require(ctx context.Context, q pluginRemoveLinkJournalReader) error {
	var route, state string
	var ordinal int
	var sealed []byte
	err := q.QueryRowContext(ctx, `SELECT route_id,ordinal,state,sealed FROM plugin_message_queue WHERE id=?`, row.entry.ID).Scan(&route, &ordinal, &state, &sealed)
	if err != nil || route != row.entry.RouteID || ordinal != row.entry.Ordinal || state != row.entry.State || !bytes.Equal(sealed, row.sealed) {
		return seedhost.ErrUnknown
	}
	return nil
}

func (p *PluginStore) writePluginMessageIntentTx(ctx context.Context, tx *sql.Tx, q pluginNoticeQueueEntry) (pluginNoticeQueueRow, error) {
	var row pluginNoticeQueueRow
	if q.State != "intent" {
		return row, seedhost.ErrUnsafe
	}
	sealed, err := p.noticeSeal("queue", q.ID, q)
	if err != nil {
		return row, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE plugin_message_queue SET state='intent',sealed=? WHERE id=? AND route_id=? AND ordinal=? AND state='queued'`, sealed, q.ID, q.RouteID, q.Ordinal)
	if err != nil {
		return row, err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return row, seedhost.ErrUnsafe
	}
	row = pluginNoticeQueueRow{entry: q, sealed: sealed}
	if err = row.require(ctx, tx); err != nil {
		return pluginNoticeQueueRow{}, err
	}
	return row, nil
}

func (p *PluginStore) finishPluginMessageRowTx(ctx context.Context, tx *sql.Tx, intent pluginNoticeQueueRow, q pluginNoticeQueueEntry) (pluginNoticeQueueRow, error) {
	var row pluginNoticeQueueRow
	if intent.entry.State != "intent" || len(intent.sealed) == 0 || (q.State != "consumed" && q.State != "unknown" && !(q.State == "revoked" && q.ErrorCode == "NOTICE_AUTH_REVOKED" && q.Modules == 0 && q.FinishedAt != "")) || q.AttemptAt != intent.entry.AttemptAt || noticeQueueInitialSHA(q) != noticeQueueInitialSHA(intent.entry) {
		return row, seedhost.ErrUnsafe
	}
	sealed, err := p.noticeSeal("queue", q.ID, q)
	if err != nil {
		return row, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE plugin_message_queue SET state=?,sealed=? WHERE id=? AND route_id=? AND ordinal=? AND state='intent' AND sealed=?`, q.State, sealed, q.ID, q.RouteID, q.Ordinal, intent.sealed)
	if err != nil {
		return row, err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return row, seedhost.ErrUnknown
	}
	row = pluginNoticeQueueRow{entry: q, sealed: sealed}
	if err = row.require(ctx, tx); err != nil {
		return pluginNoticeQueueRow{}, err
	}
	return row, nil
}

func (p *PluginStore) commitPluginMessageRow(ctx context.Context, tx *sql.Tx, operation string, expected pluginNoticeQueueRow) error {
	if err := p.commitNoticeTx(tx, operation); err != nil {
		_ = tx.Rollback()
		read, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		return expected.require(read, p.store.DB)
	}
	return nil
}

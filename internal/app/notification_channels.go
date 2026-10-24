package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
)

// Value preserves the original SystemConfig.Notifications list, including
// unknown channel/config fields and JSON number types. Credential-bearing
// values are private; list/log APIs must use an explicit redacted projection.
type NotificationChannelSnapshot struct {
	Revision int64
	Value    []any
}
type notificationChannelFact struct {
	Version     int
	Revision    int64
	PreviousSHA string
	Value       []any
}

const notificationChannelSchema = `CREATE TABLE IF NOT EXISTS notification_channel_head(id TEXT PRIMARY KEY,revision INTEGER NOT NULL,sealed BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS notification_channel_history(revision INTEGER PRIMARY KEY,sealed BLOB NOT NULL);`

func notificationChannelsError() error {
	return bad("NOTIFICATION_CHANNEL_EVIDENCE_MISSING", "通知渠道配置证据缺失或无法校验")
}
func notificationChannelSHA(f notificationChannelFact) string {
	return tokenHash(string(mustCleanSeedJSON(f)))
}
func (p *PluginStore) initNotificationChannelsTx(ctx context.Context, tx *sql.Tx) error {
	if _, e := tx.ExecContext(ctx, notificationChannelSchema); e != nil {
		return e
	}
	var marker []byte
	e := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='core_settings_lifecycle' AND id='notifications/v1'`).Scan(&marker)
	if e == nil {
		raw, e := p.unseal("core_notification_marker", "system", "main", marker)
		if e != nil || string(raw) != `{"version":1}` {
			return notificationChannelsError()
		}
		_, e = p.notificationChannelsReadTx(ctx, tx)
		return e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	var count int
	if e = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM notification_channel_head)+(SELECT COUNT(*) FROM notification_channel_history)`).Scan(&count); e != nil {
		return e
	}
	if count != 0 {
		return notificationChannelsError()
	}
	f := notificationChannelFact{Version: 1, Value: []any{}}
	sealed, e := p.seal("core_notifications", "system", "0", mustCleanSeedJSON(f))
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO notification_channel_history VALUES(0,?)`, sealed); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO notification_channel_head VALUES('main',0,?)`, sealed); e != nil {
		return e
	}
	marker, e = p.seal("core_notification_marker", "system", "main", []byte(`{"version":1}`))
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO records VALUES('core_settings_lifecycle','notifications/v1',?,?)`, marker, now())
	return e
}
func (p *PluginStore) notificationChannelsReadTx(ctx context.Context, q pluginRemoveLinkJournalReader) (NotificationChannelSnapshot, error) {
	var out NotificationChannelSnapshot
	var head []byte
	if e := q.QueryRowContext(ctx, `SELECT revision,sealed FROM notification_channel_head WHERE id='main'`).Scan(&out.Revision, &head); e != nil {
		return out, notificationChannelsError()
	}
	if out.Revision < 0 {
		return out, notificationChannelsError()
	}
	rows, e := q.QueryContext(ctx, `SELECT revision,sealed FROM notification_channel_history ORDER BY revision`)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	var expected int64
	previous := ""
	for rows.Next() {
		var revision int64
		var sealed []byte
		if e = rows.Scan(&revision, &sealed); e != nil {
			return out, e
		}
		var f notificationChannelFact
		raw, err := p.unseal("core_notifications", "system", notificationChannelRevisionKey(revision), sealed)
		if err != nil || cleanupJournalStrict(raw, &f) != nil || f.Version != 1 || f.Revision != expected || revision != expected || f.PreviousSHA != previous || f.Value == nil {
			return out, notificationChannelsError()
		}
		if revision == out.Revision {
			if !bytes.Equal(head, sealed) {
				return out, notificationChannelsError()
			}
			out.Value = f.Value
		}
		previous = notificationChannelSHA(f)
		expected++
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	if expected != out.Revision+1 || out.Value == nil {
		return out, notificationChannelsError()
	}
	return out, nil
}
func notificationChannelRevisionKey(revision int64) string { return strconv.FormatInt(revision, 10) }

// NotificationChannels reads one consistent authenticated snapshot. Callers
// may retain it for source isolation; changing current settings never rewrites
// an existing accepted message's frozen recipients or source snapshot.
func (p *PluginStore) NotificationChannels(ctx context.Context) (NotificationChannelSnapshot, error) {
	tx, e := p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return NotificationChannelSnapshot{}, e
	}
	defer tx.Rollback()
	out, e := p.notificationChannelsReadTx(ctx, tx)
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}
func (p *PluginStore) notificationChannelsSaveTx(ctx context.Context, tx *sql.Tx, revision int64, value []any) (NotificationChannelSnapshot, error) {
	old, e := p.notificationChannelsReadTx(ctx, tx)
	if e != nil {
		return old, e
	}
	if old.Revision != revision {
		return old, bad("NOTIFICATION_CHANNEL_REVISION_CONFLICT", "通知渠道配置已变化，请重新载入")
	}
	raw, e := pluginJSONLimit(value, false, pluginValueLimit)
	if e != nil {
		return old, e
	}
	var copied []any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&copied) != nil || copied == nil {
		return old, bad("INVALID_NOTIFICATION_CHANNELS", "通知渠道配置应为列表")
	}
	var previousSealed []byte
	if e = tx.QueryRowContext(ctx, `SELECT sealed FROM notification_channel_history WHERE revision=?`, revision).Scan(&previousSealed); e != nil {
		return old, e
	}
	previousRaw, e := p.unseal("core_notifications", "system", notificationChannelRevisionKey(revision), previousSealed)
	if e != nil {
		return old, e
	}
	var previous notificationChannelFact
	if cleanupJournalStrict(previousRaw, &previous) != nil {
		return old, notificationChannelsError()
	}
	next := notificationChannelFact{Version: 1, Revision: revision + 1, PreviousSHA: notificationChannelSHA(previous), Value: copied}
	sealed, e := p.seal("core_notifications", "system", notificationChannelRevisionKey(next.Revision), mustCleanSeedJSON(next))
	if e != nil {
		return old, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO notification_channel_history VALUES(?,?)`, next.Revision, sealed); e != nil {
		return old, e
	}
	r, e := tx.ExecContext(ctx, `UPDATE notification_channel_head SET revision=?,sealed=? WHERE id='main' AND revision=? AND sealed=?`, next.Revision, sealed, revision, previousSealed)
	if e != nil {
		return old, e
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return old, notificationChannelsError()
	}
	return NotificationChannelSnapshot{next.Revision, copied}, nil
}
func (p *PluginStore) SaveNotificationChannels(ctx context.Context, revision int64, value []any) (NotificationChannelSnapshot, error) {
	tx, e := p.store.DB.BeginTx(ctx, nil)
	if e != nil {
		return NotificationChannelSnapshot{}, e
	}
	defer tx.Rollback()
	out, e := p.notificationChannelsSaveTx(ctx, tx, revision, value)
	if e != nil {
		return out, e
	}
	return out, tx.Commit()
}

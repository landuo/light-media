package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	scrapehost "light-media/internal/plugins/scrapefileclean/host"
)

// These are transfer receipts for the existing cleanup outbox, not a second
// execution queue. A published source event is not a completed consumer action;
// history_saved is not a NoticeMessage or an external delivery acknowledgement.
const pluginScrapeOutboxSchema = pluginScrapeCleanEffectsSchema + `
CREATE TABLE IF NOT EXISTS plugin_download_file_events(
 file_event_id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS plugin_scrape_outbox_handoffs(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL,token TEXT NOT NULL,sealed BLOB NOT NULL,
 PRIMARY KEY(instance_id,generation,token));`

type pluginScrapeHandoff struct {
	Version                          int
	Identity, Kind, Stage, Reference string
	Effect                           []byte
	PublicDigest                     []byte
	Notice                           *scrapeNoticeProof
}

type pluginScrapeAuditCursor struct {
	After, Through int64
}

type pluginScrapeOutboxRow struct {
	Instance, Token, Created string
	Generation               int64
	Sealed                   []byte
}

func (a *App) committedScrapeOutbox(ctx context.Context, tx *sql.Tx, row pluginScrapeOutboxRow) (*pluginScrapeCleanEffects, scrapehost.Effect, pluginScrapeCleanPrivateOutbox, error) {
	var x scrapehost.Effect
	var box pluginScrapeCleanPrivateOutbox
	if !pluginStorageID.MatchString(row.Instance) || row.Generation < 1 || !pluginStorageDigest.MatchString(row.Token) {
		return nil, x, box, scrapehost.ErrUnsafe
	}
	e := &pluginScrapeCleanEffects{a: a, j: &pluginScrapeCleanJournal{p: a.pluginStore, instance: row.Instance, generation: row.Generation}}
	raw, err := a.pluginStore.unseal("scrape_journal", row.Instance, e.aad("outbox", row.Token), row.Sealed)
	if err != nil {
		return nil, x, box, err
	}
	var encoded struct {
		Effect  scrapehost.Effect
		Payload []byte
	}
	if scrapeJournalStrict(raw, &box) != nil || box.Version != 1 || box.CreatedAt != row.Created || box.Timezone == "" || scrapeJournalStrict(box.Effect, &encoded) != nil {
		return nil, x, box, scrapehost.ErrUnsafe
	}
	x = encoded.Effect
	x.Payload = encoded.Payload
	if x.Binding.InstanceID != row.Instance || x.Binding.Generation != row.Generation || x.Binding.Revision < 1 || e.token(x.ID) != row.Token || x.State != "" || x.Error != "" || x.Result {
		return nil, x, box, scrapehost.ErrUnsafe
	}
	_, original, err := e.identity(x)
	if err != nil || !bytes.Equal(original, box.Effect) {
		return nil, x, box, scrapehost.ErrUnsafe
	}
	state, _, _, stateErr := e.j.read(ctx, tx)
	if stateErr != nil {
		return nil, x, box, stateErr
	}
	fact, exists := state.Effects[x.ID]
	if !exists || fact.State != "confirmed" || !fact.Result || !scrapeEffectEqual(fact, x) {
		return nil, x, box, scrapeJournalCorrupt()
	}
	// The producer's SQL receipt proves that this exact intent was authorized
	// and committed; possessing a raw pending row is not such proof.
	receipt, err := e.receipt(ctx, tx, x)
	if err != nil || !receipt.Result {
		if err == nil {
			err = scrapehost.ErrUnsafe
		}
		return nil, x, box, err
	}
	return e, x, box, nil
}

func scrapeNoticeDigest(e *pluginScrapeCleanEffects, n Notification) []byte {
	n.Read = false // Marking a message read never invalidates its original fact.
	raw, _ := json.Marshal(n)
	return e.j.mac("notice-history-value", raw)
}

func putScrapeNoticeTx(ctx context.Context, tx *sql.Tx, n Notification) error {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='notification' AND id=?`, n.ID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return txPut(ctx, tx, "notification", n.ID, n)
	}
	if err != nil {
		return err
	}
	var old Notification
	if scrapeJournalStrict(raw, &old) != nil {
		return scrapehost.ErrUnsafe
	}
	old.Read = false
	if old != n {
		return scrapehost.ErrUnsafe
	}
	return nil // Preserve an existing matching message's read state and time.
}

// The queue's indexed existence join is not treated as cryptographic evidence.
// Audit bounded pages on startup/each dispatcher cycle; corruption blocks this
// outbox without preventing unrelated already queued plugin batches from drain.
func (a *App) auditScrapeFileCleanHandoffs(ctx context.Context, cursor pluginScrapeAuditCursor, limit int) (pluginScrapeAuditCursor, error) {
	if cursor.After < 0 || cursor.Through < cursor.After || limit < 1 || limit > 200 {
		return cursor, scrapehost.ErrUnsafe
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return cursor, err
	}
	defer tx.Rollback()
	// New transfers cannot extend an in-progress audit round indefinitely.
	if cursor.Through == 0 {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rowid),0) FROM plugin_scrape_outbox_handoffs`).Scan(&cursor.Through); err != nil {
			return cursor, err
		}
	}
	type item struct {
		sequence int64
		row      pluginScrapeOutboxRow
		sealed   []byte
	}
	rows, err := tx.QueryContext(ctx, `SELECT h.rowid,h.instance_id,h.generation,h.token,h.sealed,o.sealed,o.created_at FROM plugin_scrape_outbox_handoffs h LEFT JOIN plugin_scrape_private_outbox o ON o.instance_id=h.instance_id AND o.generation=h.generation AND o.token=h.token WHERE h.rowid>? AND h.rowid<=? ORDER BY h.rowid LIMIT ?`, cursor.After, cursor.Through, limit)
	if err != nil {
		return cursor, err
	}
	items := []item{}
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.sequence, &v.row.Instance, &v.row.Generation, &v.row.Token, &v.sealed, &v.row.Sealed, &v.row.Created); err != nil {
			rows.Close()
			return cursor, err
		}
		items = append(items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return cursor, err
	}
	for _, v := range items {
		e, x, box, err := a.committedScrapeOutbox(ctx, tx, v.row)
		if err != nil {
			return cursor, err
		}
		raw, err := a.pluginStore.unseal("scrape_journal", v.row.Instance, e.aad("handoff", v.row.Token), v.sealed)
		if err != nil {
			return cursor, err
		}
		var h pluginScrapeHandoff
		identity, original, err := e.identity(x)
		if err != nil || scrapeJournalStrict(raw, &h) != nil || h.Version != 1 || h.Identity != identity || h.Kind != x.Kind || !bytes.Equal(h.Effect, original) || h.Stage != box.State {
			return cursor, scrapehost.ErrUnsafe
		}
		switch x.Kind {
		case "download_file_deleted":
			wantID := "sfe_" + hex.EncodeToString(e.j.mac("file-event", []byte(v.row.Token)))
			if h.Stage != "published" || h.Reference != wantID || len(h.PublicDigest) != 0 {
				return cursor, scrapehost.ErrUnsafe
			}
			private, err := a.pluginStore.downloadFileEvent(ctx, tx, wantID)
			if err != nil || private.Src != x.Path || private.EffectID != x.ID || private.Generation != v.row.Generation || private.Revision != x.Binding.Revision || private.Instance != v.row.Instance {
				return cursor, scrapehost.ErrUnsafe
			}
			var eventRaw []byte
			if err := tx.QueryRowContext(ctx, `SELECT payload FROM domain_events WHERE event_id=?`, domainEventID("downloadfile.deleted", wantID)).Scan(&eventRaw); err != nil {
				return cursor, err
			}
			var got DomainEvent
			want, err := validateDomainEvent(DomainEvent{Type: "downloadfile.deleted", EventID: domainEventID("downloadfile.deleted", wantID), FileEventID: wantID, OccurredAt: v.row.Created})
			if err != nil || scrapeJournalStrict(eventRaw, &got) != nil || got != want {
				return cursor, scrapehost.ErrUnsafe
			}
		case "notification":
			wantID := "scrape_notice_" + hex.EncodeToString(e.j.mac("notice-history", []byte(v.row.Token)))
			if h.Stage != "history_saved" || h.Reference != wantID {
				return cursor, scrapehost.ErrUnsafe
			}
			var noticeRaw []byte
			if err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='notification' AND id=?`, wantID).Scan(&noticeRaw); err != nil {
				return cursor, err
			}
			var notice Notification
			if scrapeJournalStrict(noticeRaw, &notice) != nil || notice.ID != wantID || !hmac.Equal(h.PublicDigest, scrapeNoticeDigest(e, notice)) {
				return cursor, scrapehost.ErrUnsafe
			}
		default:
			return cursor, scrapehost.ErrUnsupported
		}
		cursor.After = v.sequence
	}
	if len(items) < limit || cursor.After >= cursor.Through {
		cursor = pluginScrapeAuditCursor{}
	}
	return cursor, nil
}

func (a *App) dispatchScrapeFileCleanOutboxes(ctx context.Context, limit int) error {
	if limit < 1 || limit > 200 {
		return scrapehost.ErrUnsafe
	}
	for range limit {
		found, err := a.transferScrapeFileCleanOutbox(ctx)
		if err != nil || !found {
			return err
		}
	}
	return nil
}

// Keep insertion order, including equal timestamps and clock regressions. The
// original broadcast queue does not promise consumer completion in this order.
// Retired producers may transfer already committed intents: no current worker,
// config, filesystem or downloader authority is acquired by this operation.
func (a *App) transferScrapeFileCleanOutbox(ctx context.Context) (bool, error) {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var instance, token, created string
	var generation int64
	var sealed []byte
	err = tx.QueryRowContext(ctx, `SELECT o.instance_id,o.generation,o.token,o.sealed,o.created_at
FROM plugin_scrape_private_outbox o LEFT JOIN plugin_scrape_outbox_handoffs h
ON h.instance_id=o.instance_id AND h.generation=o.generation AND h.token=o.token
WHERE h.token IS NULL ORDER BY o.rowid LIMIT 1`).Scan(&instance, &generation, &token, &sealed, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := time.Parse(time.RFC3339Nano, created); err != nil {
		return true, scrapehost.ErrUnsafe
	}
	e, x, box, err := a.committedScrapeOutbox(ctx, tx, pluginScrapeOutboxRow{Instance: instance, Token: token, Created: created, Generation: generation, Sealed: sealed})
	if err != nil {
		return true, err
	}
	if box.State != "pending" {
		return true, scrapehost.ErrUnsafe
	}
	identity, original, err := e.identity(x)
	if err != nil {
		return true, err
	}
	handoff := pluginScrapeHandoff{Version: 1, Identity: identity, Kind: x.Kind, Effect: original}
	switch x.Kind {
	case "download_file_deleted":
		var data struct {
			Src string `json:"src"`
		}
		if scrapeJournalStrict(x.Payload, &data) != nil || data.Src != x.Path || !importPath(data.Src) {
			return true, scrapehost.ErrUnsafe
		}
		fileID := "sfe_" + hex.EncodeToString(e.j.mac("file-event", []byte(token)))
		private := pluginDownloadFileEvent{Version: 1, ID: fileID, Instance: instance, Generation: generation, Revision: x.Binding.Revision, EffectID: x.ID, Src: data.Src}
		privateRaw, err := json.Marshal(private)
		if err != nil || len(privateRaw) > pluginValueLimit {
			return true, scrapehost.ErrUnsafe
		}
		privateSealed, err := a.pluginStore.seal("scrape_download_file_event", instance, fileID, privateRaw)
		if err != nil {
			return true, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_download_file_events VALUES(?,?,?,?)`, fileID, instance, generation, privateSealed); err != nil {
			return true, err
		}
		if err = recordDomainEventTx(ctx, tx, DomainEvent{EventID: domainEventID("downloadfile.deleted", fileID), Type: "downloadfile.deleted", FileEventID: fileID, OccurredAt: created}); err != nil {
			return true, err
		}
		handoff.Stage, handoff.Reference = "published", fileID
		box.State = "published"
	case "notification":
		var data struct {
			Title string `json:"title"`
			Text  string `json:"text"`
		}
		if scrapeJournalStrict(x.Payload, &data) != nil {
			return true, scrapehost.ErrUnsafe
		}
		// ScrapeFileClean calls post_message with SiteMessage and no explicit user,
		// source or channel. save_history defaults true and precedes routing.
		settings := a.Settings()
		noticeID := "scrape_notice_" + hex.EncodeToString(e.j.mac("notice-history", []byte(token)))
		n := Notification{ID: noticeID, Title: cleanWithSettings(data.Title, settings), Message: cleanWithSettings(data.Text, settings), MType: "站点", Link: "", CreatedAt: created}
		if err = putScrapeNoticeTx(ctx, tx, n); err != nil {
			return true, err
		}
		handoff.Notice, err = a.initScrapeNoticeTx(ctx, tx, e, x, token, box, n)
		if err != nil {
			return true, err
		}
		handoff.PublicDigest = scrapeNoticeDigest(e, n)
		handoff.Stage, handoff.Reference = "history_saved", noticeID
		box.State = "history_saved" // Routing is durably queued; external delivery remains separately receipted.
	default:
		return true, scrapehost.ErrUnsupported
	}
	handoffRaw, err := json.Marshal(handoff)
	if err != nil {
		return true, err
	}
	handoffSealed, err := a.pluginStore.seal("scrape_journal", instance, e.aad("handoff", token), handoffRaw)
	if err != nil {
		return true, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_scrape_outbox_handoffs VALUES(?,?,?,?)`, instance, generation, token, handoffSealed); err != nil {
		return true, err
	}
	boxRaw, err := json.Marshal(box)
	if err != nil {
		return true, err
	}
	boxSealed, err := a.pluginStore.seal("scrape_journal", instance, e.aad("outbox", token), boxRaw)
	if err != nil {
		return true, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_scrape_private_outbox SET sealed=? WHERE instance_id=? AND generation=? AND token=?`, boxSealed, instance, generation, token); err != nil {
		return true, err
	}
	return true, tx.Commit()
}

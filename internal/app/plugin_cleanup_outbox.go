package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	cleanuphost "light-media/internal/plugins/removelink/host"
)

// These are transfer receipts for the existing cleanup outbox, not a second
// execution queue. A published source event is not a completed consumer action;
// history_saved is not a NoticeMessage or an external delivery acknowledgement.
const pluginCleanupOutboxSchema = pluginRemoveLinkEffectsSchema + `
CREATE TABLE IF NOT EXISTS plugin_download_file_events(
 file_event_id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,generation INTEGER NOT NULL,sealed BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS plugin_cleanup_outbox_handoffs(
 instance_id TEXT NOT NULL,generation INTEGER NOT NULL,token TEXT NOT NULL,sealed BLOB NOT NULL,
 PRIMARY KEY(instance_id,generation,token));`

var pluginFileEventIDPattern = regexp.MustCompile(`^(pfe|sfe|hfe)_[a-f0-9]{64}$`)

type pluginDownloadFileEvent struct {
	Version                int
	ID, Instance, EffectID string
	Generation, Revision   int64
	Hash                   string `json:",omitempty"`
	Src                    string
}

type pluginCleanupHandoff struct {
	Version                          int
	Identity, Kind, Stage, Reference string
	Effect                           []byte
	PublicDigest                     []byte
	Notice                           *removeLinkNoticeProof `json:",omitempty"`
}

type pluginCleanupAuditCursor struct {
	After, Through int64
}

type pluginCleanupOutboxRow struct {
	Instance, Token, Created string
	Generation               int64
	Sealed                   []byte
}

func validateDownloadFileDomainEvent(in DomainEvent) (DomainEvent, error) {
	invalid := func() (DomainEvent, error) {
		return in, bad("INVALID_DOMAIN_EVENT", "域事件契约无效，业务结果未提交")
	}
	if in.Type != "downloadfile.deleted" || in.SchemaVersion != 1 || in.Sequence != 0 || !domainEventIDPattern.MatchString(in.EventID) || !pluginFileEventIDPattern.MatchString(in.FileEventID) {
		return invalid()
	}
	if in.WorkflowID != "" || in.JobID != "" || in.MediaID != "" || in.IntentID != "" || in.NativeDownloadID != "" || in.AssetID != "" || in.SiteID != "" || in.SubscriptionID != "" || in.CausationID != "" || in.ParentJobID != "" {
		return invalid()
	}
	if in.Purpose == "" {
		in.Purpose = "file_deletion"
	}
	if in.Origin == "" {
		in.Origin = "core"
	}
	if in.SubjectID == "" {
		in.SubjectID = in.FileEventID
	}
	if in.SubjectKind == "" {
		in.SubjectKind = "file_event"
	}
	if in.Purpose != "file_deletion" || in.Origin != "core" || in.SubjectID != in.FileEventID || in.SubjectKind != "file_event" || in.EventID != domainEventID(in.Type, in.FileEventID) {
		return invalid()
	}
	if in.OccurredAt == "" {
		in.OccurredAt = now()
	}
	at, err := time.Parse(time.RFC3339Nano, in.OccurredAt)
	if err != nil {
		return invalid()
	}
	in.OccurredAt = at.UTC().Format(time.RFC3339Nano)
	return in, nil
}

// A public opaque reference cannot introduce a path or a torrent hash. Only an
// encrypted, previously authorized source effect supplies its one-field body.
func validateDownloadFileEventBackingTx(ctx context.Context, tx *sql.Tx, event DomainEvent) error {
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT file_event_id FROM plugin_download_file_events WHERE file_event_id=?`, event.FileEventID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return bad("FILE_EVENT_UNCOMMITTED", "文件删除事件缺少已提交的私有事实")
		}
		return err
	}
	return nil
}

func (p *PluginStore) downloadFileEvent(ctx context.Context, q pluginRemoveLinkJournalReader, eventID string) (pluginDownloadFileEvent, error) {
	var out pluginDownloadFileEvent
	if strings.HasPrefix(eventID, "hfe_") {
		return p.historyFileEvent(ctx, q, eventID)
	}
	if strings.HasPrefix(eventID, "sfe_") {
		return p.scrapeDownloadFileEvent(ctx, q, eventID)
	}
	if !pluginFileEventIDPattern.MatchString(eventID) {
		return out, cleanuphost.ErrUnsafe
	}
	var instance string
	var generation int64
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT instance_id,generation,sealed FROM plugin_download_file_events WHERE file_event_id=?`, eventID).Scan(&instance, &generation, &sealed); err != nil {
		return out, err
	}
	raw, err := p.unseal("download_file_event", instance, eventID, sealed)
	if err != nil {
		return out, err
	}
	if cleanupJournalStrict(raw, &out) != nil || out.Version != 1 || out.Hash != "" || out.ID != eventID || out.Instance != instance || !pluginStorageID.MatchString(instance) || out.Generation != generation || generation < 1 || out.Revision < 1 || out.EffectID == "" || !importPath(out.Src) {
		return out, cleanuphost.ErrUnsafe
	}
	return out, nil
}

func (a *App) committedCleanupOutbox(ctx context.Context, tx *sql.Tx, row pluginCleanupOutboxRow) (*pluginRemoveLinkEffects, cleanuphost.Effect, pluginRemoveLinkPrivateOutbox, error) {
	return a.pluginStore.committedCleanupOutbox(ctx, tx, row)
}

func (p *PluginStore) committedCleanupOutbox(ctx context.Context, q pluginRemoveLinkJournalReader, row pluginCleanupOutboxRow) (*pluginRemoveLinkEffects, cleanuphost.Effect, pluginRemoveLinkPrivateOutbox, error) {
	var x cleanuphost.Effect
	var box pluginRemoveLinkPrivateOutbox
	if !pluginStorageID.MatchString(row.Instance) || row.Generation < 1 || !pluginStorageDigest.MatchString(row.Token) {
		return nil, x, box, cleanuphost.ErrUnsafe
	}
	e := &pluginRemoveLinkEffects{j: &pluginRemoveLinkJournal{p: p, instance: row.Instance, generation: row.Generation}}
	raw, err := p.unseal("cleanup_journal", row.Instance, e.aad("outbox", row.Token), row.Sealed)
	if err != nil {
		return nil, x, box, err
	}
	var encoded struct {
		Effect  cleanuphost.Effect
		Payload []byte
	}
	if cleanupJournalStrict(raw, &box) != nil || (box.Version != 1 && box.Version != 2 && box.Version != 3) || cleanupJournalStrict(box.Effect, &encoded) != nil {
		return nil, x, box, cleanuphost.ErrUnsafe
	}
	x = encoded.Effect
	x.Payload = encoded.Payload
	if x.Binding.InstanceID != row.Instance || x.Binding.Generation != row.Generation || x.Binding.Revision < 1 || e.token(x.ID) != row.Token || x.State != "" || x.Error != "" || x.SourceError != "" || x.Result {
		return nil, x, box, cleanuphost.ErrUnsafe
	}
	_, original, err := e.identity(x)
	if err != nil || !bytes.Equal(original, box.Effect) {
		return nil, x, box, cleanuphost.ErrUnsafe
	}
	// The producer's SQL receipt proves that this exact intent was authorized
	// and committed; possessing a raw pending row is not such proof.
	receipt, err := e.receipt(ctx, q, x)
	if err != nil || !receipt.Result {
		if err == nil {
			err = cleanuphost.ErrUnsafe
		}
		return nil, x, box, err
	}
	if box.Version >= 2 {
		if x.Kind != "notification" || box.Notice == nil || row.Created != box.Notice.CreatedAt {
			return nil, x, box, cleanuphost.ErrUnsafe
		}
	} else {
		if box.Notice != nil || receipt.NoticeAcceptanceSHA != "" {
			return nil, x, box, cleanuphost.ErrUnsafe
		}
		var routes int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_notice_routes WHERE instance_id=? AND generation=? AND source_token=?`, row.Instance, row.Generation, row.Token).Scan(&routes); err != nil {
			return nil, x, box, err
		}
		if routes != 0 {
			return nil, x, box, cleanuphost.ErrUnsafe
		}
	}
	return e, x, box, nil
}

func cleanupNoticeDigest(e *pluginRemoveLinkEffects, n Notification) []byte {
	n.Read = false // Marking a message read never invalidates its original fact.
	raw, _ := json.Marshal(n)
	return e.j.mac("notice-history-value", raw)
}

func putCleanupNoticeTx(ctx context.Context, tx *sql.Tx, n Notification) error {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='notification' AND id=?`, n.ID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return txPut(ctx, tx, "notification", n.ID, n)
	}
	if err != nil {
		return err
	}
	var old Notification
	if cleanupJournalStrict(raw, &old) != nil {
		return cleanuphost.ErrUnsafe
	}
	old.Read = false
	if old != n {
		return cleanuphost.ErrUnsafe
	}
	return nil // Preserve an existing matching message's read state and time.
}

// The queue's indexed existence join is not treated as cryptographic evidence.
// Audit bounded pages on startup/each dispatcher cycle; corruption blocks this
// outbox without preventing unrelated already queued plugin batches from drain.
func (a *App) auditRemoveLinkHandoffs(ctx context.Context, cursor pluginCleanupAuditCursor, limit int) (pluginCleanupAuditCursor, error) {
	if cursor.After < 0 || cursor.Through < cursor.After || limit < 1 || limit > 200 {
		return cursor, cleanuphost.ErrUnsafe
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return cursor, err
	}
	defer tx.Rollback()
	// New transfers cannot extend an in-progress audit round indefinitely.
	if cursor.Through == 0 {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(rowid),0) FROM plugin_cleanup_outbox_handoffs`).Scan(&cursor.Through); err != nil {
			return cursor, err
		}
	}
	type item struct {
		sequence int64
		row      pluginCleanupOutboxRow
		sealed   []byte
	}
	rows, err := tx.QueryContext(ctx, `SELECT h.rowid,h.instance_id,h.generation,h.token,h.sealed,o.sealed,o.created_at FROM plugin_cleanup_outbox_handoffs h LEFT JOIN plugin_cleanup_private_outbox o ON o.instance_id=h.instance_id AND o.generation=h.generation AND o.token=h.token WHERE h.rowid>? AND h.rowid<=? ORDER BY h.rowid LIMIT ?`, cursor.After, cursor.Through, limit)
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
		e, x, box, err := a.committedCleanupOutbox(ctx, tx, v.row)
		if err != nil {
			return cursor, err
		}
		raw, err := a.pluginStore.unseal("cleanup_journal", v.row.Instance, e.aad("handoff", v.row.Token), v.sealed)
		if err != nil {
			return cursor, err
		}
		var h pluginCleanupHandoff
		identity, original, err := e.identity(x)
		if err != nil || cleanupJournalStrict(raw, &h) != nil || h.Version != 1 || h.Identity != identity || h.Kind != x.Kind || !bytes.Equal(h.Effect, original) || h.Stage != box.State {
			return cursor, cleanuphost.ErrUnsafe
		}
		switch x.Kind {
		case "download_file_deleted":
			wantID := "pfe_" + hex.EncodeToString(e.j.mac("file-event", []byte(v.row.Token)))
			if h.Stage != "published" || h.Reference != wantID || len(h.PublicDigest) != 0 || h.Notice != nil {
				return cursor, cleanuphost.ErrUnsafe
			}
			private, err := a.pluginStore.downloadFileEvent(ctx, tx, wantID)
			if err != nil || private.Src != x.Path || private.EffectID != x.ID || private.Generation != v.row.Generation || private.Revision != x.Binding.Revision || private.Instance != v.row.Instance {
				return cursor, cleanuphost.ErrUnsafe
			}
			var eventRaw []byte
			if err := tx.QueryRowContext(ctx, `SELECT payload FROM domain_events WHERE event_id=?`, domainEventID("downloadfile.deleted", wantID)).Scan(&eventRaw); err != nil {
				return cursor, err
			}
			var got DomainEvent
			want, err := validateDomainEvent(DomainEvent{Type: "downloadfile.deleted", EventID: domainEventID("downloadfile.deleted", wantID), FileEventID: wantID, OccurredAt: v.row.Created})
			if err != nil || cleanupJournalStrict(eventRaw, &got) != nil || got != want {
				return cursor, cleanuphost.ErrUnsafe
			}
		case "notification":
			wantID := "cleanup_notice_" + hex.EncodeToString(e.j.mac("notice-history", []byte(v.row.Token)))
			if h.Stage != "history_saved" || h.Reference != wantID {
				return cursor, cleanuphost.ErrUnsafe
			}
			var noticeRaw []byte
			if err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='notification' AND id=?`, wantID).Scan(&noticeRaw); err != nil {
				return cursor, err
			}
			var notice Notification
			if cleanupJournalStrict(noticeRaw, &notice) != nil || notice.ID != wantID || !hmac.Equal(h.PublicDigest, cleanupNoticeDigest(e, notice)) {
				return cursor, cleanuphost.ErrUnsafe
			}
			if box.Version >= 2 {
				if h.Notice == nil {
					return cursor, cleanuphost.ErrUnsafe
				}
				if h.Notice.RouteID != a.pluginStore.noticeID("route", v.row.Instance, v.row.Generation, v.row.Token) {
					return cursor, cleanuphost.ErrUnsafe
				}
				if _, _, err := a.pluginStore.readNoticeRoute(ctx, tx, h.Notice.RouteID); err != nil {
					return cursor, err
				}
			} else if h.Notice != nil {
				return cursor, cleanuphost.ErrUnsafe
			}
		default:
			return cursor, cleanuphost.ErrUnsupported
		}
		cursor.After = v.sequence
	}
	if len(items) < limit || cursor.After >= cursor.Through {
		cursor = pluginCleanupAuditCursor{}
	}
	return cursor, nil
}

func (a *App) dispatchRemoveLinkOutboxes(ctx context.Context, limit int) error {
	if limit < 1 || limit > 200 {
		return cleanuphost.ErrUnsafe
	}
	for range limit {
		found, err := a.transferRemoveLinkOutbox(ctx)
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
func (a *App) transferRemoveLinkOutbox(ctx context.Context) (bool, error) {
	return a.transferRemoveLinkOutboxFor(ctx, "", 0, "")
}

// A synchronous source barrier must transfer only its own accepted token, not
// whichever unrelated pending producer happens to be oldest in the queue.
func (a *App) transferRemoveLinkOutboxFor(ctx context.Context, wantedInstance string, wantedGeneration int64, wantedToken string) (bool, error) {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var instance, token, created string
	var generation int64
	var sealed []byte
	query := `SELECT o.instance_id,o.generation,o.token,o.sealed,o.created_at
FROM plugin_cleanup_private_outbox o LEFT JOIN plugin_cleanup_outbox_handoffs h
ON h.instance_id=o.instance_id AND h.generation=o.generation AND h.token=o.token
WHERE h.token IS NULL`
	var args []any
	if wantedInstance != "" {
		query += ` AND o.instance_id=? AND o.generation=? AND o.token=?`
		args = []any{wantedInstance, wantedGeneration, wantedToken}
	}
	query += ` ORDER BY o.rowid LIMIT 1`
	err = tx.QueryRowContext(ctx, query, args...).Scan(&instance, &generation, &token, &sealed, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := time.Parse(time.RFC3339Nano, created); err != nil {
		return true, cleanuphost.ErrUnsafe
	}
	e, x, box, err := a.committedCleanupOutbox(ctx, tx, pluginCleanupOutboxRow{Instance: instance, Token: token, Created: created, Generation: generation, Sealed: sealed})
	if err != nil {
		return true, err
	}
	if box.State != "pending" {
		return true, cleanuphost.ErrUnsafe
	}
	identity, original, err := e.identity(x)
	if err != nil {
		return true, err
	}
	handoff := pluginCleanupHandoff{Version: 1, Identity: identity, Kind: x.Kind, Effect: original}
	switch x.Kind {
	case "download_file_deleted":
		var data struct {
			Src string `json:"src"`
		}
		if cleanupJournalStrict(x.Payload, &data) != nil || data.Src != x.Path || !importPath(data.Src) {
			return true, cleanuphost.ErrUnsafe
		}
		fileID := "pfe_" + hex.EncodeToString(e.j.mac("file-event", []byte(token)))
		private := pluginDownloadFileEvent{Version: 1, ID: fileID, Instance: instance, Generation: generation, Revision: x.Binding.Revision, EffectID: x.ID, Src: data.Src}
		privateRaw, err := json.Marshal(private)
		if err != nil || len(privateRaw) > pluginValueLimit {
			return true, cleanuphost.ErrUnsafe
		}
		privateSealed, err := a.pluginStore.seal("download_file_event", instance, fileID, privateRaw)
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
		if cleanupJournalStrict(x.Payload, &data) != nil {
			return true, cleanuphost.ErrUnsafe
		}
		// RemoveLink calls post_message with SiteMessage and no explicit user,
		// source or channel. save_history defaults true and precedes routing.
		settings := a.Settings()
		noticeID := "cleanup_notice_" + hex.EncodeToString(e.j.mac("notice-history", []byte(token)))
		n := Notification{ID: noticeID, Title: cleanWithSettings(data.Title, settings), Message: cleanWithSettings(data.Text, settings), MType: "站点", Link: "#/plugins?tab=installed&id=RemoveLink", CreatedAt: created}
		if box.Version >= 2 {
			accepted, err := removeLinkAcceptedHistory(e, token, box, x)
			if err != nil {
				return true, err
			}
			n = accepted
		}
		if err = putCleanupNoticeTx(ctx, tx, n); err != nil {
			return true, err
		}
		handoff.PublicDigest = cleanupNoticeDigest(e, n)
		handoff.Stage, handoff.Reference = "history_saved", noticeID
		if box.Version >= 2 {
			handoff.Notice, err = a.pluginStore.initRemoveLinkNoticeTx(ctx, tx, e, x, token, box, n)
			if err != nil {
				return true, err
			}
		}
		box.State = "history_saved" // Router completion and delivery have separate receipts.
	default:
		return true, cleanuphost.ErrUnsupported
	}
	handoffRaw, err := json.Marshal(handoff)
	if err != nil {
		return true, err
	}
	handoffSealed, err := a.pluginStore.seal("cleanup_journal", instance, e.aad("handoff", token), handoffRaw)
	if err != nil {
		return true, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_cleanup_outbox_handoffs VALUES(?,?,?,?)`, instance, generation, token, handoffSealed); err != nil {
		return true, err
	}
	boxRaw, err := json.Marshal(box)
	if err != nil {
		return true, err
	}
	boxSealed, err := a.pluginStore.seal("cleanup_journal", instance, e.aad("outbox", token), boxRaw)
	if err != nil {
		return true, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE plugin_cleanup_private_outbox SET sealed=? WHERE instance_id=? AND generation=? AND token=?`, boxSealed, instance, generation, token); err != nil {
		return true, err
	}
	return true, a.pluginStore.commitNoticeTx(tx, "remove_link_handoff")
}

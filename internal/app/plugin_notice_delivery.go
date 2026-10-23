package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"time"

	"light-media/internal/notice"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

var pluginNoticeReferencePattern = regexp.MustCompile(`^pnm_[a-f0-9]{64}$`)

func validateNoticeMessageDomainEvent(in DomainEvent) (DomainEvent, error) {
	invalid := func() (DomainEvent, error) { return in, bad("INVALID_DOMAIN_EVENT", "通知事件契约无效") }
	if in.SchemaVersion != 1 || in.Sequence != 0 || in.Type != "notice.message" || !pluginNoticeReferencePattern.MatchString(in.NoticeID) || in.EventID != domainEventID("notice.message", in.NoticeID) {
		return invalid()
	}
	if in.JobID != "" || in.WorkflowID != "" || in.MediaID != "" || in.IntentID != "" || in.NativeDownloadID != "" || in.FileEventID != "" || in.AssetID != "" || in.SiteID != "" || in.SubscriptionID != "" || in.CausationID != "" || in.ParentJobID != "" {
		return invalid()
	}
	if in.Purpose == "" {
		in.Purpose = "notification"
	}
	if in.SubjectID == "" {
		in.SubjectID = in.NoticeID
	}
	if in.SubjectKind == "" {
		in.SubjectKind = "notice"
	}
	if in.Origin == "" {
		in.Origin = "core"
	}
	if in.Purpose != "notification" || in.SubjectID != in.NoticeID || in.SubjectKind != "notice" || in.Origin != "core" {
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
func validateNoticeMessageBackingTx(ctx context.Context, tx *sql.Tx, event DomainEvent) error {
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM plugin_notice_publications WHERE id=?`, event.NoticeID).Scan(&id); err != nil {
		return bad("NOTICE_EVENT_UNCOMMITTED", "通知事件缺少已提交私有事实")
	}
	return nil
}
func (a *App) publishPluginNoticeTx(ctx context.Context, tx *sql.Tx, r pluginNoticeRoute, ordinal int, data notice.Object) (any, error) {
	if err := (*PluginStore)(nil).checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return nil, err
	}
	id := "pnm_" + a.pluginStore.noticeID("publication", r.ID, ordinal)
	raw, err := cleanupJournalJSON(data)
	if err != nil {
		return nil, err
	}
	pub := pluginNoticePublication{Version: 1, ID: id, RouteID: r.ID, Ordinal: ordinal, Data: raw}
	sealed, err := a.pluginStore.noticeSeal("publication", id, pub)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_notice_publications VALUES(?,?,?,?)`, id, r.ID, ordinal, sealed); err != nil {
		return nil, err
	}
	if err = recordDomainEventTx(ctx, tx, DomainEvent{Type: "notice.message", EventID: domainEventID("notice.message", id), NoticeID: id, OccurredAt: r.CreatedAt}); err != nil {
		return nil, err
	}
	return map[string]any{"notice_id": id}, nil
}

// Consumer access returns only an authenticated original event body. Possessing
// its public opaque ID is not a grant to send, pick a URL or use credentials.
func (p *PluginStore) noticeMessage(ctx context.Context, q pluginRemoveLinkJournalReader, id string) (notice.Object, error) {
	if !pluginNoticeReferencePattern.MatchString(id) {
		return nil, seedhost.ErrUnsafe
	}
	var routeID string
	var ordinal int
	var sealed []byte
	if err := q.QueryRowContext(ctx, `SELECT route_id,ordinal,sealed FROM plugin_notice_publications WHERE id=?`, id).Scan(&routeID, &ordinal, &sealed); err != nil {
		return nil, err
	}
	var pub pluginNoticePublication
	if err := p.noticeUnseal("publication", id, sealed, &pub); err != nil {
		return nil, err
	}
	if pub.Version != 1 || pub.ID != id || pub.RouteID != routeID || pub.Ordinal != ordinal || id != "pnm_"+p.noticeID("publication", routeID, ordinal) {
		return nil, seedhost.ErrUnsafe
	}
	_, steps, err := p.readNoticeRoute(ctx, q, routeID)
	if err != nil {
		return nil, err
	}
	if ordinal < 0 || ordinal >= len(steps) || steps[ordinal].Kind != "publish" || steps[ordinal].InputSHA != tokenHash(string(pub.Data)) {
		return nil, seedhost.ErrUnsafe
	}
	value, err := noticeValue(steps[ordinal].Output)
	if err != nil {
		return nil, err
	}
	out, ok := value.(map[string]any)
	if !ok || out["notice_id"] != id {
		return nil, seedhost.ErrUnsafe
	}
	var data notice.Object
	if cleanupJournalStrict(pub.Data, &data) != nil {
		return nil, seedhost.ErrUnsafe
	}
	return data, nil
}
func noticeTruth(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case []any:
		return len(x) != 0
	case map[string]any:
		return len(x) != 0
	case json.Number:
		n, _, err := big.ParseFloat(x.String(), 10, 256, big.ToNearestEven)
		return err != nil || n.Sign() != 0
	}
	return true
}
func noticeQueueInitialSHA(q pluginNoticeQueueEntry) string {
	q.State = "queued"
	q.AttemptAt = ""
	q.FinishedAt = ""
	q.Modules = 0
	q.ErrorCode = ""
	return tokenHash(string(mustCleanSeedJSON(q)))
}
func (a *App) acceptPluginMessageTx(ctx context.Context, tx *sql.Tx, r pluginNoticeRoute, ordinal int, call notice.Call) (any, error) {
	if err := (*PluginStore)(nil).checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return nil, err
	}
	if len(call.Args) != 1 || call.Args[0] != "post_message" {
		return nil, seedhost.ErrUnsafe
	}
	periods, err := a.pluginStore.currentNoticePeriods()
	if err != nil {
		return nil, err
	}
	at := a.pluginStore.noticeNow()
	// Source MessageHelper uses datetime.now(), the process-local clock, rather
	// than settings.TZ used by individual plugins' scheduled jobs.
	direct := noticeTruth(call.Kwargs["immediately"]) || notice.Allowed(periods, at)
	var pending int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_message_queue WHERE state IN ('queued','intent')`).Scan(&pending); err != nil {
		return nil, err
	}
	if pending >= pluginNoticePendingLimit {
		return nil, bad("NOTICE_QUEUE_FULL", "通知队列已满，未接受本次发送")
	}
	q := pluginNoticeQueueEntry{Version: 1, ID: a.pluginStore.noticeID("queue", r.ID, ordinal), RouteID: r.ID, Ordinal: ordinal, State: "queued", Direct: direct, Call: call, CreatedAt: at.UTC().Format(time.RFC3339Nano)}
	sealed, err := a.pluginStore.noticeSeal("queue", q.ID, q)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_message_queue(id,route_id,ordinal,state,sealed) VALUES(?,?,?,?,?)`, q.ID, r.ID, ordinal, q.State, sealed); err != nil {
		return nil, err
	}
	return map[string]any{"id": q.ID, "direct": q.Direct, "accepted_sha": noticeQueueInitialSHA(q)}, nil
}
func (p *PluginStore) readPluginMessage(ctx context.Context, qry pluginRemoveLinkJournalReader, id string) (pluginNoticeQueueEntry, error) {
	var q pluginNoticeQueueEntry
	if !pluginStorageDigest.MatchString(id) {
		return q, seedhost.ErrUnsafe
	}
	var routeID, state string
	var ordinal int
	var sealed []byte
	if err := qry.QueryRowContext(ctx, `SELECT route_id,ordinal,state,sealed FROM plugin_message_queue WHERE id=?`, id).Scan(&routeID, &ordinal, &state, &sealed); err != nil {
		return q, err
	}
	if err := p.noticeUnseal("queue", id, sealed, &q); err != nil {
		return q, err
	}
	if q.Version != 1 || q.ID != id || q.RouteID != routeID || q.Ordinal != ordinal || q.State != state || q.ID != p.noticeID("queue", routeID, ordinal) {
		return q, seedhost.ErrUnsafe
	}
	switch q.State {
	case "queued", "intent", "consumed", "unknown", "revoked":
	default:
		return q, seedhost.ErrUnsafe
	}
	if q.State == "revoked" && (q.ErrorCode != "NOTICE_AUTH_REVOKED" || q.Modules != 0 || q.FinishedAt == "") {
		return q, seedhost.ErrUnsafe
	}
	route, steps, err := p.readNoticeRoute(ctx, qry, routeID)
	if err != nil {
		return q, err
	}
	if q.State == "revoked" && (route.Version != 2 || route.BusinessV2 == nil || route.BusinessV2.Binding.Kind != "site_message") {
		return q, seedhost.ErrUnsafe
	}
	if ordinal < 0 || ordinal >= len(steps) || steps[ordinal].Kind != "queue" || steps[ordinal].InputSHA != tokenHash(string(mustCleanSeedJSON(q.Call))) {
		return q, seedhost.ErrUnsafe
	}
	value, err := noticeValue(steps[ordinal].Output)
	if err != nil {
		return q, err
	}
	out, ok := value.(map[string]any)
	if !ok || out["id"] != id || out["direct"] != q.Direct || out["accepted_sha"] != noticeQueueInitialSHA(q) {
		return q, seedhost.ErrUnsafe
	}
	return q, nil
}
func (p *PluginStore) savePluginMessageTx(ctx context.Context, tx *sql.Tx, q pluginNoticeQueueEntry) error {
	if err := (*PluginStore)(nil).checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return err
	}
	sealed, err := p.noticeSeal("queue", q.ID, q)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE plugin_message_queue SET state=?,sealed=? WHERE id=? AND route_id=? AND ordinal=?`, q.State, sealed, q.ID, q.RouteID, q.Ordinal)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return seedhost.ErrUnsafe
	}
	return nil
}

func (p *PluginStore) commitPluginMessage(ctx context.Context, tx *sql.Tx, operation string, expected pluginNoticeQueueEntry) error {
	if err := (*PluginStore)(nil).checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return err
	}
	if err := p.commitNoticeTx(tx, operation); err != nil {
		_ = tx.Rollback()
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		current, readErr := p.readPluginMessage(readCtx, p.store.DB, expected.ID)
		if readErr != nil || !bytes.Equal(mustCleanSeedJSON(current), mustCleanSeedJSON(expected)) {
			return seedhost.ErrUnknown
		}
	}
	return nil
}

// Actual registered MessageChain modules consume the authenticated queue call.
// Their private producers freeze recipients; neither queue JSON nor an SDK can
// select a transport or acquire a native owner grant.
func (a *App) sendPluginMessageModules(ctx context.Context, q pluginNoticeQueueEntry) (int, error) {
	if err := (*PluginStore)(nil).checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return 0, err
	}
	return a.larkMessageModules(ctx, q)
}
func (a *App) attemptPluginMessage(ctx context.Context, id string) error {
	if err := (*PluginStore)(nil).checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return err
	}
	release, err := a.pluginStore.noticeLock(ctx, "queue/"+id)
	if err != nil {
		return seedhost.ErrUnknown
	}
	defer release()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return seedhost.ErrUnknown
	}
	defer tx.Rollback()
	q, err := a.pluginStore.readPluginMessage(ctx, tx, id)
	if err != nil {
		return seedhost.ErrUnknown
	}
	if q.State == "revoked" {
		if q.Direct {
			return siteMessageAuthorityRevoked{}
		}
		return nil
	}
	if q.State == "consumed" || q.State == "unknown" {
		return nil
	}
	if q.State == "intent" {
		// A prior process may have sent. Consume as unknown and keep its durable
		// fact without rerunning the callback or blocking later FIFO entries.
		q.State = "unknown"
		q.ErrorCode = "NOTICE_SEND_UNCONFIRMED"
		q.FinishedAt = now()
		if err = a.pluginStore.savePluginMessageTx(ctx, tx, q); err != nil {
			return seedhost.ErrUnknown
		}
		if err = a.pluginStore.commitPluginMessage(ctx, tx, "queue_unknown", q); err != nil {
			return seedhost.ErrUnknown
		}
		return nil
	}
	// Current authority is checked in this same transaction before minting the
	// durable send intent. Existing unknown intents are never re-sent.
	if err = a.requireSiteMessageQueueAuthorityTx(ctx, tx, q); err != nil {
		if !siteMessageIsAuthorityRevoked(err) {
			return err
		}
		revokedErr := err
		q.State = "revoked"
		q.ErrorCode = "NOTICE_AUTH_REVOKED"
		q.Modules = 0
		q.FinishedAt = now()
		if err = a.pluginStore.savePluginMessageTx(ctx, tx, q); err != nil {
			return seedhost.ErrUnknown
		}
		if err = a.pluginStore.commitPluginMessage(ctx, tx, "queue_revoked", q); err != nil {
			return seedhost.ErrUnknown
		}
		if q.Direct {
			return revokedErr
		}
		return nil
	}
	q.State = "intent"
	q.AttemptAt = a.pluginStore.noticeNow().UTC().Format(time.RFC3339Nano)
	intent, err := a.pluginStore.writePluginMessageIntentTx(ctx, tx, q)
	if err != nil {
		return seedhost.ErrUnknown
	}
	if err = a.pluginStore.commitPluginMessageRow(ctx, tx, "queue_intent", intent); err != nil {
		return seedhost.ErrUnknown
	}
	// This is deliberately after Commit and outside lifecycle/SQL locks. Only a
	// trusted native host may install send; worker JSON cannot supply a callback.
	send := a.pluginStore.notices.send
	if send == nil {
		send = a.sendPluginMessageModules
	}
	var copied pluginNoticeQueueEntry
	if cleanupJournalStrict(mustCleanSeedJSON(q), &copied) != nil {
		return seedhost.ErrUnknown
	}
	delete(copied.Call.Kwargs, "immediately")
	// A revocation may have committed after intent acceptance. Recheck at the
	// dispatch boundary, outside the completed transaction and lifecycle locks.
	// An authenticated revocation before invocation is a known not-sent result;
	// all uncertain evidence retains the intent and must never cause a resend.
	var revokedErr error
	if err = a.requireSiteMessageQueueAuthority(ctx, copied); err != nil {
		if !siteMessageIsAuthorityRevoked(err) {
			return seedhost.ErrUnknown
		}
		revokedErr = err
	}
	var modules int
	var sendErr error
	if revokedErr == nil {
		modules, sendErr = invokePluginNoticeSender(ctx, send, copied)
	}
	q.Modules = modules
	q.FinishedAt = a.pluginStore.noticeNow().UTC().Format(time.RFC3339Nano)
	q.State = "consumed"
	if sendErr != nil {
		q.ErrorCode = "NOTICE_MODULE_FAILED"
		if errors.Is(sendErr, seedhost.ErrUnknown) {
			q.State = "unknown"
			q.ErrorCode = "NOTICE_SEND_UNCONFIRMED"
		}
	}
	if revokedErr != nil {
		q.State = "revoked"
		q.ErrorCode = "NOTICE_AUTH_REVOKED"
		q.Modules = 0
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	finish, err := a.Store.DB.BeginTx(finishCtx, nil)
	if err != nil {
		return seedhost.ErrUnknown
	}
	defer finish.Rollback()
	// The external result belongs to this exact already authenticated intent.
	// Re-parsing its full provider/route here would spend the bounded receipt
	// deadline on unrelated business data and could discard a known response.
	result, err := a.pluginStore.finishPluginMessageRowTx(finishCtx, finish, intent, q)
	if err != nil {
		return seedhost.ErrUnknown
	}
	if err = a.pluginStore.commitPluginMessageRow(finishCtx, finish, "queue_finish", result); err != nil {
		return seedhost.ErrUnknown
	}
	if revokedErr != nil && q.Direct {
		return revokedErr
	}
	return nil // Original MessageQueue._send consumes callback errors.
}

func invokePluginNoticeSender(ctx context.Context, send func(context.Context, pluginNoticeQueueEntry) (int, error), q pluginNoticeQueueEntry) (modules int, err error) {
	if err := (*PluginStore)(nil).checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return 0, err
	}
	defer func() {
		if recover() != nil {
			modules = 0
			err = seedhost.ErrUnknown
		}
	}()
	return send(ctx, q)
}
func (a *App) drainPluginMessages(ctx context.Context, limit int) error {
	if err := (*PluginStore)(nil).checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return err
	}
	if limit < 1 || limit > 200 {
		return seedhost.ErrUnsafe
	}
	a.pluginStore.notices.drain.Lock()
	defer a.pluginStore.notices.drain.Unlock()
	after := a.pluginStore.notices.queueAfter
	for i := 0; i < limit; i++ {
		periods, err := a.pluginStore.noticePeriods(ctx)
		if err != nil {
			return err
		}
		if !notice.Allowed(periods, a.pluginStore.noticeNow()) {
			return nil
		}
		var id string
		var seq int64
		err = a.Store.DB.QueryRowContext(ctx, `SELECT seq,id FROM plugin_message_queue WHERE state IN ('queued','intent') AND seq>? ORDER BY seq LIMIT 1`, after).Scan(&seq, &id)
		if errors.Is(err, sql.ErrNoRows) {
			a.pluginStore.notices.queueAfter = 0
			return nil
		}
		if err != nil {
			return err
		}
		entry, err := a.pluginStore.readPluginMessage(ctx, a.Store.DB, id)
		if err != nil {
			return err
		}
		if entry.Direct {
			after = seq
			a.pluginStore.notices.queueAfter = after
			continue
		} // direct calls belong to the source continuation, not FIFO
		if err = a.attemptPluginMessage(ctx, id); err != nil {
			return err
		}
		after = seq
		a.pluginStore.notices.queueAfter = after
	}
	return nil
}

func (a *App) pluginNoticeWorker(ctx context.Context) {
	// The original monitor checks once before its first ten-second wait.
	if err := a.drainPluginMessages(ctx, 200); err != nil && ctx.Err() == nil {
		a.log("ERROR", "plugins", "notice_queue_failed", "通知队列未完成，已保留发送记录", "", "")
	}
	routes := time.NewTicker(time.Second)
	queue := time.NewTicker(10 * time.Second)
	defer routes.Stop()
	defer queue.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-routes.C:
			if err := a.resumePluginNoticeRoutes(ctx, 200); err != nil && ctx.Err() == nil {
				a.log("ERROR", "plugins", "notice_route_failed", "通知路由未完成，已保留原始阶段", "", "")
			}
		case <-queue.C:
			if err := a.drainPluginMessages(ctx, 200); err != nil && ctx.Err() == nil {
				a.log("ERROR", "plugins", "notice_queue_failed", "通知队列未完成，已保留发送记录", "", "")
			}
		}
	}
}

package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"light-media/internal/notice"
	seed "light-media/internal/plugins/cleaninvalidseed"
	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
)

type pluginNoticeDriver struct {
	a       *App
	id      string
	cursor  int
	failure *removeLinkSourceFailure
}

func noticeValue(raw json.RawMessage) (any, error) {
	var v any
	if cleanupJournalStrict(raw, &v) != nil {
		return nil, seedhost.ErrUnsafe
	}
	return v, nil
}
func sourceNoticeError(err error) string {
	if err == nil {
		return ""
	}
	var e notice.SourceError
	if errors.As(err, &e) {
		return e.Class
	}
	var se *seed.SourceError
	if errors.As(err, &se) {
		return se.Type
	}
	return "RuntimeError"
}
func (d *pluginNoticeDriver) step(ctx context.Context, kind string, input any, apply func(*sql.Tx, pluginNoticeRoute, int) (any, error)) (any, error) {
	raw, err := cleanupJournalJSON(input)
	if err != nil {
		return nil, err
	}
	inputSHA := tokenHash(string(raw))
	ordinal := d.cursor
	tx, err := d.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, seedhost.ErrUnknown
	}
	defer tx.Rollback()
	r, steps, err := d.a.pluginStore.readNoticeRoute(ctx, tx, d.id)
	if err != nil {
		return nil, seedhost.ErrUnknown
	}
	if err = d.a.pluginStore.requireAutoSignInNoticeExecutionTx(ctx, tx, r); err != nil {
		return nil, err
	}
	if ordinal < r.Count {
		s := steps[ordinal]
		if s.Kind != kind || s.InputSHA != inputSHA {
			return nil, seedhost.ErrUnknown
		}
		value, err := noticeValue(s.Output)
		if err == nil {
			d.cursor++
		}
		return value, err
	}
	if ordinal != r.Count || r.Complete || ordinal >= pluginNoticeStepLimit {
		return nil, seedhost.ErrUnknown
	}
	if err = d.a.requireNoticeV2ExecutionTx(ctx, tx, r); err != nil {
		return nil, err
	}
	value, err := apply(tx, r, ordinal)
	if err != nil {
		d.failure = &removeLinkSourceFailure{Kind: kind, Ordinal: ordinal, InputSHA: inputSHA}
		return nil, err
	}
	output, err := cleanupJournalJSON(value)
	if err != nil {
		return nil, err
	}
	s := pluginNoticeStep{Version: 1, RouteID: r.ID, Ordinal: ordinal, Kind: kind, InputSHA: inputSHA, PreviousSHA: r.LastSHA, Output: output}
	sealed, err := d.a.pluginStore.noticeSeal(fmt.Sprintf("step/%d", ordinal), r.ID, s)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_notice_route_steps VALUES(?,?,?)`, r.ID, ordinal, sealed); err != nil {
		return nil, err
	}
	r.Count++
	r.LastSHA = tokenHash(string(mustCleanSeedJSON(s)))
	if err = d.a.pluginStore.saveNoticeRouteTx(ctx, tx, r); err != nil {
		return nil, err
	}
	if err = d.a.pluginStore.commitNoticeTx(tx, "route_step"); err != nil {
		_ = tx.Rollback()
		// Local SQL receipt readback, never a new external attempt or a new stage.
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		current, done, e := d.a.pluginStore.readNoticeRoute(readCtx, d.a.Store.DB, d.id)
		if e != nil || current.Count <= ordinal || done[ordinal].Kind != kind || done[ordinal].InputSHA != inputSHA || !bytes.Equal(done[ordinal].Output, output) {
			return nil, seedhost.ErrUnknown
		}
	}
	d.cursor++
	return noticeValue(output)
}
func (d *pluginNoticeDriver) finish(ctx context.Context, sourceErr error) error {
	tx, err := d.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return seedhost.ErrUnknown
	}
	defer tx.Rollback()
	r, _, err := d.a.pluginStore.readNoticeRoute(ctx, tx, d.id)
	if err != nil {
		return seedhost.ErrUnknown
	}
	if r.Count != d.cursor {
		return seedhost.ErrUnknown
	}
	if err = d.a.pluginStore.requireAutoSignInNoticeExecutionTx(ctx, tx, r); err != nil {
		return err
	}
	if err = d.a.requireNoticeV2ExecutionTx(ctx, tx, r); err != nil {
		return err
	}
	r.Complete = true
	r.SourceError = sourceNoticeError(sourceErr)
	if err = d.a.pluginStore.finishAutoSignInNoticeTx(ctx, tx, r, d.cursor, sourceErr, d.failure); err != nil {
		return seedhost.ErrUnknown
	}
	if err = d.a.pluginStore.finishRemoveLinkSourceTx(ctx, tx, r, d.cursor, sourceErr, d.failure); err != nil {
		return seedhost.ErrUnknown
	}
	if r.Kind == "site_statistic" {
		if err = d.a.pluginStore.finishSiteStatisticNoticeTx(ctx, tx, r); err != nil {
			return seedhost.ErrUnknown
		}
	}
	if err = d.a.pluginStore.saveNoticeRouteTx(ctx, tx, r); err != nil {
		return seedhost.ErrUnknown
	}
	if err = d.a.pluginStore.commitNoticeTx(tx, "route_finish"); err != nil {
		_ = tx.Rollback()
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		current, _, readErr := d.a.pluginStore.readNoticeRoute(readCtx, d.a.Store.DB, d.id)
		if readErr != nil || !bytes.Equal(mustCleanSeedJSON(current), mustCleanSeedJSON(r)) {
			return seedhost.ErrUnknown
		}
	}
	return nil
}

// runNoticeRoute replays the original pure Router against receipt-backed
// callbacks. A callback replays only its own recorded result; it never repeats a
// publication, an accepted core queue call or an external attempt.
func (a *App) runNoticeRoute(ctx context.Context, id string) error {
	release, err := a.pluginStore.noticeLock(ctx, "route/"+id)
	if err != nil {
		return seedhost.ErrUnknown
	}
	defer release()
	return a.runNoticeRouteLocked(ctx, id)
}
func (a *App) runNoticeRouteLocked(ctx context.Context, id string) error {
	if _, err := a.pluginStore.noticePeriods(ctx); err != nil {
		return seedhost.ErrUnknown
	}
	initial, _, err := a.pluginStore.readNoticeRoute(ctx, a.Store.DB, id)
	if err != nil {
		return seedhost.ErrUnknown
	}
	if initial.Kind == "auto_signin" {
		tx, guardErr := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if guardErr != nil {
			return seedhost.ErrUnknown
		}
		guardErr = a.pluginStore.requireAutoSignInNoticeExecutionTx(ctx, tx, initial)
		_ = tx.Rollback()
		if guardErr != nil {
			return seedhost.ErrUnknown
		}
	}
	if initial.Complete {
		if initial.SourceError != "" {
			return &seed.SourceError{Type: initial.SourceError, Operation: "post_message"}
		}
		return nil
	}
	if initial.Version == 2 {
		return a.runNoticeBusinessV2(ctx, initial)
	}
	var fields notice.Object
	if cleanupJournalStrict(initial.Envelope, &fields) != nil {
		return seedhost.ErrUnknown
	}
	env, err := notice.NewEnvelope(fields)
	if err != nil {
		return seedhost.ErrUnknown
	}
	d := &pluginNoticeDriver{a: a, id: id}
	// This is a real host settings read, kept separate from the source envelope.
	value, err := d.step(ctx, "superuser", nil, func(tx *sql.Tx, _ pluginNoticeRoute, _ int) (any, error) {
		name, err := a.pluginStore.noticePreference(ctx, tx, "superuser", "")
		if err != nil {
			return nil, err
		}
		if name != nil {
			return name, nil
		}
		var user string
		err = tx.QueryRowContext(ctx, `SELECT username FROM admin WHERE id=1`).Scan(&user)
		if errors.Is(err, sql.ErrNoRows) {
			return "admin", nil
		}
		return user, err
	})
	if err != nil {
		if errors.Is(err, seedhost.ErrUnknown) || errors.Is(err, seedhost.ErrUnsafe) || ctx.Err() != nil {
			return seedhost.ErrUnknown
		}
		if finishErr := d.finish(ctx, err); finishErr != nil {
			return finishErr
		}
		return &seed.SourceError{Type: sourceNoticeError(err), Operation: "post_message"}
	}
	superuser, ok := value.(string)
	if !ok {
		err = notice.SourceError{Class: "TypeError"}
		if finishErr := d.finish(ctx, err); finishErr != nil {
			return finishErr
		}
		return &seed.SourceError{Type: "TypeError", Operation: "post_message"}
	}
	router := notice.Router{Dependencies: notice.Dependencies{
		Superuser: superuser,
		SaveHistory: func(_ context.Context, v notice.Object) error {
			_, err := d.step(ctx, "history", v, func(tx *sql.Tx, r pluginNoticeRoute, _ int) (any, error) {
				if r.Kind == "auto_signin" {
					return a.pluginStore.autoSignInNoticeHistoryTx(ctx, tx, r, v)
				}
				if r.HistoryID == "" || !bytes.Equal(mustCleanSeedJSON(v), r.Envelope) {
					return nil, seedhost.ErrUnsafe
				}
				return map[string]any{"history_id": r.HistoryID}, nil
			})
			return err
		},
		NotificationSwitch: func(_ context.Context, key any) (any, error) {
			return d.step(ctx, "switch", key, func(tx *sql.Tx, _ pluginNoticeRoute, _ int) (any, error) {
				s, ok := key.(string)
				if !ok {
					return nil, notice.SourceError{Class: "AttributeError"}
				}
				return a.pluginStore.noticePreference(ctx, tx, "switch", s)
			})
		},
		UserSettings: func(_ context.Context, key any) (any, error) {
			return d.step(ctx, "user", key, func(tx *sql.Tx, _ pluginNoticeRoute, _ int) (any, error) {
				s, ok := key.(string)
				if !ok {
					return nil, notice.SourceError{Class: "TypeError"}
				}
				return a.pluginStore.noticePreference(ctx, tx, "user", s)
			})
		},
		NoticeMessage: func(_ context.Context, data notice.Object) error {
			_, err := d.step(ctx, "publish", data, func(tx *sql.Tx, r pluginNoticeRoute, ordinal int) (any, error) {
				return a.publishPluginNoticeTx(ctx, tx, r, ordinal, data)
			})
			return err
		},
		QueueCall: func(_ context.Context, call notice.Call) error {
			value, err := d.step(ctx, "queue", call, func(tx *sql.Tx, r pluginNoticeRoute, ordinal int) (any, error) {
				return a.acceptPluginMessageTx(ctx, tx, r, ordinal, call)
			})
			if err != nil {
				return err
			}
			out, ok := value.(map[string]any)
			if !ok {
				return seedhost.ErrUnknown
			}
			queueID, ok := out["id"].(string)
			if !ok {
				return seedhost.ErrUnknown
			}
			direct, ok := out["direct"].(bool)
			if !ok {
				return seedhost.ErrUnknown
			}
			if direct {
				return a.attemptPluginMessage(ctx, queueID)
			}
			return nil
		},
	}}
	err = router.Post(ctx, env, notice.Business{}, notice.Object{"current_time": initial.CurrentTime})
	if errors.Is(err, seedhost.ErrUnknown) || errors.Is(err, seedhost.ErrUnsafe) || ctx.Err() != nil {
		return seedhost.ErrUnknown
	}
	if finishErr := d.finish(ctx, err); finishErr != nil {
		return finishErr
	}
	if err != nil {
		return &seed.SourceError{Type: sourceNoticeError(err), Operation: "post_message"}
	}
	return nil
}

// The actual native source waits here after its local journal transaction has
// committed. The live grant authenticates this call; route facts alone cannot
// fabricate source continuation/config/file/downloader authority.
func (c *pluginCleanInvalidSeedCore) AfterNotice(ctx context.Context, inv seedhost.Invocation, n seedhost.Notice) error {
	lease, err := c.active(ctx, inv)
	if err != nil {
		return seedhost.ErrUnknown
	}
	tx, err := c.o.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return seedhost.ErrUnknown
	}
	state, _, err := c.o.j.read(ctx, tx)
	if err != nil {
		_ = tx.Rollback()
		return seedhost.ErrUnknown
	}
	r, ok := state.Runs[inv.RunID]
	effect := seedhost.Effect{Binding: inv.Binding, Authority: r.Effective, ConfigReceipt: r.ConfigReceipt, RunID: inv.RunID, ID: n.ID, Kind: "notice", Phase: "confirmed", Args: n.Payload}
	if !ok || !cleanSeedHasGrant(lease, effect) {
		_ = tx.Rollback()
		return seedhost.ErrUnknown
	}
	if _, err = c.verifyTx(ctx, tx, lease, inv, effect); err != nil {
		_ = tx.Rollback()
		return seedhost.ErrUnknown
	}
	token := c.o.j.token("outbox", n.ID, -1)
	if _, err = readCleanSeedPrivateNotice(ctx, tx, c.o.j, token); err != nil {
		_ = tx.Rollback()
		return seedhost.ErrUnknown
	}
	receipt, err := cleanSeedReadReceipt(ctx, tx, c.o.j, n.ID)
	_ = tx.Rollback()
	var proof pluginCleanInvalidSeedNoticeReceipt
	if err != nil || cleanupJournalStrict(receipt.Payload, &proof) != nil || proof.RouteID == "" {
		return seedhost.ErrUnknown
	}
	return c.o.a.runNoticeRoute(ctx, proof.RouteID)
}

// Accepted facts can finish their own routing after a crash without replaying
// the original Clean function or granting its interrupted run new effects.

func (a *App) resumePluginNoticeRoutes(ctx context.Context, limit int) error {
	if limit < 1 || limit > 200 {
		return seedhost.ErrUnsafe
	}
	p := a.pluginStore
	p.notices.mu.Lock()
	after, through := p.notices.routeAfter, p.notices.routeThrough
	p.notices.mu.Unlock()
	if through == 0 {
		if err := a.Store.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(rowid),0) FROM plugin_notice_routes`).Scan(&through); err != nil {
			return err
		}
	}
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT rowid,id FROM plugin_notice_routes WHERE rowid>? AND rowid<=? ORDER BY rowid LIMIT ?`, after, through, limit)
	if err != nil {
		return err
	}
	type item struct {
		seq int64
		id  string
	}
	var entries []item
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.seq, &v.id); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var first error
	for _, v := range entries {
		release, ok := p.tryNoticeLock("route/" + v.id)
		if ok {
			r, _, e := p.readNoticeRoute(ctx, a.Store.DB, v.id)
			// Core collection recovery reconstructs its live job/API authority and
			// resumes ordinals in order. A generic route scan cannot supply that
			// authority, even though it can validate the immutable route.
			siteMessage := r.BusinessV2 != nil && r.BusinessV2.Binding.Kind == "site_message"
			if e == nil && !r.Complete && !siteMessage {
				e = a.runNoticeRouteLocked(ctx, v.id)
			}
			release()
			if e != nil && first == nil {
				first = e
			}
		}
		after = v.seq
	}
	if len(entries) < limit || after >= through {
		after, through = 0, 0
	}
	p.notices.mu.Lock()
	p.notices.routeAfter, p.notices.routeThrough = after, through
	p.notices.mu.Unlock()
	// The source worker may have exited while this accepted route was still
	// incomplete. Finish that original run without invoking its SDK again.
	if err := a.resumeSiteMessageCollections(ctx); err != nil && first == nil {
		first = err
	}
	if err := p.reconcileSiteStatisticNoticeRuns(ctx); err != nil && first == nil {
		first = err
	}
	return first
}

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
	failure *noticeSourceFailure
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
		d.failure = &noticeSourceFailure{Kind: kind, Ordinal: ordinal, InputSHA: inputSHA}
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
	if err = d.a.requireNoticeV2ExecutionTx(ctx, tx, r); err != nil {
		return err
	}
	r.Complete = true
	r.SourceError = sourceNoticeError(sourceErr)
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
		var unavailable *APIError
		if errors.As(err, &unavailable) && unavailable.Code == "PLUGIN_ADAPTER_UNAVAILABLE" {
			return err
		}
		return seedhost.ErrUnknown
	}
	if initial.Version != 2 {
		return bad("PLUGIN_ADAPTER_UNAVAILABLE", "原插件通知来源尚未适配")
	}
	if initial.Complete {
		if initial.SourceError != "" {
			return &seed.SourceError{Type: initial.SourceError, Operation: "post_message"}
		}
		return nil
	}
	return a.runNoticeBusinessV2(ctx, initial)
}

// The actual native source waits here after its local journal transaction has
// committed. The live grant authenticates this call; route facts alone cannot
// fabricate source continuation/config/file/downloader authority.

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
	return first
}

type noticeSourceFailure struct {
	Kind     string
	Ordinal  int
	InputSHA string
}

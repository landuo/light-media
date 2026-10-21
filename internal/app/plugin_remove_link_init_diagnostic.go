package app

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"time"

	cleanuphost "light-media/internal/plugins/removelink/host"
)

// This is the local source systemmessage path, not post_message, a worker
// capability, the notification outbox, or a source Router barrier. Native errno
// text is retained (after redaction); it is not claimed to be Python str(e).
type removeLinkInitDiagnostic struct {
	Version                  int
	RunID, Scope             string
	Binding                  cleanuphost.Binding
	Ordinal                  int
	Path, Mode, Stage, Cause string
	Notice                   Notification
}

// Called within configure's original Guard. It must not reacquire lifecycle
// locks, re-open observers, clear Active/Blocked, or finish the lifecycle run.
func (o *pluginRemoveLinkOwner) watchInitDiagnostic(ctx context.Context, b cleanuphost.Binding, scope string, failure *cleanuphost.WatchInitFailure) error {
	if !failure.Valid() || scope == "" {
		return cleanuphost.ErrUnsafe
	}
	call, ok := ctx.Value(pluginRemoveLinkOwnerCallKey{}).(pluginRemoveLinkOwnerCall)
	if !ok || call.owner != o || call.run == nil || call.run.Action != "init" && call.run.Action != "reload" {
		return cleanuphost.ErrUnsafe
	}
	o.e.mu.Lock()
	defer o.e.mu.Unlock()
	l, err := o.e.active(ctx)
	if err != nil {
		return err
	}
	if l.binding != b || l.action != "configure" || l.runID != call.run.ID {
		return cleanuphost.ErrUnsafe
	}
	spec := failure.Spec()
	proof := removeLinkInitDiagnostic{Version: 1, RunID: l.runID, Scope: scope, Binding: b, Ordinal: failure.Ordinal(), Path: spec.Path, Mode: spec.Mode, Stage: failure.Stage(), Cause: failure.Error()}
	key := "watch-init:" + o.j.p.noticeID("remove-link-watch-init", b, l.runID, scope, proof.Ordinal, proof.Path, proof.Mode, proof.Stage, proof.Cause)
	tx, err := o.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = o.e.guardTx(ctx, tx, l); err != nil {
		return err
	}
	state, _, _, err := o.j.read(ctx, tx)
	if err != nil {
		return err
	}
	if err = o.validateInitDiagnosticState(state, proof, call.run.Action); err != nil {
		return err
	}
	label := "硬链接"
	if spec.Mode == "strm" {
		label = " STRM "
	}
	settings := o.a.Settings()
	proof.Notice = Notification{ID: "cleanup_init_notice_" + o.j.p.noticeID("remove-link-watch-init-notice", key), Title: cleanWithSettings("媒体文件清理", settings), Message: cleanWithSettings(spec.Path+" 启动"+label+"监控失败："+failure.Error(), settings), MType: "系统", CreatedAt: state.Active.At.UTC().Format(time.RFC3339Nano)}
	fact := cleanuphost.Effect{ID: key, Scope: scope, Kind: "watch_init_diagnostic", Binding: b, Path: spec.Path, Role: spec.Mode, Stage: proof.Stage, State: "confirmed", Result: true, Payload: mustCleanSeedJSON(proof)}
	if old, exists := state.Effects[key]; exists {
		if !reflect.DeepEqual(old, fact) {
			return cleanuphost.ErrUnsafe
		}
		if err = o.readInitDiagnosticReceipt(ctx, tx, fact, proof.Notice); err != nil {
			return err
		}
		return readRemoveLinkInitNotice(ctx, tx, proof.Notice)
	}
	// This INSERT-only receipt is sealed independently from the mutable host
	// checkpoint. Its authenticated effect includes the exact public history.
	if err = o.e.saveReceipt(ctx, tx, fact, pluginRemoveLinkCoreReceipt{Result: true, HistoryID: proof.Notice.ID}); err != nil {
		return err
	}
	if err = putCleanupNoticeTx(ctx, tx, proof.Notice); err != nil {
		return err
	}
	if err = o.j.updateTx(ctx, tx, func(s *cleanuphost.Durable) error {
		if err := o.validateInitDiagnosticState(*s, proof, call.run.Action); err != nil {
			return err
		}
		if _, exists := s.Effects[key]; exists {
			return cleanuphost.ErrUnsafe
		}
		s.Effects[key] = fact
		return nil
	}); err != nil {
		return err
	}
	commit := tx.Commit
	if o.initDiagnosticCommit != nil {
		commit = func() error { return o.initDiagnosticCommit(tx) }
	}
	if err = commit(); err == nil {
		return nil
	}
	_ = tx.Rollback()
	// A lost commit ACK permits only bounded exact read-back, not another SQL
	// insertion or observer attempt. Matching public history alone proves nothing.
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	check, readErr := o.a.Store.DB.BeginTx(readCtx, &sql.TxOptions{ReadOnly: true})
	if readErr != nil {
		return errors.Join(err, cleanuphost.ErrUncertain)
	}
	defer check.Rollback()
	if _, readErr = o.e.guardTx(readCtx, check, l); readErr == nil {
		var saved cleanuphost.Durable
		saved, _, _, readErr = o.j.read(readCtx, check)
		if readErr == nil {
			readErr = o.validateInitDiagnosticState(saved, proof, call.run.Action)
		}
		if readErr == nil && !reflect.DeepEqual(saved.Effects[key], fact) {
			readErr = cleanuphost.ErrUncertain
		}
		if readErr == nil {
			readErr = o.readInitDiagnosticReceipt(readCtx, check, fact, proof.Notice)
		}
		if readErr == nil {
			readErr = readRemoveLinkInitNotice(readCtx, check, proof.Notice)
		}
	}
	if readErr == nil {
		return nil
	}
	return errors.Join(err, cleanuphost.ErrUncertain, readErr)
}

func (o *pluginRemoveLinkOwner) validateInitDiagnosticState(s cleanuphost.Durable, p removeLinkInitDiagnostic, action string) error {
	t := s.Transition
	if p.Binding.InstanceID != o.instance || p.Binding.Generation != o.generation || s.Binding != p.Binding || s.Blocked != p.Cause || s.Active == nil || s.Active.Kind != "configure" || s.Active.ID != p.Scope || t == nil || t.RunID != p.RunID || t.Kind != action || t.To != p.Binding || t.Phase != "published" && t.Phase != "published_drained" {
		return cleanuphost.ErrUnsafe
	}
	if _, exists := s.Effects["lifecycle:"+p.RunID]; exists {
		return cleanuphost.ErrUnsafe
	}
	return nil
}
func readRemoveLinkInitNotice(ctx context.Context, tx *sql.Tx, expected Notification) error {
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='notification' AND id=?`, expected.ID).Scan(&raw); err != nil {
		return err
	}
	var actual Notification
	if cleanupJournalStrict(raw, &actual) != nil {
		return cleanuphost.ErrUnsafe
	}
	actual.Read = false
	if actual != expected {
		return cleanuphost.ErrUnsafe
	}
	return nil
}

func (o *pluginRemoveLinkOwner) readInitDiagnosticReceipt(ctx context.Context, tx *sql.Tx, fact cleanuphost.Effect, notice Notification) error {
	receipt, err := o.e.receipt(ctx, tx, fact)
	if err != nil {
		return err
	}
	if !receipt.Result || receipt.HistoryID != notice.ID || receipt.FileID != "" || receipt.ExecutionKey != "" || receipt.AssetID != "" {
		return cleanuphost.ErrUnsafe
	}
	return nil
}

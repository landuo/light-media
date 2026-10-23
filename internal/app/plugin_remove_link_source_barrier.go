package app

import (
	"bytes"
	"context"
	"errors"
	"time"

	"light-media/internal/plugins/removelink"

	cleanuphost "light-media/internal/plugins/removelink/host"
)

// completeRemoveLinkSourceBarrier is a continuation of the same serialized
// Host action. It never re-enters that Host, refreshes a selection, or borrows a
// newer configuration's authority. Only a proven source return can continue it.
func (e *pluginRemoveLinkEffects) completeRemoveLinkSourceBarrier(ctx context.Context, x cleanuphost.Effect) error {
	e.mu.Lock()
	l, err := e.active(ctx)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	if l.pause == nil || l.resume == nil {
		e.mu.Unlock()
		return cleanuphost.ErrUnsafe
	}
	tx, err := e.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	raw, err := e.guardTx(ctx, tx, l)
	var before cleanuphost.Durable
	if err == nil {
		before, _, _, err = e.j.read(ctx, tx)
	}
	if err == nil {
		err = e.validate(ctx, l, before, x, raw)
	}
	if err == nil {
		_, err = e.receipt(ctx, tx, x)
	}
	if err == nil {
		err = e.reconcileTx(ctx, tx, l, before)
	}
	if err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	_ = tx.Rollback()
	if err != nil {
		e.mu.Unlock()
		return cleanuphost.ErrUncertain
	}
	frozen := mustCleanSeedJSON(before)
	// Pause, rather than issue a fresh lease: prior and the frozen selections
	// remain exactly those of the original action, including prior deletions.
	e.lease = nil
	e.mu.Unlock()
	l.pause()

	var resumeErr error
	sourceErr := func() error {
		defer func() { resumeErr = l.resume() }()
		// Always reacquire in the owner lock order, including on a callback panic.
		// The deferred resume below does not restore file authority on its own.
		_, transferErr := e.a.transferRemoveLinkOutboxFor(ctx, e.j.instance, e.j.generation, e.token(x.ID))
		routeID := e.j.p.noticeID("route", e.j.instance, e.j.generation, e.token(x.ID))
		if transferErr != nil {
			readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			route, _, readErr := e.j.p.readNoticeRoute(readCtx, e.a.Store.DB, routeID)
			cancel()
			if readErr == nil && route.EffectID == x.ID {
				transferErr = nil
			}
		}
		if transferErr == nil {
			// Only this foreign Router boundary is recovered. An unhandled
			// callback panic supplies no source result and cannot restore the
			// file lease. Sender failures already consumed by MessageQueue
			// remain normal Router returns, exactly as in the source.
			panicked := func() (panicked bool) {
				defer func() {
					if recover() != nil {
						panicked = true
					}
				}()
				_ = e.a.runNoticeRoute(ctx, routeID)
				return false
			}()
			if panicked {
				return cleanuphost.ErrUncertain
			}
		}
		// A lost SQL ACK is not a second route attempt. Read the exact independent
		// proof with a bounded cancellation-free context, or stop as unknown.
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		return e.readRemoveLinkSourceResult(readCtx, x)
	}()
	if resumeErr != nil {
		return cleanuphost.ErrUncertain
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lease != nil || l.nonce == "" || ctx.Err() != nil {
		return cleanuphost.ErrUncertain
	}
	tx, err = e.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return cleanuphost.ErrUncertain
	}
	defer tx.Rollback()
	raw, err = e.guardTx(ctx, tx, l)
	var after cleanuphost.Durable
	if err == nil {
		after, _, _, err = e.j.read(ctx, tx)
	}
	if err == nil && !bytes.Equal(frozen, mustCleanSeedJSON(after)) {
		err = cleanuphost.ErrUnsafe
	}
	if err == nil {
		err = e.validate(ctx, l, after, x, raw)
	}
	if err != nil {
		return cleanuphost.ErrUncertain
	}
	var known *removelink.SourceNoticeError
	if sourceErr != nil && (!errors.As(sourceErr, &known) || !known.Valid()) {
		return cleanuphost.ErrUncertain
	}
	e.lease = l
	return sourceErr
}

// SourceNoticeResult validates a confirmed v3 host replay without rerouting or
// using a mutable host checkpoint as independent proof of source completion.
func (e *pluginRemoveLinkEffects) SourceNoticeResult(ctx context.Context, x cleanuphost.Effect) error {
	if x.Kind != "notification" || x.SourceBarrierVersion != 1 {
		return cleanuphost.ErrUnsafe
	}
	return e.readRemoveLinkSourceResult(ctx, x)
}

// Read-only recovery of a completed Host action cannot promote an unauthenticated
// journal "confirmed" flag into a source return or a deferred completion.
func (e *pluginRemoveLinkEffects) confirmedRemoveLinkSourceEffect(ctx context.Context, x cleanuphost.Effect) error {
	if x.SourceBarrierVersion != 1 || x.Kind != "notification" || x.State != "confirmed" || !x.Result {
		return cleanuphost.ErrUncertain
	}
	err := e.readRemoveLinkSourceResult(ctx, x)
	if err == nil {
		if x.SourceError != "" {
			return cleanuphost.ErrUncertain
		}
		return nil
	}
	var known *removelink.SourceNoticeError
	if errors.As(err, &known) && known.Valid() && known.Class == x.SourceError {
		return nil
	}
	return cleanuphost.ErrUncertain
}

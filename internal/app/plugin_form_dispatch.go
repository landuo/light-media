package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// pluginFormCallback is a compiled host hook, never an SDK method or a worker
// supplied mode. The detached view is a captured image: a hook may mutate its
// local copies, but cannot change the session or another callback's view.
// Hooks must use ctx synchronously; retaining it or starting background work is
// prohibited. Cancellation is cooperative, not an OS sandbox for trusted Go.
type pluginFormCallback func(context.Context, pluginFormView) (json.RawMessage, error)

// Every used mode requires its own explicit callback. Started and LegacyUnknown
// must restore and validate real business state; unknown is NOT proof of init.
// Preview and Cold are read-only, including when retained business state exists:
// the hook must validate it rather than overwrite, fabricate or silently reset it.
// No missing callback falls back to Init or another mode.
type pluginFormCallbacks struct {
	Preview       pluginFormCallback
	Cold          pluginFormCallback
	Started       pluginFormCallback
	LegacyUnknown pluginFormCallback
}

type pluginFormCallbackScopeKey struct{}
type pluginFormCallbackScope struct {
	session  *pluginFormSession
	readOnly bool
}

type pluginFormConfigWriteKey struct{}
type pluginFormConfigWrite struct {
	store    *PluginStore
	tx       *sql.Tx
	instance string
	session  *pluginFormSession
	scope    *pluginFormCallbackScope
}

// A data-only proof is retained inside the transaction wrapper after common
// same-TX admission. The config has changed within that transaction, so an old
// config-anchor comparison would reject the legitimate journal projection.
// No callback context receives this proof, and it never authorizes config.
type pluginFormDataWriteKey struct{}
type pluginFormDataWrite struct {
	store    *PluginStore
	tx       *sql.Tx
	instance string
	session  *pluginFormSession
}

type pluginFormWriteKind uint8

const (
	pluginFormWriteData pluginFormWriteKind = iota
	pluginFormWriteProjection
	pluginFormWriteDirectConfig
	pluginFormWriteLifecycle
)

func pluginFormCallbackScopeFromContext(ctx context.Context) *pluginFormCallbackScope {
	if ctx == nil {
		return nil
	}
	scope, _ := ctx.Value(pluginFormCallbackScopeKey{}).(*pluginFormCallbackScope)
	return scope
}

// checkPluginFormWrite protects the common APIs, before preparation or effects.
// It deliberately does not claim to intercept arbitrary SQL, native owners,
// memory/cache mutation, filesystem/network access, or a hook discarding ctx.
// Those remain prohibited trusted-hook behavior, not a new worker capability.
func (p *PluginStore) checkPluginFormWrite(ctx context.Context, instance string, kind pluginFormWriteKind) error {
	scope := pluginFormCallbackScopeFromContext(ctx)
	if scope == nil {
		return nil // Existing non-dispatch contracts, including cold defaults, stay intact.
	}
	session := pluginFormSessionFromContext(ctx)
	if scope.session == nil || session != scope.session || p == nil || session.store != p || session.requested.InstanceID != instance || scope.readOnly || kind == pluginFormWriteDirectConfig || kind == pluginFormWriteLifecycle {
		err := pluginFormViewDenied()
		if scope.session != nil {
			scope.session.captureHostError(err)
		}
		return err
	}
	if err := ctx.Err(); err != nil {
		session.captureHostError(err)
		return err
	}
	if session.hostFailure != nil {
		failure := *session.hostFailure
		return &failure
	}
	return nil
}

// Only transitionInstanceConfig gives this capability to its exact config
// write and subsequent propagation, after same-TX session admission. It is
// bound to the current callback scope as well as Store/TX/instance/session and
// is never passed to a business projector.
func (p *PluginStore) pluginFormConfigWriteContext(ctx context.Context, tx *sql.Tx, instance string) context.Context {
	return context.WithValue(ctx, pluginFormConfigWriteKey{}, pluginFormConfigWrite{store: p, tx: tx, instance: instance, session: pluginFormSessionFromContext(ctx), scope: pluginFormCallbackScopeFromContext(ctx)})
}

// Propagation and revision-chain records are configuration writes too, even
// when they only touch queued inputs or scheduler/event metadata. A Form hook
// cannot invoke them directly or borrow a proof from another callback or TX.
// This does not introduce authority outside dispatchPluginForm.
func (p *PluginStore) checkPluginFormConfigProjection(ctx context.Context, tx *sql.Tx, instance string) error {
	scope := pluginFormCallbackScopeFromContext(ctx)
	if scope == nil {
		return nil
	}
	if err := p.checkPluginFormWrite(ctx, instance, pluginFormWriteProjection); err != nil {
		return err
	}
	if permit, ok := ctx.Value(pluginFormConfigWriteKey{}).(pluginFormConfigWrite); ok && permit.store == p && permit.tx == tx && tx != nil && permit.instance == instance && permit.session == scope.session && permit.scope == scope {
		return nil
	}
	err := pluginFormViewDenied()
	scope.session.captureHostError(err)
	return err
}

func (p *PluginStore) checkPluginFormValueWrite(ctx context.Context, tx *sql.Tx, instance, kind string) error {
	if kind == "config" {
		return p.checkPluginFormConfigProjection(ctx, tx, instance)
	}
	if err := p.checkPluginFormWrite(ctx, instance, pluginFormWriteData); err != nil {
		return err
	}
	scope := pluginFormCallbackScopeFromContext(ctx)
	if scope == nil {
		return nil
	}
	if permit, ok := ctx.Value(pluginFormDataWriteKey{}).(pluginFormDataWrite); ok {
		if permit.store == p && permit.tx == tx && permit.instance == instance && permit.session == scope.session {
			return nil
		}
		err := pluginFormViewDenied()
		scope.session.captureHostError(err)
		return err
	}
	// Bare helpers and unadmitted wrappers must verify the current anchor in
	// their OWN transaction, before any KV write. Final Form validation alone
	// cannot undo an already committed data write after config drift.
	current, err := p.pluginFormAnchorRead(ctx, tx, scope.session.requested, scope.session.origin)
	if err == nil {
		err = comparePluginFormAnchors(scope.session.anchor, current)
	}
	if err != nil {
		scope.session.captureHostError(err)
	}
	return err
}

// dispatchPluginForm selects behavior exclusively from the authenticated
// request session. Short read transactions close before the hook runs; neither
// a SQL transaction nor a global mutex spans business code. All invocations
// share the session's original deadline, further narrowed by the caller's ctx.
func (p *PluginStore) dispatchPluginForm(ctx context.Context, requested PluginInstallation, callbacks pluginFormCallbacks) (result json.RawMessage, returnErr error) {
	session := pluginFormSessionFromContext(ctx)
	failureSession := session
	if outer := pluginFormCallbackScopeFromContext(ctx); outer != nil && outer.session != nil {
		failureSession = outer.session
	}
	callbackActive := false
	// Install containment before the first validation. A trusted adapter must
	// not swallow an early binding error or recover a panic and retry a clean
	// call against the same request. Never format, log or retain panic payloads.
	// callbackActive also covers legacy panic(nil) behavior without its value.
	defer func() {
		panicked := recover() != nil
		if panicked || callbackActive {
			result, returnErr = nil, bad("PLUGIN_FORM_FAILED", "加载插件配置表单失败")
		}
		if returnErr == nil {
			return
		}
		result = nil
		failure := canonicalPluginFormHostError(returnErr)
		if failureSession != nil {
			failureSession.captureHostError(returnErr)
			failure = failureSession.hostFailure
		}
		// Preserve context cancellation for trusted callers. HTTP retains only
		// its redacted sticky error. No error or panic details escape otherwise.
		if !errors.Is(returnErr, context.Canceled) && !errors.Is(returnErr, context.DeadlineExceeded) {
			if failure == nil {
				failure = bad("PLUGIN_FORM_FAILED", "加载插件配置表单失败")
			}
			copy := *failure
			returnErr = &copy
		}
	}()
	view, err := p.trustedPluginFormView(ctx, requested)
	if err != nil {
		return nil, err
	}
	if outer := pluginFormCallbackScopeFromContext(ctx); outer != nil && outer.session != session {
		return nil, pluginFormViewDenied()
	}
	if session.deadline.IsZero() {
		return nil, pluginFormViewDenied()
	}
	ctx, cancel := context.WithDeadline(ctx, session.deadline)
	defer cancel()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = session.validateAnchor(ctx); err != nil {
		return nil, err
	}
	var callback pluginFormCallback
	readOnly := false
	switch view.mode {
	case pluginFormPreview:
		callback, readOnly = callbacks.Preview, true
	case pluginFormInstalledCold:
		callback, readOnly = callbacks.Cold, true
	case pluginFormInstalledStarted:
		callback = callbacks.Started
	case pluginFormInstalledUnknown:
		callback = callbacks.LegacyUnknown
	default:
		return nil, pluginFormViewDenied()
	}
	if callback == nil {
		return nil, bad("PLUGIN_FORM_CALLBACK_UNSUPPORTED", "插件实现未提供当前模式的可信表单回调")
	}
	ctx = context.WithValue(ctx, pluginFormCallbackScopeKey{}, &pluginFormCallbackScope{session: session, readOnly: readOnly})
	callbackActive = true
	result, err = callback(ctx, view)
	callbackActive = false
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = session.validateFinal(ctx); err != nil {
		return nil, err
	}
	return bytes.Clone(result), nil
}

func pluginFormDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(10 * time.Second)
	if inherited, ok := ctx.Deadline(); ok && inherited.Before(deadline) {
		deadline = inherited
	}
	return deadline
}

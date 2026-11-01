package download

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"light-media/internal/plugins/downloadercommands"
)

// WithTagScope gives the source leaf one fixed native operation. The lock and
// monotonic selected-dispatch proof span every read, delay and cleanup write.
// None of the private primitives below enters owner.run a second time.
func (q *sharedQBCommands) WithTagScope(ctx context.Context, fn func(context.Context, downloadercommands.QBTagPrimitives) (any, error)) (any, error) {
	if q == nil || q.owner == nil || fn == nil {
		return nil, commandBoundary("missing fixed tag scope")
	}
	var direct *downloadercommands.SourceException
	var proof *mutationScope
	result, err := q.owner.run(ctx, func(ctx context.Context) (any, error) {
		proof = mutationScopeFor(ctx)
		port := &sharedQBTagScope{q: q, proof: proof, active: true}
		defer func() {
			port.mu.Lock()
			port.active = false
			port.mu.Unlock()
		}()
		result, err := fn(ctx, port)
		// Only the source leaf's direct, uncaught first-row .get failure.
		// Never search wrapped/provider errors for a source classification.
		if value, ok := err.(*downloadercommands.SourceException); ok && value != nil && value.Class == "AttributeError" {
			direct = value
		}
		return result, err
	})
	if rejected, ok := err.(*mutationNotDispatched); ok && direct != nil && rejected != nil &&
		rejected.seal == nativeMutationProofSeal && rejected.class == mutationFailureNative && rejected.cause == direct &&
		proof != nil && !proof.delegated.Load() && proof.terminal.Load() == mutationFailureNative && ctx != nil && ctx.Err() == nil {
		// The exact unchanged cause also proves run's final owner/ctx guard
		// succeeded. No selected operation occurred; preserve this one source
		// exception rather than losing its envelope to the private proof.
		return result, direct
	}
	return result, err
}

type sharedQBTagScope struct {
	q      *sharedQBCommands
	proof  *mutationScope
	mu     sync.Mutex
	active bool
}

func (p *sharedQBTagScope) check(ctx context.Context) error {
	if ctx == nil || !p.active || p.proof == nil || mutationScopeFor(ctx) != p.proof {
		return commandBoundary("tag primitive outside its fixed active scope")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.q.owner.iyuu.options.Guard(ctx); err != nil {
		mutationOwnerDenied(ctx, err)
		return commandBoundary("tag scope authority revoked")
	}
	return nil
}

// sourceError is intentionally a small allowlist. Unknown transport/body
// outcomes, response bounds, malformed guards and arbitrary errors are not
// turned into an ordinary caught source query exception. Most importantly,
// SourceException after a prior selected write must not be swallowed by the
// leaf before the outer run sees the aggregate's error.
func (p *sharedQBTagScope) sourceError(ctx context.Context, err error) error {
	if guardErr := p.check(ctx); guardErr != nil {
		return guardErr
	}
	if err == nil {
		return nil
	}
	var source *downloadercommands.SourceException
	if p.proof.delegated.Load() {
		if errors.As(err, &source) {
			return failure("qb_tag_scope", "selected_outcome_unknown")
		}
		return err
	}
	if p.proof.terminal.Load() != mutationFailureNative || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var boundary *downloadercommands.BoundaryError
	if errors.As(err, &boundary) || errors.As(err, &source) {
		return err
	}
	var native *Error
	if !errors.As(err, &native) {
		return err
	}
	known := native.Operation == "login" && (native.Code == "authentication_failed" || native.Code == "missing_session_cookie")
	if native.Operation == "request" && strings.HasPrefix(native.Code, "http_") {
		code, parseErr := strconv.Atoi(strings.TrimPrefix(native.Code, "http_"))
		known = parseErr == nil && code >= 100 && code <= 599
	}
	if native.Operation == "qb_completed" && (native.Code == "invalid_json_list" || native.Code == "invalid_json_object") {
		known = true // Existing exact vendor-cast preparation, not completion.
	}
	if known {
		return &downloadercommands.SourceException{Class: "APIError", Message: "qB native query failed"}
	}
	return err
}

func (p *sharedQBTagScope) Info(ctx context.Context, status any) (*downloadercommands.QBTagRows, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.check(ctx); err != nil {
		return nil, err
	}
	// The vendor tests set membership before encoding the optional filter.
	// Ordinary list/dict values are unhashable; do not silently stringify them.
	switch status.(type) {
	case []any, []string, map[string]any:
		return nil, p.sourceError(ctx, &downloadercommands.SourceException{Class: "TypeError", Message: "unhashable status filter"})
	}
	filter := status
	if s, ok := status.(string); ok && (s == "paused" || s == "stopped" || s == "resumed" || s == "running") {
		version, err := p.q.version(ctx)
		if err != nil {
			return nil, p.sourceError(ctx, err)
		}
		modern, err := iyuuVersionAtLeast(version, 2, 11)
		if err != nil {
			return nil, p.sourceError(ctx, err)
		}
		switch {
		case s == "stopped" && !modern:
			filter = "paused"
		case s == "paused" && modern:
			filter = "stopped"
		case s == "running" && !modern:
			filter = "resumed"
		case s == "resumed" && modern:
			filter = "running"
		}
	}
	form := url.Values{}
	if err := commandSet(form, "filter", filter); err != nil {
		return nil, p.sourceError(ctx, err)
	}
	raw, err := p.q.owner.iyuu.client.qb(mutationReadOnly(ctx), http.MethodPost, "torrents/info", form.Encode(), "application/x-www-form-urlencoded")
	if err != nil {
		return nil, p.sourceError(ctx, err)
	}
	raw, err = qbittorrentCompletedVendorRows(raw)
	if err != nil {
		return nil, p.sourceError(ctx, err)
	}
	var rows []any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&rows); err != nil {
		return nil, p.sourceError(ctx, &downloadercommands.SourceException{Class: "APIError", Message: "qB native list decode failed"})
	}
	if err := p.sourceError(ctx, nil); err != nil {
		return nil, err
	}
	return &downloadercommands.QBTagRows{Values: rows}, nil
}

// The pinned _list2string leaves non-iterable scalars unchanged. Restrict
// this new primitive to the ordinary JSON scalar/list domain; do not change
// old commandJoined or add dictionary/other iterable emulation.
func sharedQBTagField(value any, separator string) (*string, error) {
	switch value.(type) {
	case nil, string, []any, []string:
		return commandJoined(value, separator, false)
	case bool, int, int64, json.Number, float64:
		encoded, err := commandScalar(value)
		if err != nil {
			return nil, err
		}
		return &encoded, nil
	default:
		return nil, commandBoundary("tag scalar/list preparation domain exceeded")
	}
}

func (p *sharedQBTagScope) RemoveTags(ctx context.Context, ids, tags any) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.check(ctx); err != nil {
		return nil, err
	}
	hashes, err := sharedQBTagField(ids, "|")
	if err != nil {
		return nil, p.sourceError(ctx, err)
	}
	labels, err := sharedQBTagField(tags, ",")
	if err != nil {
		return nil, p.sourceError(ctx, err)
	}
	form := url.Values{}
	if hashes != nil {
		form.Set("hashes", *hashes)
	}
	if labels != nil {
		form.Set("tags", *labels)
	}
	return p.write(ctx, "torrents/removeTags", form)
}

func (p *sharedQBTagScope) DeleteTags(ctx context.Context, tags any) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.check(ctx); err != nil {
		return nil, err
	}
	labels, err := sharedQBTagField(tags, ",")
	if err != nil {
		return nil, p.sourceError(ctx, err)
	}
	form := url.Values{}
	if labels != nil {
		form.Set("tags", *labels)
	}
	return p.write(ctx, "torrents/deleteTags", form)
}

// Caller already holds this private scope's mutex; selected request proof is
// the original outer scope's proof, never a new per-write proof.
func (p *sharedQBTagScope) write(ctx context.Context, endpoint string, form url.Values) (any, error) {
	version, err := p.q.version(ctx)
	if err != nil {
		return nil, p.sourceError(ctx, err)
	}
	supported, err := iyuuVersionAtLeast(version, 2, 3)
	if err != nil {
		return nil, p.sourceError(ctx, err)
	}
	if !supported {
		return nil, p.sourceError(ctx, nil)
	}
	_, err = p.q.owner.iyuu.client.qbSelected(ctx, http.MethodPost, endpoint, form.Encode(), "application/x-www-form-urlencoded")
	return nil, p.sourceError(ctx, err)
}

func (p *sharedQBTagScope) Sleep(ctx context.Context, delay time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.check(ctx); err != nil {
		return err
	}
	return p.sourceError(ctx, p.q.owner.iyuu.options.Sleep(ctx, delay))
}

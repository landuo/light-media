package download

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"

	"light-media/internal/plugins/downloadercommands"
)

// One aggregate lock/proof spans source preflight, both cookie endpoints and
// torrents/add. A delegated cookie write cannot be forgotten by a later read,
// local error, owner revocation or final Add. No primitive enters run again.
func (q *sharedQBCommands) WithAddCookieScope(ctx context.Context, fn func(context.Context, downloadercommands.QBAddCookiePrimitives) (any, error)) (any, error) {
	if q == nil || q.owner == nil || fn == nil {
		return nil, commandBoundary("missing fixed URL-cookie scope")
	}
	return q.owner.run(ctx, func(ctx context.Context) (any, error) {
		port := &sharedQBAddCookieScope{q: q, proof: mutationScopeFor(ctx), active: true}
		defer func() { port.mu.Lock(); port.active = false; port.mu.Unlock() }()
		return fn(ctx, port)
	})
}

type sharedQBAddCookieScope struct {
	q      *sharedQBCommands
	proof  *mutationScope
	mu     sync.Mutex
	active bool
}

func (p *sharedQBAddCookieScope) check(ctx context.Context) error {
	if ctx == nil || !p.active || p.proof == nil || mutationScopeFor(ctx) != p.proof {
		return commandBoundary("cookie primitive outside its fixed active scope")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.q.owner.iyuu.options.Guard(ctx); err != nil {
		mutationOwnerDenied(ctx, err)
		return commandBoundary("cookie scope authority revoked")
	}
	return nil
}

// Only definite pre-selected native preparation/query failures can enter the
// original ordinary catch/fallback. Never classify unknown transport outcomes,
// bounds, authority, cancellation or any error after a selected dispatch as a
// source exception. Error strings are fixed and contain no cookies/responses.
func (p *sharedQBAddCookieScope) sourceError(ctx context.Context, err error) error {
	if guardErr := p.check(ctx); guardErr != nil {
		return guardErr
	}
	if err == nil {
		return nil
	}
	var source *downloadercommands.SourceException
	if p.proof.delegated.Load() {
		if errors.As(err, &source) {
			return failure("qb_cookie_scope", "selected_outcome_unknown")
		}
		return err
	}
	if p.proof.terminal.Load() != mutationFailureNative || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var boundary *downloadercommands.BoundaryError
	if errors.As(err, &boundary) {
		return err
	}
	// Only our direct, fixed JSON decode error is constructed as SourceException.
	if direct, ok := err.(*downloadercommands.SourceException); ok {
		return direct
	}
	var native *Error
	if !errors.As(err, &native) {
		return err
	}
	known := native.Operation == "login" && (native.Code == "authentication_failed" || native.Code == "missing_session_cookie")
	if native.Operation == "request" && strings.HasPrefix(native.Code, "http_") {
		code, e := strconv.Atoi(strings.TrimPrefix(native.Code, "http_"))
		known = e == nil && code >= 100 && code <= 599
	}
	if known {
		return &downloadercommands.SourceException{Class: "APIError", Message: "qB native cookie query failed"}
	}
	return err
}
func (p *sharedQBAddCookieScope) version(ctx context.Context) (string, error) {
	raw, err := p.q.owner.iyuu.client.qb(mutationReadOnly(ctx), http.MethodGet, "app/webapiVersion", "", "")
	return string(raw), p.sourceError(ctx, err)
}
func (p *sharedQBAddCookieScope) WebAPIVersion(ctx context.Context) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.check(ctx); err != nil {
		return nil, err
	}
	return p.version(ctx)
}
func (p *sharedQBAddCookieScope) gate(ctx context.Context) (bool, error) {
	version, err := p.version(ctx)
	if err != nil {
		return false, err
	}
	supported, err := downloadercommands.QBRequireCookieAPI(version)
	return supported, p.sourceError(ctx, err)
}
func (p *sharedQBAddCookieScope) Cookies(ctx context.Context) ([]any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.check(ctx); err != nil {
		return nil, err
	}
	// Pinned Application.web_api_version is a non-cached property. The vendor
	// guard performs this read even though the source already checked a version.
	supported, err := p.gate(ctx)
	if err != nil || !supported {
		return nil, err
	}
	raw, err := p.q.owner.iyuu.client.qb(mutationReadOnly(ctx), http.MethodGet, "app/cookies", "", "")
	if err != nil {
		return nil, p.sourceError(ctx, err)
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err = d.Decode(&value); err != nil {
		return nil, p.sourceError(ctx, &downloadercommands.SourceException{Class: "APIError", Message: "qB cookie response JSON decode failed"})
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return nil, p.sourceError(ctx, &downloadercommands.SourceException{Class: "APIError", Message: "qB cookie response JSON decode failed"})
	}
	if err = p.sourceError(ctx, nil); err != nil {
		return nil, err
	}
	if rows, ok := value.([]any); ok {
		return rows, nil
	}
	// CookieList(list_entries or []) consumes ordinary falsey JSON as empty.
	if value == nil || value == false || value == "" {
		return []any{}, nil
	}
	if n, ok := value.(json.Number); ok {
		v, e := strconv.ParseFloat(string(n), 64)
		if e == nil && v == 0 {
			return []any{}, nil
		}
	}
	if object, ok := value.(map[string]any); ok && len(object) == 0 {
		return []any{}, nil
	}
	return nil, commandBoundary("non-list truthy CookieList construction is outside the fixed JSON domain")
}
func (p *sharedQBAddCookieScope) SetCookies(ctx context.Context, cookies []map[string]any) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.check(ctx); err != nil {
		return nil, err
	}
	// Source app_set_cookies prepares compact JSON before its vendor gate.
	payload, err := qbCookieJSON(cookies)
	if err != nil {
		return nil, p.sourceError(ctx, err)
	}
	supported, err := p.gate(ctx)
	if err != nil || !supported {
		return nil, err
	}
	_, err = p.q.owner.iyuu.client.qbSelected(ctx, http.MethodPost, "app/setCookies", url.Values{"cookies": {payload}}.Encode(), "application/x-www-form-urlencoded")
	// Vendor returns None regardless of non-raising response text. This write
	// has already marked the aggregate proof selected before any later Add.
	return nil, p.sourceError(ctx, err)
}
func (p *sharedQBAddCookieScope) Add(ctx context.Context, in downloadercommands.QBAddCall) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.check(ctx); err != nil {
		return nil, err
	}
	result, err := p.q.addInside(ctx, in)
	return result, p.sourceError(ctx, err)
}

// Fixed normalized cookie field order follows original __normalize_cookie;
// site cookies use its first four fields. Python dumps uses compact separators
// and ensure_ascii=True. No generic JSON/RPC surface is exposed here.
func qbCookieJSON(cookies []map[string]any) (string, error) {
	var out strings.Builder
	out.WriteByte('[')
	for i, cookie := range cookies {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteByte('{')
		first := true
		for _, field := range []string{"domain", "path", "name", "value", "expirationDate"} {
			value, present := cookie[field]
			if !present {
				continue
			}
			encoded, err := qbCookieJSONScalar(value)
			if err != nil {
				return "", err
			}
			if !first {
				out.WriteByte(',')
			}
			first = false
			out.WriteString(`"` + field + `":`)
			out.WriteString(encoded)
		}
		out.WriteByte('}')
	}
	out.WriteByte(']')
	return out.String(), nil
}
func qbCookieJSONScalar(value any) (string, error) {
	switch value.(type) {
	case nil:
		return "null", nil
	case bool:
		if value.(bool) {
			return "true", nil
		}
		return "false", nil
	case int, int64, json.Number, float64:
		return commandScalar(value)
	case string:
		var b bytes.Buffer
		e := json.NewEncoder(&b)
		e.SetEscapeHTML(false)
		if err := e.Encode(value); err != nil {
			return "", err
		}
		var out strings.Builder
		for _, r := range strings.TrimSuffix(b.String(), "\n") {
			if r < 127 {
				out.WriteRune(r)
			} else if r <= 0xffff {
				fmt.Fprintf(&out, `\u%04x`, r)
			} else {
				a, b := utf16.EncodeRune(r)
				fmt.Fprintf(&out, `\u%04x\u%04x`, a, b)
			}
		}
		return out.String(), nil
	default:
		return "", commandBoundary("structured stored cookie field is outside the fixed scalar JSON domain")
	}
}

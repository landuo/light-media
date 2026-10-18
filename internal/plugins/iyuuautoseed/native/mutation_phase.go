package download

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
)

// These proofs are process-local native facts. A daemon error code, JSON value,
// SDK payload, wrapped error or aggregate cannot construct the private seal.
type mutationNotDispatched struct {
	cause error
	seal  *mutationProofSeal
	class uint32
}
type mutationProofSeal struct{ marker byte }

var nativeMutationProofSeal = &mutationProofSeal{marker: 1}

const (
	mutationFailureNative uint32 = iota
	mutationFailureOwner
	mutationFailureCancelled
)

func (e *mutationNotDispatched) Error() string { return e.cause.Error() }
func (e *mutationNotDispatched) Unwrap() error { return e.cause }

// MutationNotDispatched proves this entire public IYUU mutation call did not
// delegate its selected request. Read-only authentication/preflight may occur.
// Only the exact returned object is accepted; joining/wrapping loses the proof.
func MutationNotDispatched(err error) bool {
	e, ok := err.(*mutationNotDispatched)
	return ok && e != nil && e.cause != nil && e.seal == nativeMutationProofSeal && e.class <= mutationFailureCancelled
}

// MutationFailureClass is meaningful only for an exact NotDispatched proof.
// owner_denied/cancelled are terminal conditions, not ignorable source False.
func MutationFailureClass(err error) string {
	if !MutationNotDispatched(err) {
		return ""
	}
	switch err.(*mutationNotDispatched).class {
	case mutationFailureOwner:
		return "owner_denied"
	case mutationFailureCancelled:
		return "cancelled"
	default:
		return "native"
	}
}

type mutationScopeKey struct{}
type mutationRoleKey struct{}
type mutationRequestRole uint8

const (
	mutationRoleUnknown mutationRequestRole = iota
	mutationRoleReadOnly
	mutationRoleSelected
)

type mutationScope struct {
	// Monotonic across login/session retries and all request wrappers. A local
	// failure in a later attempt must never erase an earlier delegation.
	delegated atomic.Bool
	terminal  atomic.Uint32
}

func beginMutation(ctx context.Context) (context.Context, *mutationScope) {
	s := &mutationScope{}
	if ctx == nil {
		return nil, s
	}
	return context.WithValue(ctx, mutationScopeKey{}, s), s
}
func mutationScopeFor(ctx context.Context) *mutationScope {
	if ctx == nil {
		return nil
	}
	s, _ := ctx.Value(mutationScopeKey{}).(*mutationScope)
	return s
}
func mutationRoleFor(ctx context.Context) mutationRequestRole {
	if ctx == nil {
		return mutationRoleUnknown
	}
	role, _ := ctx.Value(mutationRoleKey{}).(mutationRequestRole)
	return role
}
func mutationRoleContext(ctx context.Context, role mutationRequestRole) context.Context {
	if ctx == nil {
		return nil
	}
	return context.WithValue(ctx, mutationRoleKey{}, role)
}
func mutationReadOnly(ctx context.Context) context.Context {
	return mutationRoleContext(ctx, mutationRoleReadOnly)
}
func mutationSelected(ctx context.Context) context.Context {
	return mutationRoleContext(ctx, mutationRoleSelected)
}
func mutationSelectedCall(ctx context.Context) bool {
	return mutationScopeFor(ctx) != nil && mutationRoleFor(ctx) != mutationRoleReadOnly
}
func (s *mutationScope) finish(err error, outcome *string) error {
	if err == nil {
		return nil
	}
	if s.delegated.Load() {
		*outcome = "unknown"
		return err
	}
	class := s.terminal.Load()
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		class = mutationFailureCancelled
	}
	*outcome = "rejected"
	return &mutationNotDispatched{cause: err, seal: nativeMutationProofSeal, class: class}
}
func mutationOwnerDenied(ctx context.Context, cause error) {
	if s := mutationScopeFor(ctx); s != nil {
		class := mutationFailureOwner
		if ctx.Err() != nil || errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
			class = mutationFailureCancelled
		}
		s.terminal.Store(class)
	}
}

// The marker is inside Guard/Bearer and outside the actual network transport.
// Every unclassified request is conservative: only explicit read-only roles
// avoid marking delegation. Header/URL/context rejection occurs before next.
type mutationDispatchTransport struct{ next http.RoundTripper }

func (t *mutationDispatchTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if s := mutationScopeFor(req.Context()); s != nil {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		if req.URL == nil || req.URL.Host == "" || req.URL.User != nil || req.URL.Opaque != "" || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
			return nil, failure("request", "invalid_request")
		}
		for name, values := range req.Header {
			if !mutationHeaderNameValid(name) {
				return nil, failure("request", "invalid_header")
			}
			for _, value := range values {
				if !mutationHeaderValueValid(value) {
					return nil, failure("request", "invalid_header")
				}
			}
		}
		if mutationRoleFor(req.Context()) != mutationRoleReadOnly {
			s.delegated.Store(true)
			// These selected POSTs have nonempty bodies. Do not give the HTTP
			// transport a body-replay callback for a second wire attempt.
			clone := req.Clone(req.Context())
			clone.GetBody = nil
			req = clone
		}
	}
	return t.next.RoundTrip(req)
}
func (t *mutationDispatchTransport) CloseIdleConnections() {
	if c, ok := t.next.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

// HTTP token and field-value checks run locally, before network delegation.
func mutationHeaderNameValid(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		switch c {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		default:
			return false
		}
	}
	return true
}
func mutationHeaderValueValid(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == 0x7f || c < 0x20 && c != '\t' {
			return false
		}
	}
	return true
}

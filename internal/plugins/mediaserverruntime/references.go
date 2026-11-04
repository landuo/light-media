package mediaserverruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"reflect"
	"sync"
)

// ObjectReferences belongs to one real persistent plugin object/session. It
// retains concrete media objects, not invocation authority. Every public use
// requires a fresh host-owned current-call guard; workers never supply it.
// Closing this table releases registrations only, never the shared vendors.
// No name lookup/config-generation substitution or vendor method occurs here.
type ObjectReferences struct {
	mu      sync.Mutex
	closed  bool
	values  map[string]Vendor
	reverse map[Vendor]string
}

type ReferenceCurrent func(context.Context) error

var (
	ErrReferenceClosed       = errors.New("media object reference owner closed")
	ErrReferenceDenied       = errors.New("media object reference is not owned by this plugin session")
	ErrReferenceGuardMissing = errors.New("media object reference requires current call admission")
	ErrReferenceIdentity     = errors.New("media object reference requires a concrete pointer object")
)

func NewObjectReferences() *ObjectReferences {
	return &ObjectReferences{values: make(map[string]Vendor), reverse: make(map[Vendor]string)}
}
func referenceCurrent(ctx context.Context, current ReferenceCurrent) error {
	if ctx == nil || current == nil {
		return ErrReferenceGuardMissing
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := current(ctx); err != nil {
		return err
	}
	return ctx.Err()
}
func concreteReference(v Vendor) bool {
	if v == nil {
		return false
	}
	p := reflect.ValueOf(v)
	return p.Kind() == reflect.Pointer && !p.IsNil() && p.Comparable()
}
func validReference(token string) bool {
	if len(token) != 67 || token[:3] != "ms_" {
		return false
	}
	for _, c := range token[3:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Retain deduplicates by actual object identity, not provider/name/config.
// Query ServiceInfo/config projections are not cached in this table.
func (r *ObjectReferences) Retain(ctx context.Context, current ReferenceCurrent, v Vendor) (string, error) {
	if err := referenceCurrent(ctx, current); err != nil {
		return "", err
	}
	if r == nil {
		return "", ErrReferenceClosed
	}
	if !concreteReference(v) {
		return "", ErrReferenceIdentity
	}
	// Neither host callbacks nor entropy acquisition runs while holding mu.
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	proposed := "ms_" + hex.EncodeToString(nonce[:])
	if err := referenceCurrent(ctx, current); err != nil {
		return "", err
	}
	r.mu.Lock()
	if err := ctx.Err(); err != nil {
		r.mu.Unlock()
		return "", err
	}
	if r.closed || r.values == nil || r.reverse == nil {
		r.mu.Unlock()
		return "", ErrReferenceClosed
	}
	token, ok := r.reverse[v]
	if !ok {
		if _, exists := r.values[proposed]; exists {
			r.mu.Unlock()
			return "", ErrReferenceDenied
		}
		token = proposed
		r.values[token] = v
		r.reverse[v] = token
	}
	r.mu.Unlock()
	// A failed fresh admission never returns a reference. Do not erase a
	// shared registration another concurrent admitted query may have obtained.
	if err := referenceCurrent(ctx, current); err != nil {
		return "", err
	}
	r.mu.Lock()
	live := !r.closed && r.values[token] == v
	r.mu.Unlock()
	if !live {
		return "", ErrReferenceClosed
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return token, nil
}

// Resolve returns the exact retained object. The caller still validates current
// admission around each real operation; obtaining a pointer is not a new grant.
func (r *ObjectReferences) Resolve(ctx context.Context, current ReferenceCurrent, token string) (Vendor, error) {
	if err := referenceCurrent(ctx, current); err != nil {
		return nil, err
	}
	if r == nil {
		return nil, ErrReferenceClosed
	}
	r.mu.Lock()
	if err := ctx.Err(); err != nil {
		r.mu.Unlock()
		return nil, err
	}
	if r.closed || r.values == nil || r.reverse == nil {
		r.mu.Unlock()
		return nil, ErrReferenceClosed
	}
	v := r.values[token]
	r.mu.Unlock()
	if !validReference(token) || v == nil {
		return nil, ErrReferenceDenied
	}
	// Host validation may safely inspect other owners or retire this table.
	// It never runs under our lock, avoiding table/App/SQL lock inversion.
	if err := referenceCurrent(ctx, current); err != nil {
		return nil, err
	}
	r.mu.Lock()
	live := !r.closed && r.values[token] == v
	r.mu.Unlock()
	if !live {
		return nil, ErrReferenceClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return v, nil
}

// Close is host-only exact session retirement, after legitimate cleanup calls.
// Callback completion MUST NOT call it. Shared owner/module lifecycle is separate.
func (r *ObjectReferences) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	r.values = nil
	r.reverse = nil
}

package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"

	"light-media/internal/plugins/mediaserverruntime"
	"light-media/internal/plugins/mediaservervendors"
)

// This table belongs to one persistent original plugin object. It retains the
// real Plex library pointer returned by its vendor, not a name/config lookup.
// Callback completion and config reload do not expire these identities; plugin
// object retirement does. No Library methods are exposed by this table.
type originalMediaServerLibraryReferences struct {
	mu      sync.Mutex
	closed  bool
	values  map[string]any
	reverse map[any]string
}

func (o *originalMediaServerReferenceOwner) libraryTable() (*originalMediaServerLibraryReferences, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil, mediaserverruntime.ErrReferenceClosed
	}
	if o.libraries == nil {
		o.libraries = &originalMediaServerLibraryReferences{values: make(map[string]any), reverse: make(map[any]string)}
	}
	return o.libraries, nil
}

func originalMediaServerRefreshCurrent(ctx context.Context, current mediaserverruntime.ReferenceCurrent) error {
	if ctx == nil || current == nil {
		return errors.New("media refresh requires current invocation")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := current(ctx); err != nil {
		return err
	}
	return ctx.Err()
}

func (t *originalMediaServerLibraryReferences) retain(ctx context.Context, current mediaserverruntime.ReferenceCurrent, library any) (string, error) {
	if err := originalMediaServerRefreshCurrent(ctx, current); err != nil {
		return "", err
	}
	if !mediaservervendors.IsPlexRefreshLibraryObject(library) {
		return "", errors.New("media refresh did not return a concrete Plex library")
	}
	// The exact private concrete pointer was checked before using interface map
	// equality. Do not accept arbitrary comparable objects as source libraries.
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	token := "ml_" + hex.EncodeToString(entropy[:])
	if err := originalMediaServerRefreshCurrent(ctx, current); err != nil {
		return "", err
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return "", mediaserverruntime.ErrReferenceClosed
	}
	if old, ok := t.reverse[library]; ok {
		token = old
	} else {
		if _, exists := t.values[token]; exists {
			t.mu.Unlock()
			return "", errors.New("media library reference collision")
		}
		t.values[token] = library
		t.reverse[library] = token
	}
	t.mu.Unlock()
	if err := originalMediaServerRefreshCurrent(ctx, current); err != nil {
		return "", err
	}
	t.mu.Lock()
	closed := t.closed
	same := t.values[token] == library
	t.mu.Unlock()
	if closed || !same {
		return "", mediaserverruntime.ErrReferenceClosed
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return token, nil
}

func (t *originalMediaServerLibraryReferences) close() {
	t.mu.Lock()
	t.closed = true
	t.values = nil
	t.reverse = nil
	t.mu.Unlock()
}

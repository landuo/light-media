// This file implements only the retained source Plex wrapper lifecycle.
// PlexSDKFactory supplies the concrete private SDK/session implementation.
// This wrapper retains that object rather than substituting configuration data.
package mediaservervendors

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"

	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
)

// PlexServer is the SDK object, not its configuration or an HTTP status.
// Sections retains the SDK result as an opaque object for later consumers;
// this lifecycle slice neither fabricates nor interprets library DTOs.
type PlexServer interface {
	Truthy(context.Context) (bool, error)
	Sections(context.Context) (any, error)
}

// PlexSession is the separate requests.Session created by __adapt_plex_session.
// It is NOT the session internally owned by PlexServer. ReplaceHeaders means
// replacement, not merge with transport defaults. Close must retain its source
// reusable-session semantics; it is not a vendor terminal state transition.
type PlexSession interface {
	ReplaceHeaders(context.Context, []PlexHeader) error
	Close(context.Context) error
}
type PlexHeader struct{ Name, Value string }

// Raw header assignment is separate from Requests prepare_headers validation.
type PlexRawHeader struct {
	Name  string
	Value json.RawMessage
}
type PlexRawSession interface {
	ReplaceRawHeaders(context.Context, []PlexRawHeader) error
}
type PlexDiagnostic struct{ Stage, Class string }
type PlexDependencies struct {
	Connect    func(context.Context, string, string) (PlexServer, error)
	ConnectRaw func(context.Context, json.RawMessage, json.RawMessage) (PlexServer, error)
	NewSession func(context.Context) (PlexSession, error)
	Report     func(PlexDiagnostic)
}

// Plex is one real retained wrapper object. State locks are short; no App,
// config/store lock or synthetic operation-wide drain is held across SDK IO.
// There is deliberately no Close=>inactive flag, auto logout or auto reconnect.
type Plex struct {
	mu                             sync.RWMutex
	dependencies                   PlexDependencies
	host, token                    string
	hostRaw, tokenRaw, playHostRaw json.RawMessage
	playHost                       *string
	hostSet, tokenSet, playHostSet bool
	server                         PlexServer
	libraries                      any
	librariesSet                   bool
	session                        PlexSession
	syncLibraries                  json.RawMessage
	syncLibrariesSet               bool
}

var _ mediaserverruntime.PlexVendor = (*Plex)(nil)

// PlexFactory uses the already accepted Factory signature. A caller cannot
// construct a fake healthy object by omitting SDK or session dependencies.
func PlexFactory(dependencies PlexDependencies) (mediaserverruntime.Factory, error) {
	if dependencies.Connect == nil || dependencies.NewSession == nil || dependencies.Report == nil {
		return nil, errors.New("Plex requires concrete SDK, session and diagnostic adapters")
	}
	return func(ctx context.Context, config plugins.MediaServerConfig) (mediaserverruntime.Vendor, error) {
		if err := plexContext(ctx); err != nil {
			return nil, err
		}
		args, err := plexArguments(config)
		if err != nil {
			return nil, err
		}
		p := &Plex{dependencies: dependencies, syncLibraries: json.RawMessage(`[]`)}
		// Required falsey returns a real object before any field assignment.
		if args.incomplete {
			dependencies.Report(PlexDiagnostic{Stage: "configuration-incomplete"})
			return p, nil
		}
		p.hostRaw, err = constructorStandardURL(args.host)
		if err != nil {
			return nil, err
		}
		p.hostSet = true
		if text := constructorStringProjection(p.hostRaw); text != nil {
			p.host = *text
		}
		p.playHostRaw, err = constructorStandardURL(args.playHost)
		if err != nil {
			return nil, err
		}
		p.playHostSet = true
		p.playHost = constructorStringProjection(p.playHostRaw)
		p.tokenRaw = constructorJSON(args.token)
		if text := constructorStringProjection(p.tokenRaw); text != nil {
			p.token = *text
		}
		p.tokenSet = true
		if err := p.connect(ctx); err != nil {
			return nil, err
		}
		// Deliberately outside the connection catch, and performed after an ordinary
		// connection exception too. Never inject this session into Connect retroactively.
		session, err := dependencies.NewSession(ctx)
		if err != nil {
			return nil, err
		}
		if plexNil(session) {
			return nil, errors.New("Plex session adapter returned no object")
		}
		if token := constructorStringProjection(p.tokenRaw); token != nil {
			headers := []PlexHeader{{"X-Plex-Token", *token}, {"Accept", "application/json"}, {"Content-Type", "application/json"}}
			if err = session.ReplaceHeaders(ctx, headers); err != nil {
				return nil, err
			}
		} else {
			raw, ok := session.(PlexRawSession)
			if !ok {
				return nil, errors.New("Plex adapted session lacks raw header assignment")
			}
			if err = raw.ReplaceRawHeaders(ctx, []PlexRawHeader{{"X-Plex-Token", constructorJSON(p.tokenRaw)}, {"Accept", constructorStringJSON("application/json")}, {"Content-Type", constructorStringJSON("application/json")}}); err != nil {
				return nil, err
			}
		}
		p.session = session
		p.syncLibraries = append(json.RawMessage(nil), args.syncLibraries...)
		p.syncLibrariesSet = true
		return p, nil
	}, nil
}
func plexContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("Plex requires context")
	}
	return ctx.Err()
}
func plexNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
func plexOrdinary(err error) (*mediaserverruntime.SourceError, bool) {
	e, ok := err.(*mediaserverruntime.SourceError)
	return e, ok && e != nil && e.Class != "" && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
func plexMissing(attribute string) error {
	return &mediaserverruntime.SourceError{Class: "AttributeError", Cause: errors.New("'Plex' object has no attribute '" + attribute + "'")}
}
func (p *Plex) connect(ctx context.Context) error {
	if err := plexContext(ctx); err != nil {
		return err
	}
	p.mu.RLock()
	host, token, hs, ts := p.host, p.token, p.hostSet, p.tokenSet
	hostRaw, tokenRaw := constructorPrefer(p.hostRaw, &p.host), constructorPrefer(p.tokenRaw, &p.token)
	p.mu.RUnlock()
	// reconnect's attribute reads occur inside its source try/except.
	var err error
	if !hs {
		err = plexMissing("_host")
	} else if !ts {
		err = plexMissing("_token")
	}
	if err == nil {
		var server PlexServer
		if p.dependencies.ConnectRaw != nil {
			server, err = p.dependencies.ConnectRaw(ctx, hostRaw, tokenRaw)
		} else if constructorStringProjection(tokenRaw) == nil {
			// Real Requests rejects non-string JSON token headers at preparation.
			err = constructorSource("InvalidHeader")
		} else {
			server, err = p.dependencies.Connect(ctx, host, token)
		}
		if err == nil && plexNil(server) {
			return errors.New("Plex SDK constructor returned no object")
		}
		if err == nil {
			p.mu.Lock()
			p.server = server
			p.mu.Unlock()
			// Like self._plex.library.sections, read the current field after assignment;
			// do not turn concurrent reconnect into an invented atomic-success swap.
			p.mu.RLock()
			current := p.server
			p.mu.RUnlock()
			var libraries any
			if plexNil(current) {
				err = &mediaserverruntime.SourceError{Class: "AttributeError", Cause: errors.New("'NoneType' object has no attribute 'library'")}
			} else {
				libraries, err = current.Sections(ctx)
			}
			if err == nil {
				p.mu.Lock()
				p.libraries = libraries
				p.librariesSet = true
				p.mu.Unlock()
			}
		}
	}
	if err != nil {
		if source, ok := plexOrdinary(err); ok {
			p.mu.Lock()
			p.server = nil
			p.mu.Unlock() // Do not clear previously loaded libraries.
			p.dependencies.Report(PlexDiagnostic{Stage: "connection-error", Class: source.Class})
			return nil
		}
		return err // unknown/platform/context errors are never silently Python Exception
	}
	return nil
}
func (p *Plex) IsInactive(ctx context.Context) (bool, error) {
	if err := plexContext(ctx); err != nil {
		return false, err
	}
	p.mu.RLock()
	host, token, hs, ts, server := p.host, p.token, p.hostSet, p.tokenSet, p.server
	hostRaw, tokenRaw := p.hostRaw, p.tokenRaw
	p.mu.RUnlock()
	if !hs {
		return false, plexMissing("_host")
	}
	if (len(hostRaw) > 0 && !ejTruth(hostRaw)) || (len(hostRaw) == 0 && host == "") {
		return false, nil
	}
	if !ts {
		return false, plexMissing("_token")
	}
	if (len(tokenRaw) > 0 && !ejTruth(tokenRaw)) || (len(tokenRaw) == 0 && token == "") {
		return false, nil
	}
	if plexNil(server) {
		return true, nil
	}
	truthy, err := server.Truthy(ctx)
	return !truthy, err
}

// Reconnect is not part of the generic Vendor interface and is not implicitly
// invoked by IsInactive. The source Plex-specific scheduler/test calls it.
func (p *Plex) Reconnect(ctx context.Context) error { return p.connect(ctx) }
func (p *Plex) Close(ctx context.Context) error {
	if err := plexContext(ctx); err != nil {
		return err
	}
	p.mu.RLock()
	session := p.session
	p.mu.RUnlock()
	if !plexNil(session) {
		return session.Close(ctx)
	}
	return nil
}

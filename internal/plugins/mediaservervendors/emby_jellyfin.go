// Package mediaservervendors contains concrete source vendor state owners.
// It does not register a module, synthesize FNOS settings or expose credentials.
package mediaservervendors

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
)

// EmbyJellyfinDependencies are explicit host inputs, not a fallback registry.
// BaseTransport is cloned privately; its proxy/dial policy is never replaced.
// Superuser reads the actual current host setting. Report must never log values.
type EmbyJellyfinDependencies struct {
	BaseTransport *http.Transport
	Superuser     func(context.Context) (json.RawMessage, error)
	Current       func(context.Context) error
	Report        func(EmbyJellyfinDiagnostic)
}
type EmbyJellyfinDiagnostic struct{ Vendor, Operation, Severity, Class string }

// EmbyJellyfinState is an owned internal observation, never an HTTP/SDK response.
// Host, API key, username and play_host are deliberately not exposed here.
// Presence distinguishes source early-return missing attributes from JSON null.
type EmbyJellyfinState struct {
	Configured      bool
	User            json.RawMessage
	FoldersPresent  bool
	Folders         json.RawMessage
	ServerIDPresent bool
	ServerID        json.RawMessage
	SyncLibraries   json.RawMessage
}
type ejVendor struct {
	kind             string
	deps             EmbyJellyfinDependencies
	host, playHost   ejURL
	apiKey, username json.RawMessage
	mu               sync.RWMutex
	state            EmbyJellyfinState
	// Source reconnect publishes each attribute after its request; it is not an
	// atomic all-success swap. This lock only serializes concurrent reconnects.
	reconnectMu sync.Mutex
}
type Emby struct{ v *ejVendor }
type Jellyfin struct{ v *ejVendor }

func EmbyFactory(deps EmbyJellyfinDependencies) (mediaserverruntime.Factory, error) {
	if err := ejDependencies(deps); err != nil {
		return nil, err
	}
	return func(ctx context.Context, c plugins.MediaServerConfig) (mediaserverruntime.Vendor, error) {
		v, err := ejConstruct(ctx, "emby", deps, c)
		if err != nil {
			return nil, err
		}
		return &Emby{v: v}, nil
	}, nil
}
func JellyfinFactory(deps EmbyJellyfinDependencies) (mediaserverruntime.Factory, error) {
	if err := ejDependencies(deps); err != nil {
		return nil, err
	}
	return func(ctx context.Context, c plugins.MediaServerConfig) (mediaserverruntime.Vendor, error) {
		v, err := ejConstruct(ctx, "jellyfin", deps, c)
		if err != nil {
			return nil, err
		}
		return &Jellyfin{v: v}, nil
	}, nil
}
func ejDependencies(d EmbyJellyfinDependencies) error {
	if d.BaseTransport == nil || d.Superuser == nil || d.Current == nil || d.Report == nil {
		return errors.New("concrete Emby/Jellyfin dependencies are required")
	}
	return nil
}
func ejContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("media vendor requires context")
	}
	return ctx.Err()
}
func ejCopy(b json.RawMessage) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage(`null`)
	}
	return append(json.RawMessage(nil), b...)
}
func ejSource(class string) error { return &mediaserverruntime.SourceError{Class: class} }
func (v *ejVendor) report(op, severity, class string) {
	v.deps.Report(EmbyJellyfinDiagnostic{Vendor: v.kind, Operation: op, Severity: severity, Class: class})
}
func ejConstruct(ctx context.Context, kind string, d EmbyJellyfinDependencies, c plugins.MediaServerConfig) (*ejVendor, error) {
	if err := ejContext(ctx); err != nil {
		return nil, err
	}
	if !json.Valid(c.Config) {
		return nil, errors.New("invalid owned media config JSON")
	}
	fields, err := ejObject(c.Config)
	if err != nil {
		return nil, ejSource("TypeError")
	}
	if _, present := fields.values[`"sync_libraries"`]; present {
		return nil, ejSource("TypeError")
	}
	if _, present := fields.values[`"self"`]; present {
		return nil, ejSource("TypeError")
	}
	v := &ejVendor{kind: kind, deps: d, apiKey: json.RawMessage(`null`), username: json.RawMessage(`null`), state: EmbyJellyfinState{User: json.RawMessage(`null`), SyncLibraries: json.RawMessage(`[]`)}}
	host, key := fields.get("host"), fields.get("apikey")
	if !ejTruth(host) || !ejTruth(key) {
		v.report("constructor", "error", "IncompleteConfiguration")
		return v, nil
	}
	v.host, err = ejStandardize(host)
	if err != nil {
		return nil, err
	}
	if play := fields.get("play_host"); ejTruth(play) {
		v.playHost, err = ejStandardize(play)
		if err != nil {
			return nil, err
		}
	}
	v.apiKey = ejCopy(key)
	if kind == "emby" {
		v.username = ejCopy(fields.get("username"))
	}
	v.state.Configured = true
	userName := ejCopy(v.username)
	if kind == "jellyfin" || !ejTruth(userName) {
		userName, err = d.Superuser(ctx)
		if err != nil {
			return nil, err
		}
	}
	user, err := v.getUser(ctx, userName)
	if err != nil {
		return nil, err
	}
	v.state.User = ejCopy(user)
	if kind == "emby" {
		folders, e := v.getFolders(ctx)
		if e != nil {
			return nil, e
		}
		v.state.Folders = ejCopy(folders)
		v.state.FoldersPresent = true
	}
	id, err := v.getServerID(ctx)
	if err != nil {
		return nil, err
	}
	v.state.ServerID = ejCopy(id)
	v.state.ServerIDPresent = true
	if ejTruth(c.SyncLibraries) {
		v.state.SyncLibraries = ejCopy(c.SyncLibraries)
	}
	return v, nil
}
func (v *ejVendor) inactive(ctx context.Context) (bool, error) {
	if err := ejContext(ctx); err != nil {
		return false, err
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.state.Configured && !ejTruth(v.state.User), nil
}
func (v *ejVendor) snapshot() EmbyJellyfinState {
	v.mu.RLock()
	defer v.mu.RUnlock()
	s := v.state
	s.User = ejCopy(s.User)
	s.Folders = ejCopy(s.Folders)
	s.ServerID = ejCopy(s.ServerID)
	s.SyncLibraries = ejCopy(s.SyncLibraries)
	return s
}
func (e *Emby) IsInactive(ctx context.Context) (bool, error)     { return e.v.inactive(ctx) }
func (j *Jellyfin) IsInactive(ctx context.Context) (bool, error) { return j.v.inactive(ctx) }
func (e *Emby) Snapshot() EmbyJellyfinState                      { return e.v.snapshot() }
func (j *Jellyfin) Snapshot() EmbyJellyfinState                  { return j.v.snapshot() }
func (e *Emby) Reconnect(ctx context.Context) error              { return e.v.reconnect(ctx) }
func (j *Jellyfin) Reconnect(ctx context.Context) error          { return j.v.reconnect(ctx) }

// Neither vendor has Close/Disconnect here: both original module stop methods
// are pass. Closing an idle per-request transport is not a vendor stop call.
func (v *ejVendor) reconnect(ctx context.Context) error {
	if err := ejContext(ctx); err != nil {
		return err
	}
	v.reconnectMu.Lock()
	defer v.reconnectMu.Unlock()
	user, err := v.getUser(ctx, json.RawMessage(`null`))
	if err != nil {
		return err
	}
	v.mu.Lock()
	v.state.User = ejCopy(user)
	v.mu.Unlock()
	if v.kind == "emby" {
		folders, e := v.getFolders(ctx)
		if e != nil {
			return e
		}
		v.mu.Lock()
		v.state.Folders = ejCopy(folders)
		v.state.FoldersPresent = true
		v.mu.Unlock()
		return nil
	}
	id, err := v.getServerID(ctx)
	if err != nil {
		return err
	}
	v.mu.Lock()
	v.state.ServerID = ejCopy(id)
	v.state.ServerIDPresent = true
	v.mu.Unlock()
	return nil
}
func (v *ejVendor) getFolders(ctx context.Context) (json.RawMessage, error) {
	if v.host.text == "" || !ejTruth(v.apiKey) {
		return json.RawMessage(`[]`), nil
	}
	path := "Library/SelectableMediaFolders"
	if v.kind == "emby" {
		path = "emby/" + path
	}
	value, ok, err := v.getJSON(ctx, path)
	if err != nil {
		return nil, err
	}
	if !ok {
		return json.RawMessage(`[]`), nil
	}
	return value, nil
}
func (v *ejVendor) getServerID(ctx context.Context) (json.RawMessage, error) {
	if v.host.text == "" || !ejTruth(v.apiKey) {
		return json.RawMessage(`null`), nil
	}
	paths := []string{"System/Info"}
	if v.kind == "emby" {
		paths = append(paths, "emby/System/Info")
	}
	for _, path := range paths {
		value, ok, err := v.getJSON(ctx, path)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		if v.kind == "emby" && !ejTruth(value) {
			value = json.RawMessage(`{}`)
		}
		obj, e := ejObject(value)
		if e != nil {
			v.report(path, "error", "AttributeError")
			continue
		}
		id := obj.get("Id")
		if v.kind == "jellyfin" {
			return ejCopy(id), nil
		}
		if !ejTruth(id) {
			id = obj.get("ServerId")
		}
		if ejTruth(id) {
			return ejCopy(id), nil
		}
	}
	return json.RawMessage(`null`), nil
}
func (v *ejVendor) getUser(ctx context.Context, userName json.RawMessage) (json.RawMessage, error) {
	if v.host.text == "" || !ejTruth(v.apiKey) {
		return json.RawMessage(`null`), nil
	}
	users, ok, err := v.getJSON(ctx, "Users")
	if err != nil {
		return nil, err
	}
	if !ok {
		return json.RawMessage(`null`), nil
	}
	var rows []json.RawMessage
	candidates := []json.RawMessage{}
	if ejTruth(userName) {
		candidates = append(candidates, userName)
	}
	if v.kind == "emby" && ejTruth(v.username) && (len(candidates) == 0 || !ejEqual(candidates[0], v.username)) {
		candidates = append(candidates, v.username)
	}
	iterated := false
	if v.kind == "emby" || len(candidates) > 0 {
		var e error
		rows, e = ejIter(users)
		if e != nil {
			v.report("Users", "error", "TypeError")
			return json.RawMessage(`null`), nil
		}
		iterated = true
	}
	for _, name := range candidates {
		for _, row := range rows {
			obj, e := ejObject(row)
			if e != nil {
				return v.userShapeFailure()
			}
			if ejEqual(obj.get("Name"), name) {
				return ejCopy(obj.get("Id")), nil
			}
		}
	}
	if v.kind == "emby" {
		for _, row := range rows {
			obj, e := ejObject(row)
			if e != nil {
				return v.userShapeFailure()
			}
			policy, e := ejObject(obj.getDefault("Policy", json.RawMessage(`{}`)))
			if e != nil {
				return v.userShapeFailure()
			}
			if ejTruth(policy.get("IsAdministrator")) {
				return ejCopy(obj.get("Id")), nil
			}
		}
		return json.RawMessage(`null`), nil
	}
	superuser, e := v.deps.Superuser(ctx)
	if e != nil {
		return nil, e
	}
	if ejEqual(userName, superuser) {
		v.report("Users", "warning", "SuperuserNotFound")
	}
	folders, e := v.getFolders(ctx)
	if e != nil {
		return nil, e
	}
	if _, e = ejLen(folders); e != nil {
		v.report("Users", "error", "TypeError")
		return json.RawMessage(`null`), nil
	}
	if !iterated {
		rows, e = ejIter(users)
		if e != nil {
			v.report("Users", "error", "TypeError")
			return json.RawMessage(`null`), nil
		}
	}
	best := json.RawMessage(`null`)
	bestCount := -1
	for _, row := range rows {
		obj, e := ejObject(row)
		if e != nil {
			return v.userShapeFailure()
		}
		p := obj.get("Policy")
		if !ejTruth(p) {
			p = json.RawMessage(`{}`)
		}
		policy, e := ejObject(p)
		if e != nil {
			return v.userShapeFailure()
		}
		if !ejTruth(policy.get("IsAdministrator")) {
			continue
		}
		if ejTruth(policy.get("EnableAllFolders")) {
			return ejCopy(obj.get("Id")), nil
		}
		enabled := policy.get("EnabledFolders")
		if !ejTruth(enabled) {
			enabled = json.RawMessage(`[]`)
		}
		count, e := ejLen(enabled)
		if e != nil {
			v.report("Users", "error", "TypeError")
			return json.RawMessage(`null`), nil
		}
		if ejNull(best) || count > bestCount {
			best = ejCopy(obj.get("Id"))
			bestCount = count
		}
	}
	if ejNull(best) {
		v.report("Users", "warning", "AdministratorNotFound")
	} else {
		v.report("Users", "warning", "LimitedAdministratorFallback")
	}
	return best, nil
}
func (v *ejVendor) userShapeFailure() (json.RawMessage, error) {
	v.report("Users", "error", "AttributeError")
	return json.RawMessage(`null`), nil
}

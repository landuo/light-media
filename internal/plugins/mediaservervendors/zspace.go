package mediaservervendors

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
)

// These are private request-construction values, not a new RPC or permission.
// URL and Origin retain JSON string codepoints until the concrete HTTP adapter.
// Headers retain raw source values: a non-string token must not be coerced into
// a valid credential. PythonJSONData is ordered json.dumps input, not Go map
// serialization and not the requests json= parameter.
type ZSpaceField struct {
	Name  string
	Value json.RawMessage
}
type ZSpaceRequest struct {
	Method            string
	URL, Origin       json.RawMessage
	Headers           []ZSpaceField
	PythonJSONData    []ZSpaceField
	Params            []ZSpaceField // Ordered requests params=, absent on legacy constructor calls.
	Timeout           time.Duration
	AllowRedirects    bool
	VerifyCertificate bool
	UseSession        bool
}
type ZSpaceResponse struct {
	StatusCode int
	JSON       func() (json.RawMessage, error)
}

// Request is mandatory and must be a concrete origin-bound private HTTP
// adapter at production assembly, never a default success/recording fallback.
// It owns body/connection closure, unchanged environment proxy routing and
// per-hop current/credential checks. This source wrapper has no hidden client.
type ZSpaceDependencies struct {
	Current func(context.Context) error
	Request func(context.Context, ZSpaceRequest) (*ZSpaceResponse, error)
	Report  func(ZSpaceDiagnostic)
}
type ZSpaceDiagnostic struct{ Operation, Severity, Class string }

// Internal, copied observations only. Credentials/URL/token are not exposed.
// Attribute presence preserves the original constructor's early return.
type ZSpaceState struct {
	Configured, TokenPresent bool
	User                     json.RawMessage
	FoldersPresent           bool
	Folders                  json.RawMessage
	ServerIDPresent          bool
	ServerID                 json.RawMessage
	SyncLibraries            json.RawMessage
}
type ZSpace struct {
	deps                               ZSpaceDependencies
	host, playHost, username, password json.RawMessage
	mu                                 sync.RWMutex
	apiKey                             json.RawMessage
	state                              ZSpaceState
	reconnectMu                        sync.Mutex
}

func ZSpaceFactory(deps ZSpaceDependencies) (mediaserverruntime.Factory, error) {
	if deps.Current == nil || deps.Request == nil || deps.Report == nil {
		return nil, errors.New("concrete ZSpace current, HTTP and diagnostic adapters are required")
	}
	return func(ctx context.Context, config plugins.MediaServerConfig) (mediaserverruntime.Vendor, error) {
		if err := zspaceCurrent(ctx, deps); err != nil {
			return nil, err
		}
		fields, err := zspaceConstructorFields(config.Config)
		if err != nil {
			return nil, err
		}
		value := &ZSpace{deps: deps, host: zspaceNull, playHost: zspaceNull, username: zspaceNull, password: zspaceNull, apiKey: zspaceNull,
			state: ZSpaceState{User: zspaceCopy(zspaceNull), SyncLibraries: json.RawMessage(`[]`)}}
		host, username, password := fields.get("host"), fields.get("username"), fields.get("password")
		if !zspaceTruth(host) || !zspaceTruth(username) || !zspaceTruth(password) {
			value.report("constructor", "error", "IncompleteConfiguration")
			return value, nil
		}
		value.host, err = zspaceStandardize(host)
		if err != nil {
			return nil, err
		}
		value.playHost = zspaceCopy(fields.get("play_host"))
		if zspaceTruth(value.playHost) {
			value.playHost, err = zspaceStandardize(value.playHost)
			if err != nil {
				return nil, err
			}
		}
		value.username, value.password = zspaceCopy(username), zspaceCopy(password)
		value.state.Configured = true
		if zspaceTruth(config.SyncLibraries) {
			value.state.SyncLibraries = zspaceCopy(config.SyncLibraries)
		}
		value.state.FoldersPresent, value.state.ServerIDPresent = true, true
		value.state.Folders, value.state.ServerID = json.RawMessage(`[]`), zspaceCopy(zspaceNull)
		ok, err := value.Reconnect(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			value.report("constructor", "error", "ReconnectFailed")
		}
		return value, nil
	}, nil
}

type zspaceGuardError struct{ cause error }

func (e *zspaceGuardError) Error() string {
	return "ZSpace current-generation guard rejected operation"
}
func (e *zspaceGuardError) Unwrap() error { return e.cause }
func zspaceCurrent(ctx context.Context, deps ZSpaceDependencies) error {
	if ctx == nil {
		return errors.New("ZSpace requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := deps.Current(ctx); err != nil {
		return &zspaceGuardError{cause: err}
	}
	return ctx.Err()
}
func (v *ZSpace) report(operation, severity, class string) {
	v.deps.Report(ZSpaceDiagnostic{Operation: operation, Severity: severity, Class: class})
}
func (v *ZSpace) Snapshot() ZSpaceState {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := v.state
	out.User = zspaceCopy(out.User)
	if out.FoldersPresent {
		out.Folders = zspaceCopy(out.Folders)
	}
	if out.ServerIDPresent {
		out.ServerID = zspaceCopy(out.ServerID)
	}
	out.SyncLibraries = zspaceCopy(out.SyncLibraries)
	return out
}
func (v *ZSpace) IsInactive(ctx context.Context) (bool, error) {
	if err := zspaceCurrent(ctx, v.deps); err != nil {
		return false, err
	}
	if !zspaceTruth(v.host) || !zspaceTruth(v.username) || !zspaceTruth(v.password) {
		return false, nil
	}
	v.mu.RLock()
	key, user := zspaceCopy(v.apiKey), zspaceCopy(v.state.User)
	v.mu.RUnlock()
	if !zspaceTruth(key) || !zspaceTruth(user) {
		return true, nil
	}
	current, err := v.currentUser(ctx)
	if err != nil {
		return false, err
	}
	if !zspaceTruth(current) {
		return true, nil
	}
	fields, err := zspaceObject(current)
	if err != nil {
		return false, errors.New("ZSpace current-user adapter invariant")
	}
	if id := fields.get("Id"); zspaceTruth(id) {
		v.mu.Lock()
		v.state.User = zspaceCopy(id)
		v.mu.Unlock()
	}
	return false, nil
}

// Reconnect returns the original bool and retains partial attribute updates.
// It does not stop other instances or turn failure into a healthy config map.
func (v *ZSpace) Reconnect(ctx context.Context) (bool, error) {
	if err := zspaceCurrent(ctx, v.deps); err != nil {
		return false, err
	}
	v.reconnectMu.Lock()
	defer v.reconnectMu.Unlock()
	token, user, err := v.login(ctx)
	if err != nil {
		return false, err
	}
	if !zspaceTruth(token) {
		v.mu.Lock()
		v.apiKey = zspaceCopy(zspaceNull)
		v.state.TokenPresent = false
		v.state.User = zspaceCopy(zspaceNull)
		v.state.FoldersPresent, v.state.ServerIDPresent = true, true
		v.state.Folders, v.state.ServerID = json.RawMessage(`[]`), zspaceCopy(zspaceNull)
		v.mu.Unlock()
		return false, nil
	}
	v.mu.Lock()
	v.apiKey = zspaceCopy(token)
	v.state.TokenPresent = true
	v.mu.Unlock()
	// Source RHS fallback sees the PREVIOUS user under the newly received token.
	// Do not clear the old user before __get_current_user_id is evaluated.
	if !zspaceTruth(user) {
		user, err = v.currentUserID(ctx)
		if err != nil {
			return false, err
		}
	}
	v.mu.Lock()
	v.state.User = zspaceCopy(user)
	v.mu.Unlock()
	if !zspaceTruth(user) {
		v.mu.Lock()
		v.apiKey = zspaceCopy(zspaceNull)
		v.state.TokenPresent = false
		v.state.FoldersPresent, v.state.ServerIDPresent = true, true
		v.state.Folders, v.state.ServerID = json.RawMessage(`[]`), zspaceCopy(zspaceNull)
		v.mu.Unlock()
		return false, nil
	}
	folders, err := v.libraryFolders(ctx)
	if err != nil {
		return false, err
	}
	v.mu.Lock()
	v.state.Folders = zspaceCopy(folders)
	v.state.FoldersPresent = true
	v.mu.Unlock()
	id, err := v.serverID(ctx)
	if err != nil {
		return false, err
	}
	v.mu.Lock()
	v.state.ServerID = zspaceCopy(id)
	v.state.ServerIDPresent = true
	v.mu.Unlock()
	return true, nil
}

// Original ZSpaceModule.stop is pass. No Close/Disconnect operation is added.

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

// UgreenAPI is the original wrapper's REQUIRED real session boundary. Recording
// implementations belong only to finite source/target tests. No default API is
// supplied here; concrete RSA/AES-GCM/HTTP assembly is a separate dependency.
type UgreenAPI interface {
	Token(context.Context) (json.RawMessage, error)
	ImportSession(context.Context, json.RawMessage) (bool, error)
	ExportSession(context.Context) (json.RawMessage, error)
	Login(context.Context, json.RawMessage, json.RawMessage) (json.RawMessage, error)
	CurrentUser(context.Context) (json.RawMessage, error)
	Logout(context.Context) error
	Close(context.Context) error
}
type UgreenAPIOptions struct {
	Host            json.RawMessage
	SourceVerifySSL bool // Source state only; concrete private HTTPS uses approved noverify.
}

// Get returns a detached value, as original SystemConfigOper.get(deepcopy).
// Set must preserve its ORIGINAL SYNC hot-deepcopy-before-DB ordering. This is
// neither an unsealed public setting nor the async MediaServers producer.
type UgreenSessionStore interface {
	Get(context.Context) (json.RawMessage, error)
	Set(context.Context, json.RawMessage) error
}
type UgreenDiagnostic struct{ Operation, Severity, Class string }
type UgreenDependencies struct {
	Current  func(context.Context) error
	NewAPI   func(context.Context, UgreenAPIOptions) (UgreenAPI, error)
	Sessions UgreenSessionStore
	Now      func() time.Time
	Report   func(UgreenDiagnostic)
}
type UgreenState struct {
	Configured, APIInstalled bool
	UserInfo, SyncLibraries  json.RawMessage
	ScanType                 int
	SourceVerifySSL          bool
	Libraries, LibraryPaths  json.RawMessage
}
type Ugreen struct {
	deps                               UgreenDependencies
	operation                          sync.Mutex
	mu                                 sync.RWMutex
	host, username, password, playHost json.RawMessage
	api                                UgreenAPI
	state                              UgreenState
}

func UgreenFactory(deps UgreenDependencies) (mediaserverruntime.Factory, error) {
	if deps.Current == nil || deps.NewAPI == nil || deps.Sessions == nil || deps.Now == nil || deps.Report == nil {
		return nil, errors.New("real Ugreen current/API/private store/clock/report adapters required")
	}
	return func(ctx context.Context, config plugins.MediaServerConfig) (mediaserverruntime.Vendor, error) {
		if err := ugreenCurrent(ctx, deps); err != nil {
			return nil, err
		}
		fields, err := ejObject(config.Config)
		if err != nil {
			return nil, ejSource("TypeError")
		}
		for _, key := range []string{"self", "sync_libraries"} {
			if !ejNull(fields.getDefault(key, nil)) {
				return nil, ejSource("TypeError")
			}
		}
		// Even an explicit null duplicate is rejected by Python's **kwargs call.
		for _, key := range fields.keys {
			if text, e := ejText(key); e == nil && (text == "self" || text == "sync_libraries") {
				return nil, ejSource("TypeError")
			}
		}
		none := json.RawMessage(`null`)
		v := &Ugreen{deps: deps, host: ejCopy(none), username: ejCopy(none), password: ejCopy(none), playHost: ejCopy(none), state: UgreenState{UserInfo: ejCopy(none), SyncLibraries: json.RawMessage(`[]`), ScanType: 2, SourceVerifySSL: true, Libraries: json.RawMessage(`{}`), LibraryPaths: json.RawMessage(`{}`)}}
		host, user, password := fields.get("host"), fields.get("username"), fields.get("password")
		if !ejTruth(host) || !ejTruth(user) || !ejTruth(password) {
			v.report("constructor", "error", "IncompleteConfiguration")
			return v, nil
		}
		v.host, v.username, v.password = ejCopy(host), ejCopy(user), ejCopy(password)
		v.state.Configured = true
		if ejTruth(config.SyncLibraries) {
			v.state.SyncLibraries = ejCopy(config.SyncLibraries)
		}
		v.state.ScanType, err = ugreenScanType(fields.get("scan_mode"), fields.get("scan_type"))
		if err != nil {
			return nil, err
		}
		v.state.SourceVerifySSL, err = ugreenVerify(fields.getDefault("verify_ssl", json.RawMessage(`true`)))
		if err != nil {
			return nil, err
		}
		if play := fields.get("play_host"); ejTruth(play) {
			v.playHost, err = ugreenStandardize(play)
			if err != nil {
				return nil, err
			}
		}
		ok, err := v.Reconnect(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			v.report("constructor", "error", "ReconnectFailed")
		}
		return v, nil
	}, nil
}
func ugreenCurrent(ctx context.Context, deps UgreenDependencies) error {
	if ctx == nil {
		return errors.New("Ugreen requires context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := deps.Current(ctx); err != nil {
		return &EmbyJellyfinBoundaryError{Kind: "Ugreen current authority", cause: err}
	}
	return ctx.Err()
}
func ugreenCall[T any](ctx context.Context, v *Ugreen, call func() (T, error)) (T, error) {
	var zero T
	if err := ugreenCurrent(ctx, v.deps); err != nil {
		return zero, err
	}
	value, err := call()
	if guard := ugreenCurrent(ctx, v.deps); guard != nil {
		return zero, guard
	}
	return value, err
}
func (v *Ugreen) report(operation, severity, class string) {
	v.deps.Report(UgreenDiagnostic{operation, severity, class})
}
func (v *Ugreen) Snapshot() UgreenState {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := v.state
	out.APIInstalled = v.api != nil
	out.UserInfo = ejCopy(out.UserInfo)
	out.SyncLibraries = ejCopy(out.SyncLibraries)
	out.Libraries = ejCopy(out.Libraries)
	out.LibraryPaths = ejCopy(out.LibraryPaths)
	return out
}
func (v *Ugreen) authenticated(ctx context.Context) (bool, error) {
	if err := ugreenCurrent(ctx, v.deps); err != nil {
		return false, err
	}
	v.mu.RLock()
	configured, api, user := v.state.Configured, v.api, ejCopy(v.state.UserInfo)
	v.mu.RUnlock()
	if !configured || api == nil {
		return false, nil
	}
	token, err := ugreenCall(ctx, v, func() (json.RawMessage, error) { return api.Token(ctx) })
	return !ejNull(token) && !ejNull(user), err
}
func (v *Ugreen) IsAuthenticated(ctx context.Context) (bool, error) {
	v.operation.Lock()
	defer v.operation.Unlock()
	return v.authenticated(ctx)
}
func (v *Ugreen) IsInactive(ctx context.Context) (bool, error) {
	v.operation.Lock()
	defer v.operation.Unlock()
	auth, err := v.authenticated(ctx)
	if err != nil {
		return false, err
	}
	if !auth {
		return true, nil
	}
	v.mu.RLock()
	api := v.api
	v.mu.RUnlock()
	user, err := ugreenCall(ctx, v, func() (json.RawMessage, error) { return api.CurrentUser(ctx) })
	if err != nil {
		return false, err
	}
	v.mu.Lock()
	v.state.UserInfo = ejCopy(user)
	v.mu.Unlock()
	return ejNull(user), nil
}
func (v *Ugreen) newAPI(ctx context.Context) (UgreenAPI, error) {
	api, err := ugreenCall(ctx, v, func() (UgreenAPI, error) {
		return v.deps.NewAPI(ctx, UgreenAPIOptions{Host: ejCopy(v.host), SourceVerifySSL: v.state.SourceVerifySSL})
	})
	if err == nil && api == nil {
		return nil, &EmbyJellyfinBoundaryError{Kind: "nil real Ugreen API"}
	}
	return api, err
}
func (v *Ugreen) disconnect(ctx context.Context, logout bool) error {
	if err := ugreenCurrent(ctx, v.deps); err != nil {
		return err
	}
	v.mu.RLock()
	api := v.api
	v.mu.RUnlock()
	if api != nil {
		if logout {
			_, err := ugreenCall(ctx, v, func() (bool, error) { return true, api.Logout(ctx) })
			if err != nil {
				return err
			}
			if err := v.removeSession(ctx); err != nil {
				return err
			}
		}
		_, err := ugreenCall(ctx, v, func() (bool, error) { return true, api.Close(ctx) })
		if err != nil {
			return err
		}
		v.mu.Lock()
		v.api = nil
		v.state.UserInfo = json.RawMessage(`null`)
		v.mu.Unlock()
		v.report("disconnect", "debug", "Disconnected")
	}
	v.mu.Lock()
	v.state.Libraries = json.RawMessage(`{}`)
	v.state.LibraryPaths = json.RawMessage(`{}`)
	v.mu.Unlock()
	return nil
}

// AuthenticatedVendor stop uses the original DEFAULT logout=False. Do not copy
// Trime's mandatory logout behavior or destroy a reusable persisted session.
func (v *Ugreen) Disconnect(ctx context.Context) error {
	v.operation.Lock()
	defer v.operation.Unlock()
	return v.disconnect(ctx, false)
}
func (v *Ugreen) DisconnectWithLogout(ctx context.Context) error {
	v.operation.Lock()
	defer v.operation.Unlock()
	return v.disconnect(ctx, true)
}
func (v *Ugreen) Close(ctx context.Context) error { return v.Disconnect(ctx) }
func (v *Ugreen) Reconnect(ctx context.Context) (bool, error) {
	v.operation.Lock()
	defer v.operation.Unlock()
	if err := ugreenCurrent(ctx, v.deps); err != nil {
		return false, err
	}
	if !v.state.Configured {
		return false, nil
	}
	v.mu.Lock()
	v.state.Libraries = json.RawMessage(`{}`)
	v.state.LibraryPaths = json.RawMessage(`{}`)
	v.mu.Unlock()
	if err := v.disconnect(ctx, false); err != nil {
		return false, err
	}
	restored, err := v.restoreSession(ctx)
	if err != nil || restored {
		return restored, err
	}
	api, err := v.newAPI(ctx)
	if err != nil {
		return false, err
	}
	v.mu.Lock()
	v.api = api
	v.mu.Unlock()
	token, err := ugreenCall(ctx, v, func() (json.RawMessage, error) { return api.Login(ctx, ejCopy(v.username), ejCopy(v.password)) })
	if err != nil {
		return false, err
	}
	if ejNull(token) {
		return false, v.removeSession(ctx)
	}
	user, err := ugreenCall(ctx, v, func() (json.RawMessage, error) { return api.CurrentUser(ctx) })
	if err != nil {
		return false, err
	}
	v.mu.Lock()
	v.state.UserInfo = ejCopy(user)
	v.mu.Unlock()
	if !ejTruth(user) {
		return false, v.removeSession(ctx)
	}
	if err := v.saveSession(ctx); err != nil {
		return false, err
	}
	v.report("reconnect", "debug", "LoggedIn")
	return true, nil
}

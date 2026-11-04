package mediaservervendors

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"light-media/internal/plugins/mediaserverruntime"
)

const zspaceClientAuthorization = `MediaBrowser Client="MoviePilot", Device="requests", DeviceId="1", Version="1.0.0"`

// Only explicitly classified source exceptions are caught by original vendor
// helpers. Never unwrap a join/unknown error into a swallowed source error.
func zspaceOrdinary(err error) (string, bool) {
	value, ok := err.(*mediaserverruntime.SourceError)
	if !ok || value == nil || value.Class == "" || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "", false
	}
	return value.Class, true
}
func (v *ZSpace) caught(operation, severity string, err error) error {
	if class, ok := zspaceOrdinary(err); ok {
		v.report(operation, severity, class)
		return nil
	}
	return err
}
func (v *ZSpace) request(ctx context.Context, path json.RawMessage, login bool) (*ZSpaceResponse, error) {
	if err := zspaceCurrent(ctx, v.deps); err != nil {
		return nil, err
	}
	url, err := zspaceConcat(v.host, path)
	if err != nil {
		return nil, err
	}
	headers := []ZSpaceField{{Name: "X-Emby-Authorization", Value: zspaceString(zspaceClientAuthorization)}}
	v.mu.RLock()
	key := zspaceCopy(v.apiKey)
	v.mu.RUnlock()
	request := ZSpaceRequest{Method: "get", URL: url, Origin: zspaceCopy(v.host), Headers: headers,
		Timeout: 20 * time.Second, AllowRedirects: true, VerifyCertificate: false, UseSession: false}
	if login {
		request.Method = "post"
		request.Headers = append(request.Headers, ZSpaceField{Name: "Content-Type", Value: zspaceString("application/json")}, ZSpaceField{Name: "Accept", Value: zspaceString("application/json")})
		request.PythonJSONData = []ZSpaceField{{Name: "Username", Value: zspaceCopy(v.username)}, {Name: "Pw", Value: zspaceCopy(v.password)}}
	} else if zspaceTruth(key) {
		request.Headers = append(request.Headers, ZSpaceField{Name: "X-Emby-Token", Value: key})
	}
	response, err := v.deps.Request(ctx, request)
	// An invalidated current generation wins even if the request returned a
	// source-like error. No source catch can swallow this guard return.
	if current := zspaceCurrent(ctx, v.deps); current != nil {
		return nil, current
	}
	return response, err
}
func (v *ZSpace) responseJSON(ctx context.Context, response *ZSpaceResponse) (json.RawMessage, error) {
	if err := zspaceCurrent(ctx, v.deps); err != nil {
		return nil, err
	}
	if response.JSON == nil {
		return nil, errors.New("ZSpace response lacks concrete JSON decoder")
	}
	raw, err := response.JSON()
	if current := zspaceCurrent(ctx, v.deps); current != nil {
		return nil, current
	}
	if err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, errors.New("ZSpace JSON adapter returned invalid owned JSON without a source error")
	}
	return zspaceCopy(raw), nil
}
func (v *ZSpace) login(ctx context.Context) (json.RawMessage, json.RawMessage, error) {
	none := zspaceCopy(zspaceNull)
	if !zspaceTruth(v.host) || !zspaceTruth(v.username) || !zspaceTruth(v.password) {
		return none, none, nil
	}
	operation := "Users/AuthenticateByName"
	response, err := v.request(ctx, zspaceString("emby/"+operation), true)
	if err != nil {
		return none, none, v.caught(operation, "error", err)
	}
	if response == nil || response.StatusCode >= 400 {
		v.report(operation, "error", "NoResponse")
		return none, none, nil
	}
	result, err := v.responseJSON(ctx, response)
	if err != nil {
		return none, none, v.caught(operation, "error", err)
	}
	if !zspaceTruth(result) {
		result = json.RawMessage(`{}`)
	}
	fields, err := zspaceObject(result)
	if err != nil {
		v.report(operation, "error", "AttributeError")
		return none, none, nil
	}
	token := fields.get("AccessToken")
	user, err := zspaceObject(fields.getDefault("User", json.RawMessage(`{}`)))
	if err != nil {
		v.report(operation, "error", "AttributeError")
		return none, none, nil
	}
	id := user.get("Id")
	if zspaceTruth(token) {
		return zspaceCopy(token), zspaceCopy(id), nil
	}
	return none, none, nil
}
func (v *ZSpace) currentUser(ctx context.Context) (json.RawMessage, error) {
	v.mu.RLock()
	key, user := zspaceCopy(v.apiKey), zspaceCopy(v.state.User)
	v.mu.RUnlock()
	if !zspaceTruth(v.host) || !zspaceTruth(key) || !zspaceTruth(user) {
		return zspaceCopy(zspaceNull), nil
	}
	suffix, err := zspaceUserPath(user, "")
	if err != nil {
		return nil, err
	}
	response, err := v.request(ctx, suffix, false)
	if err != nil {
		return zspaceCopy(zspaceNull), v.caught("Users/current", "error", err)
	}
	if response == nil || response.StatusCode >= 400 {
		return zspaceCopy(zspaceNull), nil
	}
	result, err := v.responseJSON(ctx, response)
	if err != nil {
		return zspaceCopy(zspaceNull), v.caught("Users/current", "error", err)
	}
	if _, err := zspaceObject(result); err != nil {
		return zspaceCopy(zspaceNull), nil
	}
	return result, nil
}
func (v *ZSpace) currentUserID(ctx context.Context) (json.RawMessage, error) {
	user, err := v.currentUser(ctx)
	if err != nil {
		return nil, err
	}
	if !zspaceTruth(user) {
		return zspaceCopy(zspaceNull), nil
	}
	fields, err := zspaceObject(user)
	if err != nil {
		return nil, errors.New("ZSpace user-id adapter invariant")
	}
	id := fields.get("Id")
	if zspaceTruth(id) {
		v.mu.Lock()
		v.state.User = zspaceCopy(id)
		v.mu.Unlock()
		return zspaceCopy(id), nil
	}
	return zspaceCopy(zspaceNull), nil
}
func (v *ZSpace) libraryFolders(ctx context.Context) (json.RawMessage, error) {
	v.mu.RLock()
	key := zspaceCopy(v.apiKey)
	v.mu.RUnlock()
	if !zspaceTruth(v.host) || !zspaceTruth(key) {
		return json.RawMessage(`[]`), nil
	}
	operation := "Library/SelectableMediaFolders"
	response, err := v.request(ctx, zspaceString("emby/"+operation), false)
	if err == nil && response != nil && response.StatusCode < 400 {
		result, e := v.responseJSON(ctx, response)
		if e == nil {
			return result, nil
		}
		err = e
	}
	if err != nil {
		if e := v.caught(operation, "debug", err); e != nil {
			return nil, e
		}
	} else {
		v.report(operation, "debug", "NoResponse")
	}
	result, err := v.libraryViews(ctx)
	if err != nil {
		return nil, err
	}
	if !zspaceTruth(result) {
		return json.RawMessage(`[]`), nil
	}
	return result, nil
}

// Constructor/reconnect calls the complete no-username branch only. The public
// get_librarys/get_user business methods are not part of this factory slice.
func (v *ZSpace) libraryViews(ctx context.Context) (json.RawMessage, error) {
	v.mu.RLock()
	key, user := zspaceCopy(v.apiKey), zspaceCopy(v.state.User)
	v.mu.RUnlock()
	if !zspaceTruth(v.host) || !zspaceTruth(key) || !zspaceTruth(user) {
		return zspaceCopy(zspaceNull), nil
	}
	path, err := zspaceUserPath(user, "/Views")
	if err != nil {
		return nil, err
	}
	response, err := v.request(ctx, path, false)
	if err != nil {
		return zspaceCopy(zspaceNull), v.caught("Users/Views", "error", err)
	}
	if response == nil || response.StatusCode >= 400 {
		v.report("Users/Views", "error", "NoResponse")
		return zspaceCopy(zspaceNull), nil
	}
	value, err := v.responseJSON(ctx, response)
	if err != nil {
		return zspaceCopy(zspaceNull), v.caught("Users/Views", "error", err)
	}
	fields, err := zspaceObject(value)
	if err != nil {
		v.report("Users/Views", "error", "AttributeError")
		return zspaceCopy(zspaceNull), nil
	}
	items := fields.get("Items")
	if !zspaceArray(items) {
		return zspaceCopy(zspaceNull), nil
	}
	return zspaceCopy(items), nil
}
func (v *ZSpace) serverID(ctx context.Context) (json.RawMessage, error) {
	v.mu.RLock()
	key := zspaceCopy(v.apiKey)
	v.mu.RUnlock()
	if !zspaceTruth(v.host) || !zspaceTruth(key) {
		return zspaceCopy(zspaceNull), nil
	}
	response, err := v.request(ctx, zspaceString("emby/System/Info"), false)
	if err != nil {
		return zspaceCopy(zspaceNull), v.caught("System/Info", "error", err)
	}
	if response == nil || response.StatusCode >= 400 {
		v.report("System/Info", "error", "NoResponse")
		return zspaceCopy(zspaceNull), nil
	}
	value, err := v.responseJSON(ctx, response)
	if err != nil {
		return zspaceCopy(zspaceNull), v.caught("System/Info", "error", err)
	}
	fields, err := zspaceObject(value)
	if err != nil {
		v.report("System/Info", "error", "AttributeError")
		return zspaceCopy(zspaceNull), nil
	}
	return zspaceCopy(fields.get("Id")), nil
}

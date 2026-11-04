package mediaservervendors

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
)

func (v *Ugreen) loadSessions(ctx context.Context) (ejMembers, error) {
	raw, err := ugreenCall(ctx, v, func() (json.RawMessage, error) { return v.deps.Sessions.Get(ctx) })
	if err != nil {
		return ejMembers{}, err
	}
	// Original get returns a deepcopy; caller modifications are not an alias into
	// the current hot value. Non-dict persisted values become a fresh empty dict.
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || !json.Valid(raw) {
		return ejMembers{}, &EmbyJellyfinBoundaryError{Kind: "invalid private session JSON"}
	}
	if raw[0] != '{' {
		return ejObject(json.RawMessage(`{}`))
	}
	return ejObject(raw)
}
func (v *Ugreen) persistSessions(ctx context.Context, values ejMembers) error {
	raw := ugreenObjectRaw(values)
	_, err := ugreenCall(ctx, v, func() (bool, error) { return true, v.deps.Sessions.Set(ctx, raw) })
	return err
}
func (v *Ugreen) removeSession(ctx context.Context) error {
	key, err := v.sessionKey()
	if err != nil {
		return err
	}
	sessions, err := v.loadSessions(ctx)
	if err != nil {
		return err
	}
	if ugreenDelete(&sessions, key) {
		return v.persistSessions(ctx, sessions)
	}
	return nil
}
func (v *Ugreen) saveSession(ctx context.Context) error {
	v.mu.RLock()
	api := v.api
	v.mu.RUnlock()
	if api == nil {
		return nil
	}
	state, err := ugreenCall(ctx, v, func() (json.RawMessage, error) { return api.ExportSession(ctx) })
	if err != nil {
		return err
	}
	if !ejTruth(state) {
		return nil
	}
	sessions, err := v.loadSessions(ctx)
	if err != nil {
		return err
	}
	key, err := v.sessionKey()
	if err != nil {
		return err
	}
	fields, err := ejObject(state)
	if err != nil {
		return ejSource("TypeError")
	}
	host, err := ugreenStandardize(v.host)
	if err != nil {
		return err
	}
	digest, err := v.passwordDigest()
	if err != nil {
		return err
	}
	ugreenSet(&fields, "host", host)
	ugreenSet(&fields, "username", v.username)
	ugreenSet(&fields, "password_digest", ugreenKey(digest))
	ugreenSet(&fields, "updated_at", json.RawMessage(strconv.FormatInt(v.deps.Now().Unix(), 10)))
	ugreenSet(&sessions, key, ugreenObjectRaw(fields))
	return v.persistSessions(ctx, sessions)
}
func (v *Ugreen) restoreSession(ctx context.Context) (bool, error) {
	key, err := v.sessionKey()
	if err != nil {
		return false, err
	}
	sessions, err := v.loadSessions(ctx)
	if err != nil {
		return false, err
	}
	cached := sessions.get(key)
	fields, err := ejObject(cached)
	if err != nil {
		return false, nil
	}
	digest, err := v.passwordDigest()
	if err != nil {
		return false, err
	}
	expected := ugreenKey(digest)
	if !bytes.Equal(bytes.TrimSpace(fields.get("password_digest")), expected) {
		// The digest is an ASCII hexdigest; compare decoded strings, not arbitrary
		// JSON textual spelling (an escaped equivalent must match).
		same := false
		if text, e := ejText(fields.get("password_digest")); e == nil {
			same = text == digest
		}
		if !same {
			v.report("restore_session", "info", "PasswordChanged")
			return false, v.removeSession(ctx)
		}
	}
	api, err := v.newAPI(ctx)
	if err != nil {
		return false, err
	}
	imported, err := ugreenCall(ctx, v, func() (bool, error) { return api.ImportSession(ctx, ejCopy(cached)) })
	if err != nil {
		return false, err
	}
	if !imported {
		_, err := ugreenCall(ctx, v, func() (bool, error) { return true, api.Close(ctx) })
		if err != nil {
			return false, err
		}
		return false, v.removeSession(ctx)
	}
	user, err := ugreenCall(ctx, v, func() (json.RawMessage, error) { return api.CurrentUser(ctx) })
	if err != nil {
		return false, err
	}
	if !ejTruth(user) {
		_, err := ugreenCall(ctx, v, func() (bool, error) { return true, api.Close(ctx) })
		if err != nil {
			return false, err
		}
		if err := v.removeSession(ctx); err != nil {
			return false, err
		}
		v.report("restore_session", "info", "SessionExpired")
		return false, nil
	}
	v.mu.Lock()
	v.api = api
	v.state.UserInfo = ejCopy(user)
	v.mu.Unlock()
	v.report("restore_session", "debug", "SessionReused")
	return true, nil
}

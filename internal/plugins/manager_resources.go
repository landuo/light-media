package plugins

import (
	"context"
	"encoding/json"
	"errors"
)

const BackgroundSubmitCapability = "plugin.background.submit"
const ServicesRefreshCapability = "plugin.services.refresh"

// Manager requests finite operations for the current host-bound object. The
// Host is only used during this invocation and must never be retained by a
// goroutine. Submitted work receives its own new HostCaller from the manager.
type Manager struct{ Host HostCaller }

// Submit returns after actual bounded SDK admission, not task completion.
// It accepts only a method declared by the current artifact and copied JSON
// arguments. It cannot select another plugin, identity, executable or context.
func (m Manager) Submit(ctx context.Context, method string, payload any) error {
	if m.Host == nil { return errors.New("manager requires an active host caller") }
	raw, err := json.Marshal(struct {
		Method string `json:"method"`
		Payload any `json:"payload"`
	}{method, payload})
	if err != nil { return err }
	_, err = m.Host(ctx, BackgroundSubmitCapability, raw)
	return err
}

// RefreshServices synchronously asks the manager to read fresh standard
// declarations and replace this object's registrations. It is not submission
// or an after-return operation. Getters must not recursively call it, and a
// worker must not hold a lock needed by its getters across this operation.
func (m Manager) RefreshServices(ctx context.Context) error {
	if m.Host == nil { return errors.New("manager requires an active host caller") }
	_, err := m.Host(ctx, ServicesRefreshCapability, json.RawMessage(`{}`))
	return err
}

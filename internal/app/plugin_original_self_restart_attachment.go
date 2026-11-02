package app

import "context"

// AttachSelfRestartController is a trusted executable-host construction API,
// never a message/HTTP/plugin/SDK method. It does not spawn, signal or exit.
// cmd/server calls it only in its private, pipe-verified service child. The
// returned intent is separate from the historical same-PID RestartRequested.
// Approved native version/finish share this trusted lifetime attachment.
func (a *App) AttachSelfRestartController(parent context.Context) (<-chan struct{}, error) {
	if err := a.attachOriginalNativeSystemCommands(parent, true); err != nil {
		return nil, err
	}
	return a.originalSystemRestartIntents(), nil
}

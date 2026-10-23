package app

import (
	"context"
	"database/sql"
	"errors"
	transfer "light-media/internal/plugins/torrenttransfer"
	"net/http"
	"strings"
)

func (a *App) transferAPIWithBarrier(w http.ResponseWriter, r *http.Request, path string, in PluginInstallation) (any, error) {
	parts := strings.Split(path, "/")
	if r.Method == "POST" && len(parts) == 2 && (parts[1] == "reset" || parts[1] == "uninstall") {
		return a.transferLifecycleHTTP(r.Context(), in, parts[1])
	}
	owner, e := a.transferOwner(r.Context(), in)
	if e != nil {
		return nil, e
	}
	if e = owner.lock(r.Context()); e != nil {
		return nil, e
	}
	defer owner.unlock()
	unknown, e := owner.journal.HasUnconfirmed(r.Context(), in.InstanceID, uint64(in.Generation))
	if e != nil {
		return nil, e
	}
	if unknown {
		return nil, transfer.ErrUnconfirmed
	}
	return a.pluginAPIWithoutIYUUBarrier(w, r, path)
}

func (a *App) transferLifecycleHTTP(ctx context.Context, in PluginInstallation, operation string) (any, error) {
	prepared, e := a.prepareTransferTransition(ctx, in, operation)
	if e != nil {
		return nil, e
	}
	defer prepared.release()
	if e = prepared.drain(ctx); e != nil {
		return nil, e
	}
	a.pluginLifecycleMu.Lock()
	a.pluginMu.Lock()
	effect := func(ctx context.Context, tx *sql.Tx) error { return prepared.publishTx(ctx, tx, prepared.target(), "") }
	if operation == "reset" {
		e = a.pluginStore.iyuuResetWithEffect(ctx, in.InstanceID, effect)
	} else {
		e = a.pluginStore.iyuuUninstallWithEffect(ctx, in.InstanceID, effect)
	}
	a.pluginMu.Unlock()
	a.pluginLifecycleMu.Unlock()
	resolve, e2 := e == nil, error(nil)
	e2 = prepared.resolve(context.WithoutCancel(ctx), resolve)
	if e != nil || e2 != nil {
		return nil, errors.Join(e, e2)
	}
	if operation == "reset" {
		return map[string]bool{"reset": true}, nil
	}
	return map[string]bool{"uninstalled": true, "data_retained": pluginUninstallDataRetained(in)}, nil
}

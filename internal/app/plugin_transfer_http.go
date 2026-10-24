package app

import (
	"context"
	"database/sql"
	"errors"
)

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

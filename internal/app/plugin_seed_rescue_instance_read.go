package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"light-media/internal/plugins"
)

func (a *App) seedRescueReadInstanceConfiguration(ctx context.Context, view pluginFormView, req plugins.HTTPRequest) (json.RawMessage, error) {
	tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = a.pluginStore.validatePluginInstanceReadTx(ctx, tx, view.installation); err != nil {
		return nil, err
	}
	session := pluginFormSessionFromContext(ctx)
	return a.seedRescueReadProjectionTx(ctx, tx, view.installation, PluginRun{}, req, view.durable.Value, session.validateAnchor)
}

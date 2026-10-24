package app

import (
	"context"
	"database/sql"
)

func (a *App) validatePluginReadAuthority(ctx context.Context, authority pluginAPIAuthority) error {
	tx, err := a.Store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return a.validatePluginHTTPAuthorityTx(ctx, tx, authority)
}

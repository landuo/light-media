package app

import (
	"context"
	"database/sql"
)

// PluginProofReader is the minimal SQL read view used by host proof validators.
// It exposes no lifecycle, write, Commit or Rollback operation. Callers requiring
// an atomic snapshot must pass their existing transaction view, not open another
// transaction or silently substitute the connection pool.
type PluginProofReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

var _ PluginProofReader = pluginDefaultsTransaction{}
var _ pluginOrganizationReader = (PluginProofReader)(nil)

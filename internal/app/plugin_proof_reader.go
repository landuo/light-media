package app

import (
	"context"
	"database/sql"
)

// PluginProofReader is an internal-framework SQL read view for host proof validators.
// It is never embedded in or returned by the public Form projection facade.
// It exposes no lifecycle, write, Commit or Rollback operation. Callers requiring
// an atomic snapshot must pass their existing transaction view, not open another
// transaction or silently substitute the connection pool.
type PluginProofReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

var _ pluginOrganizationReader = (PluginProofReader)(nil)

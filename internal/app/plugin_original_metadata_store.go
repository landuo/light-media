package app

import (
	"context"
	"database/sql"
)

// managerInstallation reads authenticated class metadata without applying an old
// execution/recovery marker. It works inside an existing namespace transaction.
func (p *PluginStore) managerInstallation(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, instance string) (PluginInstallation, error) {
	var in PluginInstallation
	var sealed []byte
	err := q.QueryRowContext(ctx, `SELECT instance_id,plugin_id,version,manifest_schema,manifest,desired_enabled,installed,generation,created_at,updated_at FROM plugin_installations WHERE instance_id=?`, instance).Scan(&in.InstanceID, &in.PluginID, &in.Version, &in.ManifestSchemaVersion, &sealed, &in.DesiredEnabled, &in.Installed, &in.Generation, &in.CreatedAt, &in.UpdatedAt)
	if err != nil {
		return in, err
	}
	in.Manifest, err = p.unseal("manifest", instance, "main", sealed)
	return in, err
}

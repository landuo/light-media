package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"light-media/internal/plugins/schedule"
)

// Plain standard service declarations and encrypted metadata reads. No durable execution machinery.
type pluginScheduleDefinition struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Handler          string          `json:"handler"`
	Trigger          schedule.Spec   `json:"trigger"`
	Input            json.RawMessage `json:"input,omitempty"`
	MisfireGraceMS   int64           `json:"misfire_grace_ms,omitempty"`
	SchedulerOptions json.RawMessage `json:"kwargs,omitempty"`
	Async            bool            `json:"async,omitempty"`
}

func (p *PluginStore) Installations(ctx context.Context) ([]PluginInstallation, error) {
	rows, err := p.store.DB.QueryContext(ctx, `SELECT instance_id,plugin_id,version,manifest_schema,manifest,desired_enabled,installed,generation,created_at,updated_at FROM plugin_installations WHERE installed=1 ORDER BY created_at,instance_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PluginInstallation{}
	for rows.Next() {
		var in PluginInstallation
		var sealed []byte
		if err := rows.Scan(&in.InstanceID, &in.PluginID, &in.Version, &in.ManifestSchemaVersion, &sealed, &in.DesiredEnabled, &in.Installed, &in.Generation, &in.CreatedAt, &in.UpdatedAt); err != nil {
			return nil, err
		}
		in.Manifest, err = p.unseal("manifest", in.InstanceID, "main", sealed)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func missingPluginValue(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func pluginStrictJSON(raw json.RawMessage, value any) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value) == nil && decoder.Decode(new(any)) == io.EOF
}

package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

const pluginLifecycleField = "host_lifecycle"
const pluginLifecyclePurpose = "instance-lifecycle/v1"

const (
	pluginLifecycleUnknown  = "unknown"
	pluginLifecycleCold     = "cold"
	pluginLifecycleStarted  = "started"
	pluginLifecycleInactive = "inactive"
)

// This observation is authenticated by the existing manifest AEAD and bound to
// its instance, implementation and generation. It is NOT a monotonic ledger:
// replaying older valid ciphertext for the same binding, including restoration
// of a consistent old database snapshot, can roll the observation back. Missing
// evidence is unknown, never evidence that an owner has not started.
type pluginLifecycleMarker struct {
	UpdateReviewRequired bool   `json:"update_review_required,omitempty"`
	Purpose              string `json:"purpose"`
	InstanceID           string `json:"instance_id"`
	PluginID             string `json:"plugin_id"`
	Version              string `json:"version"`
	Generation           int64  `json:"generation"`
	State                string `json:"state"`
}

func pluginLifecycleInvalid() error {
	return bad("PLUGIN_LIFECYCLE_INVALID", "插件生命周期记录与当前实例不一致")
}

func pluginLifecycleManifest(in PluginInstallation, state string) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(in.Manifest, &fields) != nil || fields == nil || in.Generation < 1 {
		return nil, pluginLifecycleInvalid()
	}
	raw, err := json.Marshal(pluginLifecycleMarker{false, pluginLifecyclePurpose, in.InstanceID, in.PluginID, in.Version, in.Generation, state})
	if err != nil {
		return nil, err
	}
	// In particular, never carry source lifecycle authority into a new clone.
	// Keep every other manifest field, including the signed host_instance.
	fields[pluginLifecycleField] = raw
	delete(fields, pluginUpdateReviewField)
	raw, err = json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return pluginJSON(json.RawMessage(raw), true)
}

// The caller must have read and decrypted this installation from the database.
// This validates the identity binding; installed/state coherence is checked by
// the read resolver and execution claim, not while advancing a generation.
func pluginLifecycleState(in PluginInstallation) (string, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(in.Manifest, &fields) != nil || fields == nil {
		return "", pluginLifecycleInvalid()
	}
	raw, present := fields[pluginLifecycleField]
	if !present {
		if _, reviewPresent := fields[pluginUpdateReviewField]; reviewPresent {
			return "", pluginLifecycleInvalid()
		}
		return pluginLifecycleUnknown, nil
	}
	var marker pluginLifecycleMarker
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&marker) != nil || decoder.Decode(new(any)) != io.EOF || marker.Purpose != pluginLifecyclePurpose || marker.InstanceID != in.InstanceID || marker.PluginID != in.PluginID || marker.Version != in.Version || marker.Generation != in.Generation || marker.Generation < 1 {
		return "", pluginLifecycleInvalid()
	}
	if marker.State != pluginLifecycleCold && marker.State != pluginLifecycleStarted && marker.State != pluginLifecycleInactive {
		return "", pluginLifecycleInvalid()
	}
	if _, err := pluginUpdateReviewFromFields(in, marker, fields); err != nil {
		return "", err
	}
	return marker.State, nil
}

func (p *PluginStore) pluginLifecycleInstallation(ctx context.Context, q PluginProofReader, instance string) (PluginInstallation, error) {
	var in PluginInstallation
	var sealed []byte
	err := q.QueryRowContext(ctx, `SELECT instance_id,plugin_id,version,manifest_schema,manifest,desired_enabled,installed,generation,created_at,updated_at FROM plugin_installations WHERE instance_id=?`, instance).
		Scan(&in.InstanceID, &in.PluginID, &in.Version, &in.ManifestSchemaVersion, &sealed, &in.DesiredEnabled, &in.Installed, &in.Generation, &in.CreatedAt, &in.UpdatedAt)
	if err != nil {
		return in, err
	}
	in.Manifest, err = p.unseal("manifest", in.InstanceID, "main", sealed)
	if err != nil {
		return in, err
	}
	// This verifies clone metadata without creating or loading a business owner.
	_, err = p.instanceMetadata(ctx, q, in.InstanceID)
	return in, err
}

func (p *PluginStore) writePluginLifecycleTx(ctx context.Context, tx *sql.Tx, in PluginInstallation, state string) error {
	if err := p.checkPluginFormWrite(ctx, in.InstanceID, pluginFormWriteLifecycle); err != nil {
		return err
	}
	raw, err := pluginLifecycleManifest(in, state)
	if err != nil {
		return err
	}
	sealed, err := p.seal("manifest", in.InstanceID, "main", raw)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE plugin_installations SET manifest=? WHERE instance_id=? AND plugin_id=? AND version=? AND generation=? AND installed=?`, sealed, in.InstanceID, in.PluginID, in.Version, in.Generation, in.Installed)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return pluginLifecycleInvalid()
	}
	return nil
}

// Call only immediately after an authorized reset/uninstall generation bump,
// inside that same transaction. All existing owner and authorization checks
// remain at their original entry points. A failed stamp rolls back the bump and
// all effects; reset of an already-uninstalled row remains inactive, not cold.
func (p *PluginStore) recordPluginGenerationLifecycleTx(ctx context.Context, tx *sql.Tx, instance string) error {
	if err := p.checkPluginFormWrite(ctx, instance, pluginFormWriteLifecycle); err != nil {
		return err
	}
	in, err := p.pluginLifecycleInstallation(ctx, tx, instance)
	if err != nil {
		return err
	}
	previous := in
	previous.Generation--
	if _, err = pluginLifecycleState(previous); err != nil {
		return err
	}
	if carried, e := p.carryUninstalledUpdateReviewTx(ctx, tx, previous, in); e != nil {
		return e
	} else if carried {
		return nil
	}
	state := pluginLifecycleInactive
	if in.Installed {
		state = pluginLifecycleCold
	}
	return p.writePluginLifecycleTx(ctx, tx, in, state)
}

// Every pending -> running path calls this after its affected-row check and
// before commit. Any action counts, even a read action or one that later fails.
// Admission, cancellation and an uncommitted claim do not count as execution.
func (p *PluginStore) markPluginExecutionStartedTx(ctx context.Context, tx *sql.Tx, run PluginRun) error {
	if err := p.checkPluginFormWrite(ctx, run.InstanceID, pluginFormWriteLifecycle); err != nil {
		return err
	}
	in, err := p.pluginLifecycleInstallation(ctx, tx, run.InstanceID)
	if err != nil {
		return err
	}
	if !in.Installed || in.Version != run.Version || in.Generation != run.Generation {
		return pluginLifecycleInvalid()
	}
	var claimed int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs WHERE id=? AND instance_id=? AND version=? AND generation=? AND action=? AND state='running'`, run.ID, run.InstanceID, run.Version, run.Generation, run.Action).Scan(&claimed); err != nil {
		return err
	}
	if claimed != 1 {
		return pluginLifecycleInvalid()
	}
	state, err := pluginLifecycleState(in)
	if err != nil {
		return err
	}
	if err := p.requirePluginUpdateActivation(ctx, tx, run.InstanceID); err != nil {
		return err
	}
	if state == pluginLifecycleInactive {
		return pluginLifecycleInvalid()
	}
	if state == pluginLifecycleStarted {
		return nil
	}
	return p.writePluginLifecycleTx(ctx, tx, in, pluginLifecycleStarted)
}

// These types deliberately stay host-private. Neither a worker JSON payload nor
// a caller-supplied manifest/mode string is an authority to select Form behavior.
// The zero value is invalid, not preview.
type pluginFormMode uint8

const (
	pluginFormPreview pluginFormMode = iota + 1
	pluginFormInstalledCold
	pluginFormInstalledStarted
	pluginFormInstalledUnknown
)

type pluginFormResolution struct {
	mode         pluginFormMode
	installation PluginInstallation
}

// resolvePluginFormMode is a read-only host decision, not a Form invocation or
// owner fallback. It never initializes owners, writes defaults or activates a
// plugin. Its result describes this DB snapshot, not a durable capability; a
// later write must independently revalidate current identity and authorization.
func (p *PluginStore) resolvePluginFormMode(ctx context.Context, requested PluginInstallation) (pluginFormResolution, error) {
	tx, err := p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return pluginFormResolution{}, err
	}
	defer tx.Rollback()
	out, err := p.resolvePluginFormModeRead(ctx, tx, requested)
	if err != nil {
		return pluginFormResolution{}, err
	}
	return out, tx.Commit()
}

// resolvePluginFormModeRead is the same proof through an existing SQL read view.
// A defaults projector must pass its PluginDefaultsTransaction here immediately
// before using cold/warm behavior; reusing a prior resolution can race a claim.
// Never open another transaction from a projector (the pool has one connection).
// All queries below use q, including the authenticated instance metadata check.
func (p *PluginStore) resolvePluginFormModeRead(ctx context.Context, q PluginProofReader, requested PluginInstallation) (pluginFormResolution, error) {
	var out pluginFormResolution
	compiled, _, exists := builtin.Lookup(requested.PluginID)
	if !pluginStorageID.MatchString(requested.InstanceID) || !exists || compiled.Version != requested.Version || compiled.SchemaVersion != requested.ManifestSchemaVersion {
		return out, bad("PLUGIN_IMPLEMENTATION_UNAVAILABLE", "插件表单目标实现不可用")
	}
	preview := !requested.Installed && requested.Generation == 0
	if !preview && (!requested.Installed || requested.Generation < 1) {
		return out, pluginLifecycleInvalid()
	}
	current, err := p.pluginLifecycleInstallation(ctx, q, requested.InstanceID)
	if errors.Is(err, sql.ErrNoRows) {
		// An absent arbitrary clone name has no authenticated instance identity.
		if !preview || requested.InstanceID != compiled.PluginID {
			return out, pluginLifecycleInvalid()
		}
	} else if err != nil {
		return out, err
	} else {
		var stored plugins.Manifest
		if json.Unmarshal(current.Manifest, &stored) != nil || stored.Validate() != nil || stored.PluginID != current.PluginID || stored.Version != current.Version || stored.SchemaVersion != current.ManifestSchemaVersion || current.PluginID != requested.PluginID || current.Generation < 1 {
			return out, pluginLifecycleInvalid()
		}
		if _, err := p.pluginUpdateReviewRead(ctx, q, current); err != nil {
			return out, err
		}
		state, err := pluginLifecycleState(current)
		if err != nil {
			return out, err
		}
		if state != pluginLifecycleUnknown && ((state == pluginLifecycleInactive) == current.Installed) {
			return out, pluginLifecycleInvalid()
		}
		if preview {
			if current.Installed {
				return out, pluginLifecycleInvalid()
			}
			// A tombstone authenticates instance/plugin identity at its OLD
			// version. Preview intentionally uses the CURRENT compiled target;
			// no retained config, old owner or old manifest is projected below.
		} else {
			if !current.Installed || current.Version != requested.Version || current.ManifestSchemaVersion != requested.ManifestSchemaVersion || current.Generation != requested.Generation {
				return out, pluginLifecycleInvalid()
			}
			out.installation = current
			switch state {
			case pluginLifecycleCold:
				out.mode = pluginFormInstalledCold
			case pluginLifecycleStarted:
				out.mode = pluginFormInstalledStarted
			default:
				out.mode = pluginFormInstalledUnknown
			}
			return out, nil
		}
	}
	raw, err := json.Marshal(compiled)
	if err != nil {
		return out, err
	}
	out.mode = pluginFormPreview
	out.installation = PluginInstallation{InstanceID: requested.InstanceID, PluginID: compiled.PluginID, Version: compiled.Version, ManifestSchemaVersion: compiled.SchemaVersion, Manifest: raw}
	return out, nil
}

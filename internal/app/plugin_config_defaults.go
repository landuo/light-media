package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// NormalizeInstanceDefaults is the framework's pure missing-default write path.
// Callers are trusted host adapters using reviewed business declarations, never
// arbitrary worker patches. In particular it is NOT SaveConfigAndQueue: reading
// a form must not activate a paused clone or trigger an init/one-shot side effect.
// A form without a real run cannot borrow a running invocation's authority.
// Actual writes require the exact compiled implementation to declare pure/v1;
// native business owners need a separate transactional normalization hook.
func (p *PluginStore) NormalizeInstanceDefaults(ctx context.Context, snapshot PluginInstallation, expectedRevision int64, defaults map[string]json.RawMessage) (PluginStoredValue, error) {
	return p.normalizeInstanceDefaults(ctx, snapshot, expectedRevision, defaults, plugins.ConfigDefaultsPure, nil)
}

// PluginDefaultsChange contains database-current identity/state, the compiled
// implementation manifest (not full installation history), and isolated
// before/after configuration snapshots. Mutating these copies cannot rewrite
// configuration, pending inputs, or the framework's result.
type PluginDefaultsChange struct {
	Installation PluginInstallation
	Previous     PluginStoredValue
	Next         PluginStoredValue
}

// PluginDefaultsTransaction is a bounded view of the current config projection.
// Public callbacks receive no general SQL, Store, transaction, or proof reader.
// The host owns transaction completion. Hooks must remain synchronous and must
// not initialize owners or perform external effects. This capability boundary
// is not a Go sandbox: trusted same-package code can still capture private state.
type PluginDefaultsTransaction interface {
	Data(context.Context, string) (PluginStoredValue, error)
	SaveData(context.Context, string, int64, any) (PluginStoredValue, error)
	Config(context.Context) (PluginStoredValue, error)
	VerifyInitialization(context.Context, PluginInitializationProof) error
}

type pluginDefaultsTransaction struct {
	tx            *sql.Tx
	store         *PluginStore
	instance      string
	formAdmission *pluginFormDataWrite
	installation  PluginInstallation
	projection    *pluginDefaultsProjectionState
}

// Data and SaveData bind the namespace and retain framework encryption/CAS.
// Projectors need not duplicate the storage codec or choose an instance ID.
func (t pluginDefaultsTransaction) Data(ctx context.Context, key string) (value PluginStoredValue, returnErr error) {
	callerCtx := ctx
	defer func() { returnErr = t.projectionContextError(callerCtx, returnErr) }()
	ctx, cancel, err := t.projectionContext(ctx)
	if err != nil {
		return value, err
	}
	defer cancel()
	if !validPluginKey(key) {
		return value, bad("INVALID_PLUGIN_VALUE", "插件数据键无效")
	}
	var sealed []byte
	if err := t.tx.QueryRowContext(ctx, `SELECT revision,sealed,updated_at FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?`, t.instance, key).Scan(&value.Revision, &sealed, &value.UpdatedAt); err != nil {
		return value, err
	}
	value.Value, err = t.store.unseal("data", t.instance, key, sealed)
	return value, err
}
func (t pluginDefaultsTransaction) SaveData(ctx context.Context, key string, expected int64, value any) (out PluginStoredValue, returnErr error) {
	callerCtx := ctx
	defer func() { returnErr = t.projectionContextError(callerCtx, returnErr) }()
	// Reject read-only/foreign scope before even invoking custom JSON code.
	if err := t.store.checkPluginFormWrite(ctx, t.instance, pluginFormWriteData); err != nil {
		return PluginStoredValue{}, err
	}
	ctx, cancel, err := t.projectionContext(ctx)
	if err != nil {
		return PluginStoredValue{}, err
	}
	defer cancel()
	// Also check the immutable original scope before custom serialization;
	// replacing a restricted caller context with Background cannot erase it.
	if err := t.store.checkPluginFormWrite(ctx, t.instance, pluginFormWriteData); err != nil {
		return PluginStoredValue{}, err
	}
	raw, err := pluginJSONLimit(value, false, pluginDataValueLimit)
	if err != nil {
		return PluginStoredValue{}, err
	}
	// Inspect the ORIGINAL caller synchronously, not just its asynchronously
	// canceled derivative. Custom JSON code may cancel a narrower caller.
	if err := t.checkProjection(callerCtx); err != nil {
		return PluginStoredValue{}, err
	}
	if t.formAdmission != nil {
		// Only a successfully admitted config transition creates this proof.
		// It is data-only and never passed in the business projector's context.
		ctx = context.WithValue(ctx, pluginFormDataWriteKey{}, *t.formAdmission)
	}
	return t.store.saveValueTx(ctx, t.tx, t.instance, "data", key, expected, raw)
}

// Missing-default read-only no-ops do not invoke the projector. The actual
// dynamic value deliberately has only the same four public methods as the
// interface, so structural assertions cannot recover raw SQL capabilities.
type PluginDefaultsProjector func(context.Context, PluginDefaultsTransaction, PluginDefaultsChange) error

// NormalizeInstanceDefaultsWithProjection requires a compiled transactional/v1
// declaration. The framework owns CAS, missing-only writes, busy checks and
// propagation. A projector failure rolls back both config and business state.
func (p *PluginStore) NormalizeInstanceDefaultsWithProjection(ctx context.Context, snapshot PluginInstallation, expectedRevision int64, defaults map[string]json.RawMessage, projector PluginDefaultsProjector) (PluginStoredValue, error) {
	if err := p.checkPluginFormWrite(ctx, snapshot.InstanceID, pluginFormWriteProjection); err != nil {
		return PluginStoredValue{}, err
	}
	if projector == nil {
		return PluginStoredValue{}, bad("PLUGIN_DEFAULTS_UNSUPPORTED", "插件缺少可信默认值状态投影")
	}
	return p.normalizeInstanceDefaults(ctx, snapshot, expectedRevision, defaults, plugins.ConfigDefaultsTransactional, projector)
}

func (p *PluginStore) normalizeInstanceDefaults(ctx context.Context, snapshot PluginInstallation, expectedRevision int64, defaults map[string]json.RawMessage, declaration string, projector PluginDefaultsProjector) (PluginStoredValue, error) {
	if err := p.checkPluginFormWrite(ctx, snapshot.InstanceID, pluginFormWriteProjection); err != nil {
		return PluginStoredValue{}, err
	}
	var out PluginStoredValue
	if expectedRevision < 0 || !pluginStorageID.MatchString(snapshot.InstanceID) || snapshot.Generation < 1 {
		return out, bad("INVALID_PLUGIN_VALUE", "插件默认值快照无效")
	}
	for key, value := range defaults {
		if !validPluginKey(key) || !json.Valid(value) {
			return out, bad("INVALID_PLUGIN_VALUE", "插件默认字段无效")
		}
	}
	contract := pluginConfigDefaultsPure
	if declaration == plugins.ConfigDefaultsTransactional {
		contract = pluginConfigDefaultsTransactional
	} else if declaration != plugins.ConfigDefaultsPure {
		return out, bad("PLUGIN_DEFAULTS_UNSUPPORTED", "插件实现未声明所需默认值保存协议")
	}
	return p.transitionInstanceConfig(ctx, snapshot, expectedRevision, contract, func(config map[string]json.RawMessage) (any, bool) {
		changed := false
		for key, value := range defaults {
			if _, exists := config[key]; !exists {
				config[key] = append(json.RawMessage(nil), value...)
				changed = true
			}
		}
		return config, changed
	}, projector)
}

// transitionInstanceConfig owns the transaction for both missing-default and
// full Form saves. The private prepare function cannot select authority: the
// contract is checked against the exact compiled implementation, never caller
// or persisted manifests. A Form Save always reports changed, even for equal
// config, because the source Save itself is a business-state transition.
func (p *PluginStore) transitionInstanceConfig(ctx context.Context, snapshot PluginInstallation, expectedRevision int64, contract pluginConfigTransitionContract, prepare func(map[string]json.RawMessage) (any, bool), projector PluginDefaultsProjector) (PluginStoredValue, error) {
	var out PluginStoredValue
	if err := p.checkPluginFormWrite(ctx, snapshot.InstanceID, pluginFormWriteProjection); err != nil {
		return out, err
	}
	if contract == pluginConfigFormTransactional && projector == nil {
		return out, contract.unsupported()
	}
	if expectedRevision < 0 || !pluginStorageID.MatchString(snapshot.InstanceID) || snapshot.Generation < 1 {
		return out, bad("INVALID_PLUGIN_VALUE", "插件配置快照无效")
	}
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var plugin, version string
	var generation int64
	var installed, desiredEnabled bool
	if err = tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation,installed,desired_enabled FROM plugin_installations WHERE instance_id=?`, snapshot.InstanceID).Scan(&plugin, &version, &generation, &installed, &desiredEnabled); err != nil {
		return out, err
	}
	if !installed || plugin != snapshot.PluginID || version != snapshot.Version || generation != snapshot.Generation {
		return out, bad("PLUGIN_SNAPSHOT_STALE", "插件实例已重置、卸载或更换实现")
	}
	if _, err = p.instanceMetadata(ctx, tx, snapshot.InstanceID); err != nil {
		return out, err
	}
	var sealed []byte
	err = tx.QueryRowContext(ctx, `SELECT revision,sealed,updated_at FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, snapshot.InstanceID).Scan(&out.Revision, &sealed, &out.UpdatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	config := map[string]json.RawMessage{}
	if err == nil {
		out.Value, err = p.unseal("config", snapshot.InstanceID, "main", sealed)
		if err != nil {
			return out, err
		}
		if json.Unmarshal(out.Value, &config) != nil || config == nil {
			return out, bad("INVALID_PLUGIN_CONFIG", "插件配置不是有效对象")
		}
	} else {
		out.Value = json.RawMessage(`{}`)
	}
	if out.Revision != expectedRevision {
		return out, bad("PLUGIN_REVISION_CONFLICT", "插件配置已变化，请重新打开表单")
	}
	// A request-local Form session adds binding/mode/revision admission in this
	// transaction. Outside Form the original V8 exact-CAS contract is unchanged.
	session := pluginFormSessionFromContext(ctx)
	if session != nil {
		if session.store != p {
			return PluginStoredValue{}, pluginFormStale()
		}
		if err = session.admit(ctx, tx, snapshot, out); err != nil {
			return PluginStoredValue{}, err
		}
	}
	desired, changed := prepare(config)
	// Idempotent reads do not increment revision, obtain write capability, affect
	// active work, or create a synthetic management run.
	if !changed && contract != pluginConfigFormTransactional {
		return out, tx.Commit()
	}
	// Read-only no-ops remain available to every plugin, but mutable stored
	// manifests cannot opt an implementation into configuration propagation.
	manifest, _, known := builtin.Lookup(plugin)
	if !known || manifest.PluginID != plugin || manifest.Version != version || !contract.allows(manifest) {
		return out, contract.unsupported()
	}
	var active int
	// Even an opted-in implementation must not rewrite an immutable prepared
	// receipt's input hash/revision. Do not re-seal the receipt for a new snapshot.
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs r WHERE r.instance_id=? AND (r.state IN ('running','reconcile_pending') OR (r.state='pending' AND EXISTS(SELECT 1 FROM plugin_deferred_operations d WHERE d.run_id=r.id)))`, snapshot.InstanceID).Scan(&active); err != nil {
		return out, err
	}
	if active != 0 {
		return out, bad("PLUGIN_BUSY", "插件存在执行或待恢复的工作，默认值未保存，请稍后重新打开表单")
	}
	if err = p.requireSiteReferenceProtocolTx(ctx, tx, snapshot.InstanceID); err != nil {
		return out, err
	}
	raw, err := p.pluginSnapshotJSON(ctx, tx, snapshot.InstanceID, desired, true)
	if err != nil {
		return out, err
	}
	previous := out
	previousConfig := append(json.RawMessage(nil), out.Value...)
	out, err = p.saveValueTx(p.pluginFormConfigWriteContext(ctx, tx, snapshot.InstanceID), tx, snapshot.InstanceID, "config", "main", expectedRevision, raw)
	if err != nil {
		return out, err
	}
	if projector != nil {
		// Do not give a callback stale caller metadata or aliases of stored bytes.
		current := PluginInstallation{InstanceID: snapshot.InstanceID, PluginID: plugin, Version: version, Generation: generation, DesiredEnabled: desiredEnabled, Installed: installed, ManifestSchemaVersion: manifest.SchemaVersion}
		current.Manifest, err = json.Marshal(manifest)
		if err != nil {
			return PluginStoredValue{}, err
		}
		previous.Value = append(json.RawMessage(nil), previousConfig...)
		next := out
		next.Value = append(json.RawMessage(nil), out.Value...)
		transaction := newPluginDefaultsTransaction(ctx, tx, p, current)
		if session != nil {
			transaction.formAdmission = &pluginFormDataWrite{store: p, tx: tx, instance: snapshot.InstanceID, session: session}
		}
		if err = invokePluginDefaultsProjector(ctx, transaction, PluginDefaultsChange{Installation: current, Previous: previous, Next: next}, projector); err != nil {
			return PluginStoredValue{}, err
		}
	}
	// A trusted projector cannot hide a denied common-API write and still
	// commit this otherwise valid transition. Preserve the sticky failure.
	if session != nil && session.hostFailure != nil {
		failure := *session.hostFailure
		return PluginStoredValue{}, &failure
	}
	if err = p.propagateInstanceConfigTx(p.pluginFormConfigWriteContext(ctx, tx, snapshot.InstanceID), tx, snapshot.InstanceID, generation, expectedRevision, out.Revision, previousConfig, raw); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return PluginStoredValue{}, err
	}
	if session != nil {
		session.acceptCommitted(out, true)
	}
	return out, nil
}

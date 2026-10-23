package app

import (
	"context"
	"encoding/json"

	"light-media/internal/plugins"
)

type pluginConfigTransitionContract uint8

const (
	pluginConfigDefaultsPure pluginConfigTransitionContract = iota
	pluginConfigDefaultsTransactional
	pluginConfigFormTransactional
)

func (c pluginConfigTransitionContract) allows(manifest plugins.Manifest) bool {
	switch c {
	case pluginConfigDefaultsPure:
		return manifest.ConfigDefaults == plugins.ConfigDefaultsPure
	case pluginConfigDefaultsTransactional:
		return manifest.ConfigDefaults == plugins.ConfigDefaultsTransactional
	case pluginConfigFormTransactional:
		return manifest.ConfigFormProjection == plugins.ConfigFormProjectionTransactional
	default:
		return false
	}
}

func (c pluginConfigTransitionContract) unsupported() error {
	if c == pluginConfigFormTransactional {
		return bad("PLUGIN_FORM_PROJECTION_UNSUPPORTED", "插件实现未声明可信表单状态投影协议")
	}
	return bad("PLUGIN_DEFAULTS_UNSUPPORTED", "插件实现未声明所需默认值保存协议")
}

// ProjectInstanceConfig is a trusted compiled host adapter's full Form Save
// transition, not a user config-save endpoint or generic worker/RPC capability.
// The exact compiled implementation must separately declare transactional/v1
// in ConfigFormProjection and supply its business projector. Stored manifests
// and missing-default declarations cannot grant this authority.
//
// desiredRaw completely replaces the stored object, including omitted keys. An
// actual source Save always advances revision and invokes the projector, even
// when desiredRaw equals current config. This is intentionally different from
// NormalizeInstanceDefaults' read-only no-op. The projector runs after the
// encrypted config write: validate the journal against change.Previous, project
// source-defined Config/Stored state, and preserve the original init receipt.
// Only config, journal and pending snapshot/proof propagation may change in the
// transaction. This path never activates, initializes, queues work, or creates
// config-save origin authority. HTTP Form supplies a private request-local
// session through ctx; the common transaction revalidates its mode and durable
// snapshot before writing and advances it only after commit. Outside that
// context the existing exact-CAS contract is unchanged. Business source-state
// authentication and cold-fallback semantics still belong to the host adapter.
func (p *PluginStore) ProjectInstanceConfig(ctx context.Context, snapshot PluginInstallation, expectedRevision int64, desiredRaw json.RawMessage, projector PluginDefaultsProjector) (PluginStoredValue, error) {
	if err := p.checkPluginFormWrite(ctx, snapshot.InstanceID, pluginFormWriteProjection); err != nil {
		return PluginStoredValue{}, err
	}
	if projector == nil {
		return PluginStoredValue{}, bad("PLUGIN_FORM_PROJECTION_UNSUPPORTED", "插件缺少可信表单状态投影")
	}
	// Validate and take an isolated copy before opening the transaction. The
	// same exact owner-specific size/object checks are repeated in the common
	// transaction after database-current identity and compiled authority checks.
	desired, err := pluginSnapshotJSON(snapshot.PluginID, snapshot.Version, desiredRaw, true)
	if err != nil {
		return PluginStoredValue{}, err
	}
	return p.transitionInstanceConfig(ctx, snapshot, expectedRevision, pluginConfigFormTransactional, func(map[string]json.RawMessage) (any, bool) {
		return desired, true
	}, projector)
}

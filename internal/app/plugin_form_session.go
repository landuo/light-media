package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"light-media/internal/plugins"
)

// Origin is chosen by the HTTP entry, never inferred from worker input or an
// owner-cache miss. Installed unknown remains a valid read mode.
type pluginFormOrigin uint8

const (
	pluginFormOriginInstalled pluginFormOrigin = iota + 1
	pluginFormOriginInstall
)

type pluginFormSessionKey struct{}

type pluginFormAnchor struct {
	installationExists bool
	installation       PluginInstallation
	resolution         pluginFormResolution
	configExists       bool
	durable            PluginStoredValue
	configSHA          [32]byte
}

// One synchronous Runner invocation owns this session. Never retain it in an
// owner, hand it to a goroutine, or hold a lock/SQL transaction across a host
// call. It grants no worker capability. Display bytes may intentionally differ
// from durable bytes (the existing uninstall cache); only durable bytes are CAS
// authority. All accessors and committed results are isolated copies.
type pluginFormSession struct {
	store        *PluginStore
	requested    PluginInstallation
	origin       pluginFormOrigin
	resolution   pluginFormResolution
	anchor       pluginFormAnchor
	durable      PluginStoredValue
	display      PluginStoredValue
	configExists bool
	hostFailure  *APIError
	deadline     time.Time
}

func pluginFormValueCopy(value PluginStoredValue) PluginStoredValue {
	value.Value = bytes.Clone(value.Value)
	return value
}

func pluginFormInstallationCopy(in PluginInstallation) PluginInstallation {
	in.Manifest = bytes.Clone(in.Manifest)
	return in
}

func withPluginFormSession(ctx context.Context, session *pluginFormSession) context.Context {
	return context.WithValue(ctx, pluginFormSessionKey{}, session)
}

func pluginFormSessionFromContext(ctx context.Context) *pluginFormSession {
	if ctx == nil {
		return nil
	}
	session, _ := ctx.Value(pluginFormSessionKey{}).(*pluginFormSession)
	return session
}

func pluginFormStale() error {
	return bad("PLUGIN_SNAPSHOT_STALE", "插件实例或表单模式已变化，请重新打开表单")
}

func pluginFormRevisionConflict() error {
	return bad("PLUGIN_REVISION_CONFLICT", "插件配置已变化，请重新打开表单")
}

// Every read below uses q. In particular, this must not call Config(), which
// may consult its display cache through a separate DB query.
func (p *PluginStore) pluginFormAnchorRead(ctx context.Context, q PluginProofReader, requested PluginInstallation, origin pluginFormOrigin) (pluginFormAnchor, error) {
	var out pluginFormAnchor
	if origin != pluginFormOriginInstalled && origin != pluginFormOriginInstall {
		return out, pluginFormStale()
	}
	if origin == pluginFormOriginInstall && (requested.Installed || requested.Generation != 0 || requested.InstanceID != requested.PluginID) {
		return out, pluginFormStale()
	}
	if origin == pluginFormOriginInstalled && (!requested.Installed || requested.Generation < 1) {
		return out, pluginFormStale()
	}
	current, err := p.pluginLifecycleInstallation(ctx, q, requested.InstanceID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil {
		out.installationExists, out.installation = true, current
		// Preserve the install endpoint's explicit terminal outcome; a synthetic
		// generation-zero preview must not impersonate an installed generation.
		if origin == pluginFormOriginInstall && current.Installed {
			if current.PluginID != requested.PluginID {
				return out, pluginFormStale()
			}
			return out, bad("PLUGIN_ALREADY_INSTALLED", "插件已安装")
		}
	}
	out.resolution, err = p.resolvePluginFormModeRead(ctx, q, requested)
	if err != nil {
		return out, err
	}
	var sealed []byte
	err = q.QueryRowContext(ctx, `SELECT revision,sealed,updated_at FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, requested.InstanceID).Scan(&out.durable.Revision, &sealed, &out.durable.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		out.durable.Value = json.RawMessage(`{}`)
	} else if err != nil {
		return out, err
	} else {
		out.configExists = true
		out.durable.Value, err = p.unseal("config", requested.InstanceID, "main", sealed)
		if err != nil {
			return out, err
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(out.durable.Value, &fields) != nil || fields == nil {
			return out, bad("INVALID_PLUGIN_CONFIG", "插件配置不是有效对象")
		}
	}
	out.configSHA = sha256.Sum256(out.durable.Value)
	return out, nil
}

func (p *PluginStore) pluginFormAnchor(ctx context.Context, requested PluginInstallation, origin pluginFormOrigin) (pluginFormAnchor, error) {
	tx, err := p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return pluginFormAnchor{}, err
	}
	defer tx.Rollback()
	out, err := p.pluginFormAnchorRead(ctx, tx, requested, origin)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func samePluginFormIdentity(left, right pluginFormAnchor) bool {
	a, b := left.installation, right.installation
	return left.installationExists == right.installationExists && left.resolution.mode == right.resolution.mode &&
		a.InstanceID == b.InstanceID && a.PluginID == b.PluginID && a.Version == b.Version && a.ManifestSchemaVersion == b.ManifestSchemaVersion &&
		a.Generation == b.Generation && a.Installed == b.Installed && a.DesiredEnabled == b.DesiredEnabled && bytes.Equal(a.Manifest, b.Manifest)
}

func comparePluginFormAnchors(left, right pluginFormAnchor) error {
	if !samePluginFormIdentity(left, right) {
		return pluginFormStale()
	}
	if left.configExists != right.configExists || left.durable.Revision != right.durable.Revision || left.configSHA != right.configSHA || !bytes.Equal(left.durable.Value, right.durable.Value) {
		return pluginFormRevisionConflict()
	}
	return nil
}

// The bounded read sandwich preserves Config's current display projection
// without a cache API change or SQL re-entry. A same-generation/revision cache
// update is NOT detected: display is a point-in-time snapshot, not a cache epoch.
// The durable revision and raw SHA must remain equal around that read.
func (p *PluginStore) beginPluginFormSession(ctx context.Context, requested PluginInstallation, origin pluginFormOrigin) (*pluginFormSession, error) {
	if ctx == nil || p == nil {
		return nil, pluginFormViewDenied()
	}
	// Never replace a request snapshot or escape an outer read-only callback.
	// An outer scope owns the failure even if a caller rebound the session.
	if scope := pluginFormCallbackScopeFromContext(ctx); scope != nil {
		err := pluginFormViewDenied()
		if scope.session != nil {
			scope.session.captureHostError(err)
		}
		return nil, err
	}
	if existing := pluginFormSessionFromContext(ctx); existing != nil {
		err := pluginFormViewDenied()
		existing.captureHostError(err)
		return nil, err
	}
	deadline := pluginFormDeadline(ctx)
	before, err := p.pluginFormAnchor(ctx, requested, origin)
	if err != nil {
		return nil, err
	}
	display, displayErr := p.Config(ctx, requested.InstanceID)
	if displayErr != nil && !errors.Is(displayErr, sql.ErrNoRows) {
		return nil, displayErr
	}
	after, err := p.pluginFormAnchor(ctx, requested, origin)
	if err != nil {
		return nil, err
	}
	if err = comparePluginFormAnchors(before, after); err != nil {
		return nil, err
	}
	if (displayErr == nil) != before.configExists || display.Revision != before.durable.Revision {
		return nil, pluginFormRevisionConflict()
	}
	if displayErr != nil {
		display = pluginFormValueCopy(before.durable)
	}
	return &pluginFormSession{deadline: deadline, store: p, requested: pluginFormInstallationCopy(requested), origin: origin, resolution: before.resolution, anchor: before, durable: pluginFormValueCopy(before.durable), display: pluginFormValueCopy(display), configExists: before.configExists}, nil
}

// admit runs BEFORE prepare/saveValueTx in the SAME transaction. The earlier
// resolution is not an authorization token. In particular cold->started cannot
// use a prior cold fallback. Unknown->unknown is valid, not assumed cold.
func (s *pluginFormSession) admit(ctx context.Context, q PluginProofReader, snapshot PluginInstallation, previous PluginStoredValue) error {
	if err := s.store.checkPluginFormWrite(ctx, snapshot.InstanceID, pluginFormWriteProjection); err != nil {
		return err
	}
	if s.resolution.mode == pluginFormPreview || snapshot.InstanceID != s.requested.InstanceID || snapshot.PluginID != s.requested.PluginID || snapshot.Version != s.requested.Version || snapshot.Generation != s.requested.Generation {
		return bad("PLUGIN_CAPABILITY_DENIED", "插件表单不能修改预览或其他实例")
	}
	current, err := s.store.pluginFormAnchorRead(ctx, q, s.requested, s.origin)
	if err != nil {
		return err
	}
	if err = comparePluginFormAnchors(s.anchor, current); err != nil {
		return err
	}
	if previous.Revision != s.durable.Revision || !bytes.Equal(previous.Value, s.durable.Value) {
		return pluginFormRevisionConflict()
	}
	return nil
}

// Called only after successful Commit, never from a projector. An actual equal
// value source Save is still a write; missing-only no-ops never advance here.
func (s *pluginFormSession) acceptCommitted(next PluginStoredValue, didWrite bool) {
	if !didWrite {
		return
	}
	s.durable, s.display = pluginFormValueCopy(next), pluginFormValueCopy(next)
	s.configExists = true
	s.anchor.configExists = true
	s.anchor.durable = pluginFormValueCopy(next)
	s.anchor.configSHA = sha256.Sum256(next.Value)
}

func (s *pluginFormSession) validateFinal(ctx context.Context) error {
	if s.hostFailure != nil {
		copy := *s.hostFailure
		return &copy
	}
	return s.validateAnchor(ctx)
}

func (s *pluginFormSession) validateAnchor(ctx context.Context) error {
	current, err := s.store.pluginFormAnchor(ctx, s.requested, s.origin)
	if err != nil {
		return err
	}
	return comparePluginFormAnchors(s.anchor, current)
}

// Only fixed typed host codes survive SDK HOST_DENIED. Do not retain error
// strings, arbitrary APIError messages/statuses, worker errors or payloads.
func canonicalPluginFormHostError(err error) *APIError {
	var api *APIError
	if !errors.As(err, &api) || api == nil {
		return nil
	}
	message := ""
	switch api.Code {
	case "MERGE_MESSAGE_STATE_INVALID":
		message = "聚合通知状态无法通过完整性校验"
	case "MERGE_MESSAGE_STATE_MISSING":
		message = "聚合通知尚无已确认的初始化状态"
	case "PLUGIN_REVISION_CONFLICT":
		message = "插件配置已变化，请重新打开表单"
	case "PLUGIN_SNAPSHOT_STALE":
		message = "插件实例或表单模式已变化，请重新打开表单"
	case "PLUGIN_BUSY":
		message = "插件存在执行或待恢复的工作，请稍后重新打开表单"
	case "PLUGIN_LIFECYCLE_INVALID":
		message = "插件生命周期记录与当前实例不一致"
	case "PLUGIN_INSTANCE_METADATA_INVALID":
		message = "插件实例身份记录无效"
	case "PLUGIN_DATA_CORRUPT":
		message = "插件存储数据校验失败"
	case "PLUGIN_FORM_CALLBACK_UNSUPPORTED":
		message = "插件实现未提供当前模式的可信表单回调"
	case "PLUGIN_FORM_PROJECTION_UNSUPPORTED", "PLUGIN_DEFAULTS_UNSUPPORTED":
		message = "插件实现未声明所需的可信表单保存协议"
	case "PLUGIN_CAPABILITY_DENIED", "PLUGIN_IDENTITY_MISMATCH":
		message = "插件表单调用超出授权范围"
	case "INVALID_PLUGIN_CONFIG":
		message = "插件配置无效"
	case "PLUGIN_SITE_REFERENCE_UNMAPPED", "PLUGIN_SITE_PROTOCOL_UNCLAIMED", "PLUGIN_SITE_PROTOCOL_INVALID", "PLUGIN_REFERENCE_AMBIGUOUS", "TORRENT_TRANSFER_REFERENCES_UNCLAIMED", "TORRENT_TRANSFER_NAME_CONFLICT":
		message = "插件引用尚未完成验证，请检查引用配置"
	default:
		return nil
	}
	return bad(api.Code, message)
}

func (s *pluginFormSession) captureHostError(err error) {
	if err != nil && s.hostFailure == nil {
		s.hostFailure = canonicalPluginFormHostError(err)
		if s.hostFailure == nil {
			// Even an unknown/operational failure must not be hidden by a worker
			// swallowing HOST_DENIED. Retain no cause, message, status or payload.
			s.hostFailure = bad("PLUGIN_FORM_FAILED", "加载插件配置表单失败")
		}
	}
}

func (s *pluginFormSession) wrapHost(next plugins.HostHandler) plugins.HostHandler {
	return func(ctx context.Context, identity plugins.Identity, method string, raw json.RawMessage) (json.RawMessage, error) {
		if s.hostFailure != nil {
			return nil, s.hostFailure
		}
		// Reject already-observed drift before business code can reread and
		// validate the replacement config. This short read transaction ends
		// before next: inline Form writes retain their own same-TX admission,
		// and successful commits have already advanced the session anchor.
		if err := s.validateAnchor(ctx); err != nil {
			s.captureHostError(err)
			return nil, err
		}
		result, err := next(ctx, identity, method, raw)
		s.captureHostError(err)
		return result, err
	}
}

func pluginFormSourceModel(manifest plugins.Manifest, mode pluginFormMode) bool {
	if manifest.FormModelPolicy == nil {
		return false
	}
	if mode == pluginFormPreview {
		return manifest.FormModelPolicy.Preview == plugins.FormModelSource
	}
	return manifest.FormModelPolicy.Installed == plugins.FormModelSource
}

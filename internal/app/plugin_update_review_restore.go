package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"math"

	"light-media/internal/plugins/builtin"
)

type pluginReviewRestoreRequest struct {
	Version    string `json:"version"`
	Generation int64  `json:"generation"`
	Revision   int64  `json:"revision"`
}
type pluginReviewRestoreResult struct {
	Restored      bool   `json:"restored"`
	ReviewPending bool   `json:"review_pending"`
	Version       string `json:"version"`
	Generation    int64  `json:"generation"`
}

// A one-use host capability for this exact transaction and authenticated
// tombstone. It is never decoded from HTTP, stored, or placed in worker context.
type pluginReviewRestorePermit struct {
	store    *PluginStore
	tx       *sql.Tx
	previous PluginInstallation
	target   PluginInstallation
	review   pluginUpdateReview
	used     bool
}

func (p *PluginStore) reviewedRestoreTarget(ctx context.Context, q PluginProofReader, in PluginInstallation, request pluginReviewRestoreRequest) (PluginInstallation, *pluginUpdateReview, error) {
	var target PluginInstallation
	if in.Installed || in.Version != request.Version || in.Generation != request.Generation || request.Generation < 1 || request.Generation == math.MaxInt64 || request.Revision < 0 {
		return target, nil, bad("PLUGIN_SNAPSHOT_STALE", "待确认实例状态已变化，请刷新后恢复")
	}
	state, err := pluginLifecycleState(in)
	if err != nil {
		return target, nil, err
	}
	if state != pluginLifecycleInactive {
		return target, nil, pluginLifecycleInvalid()
	}
	review, err := p.pluginUpdateReviewRead(ctx, q, in)
	if err != nil {
		return target, nil, err
	}
	if review == nil {
		return target, nil, pluginUpdateReviewPending()
	}
	if review.Revision != request.Revision {
		return target, nil, bad("PLUGIN_REVISION_CONFLICT", "插件配置已变化，请刷新后恢复")
	}
	compiled, _, ok := builtin.Lookup(in.PluginID)
	if !ok || compiled.Validate() != nil || compiled.PluginID != in.PluginID || compiled.Version != in.Version || compiled.SchemaVersion != in.ManifestSchemaVersion {
		return target, nil, bad("PLUGIN_UPDATE_TARGET_UNAVAILABLE", "恢复目标不是当前已编译的精确插件版本")
	}
	raw, err := json.Marshal(compiled)
	if err != nil {
		return target, nil, err
	}
	var before, after map[string]json.RawMessage
	if json.Unmarshal(in.Manifest, &before) != nil || json.Unmarshal(raw, &after) != nil {
		return target, nil, pluginLifecycleInvalid()
	}
	if marker, ok := before["host_instance"]; ok {
		after["host_instance"] = append(json.RawMessage(nil), marker...)
	}
	raw, err = json.Marshal(after)
	if err != nil {
		return target, nil, err
	}
	target = pluginFormInstallationCopy(in)
	target.Manifest = raw
	target.ManifestSchemaVersion = compiled.SchemaVersion
	return target, review, nil
}

func (p *PluginStore) consumeReviewRestorePermit(ctx context.Context, tx *sql.Tx, in PluginInstallation, permit *pluginReviewRestorePermit) (*pluginUpdateReview, error) {
	if permit == nil || permit.used || permit.store != p || permit.tx != tx || tx == nil ||
		in.InstanceID != permit.target.InstanceID || in.PluginID != permit.target.PluginID || in.Version != permit.target.Version ||
		in.Generation != permit.target.Generation || in.Installed != permit.target.Installed || in.DesiredEnabled != permit.target.DesiredEnabled ||
		in.ManifestSchemaVersion != permit.target.ManifestSchemaVersion || !bytes.Equal(in.Manifest, permit.target.Manifest) {
		return nil, pluginLifecycleInvalid()
	}
	// Require the already-bound reset/fence transaction without consuming it twice.
	admission, err := p.resetAdmission(ctx, in.InstanceID)
	if err != nil {
		return nil, err
	}
	admission.mu.Lock()
	bound := admission.tx == tx && admission.outcome == pluginResetTransactionOpen
	admission.mu.Unlock()
	if !bound {
		return nil, errPluginRunTransitionState
	}
	current, err := p.pluginLifecycleInstallation(ctx, tx, in.InstanceID)
	if err != nil {
		return nil, err
	}
	if current.Generation != permit.previous.Generation || current.Version != permit.previous.Version || current.PluginID != permit.previous.PluginID || current.Installed || current.DesiredEnabled != permit.previous.DesiredEnabled || !bytes.Equal(current.Manifest, permit.previous.Manifest) {
		return nil, bad("PLUGIN_SNAPSHOT_STALE", "待确认实例状态已变化")
	}
	target, review, err := p.reviewedRestoreTarget(ctx, tx, current, pluginReviewRestoreRequest{current.Version, current.Generation, permit.review.Revision})
	if err != nil {
		return nil, err
	}
	if *review != permit.review || !bytes.Equal(target.Manifest, in.Manifest) {
		return nil, pluginLifecycleInvalid()
	}
	permit.used = true
	copy := *review
	return &copy, nil
}

func (p *PluginStore) restoreReviewedPlugin(ctx context.Context, instance string, request pluginReviewRestoreRequest) (out pluginReviewRestoreResult, returnErr error) {
	if err := p.checkPluginFormWrite(ctx, instance, pluginFormWriteLifecycle); err != nil {
		return out, err
	}
	in, err := p.pluginLifecycleInstallation(ctx, p.store.DB, instance)
	if err != nil {
		return out, err
	}
	if _, _, err = p.reviewedRestoreTarget(ctx, p.store.DB, in, request); err != nil {
		return out, err
	}
	ctx, admission, err := p.beginResetAdmission(ctx, in, false)
	if err != nil {
		return out, err
	}
	defer func() {
		returnErr = admission.finish(returnErr)
		if returnErr != nil {
			out = pluginReviewRestoreResult{}
		}
	}()
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer admission.rollback(tx)
	if err = p.requireResetAdmissionTx(ctx, tx, instance); err != nil {
		return out, err
	}
	current, err := p.pluginLifecycleInstallation(ctx, tx, instance)
	if err != nil {
		return out, err
	}
	if current.PluginID != in.PluginID || current.DesiredEnabled != in.DesiredEnabled || !bytes.Equal(current.Manifest, in.Manifest) {
		return out, bad("PLUGIN_SNAPSHOT_STALE", "待确认实例状态已变化")
	}
	target, review, err := p.reviewedRestoreTarget(ctx, tx, current, request)
	if err != nil {
		return out, err
	}
	permit := &pluginReviewRestorePermit{store: p, tx: tx, previous: pluginFormInstallationCopy(current), target: pluginFormInstallationCopy(target), review: *review}
	if err = p.installTxWithReviewRestore(ctx, tx, target, permit); err != nil {
		return out, err
	}
	// No configuration write, enqueue, initialization or owner execution. installTx
	// alone performs the existing install sequence, membership and owner genesis.
	restored, err := p.pluginLifecycleInstallation(ctx, tx, instance)
	if err != nil {
		return out, err
	}
	held, err := p.pluginUpdateReviewRead(ctx, tx, restored)
	if err != nil {
		return out, err
	}
	if !restored.Installed || restored.Generation != in.Generation+1 || held == nil || held.Revision != review.Revision || held.ConfigSHA != review.ConfigSHA {
		return out, pluginLifecycleInvalid()
	}
	if err = admission.commit(tx); err != nil {
		return out, err
	}
	return pluginReviewRestoreResult{true, true, restored.Version, restored.Generation}, nil
}

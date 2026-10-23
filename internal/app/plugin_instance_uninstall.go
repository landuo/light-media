package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"light-media/internal/plugins/uninstall"
)

// Presentation only, after successful shared lifecycle authorization. Cleanup
// itself uses instanceMetadata's authenticated manifest and metadata pair.
func pluginUninstallDataRetained(in PluginInstallation) bool {
	var envelope struct {
		Marker *pluginInstanceMarker `json:"host_instance"`
	}
	if json.Unmarshal(in.Manifest, &envelope) != nil || envelope.Marker == nil {
		return true
	}
	return envelope.Marker.Purpose != "instance-clone/v1" || envelope.Marker.InstanceID != in.InstanceID || envelope.Marker.SourceInstanceID == ""
}

func (p *PluginStore) requireReusableCloneTx(ctx context.Context, tx *sql.Tx, target, source, pluginID string) error {
	conflict := func() error {
		return bad("PLUGIN_CLONE_CONFLICT", "分身编号仍被安装状态、未完成任务或保留数据占用")
	}
	in, err := p.pluginLifecycleInstallation(ctx, tx, target)
	if err != nil {
		return err
	}
	if in.Installed || in.DesiredEnabled || in.PluginID != pluginID {
		return conflict()
	}
	state, err := pluginLifecycleState(in)
	if err != nil {
		return err
	}
	if state != pluginLifecycleInactive {
		return conflict()
	}
	metadata, err := p.instanceMetadata(ctx, tx, target)
	if err != nil {
		return err
	}
	if metadata.SourceInstanceID != source {
		return conflict()
	}
	var values, active int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_values WHERE instance_id=?`, target).Scan(&values); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND state IN ('pending','running','reconcile_pending')`, target).Scan(&active); err != nil {
		return err
	}
	if values != 0 || active != 0 {
		return conflict()
	}
	return nil
}

// An unknown or still-running effect must never be erased by clone removal.
// The sole exception is the existing authenticated uninstall manager's own
// receipt-producing call, not a caller-supplied run ID.
func (p *PluginStore) requireCloneRemovalIdleTx(ctx context.Context, tx *sql.Tx, instance string) error {
	exclude := ""
	if d, _ := ctx.Value(pluginDeferredExecutionKey{}).(*pluginDeferredCall); d != nil && d.a != nil && d.a.pluginStore == p && d.installation.InstanceID == instance && d.installation.PluginID == "PluginUnInstall" && d.record.Method == uninstall.ApplyCapability {
		lease, err := p.requireSupervisedFinishTx(ctx, tx, d.run.ID)
		if err != nil {
			return err
		}
		if lease != nil && lease.claim.admission.InstanceID == instance && lease.claim.admission.PluginID == "PluginUnInstall" {
			exclude = d.run.ID
		}
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND id<>? AND (state IN ('running','reconcile_pending') OR (state='pending' AND error_code<>''))`, instance, exclude).Scan(&active); err != nil {
		return err
	}
	if active != 0 {
		return errPluginRunAttemptUnknown
	}
	return nil
}

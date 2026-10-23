package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const pluginExecutionHandoffKind = "plugin_original_execution_handoff"
const originalPluginContainerID = "cb82e3f550881334ef3814426016d08d909bccd88d3297522756ba31654d129a"

type pluginExecutionSource struct {
	Instance, Fingerprint, ReceiptDigest, ImportedAt string
}

// Only the offline delivery host can supply this observation. No management or
// plugin HTTP endpoint, worker capability or form action can create a handoff.
type stoppedPluginSource struct {
	ContainerID   string `json:"container_id"`
	ImageSHA256   string `json:"image_sha256"`
	FinishedAt    string `json:"finished_at"`
	ObservedAt    string `json:"observed_at"`
	RestartPolicy string `json:"restart_policy"`
	Running       bool   `json:"running"`
	Restarting    bool   `json:"restarting"`
	PID           int    `json:"pid"`
}
type pluginExecutionHandoff struct {
	Version int
	Purpose string
	Source  pluginExecutionSource
	Stopped stoppedPluginSource
	State   string
}

func pluginHandoffRequired() error {
	return bad("PLUGIN_HANDOFF_REQUIRED", "原版执行程序尚未完成交接，迁入插件暂不运行")
}
func pluginHandoffInvalid() error {
	return bad("PLUGIN_HANDOFF_INVALID", "插件交接记录无法校验，请核对迁移来源")
}
func pluginHandoffPending(e error) bool {
	return pluginErrorCodeInternal(e) == "PLUGIN_HANDOFF_REQUIRED"
}

// Resolve the authenticated current namespace without rereading the complete
// site catalog on every downloader IO. Existing namespace/owner guards continue
// to validate their full receipts independently in the same execution path.
func (p *PluginStore) pluginExecutionSourceTx(ctx context.Context, tx *sql.Tx, instance string) (*pluginExecutionSource, error) {
	var in PluginInstallation
	in.InstanceID = instance
	if e := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&in.PluginID, &in.Version, &in.Generation); e != nil {
		return nil, e
	}
	if instance != in.PluginID {
		return nil, nil // Reviewed imports have canonical, separate identities.
	}
	var kind string
	var sealed []byte
	if isTorrentTransfer(in.PluginID) {
		var root transferOwnerRoot
		if e := tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_transfer_roots WHERE instance_id=? AND generation=?`, instance, in.Generation).Scan(&sealed); e != nil {
			return nil, e
		}
		raw, e := p.unseal("run_result", instance, transferRootKey(in.Generation), sealed)
		if e != nil || cleanupJournalStrict(raw, &root) != nil || root.Instance != instance || root.Plugin != in.PluginID || root.Generation != in.Generation {
			return nil, pluginHandoffInvalid()
		}
		if root.ReferenceProtocol != reviewedTransferProtocol {
			return nil, nil
		}
		kind = reviewedTransferMappingKind
	} else if in.PluginID == "IYUUAutoSeed" || in.PluginID == "SeedRescue" || in.PluginID == "SiteStatistic" {
		var protocol siteReferenceProtocol
		if e := tx.QueryRowContext(ctx, `SELECT sealed FROM native_site_reference_protocols WHERE instance_id=? AND generation=?`, instance, in.Generation).Scan(&sealed); e != nil {
			return nil, e
		}
		if p.store.Unseal(sealed, &protocol) != nil || protocol.Instance != instance || protocol.Plugin != in.PluginID || protocol.PluginVersion != in.Version || protocol.Generation != in.Generation {
			return nil, pluginHandoffInvalid()
		}
		if in.PluginID == "IYUUAutoSeed" && strings.HasPrefix(protocol.Source, reviewedIYUUProtocolPrefix) {
			kind = reviewedIYUUMappingKind
		} else if in.PluginID == "SeedRescue" && protocol.Protocol == reviewedSeedProtocol {
			kind = reviewedSeedMappingKind
		} else if in.PluginID == "SiteStatistic" && strings.HasPrefix(protocol.Source, reviewedStatisticProtocolPrefix) {
			kind = sourceAccountKind
		} else {
			return nil, nil
		}
	} else {
		return nil, nil
	}
	recordID := instance
	if kind == sourceAccountKind {
		recordID = "main"
	}
	if e := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, kind, recordID).Scan(&sealed); e != nil {
		return nil, e
	}
	raw, e := p.unseal(kind, instance, "main", sealed)
	if e != nil {
		return nil, pluginHandoffInvalid()
	}
	var fp struct {
		SourceFingerprint string
		Binding           struct{ SourceFingerprint string }
	}
	if json.Unmarshal(raw, &fp) != nil {
		return nil, pluginHandoffInvalid()
	}
	fingerprint := fp.SourceFingerprint
	if kind != reviewedIYUUMappingKind {
		fingerprint = fp.Binding.SourceFingerprint
	}
	if !pluginStorageDigest.MatchString(fingerprint) {
		return nil, pluginHandoffInvalid()
	}
	source := &pluginExecutionSource{Instance: instance, Fingerprint: fingerprint}
	if tx.QueryRowContext(ctx, `SELECT r.digest,r.imported_at FROM plugin_migration_receipts r JOIN plugin_migration_runs m ON m.id=r.run_id AND m.source_fingerprint=r.source_fingerprint AND m.source_plugin_id=r.source_plugin_id AND m.instance_id=r.instance_id WHERE r.source_fingerprint=? AND r.source_plugin_id=? AND r.instance_id=? AND m.state='imported'`, fingerprint, instance, instance).Scan(&source.ReceiptDigest, &source.ImportedAt) != nil || !pluginStorageDigest.MatchString(source.ReceiptDigest) {
		return nil, pluginHandoffInvalid()
	}
	return source, nil
}
func validStoppedPluginSource(v stoppedPluginSource) bool {
	finished, e := time.Parse(time.RFC3339Nano, v.FinishedAt)
	observed, oe := time.Parse(time.RFC3339Nano, v.ObservedAt)
	return v.ContainerID == originalPluginContainerID && pluginStorageDigest.MatchString(v.ImageSHA256) && !v.Running && !v.Restarting && v.PID == 0 && v.RestartPolicy == "no" && e == nil && oe == nil && !finished.IsZero() && !observed.IsZero() && !finished.After(observed)
}
func pluginHandoffSnapshotTime(source pluginExecutionSource, stopped stoppedPluginSource) error {
	imported, ie := time.Parse(time.RFC3339Nano, source.ImportedAt)
	finished, fe := time.Parse(time.RFC3339Nano, stopped.FinishedAt)
	if ie != nil || fe != nil || finished.After(imported) {
		return bad("PLUGIN_HANDOFF_STALE_SNAPSHOT", "原版停止时间晚于本次导入，请在原版停止后重新核对最终来源")
	}
	return nil
}
func (p *PluginStore) requirePluginExecutionHandoffTx(ctx context.Context, tx *sql.Tx, instance string) error {
	source, e := p.pluginExecutionSourceTx(ctx, tx, instance)
	if e != nil || source == nil {
		return e
	}
	var sealed []byte
	e = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, pluginExecutionHandoffKind, instance).Scan(&sealed)
	if errors.Is(e, sql.ErrNoRows) {
		return pluginHandoffRequired()
	}
	if e != nil {
		return e
	}
	raw, e := p.unseal(pluginExecutionHandoffKind, instance, "main", sealed)
	var proof pluginExecutionHandoff
	if e != nil || cleanupJournalStrict(raw, &proof) != nil || proof.Version != 1 || proof.Purpose != "original-execution-handoff/v1" || proof.Source != *source || !validStoppedPluginSource(proof.Stopped) || (proof.State != "accepted" && proof.State != "revoked") {
		return pluginHandoffInvalid()
	}
	if e = pluginHandoffSnapshotTime(*source, proof.Stopped); e != nil {
		return pluginHandoffInvalid()
	}
	if proof.State == "revoked" {
		return pluginHandoffRequired()
	}
	return nil
}
func (p *PluginStore) requirePluginExecutionHandoff(ctx context.Context, instance string) error {
	tx, e := p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	return p.requirePluginExecutionHandoffTx(ctx, tx, instance)
}

// The authenticated delivery host observes Docker on the source host after the
// operator has stopped the original container. It then delivers only bounded
// metadata over stdin to the offline target tool. This is not a network probe
// and never treats a disabled config flag or an HTTP200 as process quiescence.
func validateStoppedPluginInput(raw []byte) (stoppedPluginSource, error) {
	var stopped stoppedPluginSource
	if len(raw) > 4096 {
		return stopped, pluginHandoffInvalid()
	}
	fields, shapeErr := sourceSettingsObject(raw, "container_id", "image_sha256", "finished_at", "observed_at", "restart_policy", "running", "restarting", "pid")
	if shapeErr != nil || len(fields) != 8 || cleanupJournalStrict(raw, &stopped) != nil || !validStoppedPluginSource(stopped) {
		return stopped, pluginHandoffInvalid()
	}
	for _, v := range fields {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return stopped, pluginHandoffInvalid()
		}
	}
	observed, _ := time.Parse(time.RFC3339Nano, stopped.ObservedAt)
	if age := time.Since(observed); age < -5*time.Second || age > 2*time.Minute {
		return stopped, pluginHandoffInvalid()
	}
	return stopped, nil
}
func ValidateReviewedPluginStoppedInput(raw []byte) error {
	_, e := validateStoppedPluginInput(raw)
	return e
}
func (p *PluginStore) pluginExecutionHandoffTargetTx(ctx context.Context, tx *sql.Tx, instance string) (*pluginExecutionSource, error) {
	source, e := p.pluginExecutionSourceTx(ctx, tx, instance)
	if e != nil || source == nil {
		return nil, pluginHandoffInvalid()
	}
	// Verify full reference authority on the one-time delivery boundary too.
	if isTorrentTransfer(instance) {
		var generation int64
		if e = tx.QueryRowContext(ctx, `SELECT generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&generation); e == nil {
			_, _, e = p.transferRootReadTx(ctx, tx, instance, generation)
		}
	} else {
		e = p.requireSiteReferenceProtocolTx(ctx, tx, instance)
	}
	if e != nil {
		return nil, e
	}
	var active int
	if e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs WHERE instance_id=? AND state IN ('running','reconcile_pending')`, instance).Scan(&active); e != nil || active != 0 {
		return nil, pluginHandoffRequired()
	}
	return source, nil
}
func ValidateReviewedPluginExecutionHandoffTarget(ctx context.Context, db *sql.DB, key []byte, instance string, raw []byte) error {
	if db == nil || len(key) != 32 {
		return pluginHandoffInvalid()
	}
	stopped, e := validateStoppedPluginInput(raw)
	if e != nil {
		return e
	}
	p := &PluginStore{store: &Store{DB: db, key: key}}
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	source, e := p.pluginExecutionHandoffTargetTx(ctx, tx, instance)
	if e != nil {
		return e
	}
	return pluginHandoffSnapshotTime(*source, stopped)
}
func (p *PluginStore) ImportReviewedPluginExecutionHandoff(ctx context.Context, instance string, raw []byte) error {
	if err := (*PluginStore)(nil).checkPluginFormWrite(ctx, "", pluginFormWriteLifecycle); err != nil {
		return err
	}
	stopped, e := validateStoppedPluginInput(raw)
	if e != nil {
		return e
	}
	tx, e := p.store.DB.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	source, e := p.pluginExecutionHandoffTargetTx(ctx, tx, instance)
	if e != nil {
		return e
	}
	// A source stopped after an earlier target import could have changed its
	// history in between. Delivery must take its final snapshot while stopped.
	if e = pluginHandoffSnapshotTime(*source, stopped); e != nil {
		return e
	}
	proof := pluginExecutionHandoff{Version: 1, Purpose: "original-execution-handoff/v1", Source: *source, Stopped: stopped, State: "accepted"}
	sealed, e := p.seal(pluginExecutionHandoffKind, instance, "main", reviewedIYUUJSON(proof))
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, pluginExecutionHandoffKind, instance, sealed, now()); e != nil {
		return e
	}
	return tx.Commit()
}

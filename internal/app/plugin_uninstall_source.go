package app

// The installed source does not have a batch transaction: each package is
// stopped/removed before the two independent SystemConfig commits. Native
// logical package removal and its owner handoff are one small transaction;
// later failures must not undo earlier packages. The private source journal
// records these boundaries and never authorizes replay after an uncertain cut.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	cleanuphost "light-media/internal/plugins/removelink/host"
	"light-media/internal/plugins/uninstall"
)

const uninstallSourceFailed = "PLUGIN_UNINSTALL_SOURCE_FAILED"

var errUninstallSourceUnknown = errors.New("uninstall commit outcome unknown")

const pluginUninstallSourceSchema = `CREATE TABLE IF NOT EXISTS plugin_uninstall_source(
run_id TEXT PRIMARY KEY,sealed BLOB NOT NULL,FOREIGN KEY(run_id) REFERENCES plugin_runs(id));
CREATE TABLE IF NOT EXISTS plugin_uninstall_source_lists(
run_id TEXT PRIMARY KEY,sealed BLOB NOT NULL,FOREIGN KEY(run_id) REFERENCES plugin_runs(id));
CREATE TABLE IF NOT EXISTS plugin_install_sequence(
instance_id TEXT PRIMARY KEY,seq INTEGER NOT NULL UNIQUE CHECK(typeof(seq)='integer' AND seq>0),
FOREIGN KEY(instance_id) REFERENCES plugin_installations(instance_id));
`

const pluginInstallSequenceBackfill = `INSERT INTO plugin_install_sequence(instance_id,seq)
SELECT i.instance_id,(SELECT COALESCE(MAX(seq),0) FROM plugin_install_sequence)+ROW_NUMBER() OVER(ORDER BY i.rowid)
FROM plugin_installations i LEFT JOIN plugin_install_sequence s ON s.instance_id=i.instance_id WHERE s.instance_id IS NULL;`

// The original install endpoint appends only IDs absent from its installed
// list. A removed/reinstalled logical package gets a new sequence; config
// changes, enable/reset and upgrades of an installed package do not move it.
// This is local installation history, not original-system order or UI layout.
func (p *PluginStore) recordPluginInstallSequenceTx(ctx context.Context, tx *sql.Tx, instance string, wasInstalled bool) error {
	var missing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_installations i LEFT JOIN plugin_install_sequence s ON s.instance_id=i.instance_id WHERE i.instance_id<>? AND s.instance_id IS NULL`, instance).Scan(&missing); err != nil {
		return err
	}
	if missing != 0 {
		return bad("PLUGIN_INVENTORY_ORDER_INVALID", "本地插件安装顺序记录缺失")
	}
	sourceIDs, _, sourceErr := p.sourceInstalled(ctx, tx, true)
	if sourceErr != nil {
		return sourceErr
	}
	if wasInstalled || slices.Contains(sourceIDs, instance) {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_install_sequence WHERE instance_id=?`, instance).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return bad("PLUGIN_INVENTORY_ORDER_INVALID", "本地插件安装顺序记录缺失")
		}
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO plugin_install_sequence(instance_id,seq) VALUES(?,(SELECT COALESCE(MAX(seq),0)+1 FROM plugin_install_sequence)) ON CONFLICT(instance_id) DO UPDATE SET seq=excluded.seq`, instance)
	return err
}

type pluginUninstallSourceRecord struct {
	Schema                                            int
	Run, Instance, Version, Action, InputSHA          string
	Generation, InitialRevision, FinalRevision        int64
	InstalledRevision                                 int64
	Phase, Stage, Target                              string
	InstalledBefore, InstalledCache, InstalledDurable []string
	Removed                                           []string
	Result                                            json.RawMessage
	ErrorCode                                         string
}

type pluginUninstallConfigCache struct {
	mu     sync.Mutex
	values map[string]pluginUninstallCachedValue
}

type pluginUninstallCachedValue struct {
	value      PluginStoredValue
	generation int64
}

func (p *PluginStore) cacheUninstallConfig(instance string, revision, generation int64, raw json.RawMessage) {
	p.uninstallConfig.mu.Lock()
	defer p.uninstallConfig.mu.Unlock()
	if p.uninstallConfig.values == nil {
		p.uninstallConfig.values = map[string]pluginUninstallCachedValue{}
	}
	p.uninstallConfig.values[instance] = pluginUninstallCachedValue{value: PluginStoredValue{Revision: revision, Value: bytes.Clone(raw)}, generation: generation}
}

func (p *PluginStore) uninstallCachedConfig(ctx context.Context, instance string, value PluginStoredValue) PluginStoredValue {
	p.uninstallConfig.mu.Lock()
	defer p.uninstallConfig.mu.Unlock()
	if cached, ok := p.uninstallConfig.values[instance]; ok {
		var generation int64
		err := p.store.DB.QueryRowContext(ctx, `SELECT generation FROM plugin_installations WHERE instance_id=?`, instance).Scan(&generation)
		if err != nil {
			return value
		}
		if err == nil && cached.generation == generation && cached.value.Revision == value.Revision {
			value.Value = bytes.Clone(cached.value.Value)
		} else {
			delete(p.uninstallConfig.values, instance)
		}
	}
	return value
}

func (p *PluginStore) readUninstallSource(ctx context.Context, q pluginRemoveLinkJournalReader, run PluginRun) (pluginUninstallSourceRecord, []byte, error) {
	var r pluginUninstallSourceRecord
	var sealed []byte
	err := q.QueryRowContext(ctx, `SELECT sealed FROM plugin_uninstall_source WHERE run_id=?`, run.ID).Scan(&sealed)
	if err != nil {
		return r, nil, err
	}
	raw, err := p.unseal("uninstall_result", run.InstanceID, "source:v1:"+run.ID, sealed)
	if err != nil {
		return r, nil, bad("PLUGIN_UNINSTALL_EVIDENCE_INVALID", "卸载分步记录完整性无效")
	}
	if cleanupJournalStrict(raw, &r) != nil || r.Schema != 1 || r.Run != run.ID || r.Instance != run.InstanceID || r.Version != run.Version || r.Generation != run.Generation || r.Action != run.Action || r.InputSHA != deferredInputSHA(run.Input) || (r.FinalRevision != run.ConfigRevision && r.InitialRevision != run.ConfigRevision) {
		return r, nil, bad("PLUGIN_UNINSTALL_EVIDENCE_INVALID", "卸载分步记录身份无效")
	}
	if r.Phase != "started" && r.Phase != "failed" && r.Phase != "succeeded" {
		return r, nil, bad("PLUGIN_UNINSTALL_EVIDENCE_INVALID", "卸载分步记录阶段无效")
	}
	if r.Phase == "failed" && (r.ErrorCode != uninstallSourceFailed || !json.Valid(r.Result)) {
		return r, nil, bad("PLUGIN_UNINSTALL_EVIDENCE_INVALID", "卸载失败记录无效")
	}
	if r.Phase == "succeeded" && (r.ErrorCode != "" || !json.Valid(r.Result)) {
		return r, nil, bad("PLUGIN_UNINSTALL_EVIDENCE_INVALID", "卸载完成记录无效")
	}
	var listSealed []byte
	if err = q.QueryRowContext(ctx, `SELECT sealed FROM plugin_uninstall_source_lists WHERE run_id=?`, run.ID).Scan(&listSealed); err != nil {
		return r, nil, bad("PLUGIN_UNINSTALL_EVIDENCE_INVALID", "卸载安装列表证据缺失")
	}
	listRaw, err := p.unseal("uninstall_result", run.InstanceID, "installed:v1:"+run.ID, listSealed)
	var durable []string
	if err != nil || cleanupJournalStrict(listRaw, &durable) != nil || !slices.Equal(durable, r.InstalledDurable) {
		return r, nil, bad("PLUGIN_UNINSTALL_EVIDENCE_INVALID", "卸载安装列表证据不一致")
	}
	return r, sealed, nil
}

func (p *PluginStore) writeUninstallSourceTx(ctx context.Context, tx *sql.Tx, r pluginUninstallSourceRecord, previous []byte) ([]byte, error) {
	raw, err := pluginJSON(r, true)
	if err != nil {
		return nil, err
	}
	sealed, err := p.seal("uninstall_result", r.Instance, "source:v1:"+r.Run, raw)
	if err != nil {
		return nil, err
	}
	if previous == nil {
		_, err = tx.ExecContext(ctx, `INSERT INTO plugin_uninstall_source(run_id,sealed) VALUES(?,?)`, r.Run, sealed)
	} else {
		var changed sql.Result
		changed, err = tx.ExecContext(ctx, `UPDATE plugin_uninstall_source SET sealed=? WHERE run_id=? AND sealed=?`, sealed, r.Run, previous)
		if err == nil {
			if n, _ := changed.RowsAffected(); n != 1 {
				err = bad("PLUGIN_UNINSTALL_EVIDENCE_CONFLICT", "卸载分步记录已变化")
			}
		}
	}
	return sealed, err
}

// A lost COMMIT reply is not a known source exception. The exact sealed state
// committed atomically with the effect is the only proof that permits either
// success or a known failure. Any other/read-error outcome stays uncertain.
func (p *PluginStore) uninstallCommitProof(run string, expected, previous []byte) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var actual []byte
	err := p.store.DB.QueryRowContext(ctx, `SELECT sealed FROM plugin_uninstall_source WHERE run_id=?`, run).Scan(&actual)
	if err != nil {
		return false, errUninstallSourceUnknown
	}
	if bytes.Equal(actual, expected) {
		return true, nil
	}
	if bytes.Equal(actual, previous) {
		return false, nil
	}
	return false, errUninstallSourceUnknown
}

func (p *PluginStore) uninstallSourceOutcome(ctx context.Context, run PluginRun) (json.RawMessage, string, bool, error) {
	r, _, err := p.readUninstallSource(ctx, p.store.DB, run)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", false, nil
	}
	if err != nil {
		return nil, "", false, err
	}
	if r.Phase == "started" {
		return nil, "", false, nil
	}
	return bytes.Clone(r.Result), r.ErrorCode, true, nil
}

// Existing installations have no per-plugin executable. Removing their logical
// package registration is the counterpart of deleting the private Python
// directory; it never removes the executable shared by all native plugins.
type pluginUninstallSourceOperations struct {
	*uninstallPlanOperations
	a               *App
	run             *PluginRun
	installation    PluginInstallation
	targets         map[string]PluginInstallation
	prepared        map[string]*pluginUninstallCleanupPrepared
	targetRevisions map[string]int64
	record          pluginUninstallSourceRecord
	sealed          []byte
}

func (o *pluginUninstallSourceOperations) guard(ctx context.Context, tx *sql.Tx) error {
	var generation, revision int64
	var installed, desired bool
	var state, version, action string
	err := tx.QueryRowContext(ctx, `SELECT i.generation,i.installed,i.desired_enabled,v.revision,r.state,r.version,r.action FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' JOIN plugin_runs r ON r.instance_id=i.instance_id WHERE r.id=? AND i.plugin_id='PluginUnInstall' AND i.version='2.2' AND r.generation=? AND r.config_revision=?`, o.run.ID, o.run.Generation, o.run.ConfigRevision).Scan(&generation, &installed, &desired, &revision, &state, &version, &action)
	if err != nil {
		return err
	}
	wantGeneration := o.run.Generation
	wantInstalled := true
	if o.removed[o.run.InstanceID] {
		wantGeneration++
		wantInstalled = false
	}
	if generation != wantGeneration || installed != wantInstalled || desired != wantInstalled || revision != o.run.ConfigRevision || state != "running" || version != o.run.Version || action != o.run.Action {
		return bad("PLUGIN_SNAPSHOT_STALE", "卸载执行或配置已变化")
	}
	return nil
}

func (o *pluginUninstallSourceOperations) step(ctx context.Context, stage, target string, apply func(*sql.Tx) error) error {
	o.record.Stage, o.record.Target = stage, target
	o.a.pluginMu.Lock()
	defer o.a.pluginMu.Unlock()
	tx, err := o.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = o.guard(ctx, tx); err != nil {
		return err
	}
	r := o.record
	r.Stage, r.Target = stage, target
	r.Removed = append([]string{}, o.record.Removed...)
	if apply != nil {
		if err = apply(tx); err != nil {
			return err
		}
	}
	if stage == "save_installed" {
		r.InstalledDurable = append([]string{}, o.record.InstalledDurable...)
	}
	if stage == "remove_plugin" {
		r.Removed = append(r.Removed, target)
	}
	if stage == "save_own_config" {
		r.FinalRevision = o.run.ConfigRevision + 1
	}
	sealed, err := o.a.pluginStore.writeUninstallSourceTx(ctx, tx, r, o.sealed)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		committed, proofErr := o.a.pluginStore.uninstallCommitProof(o.run.ID, sealed, o.sealed)
		if proofErr != nil {
			return proofErr
		}
		if !committed {
			return err
		}
	}
	o.record, o.sealed = r, sealed
	return nil
}

func (o *pluginUninstallSourceOperations) RemoveJobs(ctx context.Context, instance string) error {
	err := o.step(ctx, "remove_jobs", instance, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE plugin_schedules SET active=0 WHERE instance_id=?`, instance)
		return err
	})
	if err == nil {
		return nil
	}
	var boundary *APIError
	if ctx.Err() != nil || errors.Is(err, errUninstallSourceUnknown) || errors.As(err, &boundary) {
		return err
	}
	// Scheduler.remove_plugin_job catches backend errors and writes a system
	// message. Failure of that message itself escapes, before class removal.
	return o.step(ctx, "scheduler_exception", instance, func(tx *sql.Tx) error {
		var manifest struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(o.targets[instance].Manifest, &manifest)
		if manifest.Name == "" {
			manifest.Name = o.targets[instance].PluginID
		}
		n := Notification{ID: "notice_uninstall_" + o.run.ID + "_" + instance, Title: "插件 " + manifest.Name + " 服务移除失败", Message: "PLUGIN_SCHEDULER_EXCEPTION", MType: "system", CreatedAt: now()}
		return putCleanupNoticeTx(ctx, tx, n)
	})
}

func (o *pluginUninstallSourceOperations) RemovePlugin(ctx context.Context, instance string) (returnErr error) {
	target, ok := o.targets[instance]
	if !ok || o.removed[instance] {
		return bad("PLUGIN_RUN_SCOPE_DENIED", "卸载目标不属于固定快照")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !target.Installed {
		// Original Manager.stop removes absent classes as a no-op. Keep the
		// tombstone generation stable and never run an old cleanup owner.
		err := o.step(ctx, "remove_plugin", instance, func(tx *sql.Tx) error {
			var generation int64
			var installed bool
			if err := tx.QueryRowContext(ctx, `SELECT generation,installed FROM plugin_installations WHERE instance_id=?`, instance).Scan(&generation, &installed); err != nil {
				return err
			}
			if installed || generation != target.Generation {
				return bad("PLUGIN_SNAPSHOT_STALE", "待卸载插件状态已变化")
			}
			return nil
		})
		if err == nil {
			o.removed[instance] = true
		}
		return err
	}
	if isUninstallCleanupPlugin(target.PluginID) {
		// Only one source target is reserved at a time. Preparing a later owner
		// cannot erase packages already removed, and no wait holds lifecycle.
		o.a.pluginLifecycleMu.Unlock()
		items, err := o.a.prepareUninstallCleanupTargets(ctx, []PluginInstallation{target})
		o.a.pluginLifecycleMu.Lock()
		if err != nil {
			return err
		}
		p := items[0]
		o.prepared[instance] = p
		defer func() {
			o.a.pluginLifecycleMu.Unlock()
			resolveErr := resolveUninstallCleanupPrepared(p, o.removed[instance])
			p.release()
			o.a.pluginLifecycleMu.Lock()
			delete(o.prepared, instance)
			if returnErr == nil {
				returnErr = resolveErr
			}
		}()
		if p.from.Generation != target.Generation || p.from.Revision != o.targetRevisions[instance] || p.pluginID != target.PluginID || p.version != target.Version {
			return bad("PLUGIN_SNAPSHOT_STALE", "清理目标代际或配置已变化")
		}
		o.a.pluginLifecycleMu.Unlock()
		err = p.drain(ctx)
		o.a.pluginLifecycleMu.Lock()
		if err != nil {
			return err
		}
	}

	if target.PluginID == "MediaServerRefresh" && target.DesiredEnabled {
		if err := o.a.drainMediaRefresh(ctx, target); err != nil {
			// Cancellation/ownership/uncertainty are native host boundaries,
			// not a source plugin callback exception. Never convert them to a
			// successful lifecycle return or extend their timeout.
			var boundary *APIError
			if ctx.Err() != nil || errors.As(err, &boundary) {
				return err
			}
			o.a.pluginExecutionLog(o.installation, *o.run, "WARN", "uninstall_stop_exception", "停止插件时发生错误，继续按原版移除插件", "PLUGIN_STOP_EXCEPTION")
		}
	}
	// Do not mutate the plan until the small target/owner transaction commits.
	local := &uninstallPlanOperations{installed: o.installed, removed: map[string]bool{}}
	err := o.step(ctx, "remove_plugin", instance, func(tx *sql.Tx) error {
		ops := &uninstallDatabaseOperations{uninstallPlanOperations: local, p: o.a.pluginStore, tx: tx, run: o.run, targets: o.targets}
		if err := ops.RemovePlugin(ctx, instance); err != nil {
			return err
		}
		if p := o.prepared[instance]; p != nil {
			to := cleanuphost.Binding{InstanceID: instance, Generation: p.from.Generation + 1, Revision: 0}
			if err := p.publishTx(ctx, tx, to, ""); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	o.removed[instance] = true
	o.a.pluginMu.Lock()
	delete(o.a.subscribeGroupCaches, instance)
	o.a.pluginMu.Unlock()
	o.a.pluginServerStatus.invalidate(instance)
	return nil
}

func (o *pluginUninstallSourceOperations) SaveInstalled(ctx context.Context, remaining []string) error {
	if err := o.uninstallPlanOperations.SaveInstalled(ctx, remaining); err != nil {
		return err
	}
	// SystemConfigOper.set always sets the warm value, then reads the durable
	// row. If the row exists and old cache equals new value it skips the DB
	// write; a prior failure must not be silently repaired by the next run.
	same := slices.Equal(o.record.InstalledCache, remaining)
	o.record.InstalledCache = append([]string{}, remaining...)
	o.a.pluginStore.cacheSourceInstalled(o.record.InstalledRevision, remaining)
	old := o.record.InstalledDurable
	err := o.step(ctx, "save_installed", "", func(tx *sql.Tx) error {
		value, revision, err := o.a.pluginStore.sourceInstalledState(ctx, tx)
		if err != nil {
			return err
		}
		if revision != o.record.InstalledRevision {
			return bad("PLUGIN_INVENTORY_ORDER_INVALID", "插件源安装列表已变化")
		}
		if value.Exists && same {
			return nil
		}
		exists := true
		if value.Exists && len(remaining) == 0 {
			exists = false
		}
		if err = o.a.pluginStore.writeSourceInstalledTx(ctx, tx, revision, remaining, exists); err != nil {
			return err
		}
		raw, err := pluginJSON(remaining, false)
		if err != nil {
			return err
		}
		sealed, err := o.a.pluginStore.seal("uninstall_result", o.run.InstanceID, "installed:v1:"+o.run.ID, raw)
		if err != nil {
			return err
		}
		changed, err := tx.ExecContext(ctx, `UPDATE plugin_uninstall_source_lists SET sealed=? WHERE run_id=?`, sealed, o.run.ID)
		if err == nil {
			if n, _ := changed.RowsAffected(); n != 1 {
				return bad("PLUGIN_UNINSTALL_EVIDENCE_INVALID", "卸载安装列表记录缺失")
			}
		}
		if err == nil {
			o.record.InstalledDurable = append([]string{}, remaining...)
		}
		return err
	})
	if err != nil {
		o.record.InstalledDurable = old
	}
	return err
}

func (o *pluginUninstallSourceOperations) SaveOwnConfig(ctx context.Context, raw json.RawMessage) error {
	// The real SystemConfigOper cache keeps this value even if its independent
	// DB commit fails. This override is memory-only, revision-bound, and cold
	// startup reads the original durable value. It never changes another plugin.
	generation := o.run.Generation
	if o.removed[o.run.InstanceID] {
		generation++
	}
	o.a.pluginStore.cacheUninstallConfig(o.run.InstanceID, o.run.ConfigRevision, generation, raw)
	updated := *o.run
	err := o.step(ctx, "save_own_config", "", func(tx *sql.Tx) error {
		ops := &uninstallDatabaseOperations{uninstallPlanOperations: o.uninstallPlanOperations, p: o.a.pluginStore, tx: tx, run: &updated, targets: o.targets}
		return ops.SaveOwnConfig(ctx, raw)
	})
	if err != nil {
		return err
	}
	*o.run = updated
	o.record.FinalRevision = updated.ConfigRevision
	return nil
}

func (o *pluginUninstallSourceOperations) finish(ctx context.Context, result uninstall.Result, sourceErr error) error {
	o.a.pluginMu.Lock()
	defer o.a.pluginMu.Unlock()
	r := o.record
	r.Phase = "succeeded"
	var err error
	r.Result, err = pluginJSON(result, true)
	if err != nil {
		return err
	}
	if sourceErr != nil {
		r.Phase = "failed"
		r.ErrorCode = uninstallSourceFailed
	}
	r.FinalRevision = o.run.ConfigRevision
	tx, err := o.a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	sealed, err := o.a.pluginStore.writeUninstallSourceTx(ctx, tx, r, o.sealed)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_uninstall_drains WHERE run_id=?`, o.run.ID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		committed, proofErr := o.a.pluginStore.uninstallCommitProof(o.run.ID, sealed, o.sealed)
		if proofErr != nil {
			return proofErr
		}
		if !committed {
			return err
		}
	}
	o.record, o.sealed = r, sealed
	return nil
}

func (a *App) applyPluginUninstallSource(ctx context.Context, run *PluginRun) (json.RawMessage, map[string]bool, error) {
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	a.pluginMu.Lock()
	actual, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || actual.InstanceID != run.InstanceID || actual.Version != run.Version || actual.Generation != run.Generation || actual.ConfigRevision != run.ConfigRevision || actual.Action != run.Action || !bytes.Equal(actual.Input, run.Input) || actual.State != "running" || (actual.Action != "init" && actual.Action != "reload") {
		a.pluginMu.Unlock()
		return nil, nil, bad("PLUGIN_RUN_SCOPE_DENIED", "卸载执行身份已变化")
	}
	if receipt, err := a.pluginStore.uninstallReceipt(ctx, actual); err == nil {
		a.pluginMu.Unlock()
		return receipt, nil, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		a.pluginMu.Unlock()
		return nil, nil, err
	}
	if old, _, e := a.pluginStore.readUninstallSource(ctx, a.Store.DB, actual); e == nil {
		a.pluginMu.Unlock()
		if old.Phase == "failed" {
			return old.Result, nil, bad(uninstallSourceFailed, "卸载源流程失败，已完成的步骤保持原样")
		}
		return nil, nil, bad("PLUGIN_DEFERRED_UNCERTAIN", "卸载分步操作已开始，禁止重复执行")
	} else if !errors.Is(e, sql.ErrNoRows) {
		a.pluginMu.Unlock()
		return nil, nil, e
	}
	installation, err := a.pluginStore.Installation(ctx, actual.InstanceID)
	if err != nil || installation.PluginID != "PluginUnInstall" || installation.Version != "2.2" || !installation.Installed || !installation.DesiredEnabled || installation.Generation != actual.Generation {
		a.pluginMu.Unlock()
		return nil, nil, bad("PLUGIN_SNAPSHOT_STALE", "卸载插件身份已变化")
	}
	config, err := a.pluginStore.Config(ctx, actual.InstanceID)
	if err != nil || config.Revision != actual.ConfigRevision {
		a.pluginMu.Unlock()
		return nil, nil, bad("PLUGIN_REVISION_CONFLICT", "卸载配置已变化")
	}
	// The source returns for an empty selection before it reads the global
	// installed list. Damaged unrelated inventory must not turn that no-op
	// into an uninstall invocation. This is an effect-free local plan.
	probe, probeErr := uninstall.Run(ctx, config.Value, &uninstallPlanOperations{installed: []string{}, removed: map[string]bool{}})
	if probeErr != nil {
		a.pluginMu.Unlock()
		return nil, nil, bad("INVALID_PLUGIN_CONFIG", "卸载选择不是原版支持的格式")
	}
	if !probe.Ran {
		a.pluginMu.Unlock()
		return json.RawMessage(`null`), nil, nil
	}
	inventory, installedIDs, durableIDs, installedRevision, err := a.pluginStore.uninstallSourceInventory(ctx)
	if err != nil {
		a.pluginMu.Unlock()
		return nil, nil, err
	}
	plan := &uninstallPlanOperations{installed: []string{}, removed: map[string]bool{}}
	targets := map[string]PluginInstallation{}
	for _, in := range inventory {
		plan.installed = append(plan.installed, in.InstanceID)
		targets[in.InstanceID] = in
	}
	planned, err := uninstall.Run(ctx, config.Value, plan)
	if err != nil {
		a.pluginMu.Unlock()
		return nil, nil, bad("INVALID_PLUGIN_CONFIG", "卸载选择不是原版支持的格式")
	}
	if !planned.Ran {
		a.pluginMu.Unlock()
		return json.RawMessage(`null`), nil, nil
	}
	if _, err = pluginJSON(planned, true); err != nil {
		a.pluginMu.Unlock()
		return nil, nil, err
	}
	revisions := map[string]int64{}
	for _, instance := range planned.Removed {
		if targets[instance].Installed && isUninstallCleanupPlugin(targets[instance].PluginID) {
			// Missing config is recorded as zero and fails at that source target,
			// rather than moving a later target's failure ahead of earlier effects.
			var revision int64
			e := a.Store.DB.QueryRowContext(ctx, `SELECT revision FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, instance).Scan(&revision)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				a.pluginMu.Unlock()
				return nil, nil, e
			}
			revisions[instance] = revision
		}
	}

	r := pluginUninstallSourceRecord{Schema: 1, Run: actual.ID, Instance: actual.InstanceID, Version: actual.Version, Action: actual.Action, InputSHA: deferredInputSHA(actual.Input), Generation: actual.Generation, InitialRevision: actual.ConfigRevision, FinalRevision: actual.ConfigRevision, Phase: "started", Stage: "admitted", InstalledRevision: installedRevision, InstalledBefore: installedIDs, InstalledCache: append([]string{}, installedIDs...), InstalledDurable: durableIDs, Removed: []string{}}
	// Include both maximum list copies and a complete result before any effect.
	// No late failure can be caused solely by the journal's own value limit.
	worst := r
	worst.Phase, worst.Stage, worst.ErrorCode = "failed", "scheduler_exception", uninstallSourceFailed
	worst.Removed = planned.Removed
	worst.Generation, worst.InitialRevision, worst.FinalRevision = 9223372036854775807, 9223372036854775807, 9223372036854775807
	for _, in := range inventory {
		if len(in.InstanceID) > len(worst.Target) {
			worst.Target = in.InstanceID
		}
	}
	worstResult := planned
	worstResult.OwnConfigReset = false
	worst.Result, err = pluginJSON(worstResult, true)
	if err == nil {
		_, err = pluginJSON(worst, true)
	}
	if err != nil {
		a.pluginMu.Unlock()
		return nil, nil, err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	var sealed []byte
	if err == nil {
		err = a.pluginStore.guardUninstallRunTx(ctx, tx, actual)
		if err == nil {
			sealed, err = a.pluginStore.writeUninstallSourceTx(ctx, tx, r, nil)
		}
		if err == nil {
			var listRaw, listSealed []byte
			listRaw, err = pluginJSON(r.InstalledDurable, false)
			if err == nil {
				listSealed, err = a.pluginStore.seal("uninstall_result", actual.InstanceID, "installed:v1:"+actual.ID, listRaw)
			}
			if err == nil {
				_, err = tx.ExecContext(ctx, `INSERT INTO plugin_uninstall_source_lists VALUES(?,?)`, actual.ID, listSealed)
			}
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
	}
	a.pluginMu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	ops := &pluginUninstallSourceOperations{uninstallPlanOperations: &uninstallPlanOperations{installed: plan.installed, removed: map[string]bool{}}, a: a, run: run, installation: installation, targets: targets, prepared: map[string]*pluginUninstallCleanupPrepared{}, targetRevisions: revisions, record: r, sealed: sealed}
	result, sourceErr := uninstall.Run(ctx, config.Value, ops)
	if errors.Is(sourceErr, errUninstallSourceUnknown) {
		return nil, ops.removed, bad("PLUGIN_DEFERRED_UNCERTAIN", "卸载提交结果未知，禁止自动重复执行")
	}
	finish, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = ops.finish(finish, result, sourceErr); err != nil {
		return nil, ops.removed, bad("PLUGIN_DEFERRED_UNCERTAIN", "卸载分步结果尚未持久确认")
	}
	if sourceErr != nil {
		return ops.record.Result, ops.removed, bad(uninstallSourceFailed, "卸载源流程失败，已完成的步骤保持原样")
	}
	return ops.record.Result, ops.removed, nil
}

func isUninstallCleanupPlugin(plugin string) bool {
	switch plugin {
	case "ScrapeFileClean", "RemoveLink", "CleanInvalidSeed", "AutoBackup", "IYUUAutoSeed", "CloudflareSpeedTest", "SeedRescue", "BrushFlowLanduo":
		return true
	}
	return isTorrentTransfer(plugin)
}

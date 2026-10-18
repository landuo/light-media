package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"time"

	seedhost "light-media/internal/plugins/cleaninvalidseed/host"
	cleanuphost "light-media/internal/plugins/removelink/host"
	"light-media/internal/plugins/uninstall"
)

// The source has one installed entry per plugin. Native instances preserve
// their insertion order (rowid), with instance IDs as the concrete form values.
// Embedded Go handlers share the application executable; there is no separate
// package directory to remove and this adapter never deletes that executable.
const pluginUninstallSchema = `CREATE TABLE IF NOT EXISTS plugin_uninstall_operations(
run_id TEXT PRIMARY KEY,instance_id TEXT NOT NULL,version TEXT NOT NULL,generation INTEGER NOT NULL,
initial_revision INTEGER NOT NULL,final_revision INTEGER NOT NULL,action TEXT NOT NULL,result BLOB NOT NULL,
FOREIGN KEY(run_id) REFERENCES plugin_runs(id));
CREATE TABLE IF NOT EXISTS plugin_uninstall_drains(
run_id TEXT NOT NULL,instance_id TEXT NOT NULL,handler_key TEXT NOT NULL,generation INTEGER NOT NULL,config_revision INTEGER NOT NULL,
PRIMARY KEY(run_id,instance_id,handler_key),FOREIGN KEY(run_id) REFERENCES plugin_runs(id));`

// These are original installed class metadata, not mutable market metadata.
// New ports without original ordering use the source's fallback of 1000.
func pluginUninstallOrder(plugin string) int {
	switch plugin {
	case "AutoSignIn", "ScrapeFileClean", "RemoveLink":
		return 0
	case "CleanInvalidSeed", "SiteStatistic":
		return 1
	case "CategoryEditor":
		return 5
	case "CustomHosts", "RemoteIdentifiers":
		return 10
	case "CloudflareSpeedTest":
		return 12
	case "MediaServerRefresh":
		return 14
	case "AutoBackup", "IYUUAutoSeed":
		return 17
	case "TorrentTransfermusic", "SeedRescue", "TorrentTransfer":
		return 18
	case "BrushFlowLanduo":
		return 21
	case "SubscribeGroup":
		return 26
	case "LarkMessager":
		return 50
	case "SystemProcess", "MergeMessageNotify", "MergeSiteSwitch":
		return 66
	case "PluginUnInstall":
		return 98
	case "MPServerStatus":
		return 99
	default:
		return 1000
	}
}

func (p *PluginStore) uninstallInventory(ctx context.Context) ([]PluginInstallation, error) {
	rows, err := p.store.DB.QueryContext(ctx, `SELECT instance_id FROM plugin_installations WHERE installed=1 ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]PluginInstallation, 0, len(ids))
	for _, id := range ids {
		item, err := p.Installation(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (a *App) pluginUninstallHost(ctx context.Context, installation PluginInstallation, run *PluginRun, method string, raw json.RawMessage) (json.RawMessage, error) {
	var input map[string]json.RawMessage
	if json.Unmarshal(raw, &input) != nil || input == nil || len(input) != 0 {
		return nil, bad("PLUGIN_RUN_SCOPE_DENIED", "卸载操作不接受插件提供的目标或参数")
	}
	if installation.PluginID != "PluginUnInstall" || installation.Version != "2.2" {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "卸载接口只允许固定版本的卸载插件")
	}
	if method == uninstall.OptionsCapability {
		a.pluginMu.Lock()
		defer a.pluginMu.Unlock()
		current, err := a.pluginStore.Installation(ctx, installation.InstanceID)
		// POST install asks the trusted catalog worker for its defaults before
		// persisting an installation. That precise host-built preview can read
		// options for a missing/uninstalled ID, but never impersonate an active
		// installation or authorize an apply call. Existing forms stay pinned.
		expectedManifest, _ := json.Marshal(uninstall.Manifest())
		preview := run == nil && installation.InstanceID == installation.PluginID && installation.Generation == 0 && !installation.Installed && !installation.DesiredEnabled && installation.CreatedAt == "" && installation.UpdatedAt == "" && installation.ManifestSchemaVersion == 1 && bytes.Equal(installation.Manifest, expectedManifest)
		valid := false
		if preview {
			if errors.Is(err, sql.ErrNoRows) {
				valid = true
			} else if err != nil {
				return nil, err
			} else {
				valid = current.PluginID == installation.PluginID && !current.Installed
			}
		} else {
			valid = err == nil && current.PluginID == installation.PluginID && current.Version == installation.Version && current.Installed && current.Generation == installation.Generation
		}
		if !valid {
			return nil, bad("PLUGIN_SNAPSHOT_STALE", "卸载插件安装状态已变化")
		}
		inventory, err := a.pluginStore.uninstallInventory(ctx)
		if err != nil {
			return nil, err
		}
		items := []uninstall.LocalPlugin{}
		for _, item := range inventory {
			var manifest struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(item.Manifest, &manifest) != nil {
				return nil, bad("PLUGIN_MANIFEST_MISMATCH", "已安装插件元数据无效")
			}
			if manifest.Name == "" {
				manifest.Name = item.PluginID
			}
			items = append(items, uninstall.LocalPlugin{"id": item.InstanceID, "installed": true, "plugin_name": manifest.Name, "plugin_version": item.Version, "plugin_order": pluginUninstallOrder(item.PluginID)})
		}
		return json.Marshal(map[string]any{"plugins": items})
	}
	if method != uninstall.ApplyCapability || run == nil || run.InstanceID != installation.InstanceID || run.Version != installation.Version {
		return nil, bad("PLUGIN_RUN_SCOPE_DENIED", "卸载操作必须属于当前初始化执行")
	}
	return a.applyPluginUninstall(ctx, run)
}

func (p *PluginStore) uninstallReceipt(ctx context.Context, run PluginRun) (json.RawMessage, error) {
	var sealed []byte
	err := p.store.DB.QueryRowContext(ctx, `SELECT result FROM plugin_uninstall_operations WHERE run_id=? AND instance_id=? AND version=? AND generation=? AND final_revision=? AND action=?`, run.ID, run.InstanceID, run.Version, run.Generation, run.ConfigRevision, run.Action).Scan(&sealed)
	if err != nil {
		return nil, err
	}
	return p.unseal("uninstall_result", run.InstanceID, run.ID, sealed)
}

// Runner transport failure after a successful reverse call must not turn a
// committed self-uninstall into a false failure or replay it. Use a fresh host
// completion context, never a worker result, to resolve that narrow boundary.
func (p *PluginStore) committedUninstallResult(ctx context.Context, run *PluginRun) (json.RawMessage, bool, error) {
	actual, err := p.Run(ctx, run.ID)
	if err != nil {
		return nil, false, err
	}
	if actual.InstanceID != run.InstanceID || actual.Version != run.Version || actual.Generation != run.Generation || actual.Action != run.Action {
		return nil, false, bad("PLUGIN_RUN_SCOPE_DENIED", "卸载完成记录与执行身份不匹配")
	}
	result, err := p.uninstallReceipt(ctx, actual)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	*run = actual
	return result, true, nil
}

func (p *PluginStore) guardUninstallRunTx(ctx context.Context, tx *sql.Tx, run PluginRun) error {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs r JOIN plugin_installations i ON i.instance_id=r.instance_id JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE r.id=? AND r.instance_id=? AND r.version=? AND r.generation=? AND r.config_revision=? AND r.action=? AND r.action IN ('init','reload') AND r.state='running' AND i.plugin_id='PluginUnInstall' AND i.version='2.2' AND i.version=r.version AND i.generation=r.generation AND i.installed=1 AND i.desired_enabled=1 AND v.revision=r.config_revision`, run.ID, run.InstanceID, run.Version, run.Generation, run.ConfigRevision, run.Action).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return bad("PLUGIN_SNAPSHOT_STALE", "卸载事务的执行身份或配置已变化")
	}
	return nil
}

// A refresh drain can quiesce a consumer before the uninstall transaction.
// Persist its exact prior active identity so a crash in that gap cannot leave
// an installed plugin silently inactive. Only the trusted plan adds entries.
func (p *PluginStore) recordUninstallDrains(ctx context.Context, run PluginRun, consumers []pluginEventSubscription, selected map[string]bool, targets map[string]PluginInstallation) error {
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = p.guardUninstallRunTx(ctx, tx, run); err != nil {
		return err
	}
	for _, consumer := range consumers {
		target := targets[consumer.InstanceID]
		if !selected[consumer.InstanceID] || target.PluginID != "MediaServerRefresh" || !target.Installed || !target.DesiredEnabled {
			continue
		}
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO plugin_uninstall_drains VALUES(?,?,?,?,?)`, run.ID, consumer.InstanceID, consumer.Handler, consumer.Generation, consumer.ConfigRevision); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (p *PluginStore) restoreUninstallDrains(ctx context.Context, runID string) error {
	tx, err := p.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE plugin_event_subscriptions SET active=1 WHERE EXISTS(
SELECT 1 FROM plugin_uninstall_drains d JOIN plugin_installations i ON i.instance_id=d.instance_id
JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main'
WHERE (?='' OR d.run_id=?) AND d.instance_id=plugin_event_subscriptions.instance_id AND d.handler_key=plugin_event_subscriptions.handler_key
AND d.generation=plugin_event_subscriptions.generation AND d.config_revision=plugin_event_subscriptions.config_revision
AND i.plugin_id='MediaServerRefresh' AND i.installed=1 AND i.desired_enabled=1 AND i.generation=d.generation AND v.revision=d.config_revision
AND NOT EXISTS(SELECT 1 FROM plugin_uninstall_operations o WHERE o.run_id=d.run_id))`, runID, runID)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_uninstall_drains WHERE ?='' OR run_id=?`, runID, runID); err != nil {
		return err
	}
	return tx.Commit()
}

// Planning executes the actual ported selection algorithm without effects.
// The same algorithm runs again against the SQL adapter inside the transaction.
type uninstallPlanOperations struct {
	installed []string
	removed   map[string]bool
	reset     json.RawMessage
}

func (o *uninstallPlanOperations) Installed(context.Context) ([]string, error) {
	return append([]string{}, o.installed...), nil
}
func (o *uninstallPlanOperations) RemoveJobs(context.Context, string) error { return nil }
func (o *uninstallPlanOperations) RemovePlugin(_ context.Context, instance string) error {
	o.removed[instance] = true
	return nil
}
func (o *uninstallPlanOperations) PackageExists(context.Context, string) (bool, error) {
	return false, nil // Each native handler is embedded in the shared Go binary.
}
func (o *uninstallPlanOperations) RemovePackage(context.Context, string) error {
	return bad("PLUGIN_PACKAGE_SCOPE_DENIED", "内置插件没有独立可删除的程序文件")
}
func (o *uninstallPlanOperations) DeleteConfig(_ context.Context, instance string, force bool) (bool, error) {
	if force || !o.removed[instance] {
		return false, bad("PLUGIN_RUN_SCOPE_DENIED", "卸载仅允许原管理器的移除后非强制清理")
	}
	return false, nil // Actual installed manager's force=false guard, not a clear.
}
func (o *uninstallPlanOperations) DeleteData(ctx context.Context, instance string, force bool) (bool, error) {
	return o.DeleteConfig(ctx, instance, force)
}
func (o *uninstallPlanOperations) SaveInstalled(_ context.Context, remaining []string) error {
	expected := []string{}
	for _, instance := range o.installed {
		if !o.removed[instance] {
			expected = append(expected, instance)
		}
	}
	if !slices.Equal(expected, remaining) {
		return bad("PLUGIN_RUN_SCOPE_DENIED", "卸载后的插件集合不匹配")
	}
	return nil
}
func (o *uninstallPlanOperations) SaveOwnConfig(_ context.Context, raw json.RawMessage) error {
	o.reset = append(json.RawMessage(nil), raw...)
	return nil
}

type uninstallDatabaseOperations struct {
	*uninstallPlanOperations
	p       *PluginStore
	tx      *sql.Tx
	run     *PluginRun
	targets map[string]PluginInstallation
}

func (o *uninstallDatabaseOperations) RemoveJobs(ctx context.Context, instance string) error {
	_, err := o.tx.ExecContext(ctx, `UPDATE plugin_schedules SET active=0 WHERE instance_id=?`, instance)
	return err
}
func (o *uninstallDatabaseOperations) RemovePlugin(ctx context.Context, instance string) error {
	target, found := o.targets[instance]
	if !found || o.removed[instance] {
		return bad("PLUGIN_RUN_SCOPE_DENIED", "卸载目标不属于当前已安装快照")
	}
	rescueParent, isRescue, e := o.p.seedRescueLifecycleParentTx(ctx, o.tx, instance)
	if e != nil {
		return e
	}
	cfParent, isCF, err := o.p.cloudflareLifecycleParentTx(ctx, o.tx, instance)
	if err != nil {
		return err
	}
	autoParent, isAuto, err := o.p.autoBackupLifecycleParentTx(ctx, o.tx, instance)
	if err != nil {
		return err
	}
	iyuuParent, isIYUU, err := o.p.iyuuLifecycleParentTx(ctx, o.tx, instance)
	if err != nil {
		return err
	}
	transferParent, isTransfer, err := o.p.transferLifecycleParentTx(ctx, o.tx, instance)
	if err != nil {
		return err
	}
	changed, err := o.tx.ExecContext(ctx, `UPDATE plugin_installations SET installed=0,desired_enabled=0,generation=generation+1,updated_at=? WHERE instance_id=? AND plugin_id=? AND version=? AND generation=? AND installed=1`, now(), instance, target.PluginID, target.Version, target.Generation)
	if err != nil {
		return err
	}
	if n, _ := changed.RowsAffected(); n != 1 {
		return bad("PLUGIN_SNAPSHOT_STALE", "待卸载插件状态已变化")
	}
	for _, query := range []string{
		`UPDATE plugin_event_subscriptions SET active=0 WHERE instance_id=?`,
		`UPDATE plugin_event_batches SET state='canceled' WHERE instance_id=? AND state IN ('collecting','queued')`,
	} {
		if _, err := o.tx.ExecContext(ctx, query, instance); err != nil {
			return err
		}
	}
	// The caller is the sole active worker, except the explicitly completed
	// nested refresh. Keep a self-uninstall invocation alive for its receipt.
	if _, err = o.tx.ExecContext(ctx, `UPDATE plugin_runs SET state='canceled',error_code='PLUGIN_UNINSTALLED',updated_at=? WHERE instance_id=? AND id<>? AND state IN ('pending','running','reconcile_pending')`, now(), instance, o.run.ID); err != nil {
		return err
	}
	if _, err = o.tx.ExecContext(ctx, `UPDATE plugin_schedule_occurrences SET state='canceled' WHERE instance_id=? AND (run_id IS NULL OR run_id<>?) AND state IN ('pending','queued','running','reconcile_pending')`, instance, o.run.ID); err != nil {
		return err
	}
	if err := o.p.removeOrganizationMemberTx(ctx, o.tx, instance); err != nil {
		return err
	}
	if isAuto {
		if err = o.p.autoBackupGenesisTx(ctx, o.tx, instance, "bulk_uninstall", autoParent); err != nil {
			return err
		}
	}
	if isIYUU {
		if err = o.p.iyuuGenesisTx(ctx, o.tx, instance, "bulk_uninstall", iyuuParent); err != nil {
			return err
		}
	}
	if isTransfer {
		if err = o.p.transferGenesisTx(ctx, o.tx, instance, "bulk_uninstall", transferParent); err != nil {
			return err
		}
	}
	if isCF {
		if err = o.p.cloudflareGenesisTx(ctx, o.tx, instance, "bulk_uninstall", cfParent); err != nil {
			return err
		}
	}
	if isRescue {
		if err = o.p.seedRescueGenesisTx(ctx, o.tx, instance, "bulk_uninstall", rescueParent); err != nil {
			return err
		}
	}
	return o.uninstallPlanOperations.RemovePlugin(ctx, instance)
}

func (o *uninstallDatabaseOperations) SaveOwnConfig(ctx context.Context, raw json.RawMessage) error {
	if !o.removed[o.run.InstanceID] {
		return o.p.replaceRunConfigTx(ctx, o.tx, o.run, raw)
	}
	// Source update_config recreates its own config after removing its class.
	// Public SaveConfig correctly rejects uninstalled instances; this private
	// CAS is allowed only within the already guarded self-uninstall transaction.
	sealed, err := o.p.seal("config", o.run.InstanceID, "main", raw)
	if err != nil {
		return err
	}
	previous := o.run.ConfigRevision
	changed, err := o.tx.ExecContext(ctx, `UPDATE plugin_values SET revision=revision+1,sealed=?,updated_at=? WHERE instance_id=? AND kind='config' AND item_key='main' AND revision=?`, sealed, now(), o.run.InstanceID, previous)
	if err != nil {
		return err
	}
	if n, _ := changed.RowsAffected(); n != 1 {
		return bad("PLUGIN_REVISION_CONFLICT", "卸载插件配置已变化")
	}
	changed, err = o.tx.ExecContext(ctx, `UPDATE plugin_runs SET config_revision=?,updated_at=? WHERE id=? AND state='running' AND config_revision=? AND generation=?`, previous+1, now(), o.run.ID, previous, o.run.Generation)
	if err != nil {
		return err
	}
	if n, _ := changed.RowsAffected(); n != 1 {
		return bad("PLUGIN_RUN_TERMINAL", "卸载执行已停止")
	}
	o.run.ConfigRevision++
	return nil
}

// Different cleanup owners share one globally sorted reservation order. Never
// acquire an owner gate while lifecycle/process is held, including rollback.
type pluginUninstallCleanupPrepared struct {
	from              cleanuphost.Binding
	pluginID, version string
	remove            *pluginRemoveLinkPrepared
	seed              *pluginCleanInvalidSeedPrepared
	auto              *pluginAutoBackupPrepared
	iyuu              *pluginIYUUPrepared
	transfer          *pluginTransferPrepared
	cloudflare        *pluginCloudflarePrepared
	rescue            *pluginSeedRescuePrepared
}

func (p *pluginUninstallCleanupPrepared) release() {
	if p.cloudflare != nil {
		p.cloudflare.o.unlock()
		return
	}
	if p.rescue != nil {
		p.rescue.o.unlock()
		return
	}
	if p.auto != nil {
		p.auto.o.unlock()
		return
	}
	if p.remove != nil {
		p.remove.release()
	} else if p.seed != nil {
		p.seed.release()
	} else if p.transfer != nil {
		p.transfer.release()
	} else if p.iyuu != nil {
		p.iyuu.release()
	}
}
func (p *pluginUninstallCleanupPrepared) drain(ctx context.Context) error {
	if p.cloudflare != nil {
		return nil
	}
	if p.rescue != nil {
		return nil
	}
	if p.auto != nil {
		return nil
	}
	if p.remove != nil {
		return p.remove.drain(ctx)
	}
	if p.seed != nil {
		return p.seed.drain(ctx)
	}
	if p.transfer != nil {
		return p.transfer.drain(ctx)
	}
	if p.iyuu != nil {
		return p.iyuu.drain(ctx)
	}
	return bad("PLUGIN_SNAPSHOT_STALE", "清理宿主预留无效")
}
func (p *pluginUninstallCleanupPrepared) publishTx(ctx context.Context, tx *sql.Tx, to cleanuphost.Binding, runID string) error {
	if p.cloudflare != nil {
		return p.cloudflare.publishTx(ctx, tx)
	}
	if p.rescue != nil {
		return p.rescue.publishTx(ctx, tx)
	}
	if p.auto != nil {
		return p.auto.publishTx(ctx, tx)
	}
	if p.remove != nil {
		return p.remove.publishTx(ctx, tx, to, runID)
	}
	if p.seed != nil {
		return p.seed.publishTx(ctx, tx, seedhost.Binding{InstanceID: to.InstanceID, Generation: to.Generation, Revision: to.Revision}, runID)
	}
	if p.transfer != nil {
		return p.transfer.publishTx(ctx, tx, to, runID)
	}
	if p.iyuu != nil {
		return p.iyuu.publishTx(ctx, tx, to, runID)
	}
	return bad("PLUGIN_SNAPSHOT_STALE", "清理宿主预留无效")
}
func resolveUninstallCleanupPrepared(p *pluginUninstallCleanupPrepared, committed bool) error {
	if p.cloudflare != nil {
		p.cloudflare.resolve(committed)
		return nil
	}
	if p.rescue != nil {
		p.rescue.resolve(committed)
		return nil
	}
	if p.auto != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if p.remove != nil {
		return p.remove.resolve(ctx, committed)
	}
	if p.seed != nil {
		return p.seed.resolve(ctx, committed)
	}
	if p.transfer != nil {
		return p.transfer.resolve(ctx, committed)
	}
	if p.iyuu != nil {
		return p.iyuu.resolve(ctx, committed)
	}
	return bad("PLUGIN_SNAPSHOT_STALE", "清理宿主预留无效")
}
func (a *App) prepareUninstallCleanupTargets(ctx context.Context, selected []PluginInstallation) ([]*pluginUninstallCleanupPrepared, error) {
	selected = append([]PluginInstallation(nil), selected...)
	sort.Slice(selected, func(i, j int) bool { return selected[i].InstanceID < selected[j].InstanceID })
	out := []*pluginUninstallCleanupPrepared{}
	fail := func(cause error) ([]*pluginUninstallCleanupPrepared, error) {
		for n := len(out) - 1; n >= 0; n-- {
			cause = errors.Join(cause, resolveUninstallCleanupPrepared(out[n], false))
			out[n].release()
		}
		return nil, cause
	}
	for n, in := range selected {
		if n > 0 && selected[n-1].InstanceID == in.InstanceID {
			return fail(bad("PLUGIN_SNAPSHOT_STALE", "清理宿主目标重复"))
		}
		p := &pluginUninstallCleanupPrepared{pluginID: in.PluginID, version: in.Version}
		var err error
		switch in.PluginID {
		case "CloudflareSpeedTest":
			p.cloudflare, err = a.prepareCloudflareTransition(ctx, in)
			if err == nil {
				p.from = cleanuphost.Binding{InstanceID: in.InstanceID, Generation: in.Generation, Revision: p.cloudflare.fromRevision}
			}
		case "SeedRescue":
			p.rescue, err = a.prepareSeedRescueTransition(ctx, in)
			if err == nil {
				p.from = cleanuphost.Binding{InstanceID: in.InstanceID, Generation: in.Generation, Revision: p.rescue.fromRevision}
			}
		case "AutoBackup":
			p.auto, err = a.prepareAutoBackupTransition(ctx, in)
			if err == nil {
				p.from = cleanuphost.Binding{InstanceID: in.InstanceID, Generation: in.Generation, Revision: p.auto.fromRevision}
			}
		case "TorrentTransfer", "TorrentTransfermusic":
			p.transfer, err = a.prepareTransferTransition(ctx, in, "uninstall")
			if err == nil {
				p.from = p.transfer.from
			}
		case "IYUUAutoSeed":
			p.iyuu, err = a.prepareIYUUTransition(ctx, in, "uninstall")
			if err == nil {
				p.from = p.iyuu.from
			}
		case "RemoveLink":
			p.remove, err = a.prepareRemoveLinkTransition(ctx, in, "uninstall")
			if err == nil {
				p.from = p.remove.from
			}
		case "CleanInvalidSeed":
			p.seed, err = a.prepareCleanInvalidSeedTransition(ctx, in, "uninstall")
			if err == nil {
				p.from = cleanuphost.Binding{InstanceID: p.seed.from.InstanceID, Generation: p.seed.from.Generation, Revision: p.seed.from.Revision}
			}
		default:
			return fail(bad("PLUGIN_SNAPSHOT_STALE", "未识别的清理宿主目标"))
		}
		if err != nil {
			return fail(err)
		}
		out = append(out, p)
	}
	return out, nil
}
func (a *App) applyPluginUninstall(ctx context.Context, run *PluginRun) (json.RawMessage, error) {
	selected, err := a.removeLinkUninstallTargets(ctx, run)
	if err != nil {
		return nil, err
	}
	prepared, err := a.prepareUninstallCleanupTargets(ctx, selected)
	if err != nil {
		return nil, err
	}
	defer func() {
		for i := len(prepared) - 1; i >= 0; i-- {
			prepared[i].release()
		}
	}()
	for _, p := range prepared {
		if err = p.drain(ctx); err != nil {
			for _, paused := range prepared {
				_ = resolveUninstallCleanupPrepared(paused, false)
			}
			return nil, err
		}
	}
	result, operationErr := a.applyPluginUninstallSQL(ctx, run, prepared)
	// The SQL helper has released lifecycle and pluginMu before an owner wait.
	var resolveErr error
	for _, p := range prepared {
		if err := resolveUninstallCleanupPrepared(p, operationErr == nil); err != nil && resolveErr == nil {
			resolveErr = err
		}
	}
	if operationErr != nil {
		return nil, operationErr
	}
	if resolveErr != nil {
		return nil, resolveErr
	}
	return result, nil
}

func (a *App) removeLinkUninstallTargets(ctx context.Context, run *PluginRun) ([]PluginInstallation, error) {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	actual, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || actual.InstanceID != run.InstanceID || actual.Version != run.Version || actual.Generation != run.Generation || actual.ConfigRevision != run.ConfigRevision || actual.Action != run.Action || actual.State != "running" {
		return nil, bad("PLUGIN_RUN_SCOPE_DENIED", "卸载执行身份已变化")
	}
	// Existing committed self/target receipts have no work left to pause.
	if _, err = a.pluginStore.uninstallReceipt(ctx, actual); err == nil {
		return nil, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	config, err := a.pluginStore.Config(ctx, run.InstanceID)
	if err != nil {
		return nil, err
	}
	inventory, err := a.pluginStore.uninstallInventory(ctx)
	if err != nil {
		return nil, err
	}
	plan := &uninstallPlanOperations{installed: []string{}, removed: map[string]bool{}}
	for _, in := range inventory {
		plan.installed = append(plan.installed, in.InstanceID)
	}
	if _, err = uninstall.Run(ctx, config.Value, plan); err != nil {
		return nil, bad("INVALID_PLUGIN_CONFIG", "卸载插件选择不是原版支持的格式")
	}
	selected := []PluginInstallation{}
	for _, in := range inventory {
		if plan.removed[in.InstanceID] && (in.PluginID == "RemoveLink" || in.PluginID == "CleanInvalidSeed" || in.PluginID == "AutoBackup" || in.PluginID == "IYUUAutoSeed" || in.PluginID == "CloudflareSpeedTest" || in.PluginID == "SeedRescue" || isTorrentTransfer(in.PluginID)) {
			selected = append(selected, in)
		}
	}
	return selected, nil
}

func (a *App) applyPluginUninstallSQL(ctx context.Context, run *PluginRun, prepared []*pluginUninstallCleanupPrepared) (json.RawMessage, error) {
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	a.pluginMu.Lock()
	actual, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil || actual.InstanceID != run.InstanceID || actual.Version != run.Version || actual.Generation != run.Generation || actual.ConfigRevision != run.ConfigRevision || actual.Action != run.Action || actual.State != "running" || (actual.Action != "init" && actual.Action != "reload") {
		a.pluginMu.Unlock()
		return nil, bad("PLUGIN_RUN_SCOPE_DENIED", "卸载请求不属于当前运行中的执行")
	}
	// A committed self-uninstall is still authorized to return its receipt,
	// even though its installation is now inactive and generation has advanced.
	if receipt, err := a.pluginStore.uninstallReceipt(ctx, actual); err == nil {
		a.pluginMu.Unlock()
		return receipt, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		a.pluginMu.Unlock()
		return nil, err
	}
	installation, err := a.pluginStore.Installation(ctx, actual.InstanceID)
	if err != nil || installation.PluginID != "PluginUnInstall" || installation.Version != "2.2" || !installation.Installed || !installation.DesiredEnabled || installation.Generation != actual.Generation {
		a.pluginMu.Unlock()
		return nil, bad("PLUGIN_SNAPSHOT_STALE", "卸载插件身份或启用状态已变化")
	}
	config, err := a.pluginStore.Config(ctx, actual.InstanceID)
	if err != nil || config.Revision != actual.ConfigRevision {
		a.pluginMu.Unlock()
		return nil, bad("PLUGIN_REVISION_CONFLICT", "卸载配置已变化，旧执行已停止")
	}
	inventory, err := a.pluginStore.uninstallInventory(ctx)
	if err != nil {
		a.pluginMu.Unlock()
		return nil, err
	}
	plan := &uninstallPlanOperations{installed: []string{}, removed: map[string]bool{}}
	targets := map[string]PluginInstallation{}
	for _, item := range inventory {
		plan.installed = append(plan.installed, item.InstanceID)
		targets[item.InstanceID] = item
	}
	planned, err := uninstall.Run(ctx, config.Value, plan)
	if err != nil {
		a.pluginMu.Unlock()
		return nil, bad("INVALID_PLUGIN_CONFIG", "卸载插件选择不是原版支持的格式")
	}
	if !planned.Ran {
		a.pluginMu.Unlock()
		return json.RawMessage(`null`), nil
	}
	for _, instance := range planned.Removed {
		if target := targets[instance]; target.PluginID == "RemoveLink" || target.PluginID == "CleanInvalidSeed" || target.PluginID == "AutoBackup" || target.PluginID == "IYUUAutoSeed" || target.PluginID == "CloudflareSpeedTest" || target.PluginID == "SeedRescue" || isTorrentTransfer(target.PluginID) {
			matched := false
			for _, p := range prepared {
				if p.from.InstanceID == instance && p.from.Generation == target.Generation && p.pluginID == target.PluginID && p.version == target.Version {
					matched = true
					break
				}
			}
			if !matched {
				a.pluginMu.Unlock()
				return nil, bad("PLUGIN_SNAPSHOT_STALE", "待卸载的清理插件发生变化，请重试")
			}
		}
	}
	// uninstall_result uses the store's explicit 1 MiB value limit. Check the
	// complete receipt before a drain or destructive SQL, and again before its
	// final seal, so no committed receipt can exceed its own read-back limit.
	if _, err = pluginJSON(planned, true); err != nil {
		a.pluginMu.Unlock()
		return nil, err
	}
	consumers, err := a.pluginStore.activeEventSubscriptions(ctx)
	if err == nil {
		err = a.pluginStore.recordUninstallDrains(ctx, actual, consumers, plan.removed, targets)
	}
	a.pluginMu.Unlock()
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		// A drain can quiesce consumers before SQL starts. If final commit fails,
		// restore only the same still-installed generation, never a newer config.
		restore, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		a.pluginMu.Lock()
		defer a.pluginMu.Unlock()
		if err := a.pluginStore.restoreUninstallDrains(restore, actual.ID); err != nil {
			a.pluginExecutionLog(installation, actual, "ERROR", "uninstall_drain_restore_pending", "卸载收尾状态尚未恢复，已保留记录供重启核对", "PLUGIN_UNINSTALL_DRAIN_RESTORE_PENDING")
		}
	}()
	for _, instance := range planned.Removed {
		target := targets[instance]
		if target.PluginID == "MediaServerRefresh" && target.Installed && target.DesiredEnabled {
			if err := a.drainMediaRefresh(ctx, target); err != nil {
				return nil, err
			}
		}
	}
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = a.pluginStore.guardUninstallRunTx(ctx, tx, actual); err != nil {
		return nil, err
	}
	for _, p := range prepared {
		var generation, revision int64
		var installed bool
		if err := tx.QueryRowContext(ctx, `SELECT i.generation,i.installed,COALESCE(v.revision,0) FROM plugin_installations i LEFT JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=? AND i.plugin_id=? AND i.version=?`, p.from.InstanceID, p.pluginID, p.version).Scan(&generation, &installed, &revision); err != nil {
			return nil, err
		}
		if !installed || generation != p.from.Generation || revision != p.from.Revision {
			return nil, bad("PLUGIN_SNAPSHOT_STALE", "清理插件配置已变化，卸载未提交")
		}
	}
	updated := actual
	ops := &uninstallDatabaseOperations{uninstallPlanOperations: &uninstallPlanOperations{installed: plan.installed, removed: map[string]bool{}}, p: a.pluginStore, tx: tx, run: &updated, targets: targets}
	result, err := uninstall.Run(ctx, config.Value, ops)
	if err != nil {
		return nil, err
	}
	for _, p := range prepared {
		if !plan.removed[p.from.InstanceID] {
			return nil, bad("PLUGIN_SNAPSHOT_STALE", "卸载目标已变化")
		}
		to := cleanuphost.Binding{InstanceID: p.from.InstanceID, Generation: p.from.Generation + 1, Revision: 0}
		if err := p.publishTx(ctx, tx, to, ""); err != nil {
			return nil, err
		}
	}
	raw, err := pluginJSON(result, true)
	if err != nil {
		return nil, err
	}
	sealed, err := a.pluginStore.seal("uninstall_result", actual.InstanceID, actual.ID, raw)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO plugin_uninstall_operations VALUES(?,?,?,?,?,?,?,?)`, actual.ID, actual.InstanceID, actual.Version, actual.Generation, actual.ConfigRevision, updated.ConfigRevision, actual.Action, sealed); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_uninstall_drains WHERE run_id=?`, actual.ID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	*run = updated
	for _, instance := range result.Removed {
		delete(a.subscribeGroupCaches, instance)
		a.pluginServerStatus.invalidate(instance)
	}
	return raw, nil
}

// All uninstallation/config writes share the receipt transaction. A missing
// receipt proves no uninstall committed; a present receipt survives removal of
// the caller itself and must not replay a later reinstallation of a target.
func (p *PluginStore) recoverUninstallRuns(ctx context.Context) error {
	if err := p.restoreUninstallDrains(ctx, ""); err != nil {
		return err
	}
	rows, err := p.store.DB.QueryContext(ctx, `SELECT r.id,r.instance_id,r.version,r.generation,r.config_revision,r.action FROM plugin_runs r JOIN plugin_installations i ON i.instance_id=r.instance_id WHERE i.plugin_id='PluginUnInstall' AND r.version='2.2' AND r.action IN ('init','reload') AND (r.state='reconcile_pending' OR (r.state='failed' AND r.error_code IN ('PLUGIN_RESULT_SAVE_FAILED','PLUGIN_RESULT_TOO_LARGE','INVALID_PLUGIN_RESULT')))`)
	if err != nil {
		return err
	}
	runs := []PluginRun{}
	for rows.Next() {
		var run PluginRun
		if err := rows.Scan(&run.ID, &run.InstanceID, &run.Version, &run.Generation, &run.ConfigRevision, &run.Action); err != nil {
			rows.Close()
			return err
		}
		runs = append(runs, run)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, run := range runs {
		result, err := p.uninstallReceipt(ctx, run)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = p.store.DB.ExecContext(ctx, `UPDATE plugin_runs SET state=CASE WHEN EXISTS(SELECT 1 FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=plugin_runs.instance_id AND i.plugin_id='PluginUnInstall' AND i.version=plugin_runs.version AND i.generation=plugin_runs.generation AND i.installed=1 AND i.desired_enabled=1 AND v.revision=plugin_runs.config_revision) THEN 'pending' ELSE 'canceled' END,updated_at=? WHERE id=? AND state='reconcile_pending'`, now(), run.ID)
			if err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		sealed, err := p.seal("run_result", run.InstanceID, run.ID, result)
		if err != nil {
			return err
		}
		if _, err = p.store.DB.ExecContext(ctx, `UPDATE plugin_runs SET state='succeeded',result=?,error_code='',updated_at=? WHERE id=? AND (state='reconcile_pending' OR (state='failed' AND error_code IN ('PLUGIN_RESULT_SAVE_FAILED','PLUGIN_RESULT_TOO_LARGE','INVALID_PLUGIN_RESULT')))`, sealed, now(), run.ID); err != nil {
			return err
		}
	}
	return nil
}

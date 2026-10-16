package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sort"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

type siteControlPeer struct {
	ID, PluginID, Version string
	Generation, Revision  int64
	Desired               bool
	Config                map[string]json.RawMessage
}

func (p *PluginStore) siteControlPeersTx(ctx context.Context, tx *sql.Tx) ([]string, map[string]siteControlPeer, error) {
	rows, err := tx.QueryContext(ctx, `SELECT instance_id,plugin_id,version,generation,desired_enabled FROM plugin_installations WHERE installed=1 ORDER BY created_at,instance_id`)
	if err != nil {
		return nil, nil, err
	}
	installed := []string{}
	pending := []siteControlPeer{}
	for rows.Next() {
		var peer siteControlPeer
		if err := rows.Scan(&peer.ID, &peer.PluginID, &peer.Version, &peer.Generation, &peer.Desired); err != nil {
			rows.Close()
			return nil, nil, err
		}
		installed = append(installed, peer.PluginID)
		if peer.PluginID == "CustomSites" || slices.ContainsFunc(builtin.SiteControlSlots, func(slot builtin.SiteControlSlot) bool { return slot.PluginID == peer.PluginID }) {
			pending = append(pending, peer)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	peers := map[string]siteControlPeer{}
	for _, peer := range pending {
		if _, exists := peers[peer.PluginID]; exists {
			return nil, nil, bad("PLUGIN_REFERENCE_AMBIGUOUS", "站点开关关联插件存在多个实例，需先完成引用映射")
		}
		var sealed []byte
		err := tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, peer.ID).Scan(&peer.Revision, &sealed)
		peer.Config = map[string]json.RawMessage{}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, err
		}
		if err == nil {
			raw, err := p.unseal("config", peer.ID, "main", sealed)
			if err != nil {
				return nil, nil, err
			}
			if json.Unmarshal(raw, &peer.Config) != nil || peer.Config == nil {
				return nil, nil, bad("INVALID_PLUGIN_CONFIG", "关联插件配置无法读取")
			}
		}
		peers[peer.PluginID] = peer
	}
	return installed, peers, nil
}

func siteControlReferences(config map[string]json.RawMessage, key string) ([]string, error) {
	raw, ok := config[key]
	if !ok {
		return []string{}, nil
	}
	values := []string{}
	if json.Unmarshal(raw, &values) != nil {
		return nil, bad("PLUGIN_SITE_REFERENCE_UNMAPPED", "原站点编号尚未映射为新项目站点引用")
	}
	if values == nil {
		values = []string{}
	}
	return values, nil
}

func siteControlCustomOptions(peers map[string]siteControlPeer) ([]builtin.SiteControlOption, error) {
	options := []builtin.SiteControlOption{}
	peer, ok := peers["CustomSites"]
	if !ok {
		return options, nil
	}
	var enabled bool
	if json.Unmarshal(peer.Config["enabled"], &enabled) != nil || !enabled {
		return options, nil
	}
	var rows []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if json.Unmarshal(peer.Config["sites"], &rows) != nil {
		return nil, bad("PLUGIN_SITE_REFERENCE_UNMAPPED", "自定义站点引用尚未映射")
	}
	for _, row := range rows {
		if row.ID != "" {
			options = append(options, builtin.SiteControlOption{Title: row.Name + " [自定义]", Value: row.ID})
		}
	}
	return options, nil
}

func (a *App) siteControlSnapshotTx(ctx context.Context, tx *sql.Tx, instanceID string, c Settings) (builtin.SiteControlSnapshot, int64, error) {
	out := builtin.SiteControlSnapshot{Sites: []builtin.SiteControlOption{}, CustomSites: []builtin.SiteControlOption{}, Installed: []string{}, Config: builtin.SiteControls{}}
	var revision int64
	var sealed []byte
	err := tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, instanceID).Scan(&revision, &sealed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, 0, err
	}
	if err == nil {
		raw, err := a.pluginStore.unseal("config", instanceID, "main", sealed)
		if err != nil {
			return out, 0, err
		}
		if json.Unmarshal(raw, &out.Config) != nil || out.Config == nil {
			return out, 0, bad("INVALID_PLUGIN_CONFIG", "聚合站点开关配置无效")
		}
	}
	sites := slices.Clone(c.Sites)
	sort.SliceStable(sites, func(i, j int) bool { return sites[i].Priority < sites[j].Priority })
	enabled := []string{}
	for _, site := range sites {
		out.Sites = append(out.Sites, builtin.SiteControlOption{Title: site.Name, Value: site.ID})
		if site.Enabled {
			enabled = append(enabled, site.ID)
		}
	}
	out.Config.SetSites("enable_sites", enabled)
	out.Config.SetSites("search_sites", c.IndexerSites)
	out.Config.SetSites("rss_sites", c.RSSSites)
	installed, peers, err := a.pluginStore.siteControlPeersTx(ctx, tx)
	if err != nil {
		return out, 0, err
	}
	out.Installed = installed
	out.CustomSites, err = siteControlCustomOptions(peers)
	if err != nil {
		return out, 0, err
	}
	for _, slot := range builtin.SiteControlSlots {
		if peer, ok := peers[slot.PluginID]; ok {
			values, err := siteControlReferences(peer.Config, slot.PeerField)
			if err != nil {
				return out, 0, err
			}
			out.Config.SetSites(slot.Key, values)
		}
	}
	return out, revision, nil
}

func (a *App) registerSiteControlsTx(ctx context.Context, tx *sql.Tx, run PluginRun, config builtin.SiteControls) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs r JOIN plugin_installations i ON i.instance_id=r.instance_id JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE r.id=? AND r.instance_id=? AND r.state='running' AND i.plugin_id='MergeSiteSwitch' AND i.installed=1 AND i.desired_enabled=1 AND i.generation=? AND r.generation=i.generation AND v.revision=? AND r.config_revision=v.revision`, run.ID, run.InstanceID, run.Generation, run.ConfigRevision).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return bad("PLUGIN_SNAPSHOT_STALE", "聚合站点开关配置已变化")
	}
	raw, _ := json.Marshal(config)
	sealed, err := a.pluginStore.seal("site_controls_watch", run.InstanceID, "main", raw)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_site_control_watchers VALUES(?,?,?,?,?) ON CONFLICT(instance_id) DO UPDATE SET generation=excluded.generation,config_revision=excluded.config_revision,config=excluded.config,updated_at=excluded.updated_at`, run.InstanceID, run.Generation, run.ConfigRevision, sealed, now())
	return err
}

func (a *App) writeSiteControlPeersTx(ctx context.Context, tx *sql.Tx, peers map[string]siteControlPeer, changes map[string]map[string]json.RawMessage) error {
	order := []string{}
	for _, slot := range builtin.SiteControlSlots {
		if _, exists := changes[slot.PluginID]; exists && !slices.Contains(order, slot.PluginID) {
			order = append(order, slot.PluginID)
		}
	}
	for _, pluginID := range order {
		peer := peers[pluginID]
		manifest, _, available := builtin.Lookup(pluginID)
		available = available && manifest.Version == peer.Version
		if a.pluginSitePeerAvailable != nil {
			available = a.pluginSitePeerAvailable(pluginID, peer.Version)
		}
		if !available || !peer.Desired {
			return bad("PLUGIN_DEPENDENCY_UNAVAILABLE", "关联插件尚未完成Go实现或仍在迁移暂存，本次开关更新未应用")
		}
		for key, value := range changes[pluginID] {
			peer.Config[key] = value
		}
		raw, err := pluginJSON(peer.Config, true)
		if err != nil {
			return err
		}
		if _, err := a.pluginStore.saveValueTx(ctx, tx, peer.ID, "config", "main", peer.Revision, raw); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE plugin_schedules SET active=0 WHERE instance_id=?`, peer.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE plugin_event_subscriptions SET active=0 WHERE instance_id=?`, peer.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE plugin_event_batches SET state='canceled' WHERE instance_id=? AND run_id IS NULL`, peer.ID); err != nil {
			return err
		}
		if _, err := a.pluginStore.enqueueRunTx(ctx, tx, peer.ID, "init", nil); err != nil {
			return err
		}
	}
	return nil
}

func controlPeerChanges(config builtin.SiteControls, peers map[string]siteControlPeer) map[string]map[string]json.RawMessage {
	out := map[string]map[string]json.RawMessage{}
	for _, slot := range builtin.SiteControlSlots {
		if _, exists := peers[slot.PluginID]; exists {
			if out[slot.PluginID] == nil {
				out[slot.PluginID] = map[string]json.RawMessage{}
			}
			values := config.Sites(slot.Key)
			out[slot.PluginID][slot.PeerField], _ = json.Marshal(values)
		}
	}
	return out
}

func (a *App) pluginSiteControlsHost(ctx context.Context, installation PluginInstallation, run *PluginRun, method string, raw json.RawMessage) (json.RawMessage, error) {
	if installation.PluginID != "MergeSiteSwitch" || string(raw) != "{}" {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "站点开关调用超出授权范围")
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if err := a.validatePluginHostRun(ctx, installation, run, method); err != nil {
		return nil, err
	}
	if method == "sites.controls.read" {
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		snapshot, revision, err := a.siteControlSnapshotTx(ctx, tx, installation.InstanceID, a.Settings())
		if err != nil {
			return nil, err
		}
		snapshot.Revision = revision
		return json.Marshal(snapshot)
	}
	if method != "sites.controls.configure" || run == nil || (run.Action != "init" && run.Action != "reload") {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "站点开关只能由配置初始化调用")
	}
	var invocation plugins.Invocation
	var activation struct {
		ConfigSaved bool `json:"config_saved"`
	}
	if json.Unmarshal(run.Input, &invocation) != nil {
		return nil, bad("INVALID_PLUGIN_RUN", "站点开关执行快照无效")
	}
	if len(invocation.Input) > 0 && json.Unmarshal(invocation.Input, &activation) != nil {
		return nil, bad("INVALID_PLUGIN_RUN", "站点开关初始化来源无效")
	}
	if activation.ConfigSaved && run.Action != "init" {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "非保存请求不能应用站点开关")
	}
	if result, err := a.pluginStore.siteControlReceipt(ctx, *run); err == nil {
		return result, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	stored, err := a.pluginStore.Config(ctx, run.InstanceID)
	if err != nil {
		return nil, err
	}
	var config builtin.SiteControls
	if json.Unmarshal(stored.Value, &config) != nil || config.Validate() != nil {
		return nil, bad("INVALID_PLUGIN_CONFIG", "聚合站点开关配置无效")
	}
	if !activation.ConfigSaved {
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		if err := a.registerSiteControlsTx(ctx, tx, *run, config); err != nil {
			return nil, err
		}
		return json.RawMessage(`{"configured":true,"applied":false}`), tx.Commit()
	}
	updated := *run
	result := json.RawMessage(`{"configured":true,"applied":true}`)
	err = a.saveSettingsWithEffect(ctx, a.Settings(), func(ctx context.Context, tx *sql.Tx, next *Settings) error {
		_, peers, err := a.pluginStore.siteControlPeersTx(ctx, tx)
		if err != nil {
			return err
		}
		custom, err := siteControlCustomOptions(peers)
		if err != nil {
			return err
		}
		customIDs := []string{}
		for _, option := range custom {
			customIDs = append(customIDs, option.Value)
		}
		normalized := builtin.NormalizeSiteControls(config, customIDs)
		enabled := normalized.Sites("enable_sites")
		for i := range next.Sites {
			next.Sites[i].Enabled = slices.Contains(enabled, next.Sites[i].ID)
		}
		next.IndexerSites = normalized.Sites("search_sites")
		next.RSSSites = normalized.Sites("rss_sites")
		if err := a.writeSiteControlPeersTx(ctx, tx, peers, controlPeerChanges(normalized, peers)); err != nil {
			return err
		}
		if err := a.pluginStore.patchRunConfigTx(ctx, tx, &updated, map[string]json.RawMessage(normalized)); err != nil {
			return err
		}
		if err := a.registerSiteControlsTx(ctx, tx, updated, normalized); err != nil {
			return err
		}
		sealed, err := a.pluginStore.seal("site_controls_result", updated.InstanceID, updated.ID, result)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO plugin_site_control_operations VALUES(?,?,?,?,?)`, updated.ID, updated.InstanceID, updated.Generation, updated.ConfigRevision, sealed)
		return err
	})
	if err != nil {
		return nil, err
	}
	*run = updated
	return result, nil
}

func (p *PluginStore) siteControlReceipt(ctx context.Context, run PluginRun) (json.RawMessage, error) {
	var sealed []byte
	if err := p.store.DB.QueryRowContext(ctx, `SELECT result FROM plugin_site_control_operations WHERE run_id=? AND instance_id=? AND generation=? AND config_revision=?`, run.ID, run.InstanceID, run.Generation, run.ConfigRevision).Scan(&sealed); err != nil {
		return nil, err
	}
	return p.unseal("site_controls_result", run.InstanceID, run.ID, sealed)
}

// Core site commits and follow updates share a transaction. Watchers contain
// the last initialized flags, so a queued config never becomes active early.
func (a *App) followSiteControlsTx(ctx context.Context, tx *sql.Tx, previous Settings, next *Settings) error {
	type change struct {
		id      string
		enabled bool
	}
	changes := []change{}
	old := map[string]SiteConfig{}
	current := map[string]bool{}
	for _, site := range previous.Sites {
		old[site.ID] = site
	}
	for _, site := range next.Sites {
		current[site.ID] = true
		if before, ok := old[site.ID]; !ok || !reflect.DeepEqual(before, site) {
			changes = append(changes, change{site.ID, site.Enabled})
		}
	}
	for _, site := range previous.Sites {
		if !current[site.ID] {
			changes = append(changes, change{site.ID, false})
			next.IndexerSites, _ = builtin.ChangeSiteControl(next.IndexerSites, site.ID, false)
			next.RSSSites, _ = builtin.ChangeSiteControl(next.RSSSites, site.ID, false)
		}
	}
	if len(changes) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT w.instance_id,w.config FROM plugin_site_control_watchers w JOIN plugin_installations i ON i.instance_id=w.instance_id WHERE i.plugin_id='MergeSiteSwitch' AND i.installed=1 AND i.desired_enabled=1 AND i.generation=w.generation AND i.version=? ORDER BY i.created_at,i.instance_id`, builtin.MergeSiteSwitchManifest().Version)
	if err != nil {
		return err
	}
	type watcher struct {
		id     string
		sealed []byte
	}
	watchers := []watcher{}
	for rows.Next() {
		var w watcher
		if err := rows.Scan(&w.id, &w.sealed); err != nil {
			rows.Close()
			return err
		}
		watchers = append(watchers, w)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, watch := range watchers {
		raw, err := a.pluginStore.unseal("site_controls_watch", watch.id, "main", watch.sealed)
		if err != nil {
			return err
		}
		var config builtin.SiteControls
		if json.Unmarshal(raw, &config) != nil || config.Validate() != nil {
			return bad("INVALID_PLUGIN_CONFIG", "站点跟随配置无法读取")
		}
		installed, peers, err := a.pluginStore.siteControlPeersTx(ctx, tx)
		if err != nil {
			return err
		}
		if !config.AnyFollow(installed) {
			continue
		}
		updates := map[string]map[string]json.RawMessage{}
		for _, slot := range builtin.SiteControlSlots {
			if !config.Follows(slot.Follow) {
				continue
			}
			var values []string
			if slot.PluginID == "" {
				if slot.Key == "search_sites" {
					values = next.IndexerSites
				} else {
					values = next.RSSSites
				}
			} else {
				peer, ok := peers[slot.PluginID]
				if !ok {
					continue
				}
				values, err = siteControlReferences(peer.Config, slot.PeerField)
				if err != nil {
					return err
				}
			}
			changed := false
			for _, change := range changes {
				var effect bool
				values, effect = builtin.ChangeSiteControl(values, change.id, change.enabled)
				changed = changed || effect
			}
			if !changed {
				continue
			}
			if slot.PluginID == "" {
				if slot.Key == "search_sites" {
					next.IndexerSites = values
				} else {
					next.RSSSites = values
				}
			} else {
				if updates[slot.PluginID] == nil {
					updates[slot.PluginID] = map[string]json.RawMessage{}
				}
				updates[slot.PluginID][slot.PeerField], _ = json.Marshal(values)
			}
		}
		if err := a.writeSiteControlPeersTx(ctx, tx, peers, updates); err != nil {
			return err
		}
	}
	return nil
}

func subscriptionSiteControls(defaults, selected []string) []string {
	if len(selected) == 0 {
		return slices.Clone(defaults)
	}
	if len(defaults) == 0 {
		return slices.Clone(selected)
	}
	intersection := []string{}
	for _, id := range selected {
		if slices.Contains(defaults, id) {
			intersection = append(intersection, id)
		}
	}
	if len(intersection) > 0 {
		return intersection
	}
	return slices.Clone(defaults)
}

func (a *App) pruneDeletedSiteSubscriptionsTx(ctx context.Context, tx *sql.Tx, previous, next Settings) error {
	deleted := []string{}
	for _, site := range previous.Sites {
		if !slices.ContainsFunc(next.Sites, func(current SiteConfig) bool { return current.ID == site.ID }) {
			deleted = append(deleted, site.ID)
		}
	}
	if len(deleted) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT payload FROM records WHERE kind='subscription'`)
	if err != nil {
		return err
	}
	changed := []Subscription{}
	for rows.Next() {
		var raw []byte
		var sub Subscription
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		if json.Unmarshal(raw, &sub) != nil {
			rows.Close()
			return bad("INVALID_SUBSCRIPTION", "订阅引用无法读取，站点删除未提交")
		}
		effect := false
		for _, id := range deleted {
			var removed bool
			sub.SiteIDs, removed = builtin.ChangeSiteControl(sub.SiteIDs, id, false)
			effect = effect || removed
		}
		if effect {
			sub.Revision++
			changed = append(changed, sub)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, sub := range changed {
		if err := txPut(ctx, tx, "subscription", sub.ID, sub); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,lease_until=0,updated_at=? WHERE kind='subscription' AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused')`, now(), sub.ID); err != nil {
			return err
		}
	}
	return nil
}

func (p *PluginStore) recoverSiteControlRuns(ctx context.Context) error {
	rows, err := p.store.DB.QueryContext(ctx, `SELECT r.id,r.instance_id,r.generation,r.config_revision FROM plugin_runs r JOIN plugin_installations i ON i.instance_id=r.instance_id WHERE i.plugin_id='MergeSiteSwitch' AND r.state='reconcile_pending' AND r.action IN ('init','reload')`)
	if err != nil {
		return err
	}
	runs := []PluginRun{}
	for rows.Next() {
		var run PluginRun
		if err := rows.Scan(&run.ID, &run.InstanceID, &run.Generation, &run.ConfigRevision); err != nil {
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
		result, err := p.siteControlReceipt(ctx, run)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := p.store.DB.ExecContext(ctx, `UPDATE plugin_runs SET state='pending',updated_at=? WHERE id=? AND state='reconcile_pending'`, now(), run.ID); err != nil {
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
		if _, err := p.store.DB.ExecContext(ctx, `UPDATE plugin_runs SET state='succeeded',result=?,error_code='',updated_at=? WHERE id=? AND state='reconcile_pending'`, sealed, now(), run.ID); err != nil {
			return err
		}
	}
	return nil
}

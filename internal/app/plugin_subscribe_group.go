package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/subscribegroup"
	"light-media/internal/torrentfilter"
)

// These private host operations accept only a real persisted invocation,
// never worker-provided metadata, subscription identities or field patches.
func (a *App) subscribeGroupInvocation(ctx context.Context, run PluginRun, actions ...string) (PluginRun, subscribegroup.Configuration, error) {
	var config subscribegroup.Configuration
	installation, err := a.pluginStore.Installation(ctx, run.InstanceID)
	if err != nil {
		return run, config, err
	}
	if installation.PluginID != "SubscribeGroup" || installation.Version != "2.8.7" || !installation.Installed || !installation.DesiredEnabled || installation.Generation != run.Generation || installation.Version != run.Version {
		return run, config, bad("PLUGIN_SNAPSHOT_STALE", "订阅规则插件身份、版本或启用状态已变化")
	}
	actual, err := a.pluginStore.Run(ctx, run.ID)
	if err != nil {
		return run, config, err
	}
	validAction := false
	for _, action := range actions {
		validAction = validAction || actual.Action == action
	}
	if actual.InstanceID != run.InstanceID || actual.Version != run.Version || actual.Generation != run.Generation || actual.ConfigRevision != run.ConfigRevision || actual.Action != run.Action || actual.State != "running" || !validAction {
		return run, config, bad("PLUGIN_RUN_SCOPE_DENIED", "订阅规则操作不属于当前运行中的执行")
	}
	stored, err := a.pluginStore.Config(ctx, run.InstanceID)
	if err != nil {
		return run, config, err
	}
	if stored.Revision != run.ConfigRevision {
		return run, config, bad("PLUGIN_REVISION_CONFLICT", "订阅规则配置已变化，旧执行已停止")
	}
	if json.Unmarshal(stored.Value, &config) != nil {
		return run, config, bad("INVALID_PLUGIN_CONFIG", "订阅规则配置无法读取")
	}
	return actual, config, nil
}
func (a *App) subscribeGroupMachine(run PluginRun, config subscribegroup.Configuration, initializing bool) (*subscribegroup.Machine, error) {
	m := subscribegroup.New()
	if cache, ok := a.subscribeGroupCaches[run.InstanceID]; ok && cache.Generation == run.Generation {
		if !initializing && cache.Revision != run.ConfigRevision {
			return nil, bad("PLUGIN_RULES_NOT_INITIALIZED", "订阅规则新配置尚未完成初始化")
		}
		raw, _ := json.Marshal(cache.Rules)
		_ = json.Unmarshal(raw, &m.Rules)
		if !initializing {
			m.Config = config
			return m, nil
		}
	}
	if !initializing && (config.Clear || config.ClearHandle) {
		return nil, bad("PLUGIN_RULES_NOT_INITIALIZED", "订阅规则清理操作尚未完成初始化")
	}
	settings := a.Settings()
	// Source list_active does not order by priority. Keep the core's stored
	// site order; source row identity order is verified again during migration.
	sites := make([]subscribegroup.Site, 0, len(settings.Sites))
	for _, site := range settings.Sites {
		sites = append(sites, subscribegroup.Site{ID: site.ID, Name: site.Name, Active: site.Enabled})
	}
	m.Init(&config, sites)
	return m, nil
}
func (a *App) publishSubscribeGroupCache(run PluginRun, m *subscribegroup.Machine) {
	if a.subscribeGroupCaches == nil {
		a.subscribeGroupCaches = map[string]subscribeGroupCache{}
	}
	raw, _ := json.Marshal(m.Rules)
	var rules map[string]subscribegroup.Rule
	_ = json.Unmarshal(raw, &rules)
	a.subscribeGroupCaches[run.InstanceID] = subscribeGroupCache{run.Generation, run.ConfigRevision, rules}
}
func (p *PluginStore) subscribeGroupReceipt(ctx context.Context, run PluginRun) (json.RawMessage, error) {
	var sealed []byte
	if err := p.store.DB.QueryRowContext(ctx, `SELECT result FROM plugin_subscribe_group_operations WHERE run_id=? AND instance_id=? AND generation=? AND config_revision=?`, run.ID, run.InstanceID, run.Generation, run.ConfigRevision).Scan(&sealed); err != nil {
		return nil, err
	}
	return p.unseal("subscribe_group_result", run.InstanceID, run.ID, sealed)
}
func (p *PluginStore) recordSubscribeGroupReceiptTx(ctx context.Context, tx *sql.Tx, run PluginRun, result json.RawMessage) error {
	sealed, err := p.seal("subscribe_group_result", run.InstanceID, run.ID, result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_subscribe_group_operations VALUES(?,?,?,?,?)`, run.ID, run.InstanceID, run.Generation, run.ConfigRevision, sealed)
	return err
}

func (p *PluginStore) guardSubscribeGroupRunTx(ctx context.Context, tx *sql.Tx, run PluginRun) error {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM plugin_runs r JOIN plugin_installations i ON i.instance_id=r.instance_id JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE r.id=? AND r.instance_id=? AND r.version=? AND r.generation=? AND r.config_revision=? AND r.action=? AND r.state='running' AND i.plugin_id='SubscribeGroup' AND i.version='2.8.7' AND i.version=r.version AND i.generation=r.generation AND i.installed=1 AND i.desired_enabled=1 AND v.revision=r.config_revision`, run.ID, run.InstanceID, run.Version, run.Generation, run.ConfigRevision, run.Action).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return bad("PLUGIN_SNAPSHOT_STALE", "订阅规则事务的执行身份已变化")
	}
	return nil
}

// All effects are local SQL and share their receipt transaction. After a
// process loss, a committed receipt is final; absence proves no partial write
// from this adapter committed, so the exact invocation can safely be retried.
func (p *PluginStore) recoverSubscribeGroupRuns(ctx context.Context) error {
	rows, err := p.store.DB.QueryContext(ctx, `SELECT r.id,r.instance_id,r.version,r.generation,r.config_revision FROM plugin_runs r JOIN plugin_installations i ON i.instance_id=r.instance_id WHERE i.plugin_id='SubscribeGroup' AND r.version='2.8.7' AND r.action IN ('init','reload','event') AND r.state='reconcile_pending'`)
	if err != nil {
		return err
	}
	runs := []PluginRun{}
	for rows.Next() {
		var run PluginRun
		if err := rows.Scan(&run.ID, &run.InstanceID, &run.Version, &run.Generation, &run.ConfigRevision); err != nil {
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
		result, err := p.subscribeGroupReceipt(ctx, run)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = p.store.DB.ExecContext(ctx, `UPDATE plugin_runs SET state=CASE WHEN EXISTS(SELECT 1 FROM plugin_installations i JOIN plugin_values v ON v.instance_id=i.instance_id AND v.kind='config' AND v.item_key='main' WHERE i.instance_id=plugin_runs.instance_id AND i.plugin_id='SubscribeGroup' AND i.version=plugin_runs.version AND i.generation=plugin_runs.generation AND i.installed=1 AND i.desired_enabled=1 AND v.revision=plugin_runs.config_revision) THEN 'pending' ELSE 'canceled' END,updated_at=? WHERE id=? AND state='reconcile_pending'`, now(), run.ID)
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
		if _, err = p.store.DB.ExecContext(ctx, `UPDATE plugin_runs SET state='succeeded',result=?,error_code='',updated_at=? WHERE id=? AND state='reconcile_pending'`, sealed, now(), run.ID); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) configureSubscribeGroup(ctx context.Context, run *PluginRun) (json.RawMessage, error) {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	actual, config, err := a.subscribeGroupInvocation(ctx, *run, "init", "reload")
	if err != nil {
		return nil, err
	}
	if receipt, err := a.pluginStore.subscribeGroupReceipt(ctx, actual); err == nil {
		return receipt, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	m, err := a.subscribeGroupMachine(actual, config, true)
	if err != nil {
		return nil, err
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	updated := actual
	if err = a.pluginStore.guardSubscribeGroupRunTx(ctx, tx, actual); err != nil {
		return nil, err
	}
	stage := config
	stage.UpdateDetails = m.Config.UpdateDetails
	if config.ClearHandle {
		if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key='history_handle'`, run.InstanceID); err != nil {
			return nil, err
		}
		stage.ClearHandle = false
		raw, _ := json.Marshal(stage)
		if err = a.pluginStore.replaceRunConfigTx(ctx, tx, &updated, raw); err != nil {
			return nil, err
		}
	}
	if config.Clear {
		if _, err = tx.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key='history'`, run.InstanceID); err != nil {
			return nil, err
		}
		stage.Clear = false
		raw, _ := json.Marshal(stage)
		if err = a.pluginStore.replaceRunConfigTx(ctx, tx, &updated, raw); err != nil {
			return nil, err
		}
	}
	// Category and download switches independently scope their consumers.
	if err = a.pluginStore.configureEventSubscriptionTx(ctx, tx, updated, "added", []string{"subscription.added"}, 0, config.Category); err != nil {
		return nil, err
	}
	if err = a.pluginStore.configureEventSubscriptionTx(ctx, tx, updated, "downloaded", []string{"download.accepted"}, 0, config.Enabled && len(config.UpdateDetails) > 0); err != nil {
		return nil, err
	}
	result, _ := json.Marshal(map[string]any{"configured": true, "history_cleared": config.Clear, "handled_cleared": config.ClearHandle})
	if err = a.pluginStore.recordSubscribeGroupReceiptTx(ctx, tx, updated, result); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	*run = updated
	a.publishSubscribeGroupCache(updated, m)
	return result, nil
}

// Preserve original KV entry fields, scalar types and number representations
// when appending. A truthy non-list cannot be appended to in the original either.
func (p *PluginStore) subscribeGroupHistoryTx(ctx context.Context, tx *sql.Tx, instance string) ([]json.RawMessage, int64, error) {
	var revision int64
	var sealed []byte
	err := tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key='history'`, instance).Scan(&revision, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return []json.RawMessage{}, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	raw, err := p.unseal("data", instance, "history", sealed)
	if err != nil {
		return nil, 0, err
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, 0, bad("INVALID_PLUGIN_DATA", "订阅规则历史不是有效JSON")
	}
	falsey := value == nil
	switch v := value.(type) {
	case bool:
		falsey = !v
	case string:
		falsey = v == ""
	case json.Number:
		n, parseErr := strconv.ParseFloat(string(v), 64)
		falsey = parseErr == nil && n == 0
	case map[string]any:
		falsey = len(v) == 0
	case []any:
		falsey = len(v) == 0
	}
	if falsey {
		return []json.RawMessage{}, revision, nil
	}
	var history []json.RawMessage
	if json.Unmarshal(raw, &history) != nil {
		return nil, 0, bad("INVALID_PLUGIN_DATA", "订阅规则历史不是可追加的列表")
	}
	return history, revision, nil
}
func subscribeGroupProjection(sub Subscription) subscribegroup.Subscription {
	var year any
	if sub.Media.Year != 0 {
		year = sub.Media.Year
	}
	kind := "电影"
	if sub.Media.MediaType == "tv" {
		kind = "电视剧"
	}
	quality := sub.Policy.Source
	if quality == "" {
		quality = sub.Policy.Codec
	}
	out := subscribegroup.Subscription{ID: sub.ID, Name: sub.Media.Title, Year: year, Type: kind, TMDBID: strconv.FormatInt(sub.Media.ID, 10), Season: sub.Season, Resolution: sub.Policy.Resolution, Quality: quality, Effect: sub.Policy.HDR, Include: sub.Policy.Include, Exclude: sub.Policy.Exclude, SavePath: sub.DownloadPath, Sites: append([]string{}, sub.SiteIDs...), FilterGroups: append([]string{}, sub.Policy.FilterGroups...)}
	if p := sub.Policy.FilterParams; p != nil {
		for _, field := range []struct {
			value  *string
			target *string
		}{{p.Resolution, &out.Resolution}, {p.Quality, &out.Quality}, {p.Effect, &out.Effect}, {p.Include, &out.Include}, {p.Exclude, &out.Exclude}} {
			if field.value != nil {
				*field.target = *field.value
			}
		}
	}
	return out
}
func applySubscribeGroupFields(sub *Subscription, fields map[string]any) error {
	for key, value := range fields {
		switch key {
		case "include", "exclude", "resolution", "quality", "effect", "save_path":
			text, ok := value.(string)
			if !ok || len(text) > 64<<10 {
				return bad("INVALID_PLUGIN_PATCH", "订阅规则字段类型或大小无效")
			}
			if key != "save_path" && sub.Policy.FilterParams == nil {
				sub.Policy.FilterParams = &torrentfilter.Parameters{}
			}
			p := sub.Policy.FilterParams
			switch key {
			case "include":
				p.Include = &text
				sub.Policy.Include = ""
			case "exclude":
				p.Exclude = &text
				sub.Policy.Exclude = ""
			case "resolution":
				p.Resolution = &text
				sub.Policy.Resolution = ""
			case "quality":
				p.Quality = &text
				sub.Policy.Source = ""
				sub.Policy.Codec = ""
			case "effect":
				p.Effect = &text
				sub.Policy.HDR = ""
			case "save_path":
				sub.DownloadPath = text
				sub.DirectoryID = ""
				sub.RootID = ""
			}
		case "sites", "filter_groups":
			values, ok := value.([]string)
			if !ok || len(values) > 1000 {
				return bad("INVALID_PLUGIN_PATCH", "订阅规则引用列表无效")
			}
			if key == "sites" {
				sub.SiteIDs = append([]string{}, values...)
			} else {
				sub.Policy.FilterGroups = append([]string{}, values...)
			}
		default:
			return bad("PLUGIN_CAPABILITY_DENIED", "订阅规则不能修改此字段")
		}
	}
	return nil
}

// The caller holds downloadMu then subMu and persists the changed subscription
// in this transaction. These internally applied fields do not revoke choices
// already selected at the exact current revision; a prior user edit still does.
func advanceSubscribeGroupRevisionTx(ctx context.Context, tx *sql.Tx, sub *Subscription) error {
	previousRevision := sub.Revision
	sub.Revision++
	if _, err := tx.ExecContext(ctx, `UPDATE records SET payload=json_set(payload,'$.subscription_revision',?),updated_at=? WHERE kind='intent' AND json_extract(payload,'$.subscription_id')=? AND json_extract(payload,'$.subscription_revision')=? AND json_extract(payload,'$.state') IN ('planned','submitting','reconcile_pending')`, sub.Revision, now(), sub.ID, previousRevision); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,lease_until=0,updated_at=? WHERE kind='subscription' AND json_extract(payload,'$.id')=? AND state IN ('queued','running','retry_wait','waiting_paused')`, now(), sub.ID)
	return err
}

func (a *App) applySubscribeGroupAdded(ctx context.Context, run PluginRun, batchID string) (json.RawMessage, error) {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	actual, config, err := a.subscribeGroupInvocation(ctx, run, "event")
	if err != nil {
		return nil, err
	}
	var invocation plugins.Invocation
	var input struct {
		BatchID string `json:"batch_id"`
	}
	if json.Unmarshal(actual.Input, &invocation) != nil || !pluginStrictJSON(invocation.Input, &input) || input.BatchID == "" || input.BatchID != batchID {
		return nil, bad("PLUGIN_BATCH_SCOPE_DENIED", "订阅规则只能处理当前投递批次")
	}
	if receipt, err := a.pluginStore.subscribeGroupReceipt(ctx, actual); err == nil {
		return receipt, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	page, err := a.pluginStore.pluginBatchEvents(ctx, actual, batchID, 0, 2)
	if err != nil {
		return nil, err
	}
	var handler string
	if err = a.Store.DB.QueryRowContext(ctx, `SELECT handler_key FROM plugin_event_batches WHERE id=? AND run_id=?`, batchID, actual.ID).Scan(&handler); err != nil {
		return nil, err
	}
	if handler != "added" {
		return nil, bad("PLUGIN_BATCH_SCOPE_DENIED", "订阅规则批次处理器不匹配")
	}
	if page.Total != 1 || len(page.Events) != 1 || !page.Complete || page.Events[0].Type != "subscription.added" {
		return nil, bad("PLUGIN_BATCH_SCOPE_DENIED", "订阅规则新增处理器只接受单个订阅新增事实")
	}
	event := page.Events[0]
	snapshot, err := a.subscriptionAddedSnapshot(ctx, event)
	if err != nil {
		return nil, err
	}
	m, err := a.subscribeGroupMachine(actual, config, false)
	if err != nil {
		return nil, err
	}
	category := ""
	if config.Category && len(m.Rules) > 0 {
		_, category, err = a.importMediaCategory(ctx, snapshot.Media)
		if err != nil {
			return nil, err
		}
	}
	// Resolve metadata before waiting for a selected download. Once it finishes,
	// read the current subscription and preserve its selected choices together;
	// never overwrite a live submit context or an earlier user-edited revision.
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	a.subMu.Lock()
	defer a.subMu.Unlock()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = a.pluginStore.guardSubscribeGroupRunTx(ctx, tx, actual); err != nil {
		return nil, err
	}
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='subscription' AND id=?`, event.SubscriptionID).Scan(&raw); err != nil {
		return nil, err
	}
	var current Subscription
	if json.Unmarshal(raw, &current) != nil || current.ID != snapshot.SubscriptionID || mediaKey(current.Media) != event.MediaID || current.Revision < snapshot.Revision {
		return nil, bad("REVIEW_PLUGIN_SUBSCRIPTION", "订阅事实与当前影视身份不一致")
	}
	if current.State == "cancelled" {
		return nil, bad("REVIEW_PLUGIN_SUBSCRIPTION", "订阅已取消，未填充旧的新增事实")
	}
	projection := subscribeGroupProjection(current)
	location, zoneErr := time.LoadLocation(identifiersTimezone())
	if zoneErr != nil {
		location = shanghai()
	}
	m.SubscriptionAdded(&projection, category, time.Now().In(location).Format("2006-01-02 15:04:05"))
	for _, update := range m.Updates {
		if update.ID != current.ID {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "订阅规则不能修改其他订阅")
		}
		before, _ := json.Marshal(current)
		if len(update.Fields) > 0 {
			if err = applySubscribeGroupFields(&current, update.Fields); err != nil {
				return nil, err
			}
		}
		after, _ := json.Marshal(current)
		if !reflect.DeepEqual(before, after) {
			if err = a.advancePlannedQualityRevisionTx(ctx, tx, current); err != nil {
				return nil, err
			}
			if err = advanceSubscribeGroupRevisionTx(ctx, tx, &current); err != nil {
				return nil, err
			}
		}
		if err = txPut(ctx, tx, "subscription", current.ID, current); err != nil {
			return nil, err
		}
	}
	if len(m.History) > 0 {
		history, revision, err := a.pluginStore.subscribeGroupHistoryTx(ctx, tx, run.InstanceID)
		if err != nil {
			return nil, err
		}
		for _, entry := range m.History {
			raw, _ := json.Marshal(entry)
			history = append(history, raw)
		}
		raw, err := pluginJSONLimit(history, false, pluginDataValueLimit)
		if err != nil {
			return nil, err
		}
		if _, err = a.pluginStore.saveValueTx(ctx, tx, run.InstanceID, "data", "history", revision, raw); err != nil {
			return nil, err
		}
	}
	result, _ := json.Marshal(map[string]any{"updated": len(m.Updates), "history_added": len(m.History)})
	if err = a.pluginStore.recordSubscribeGroupReceiptTx(ctx, tx, actual, result); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	a.publishSubscribeGroupCache(actual, m)
	return result, nil
}

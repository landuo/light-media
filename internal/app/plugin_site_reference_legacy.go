package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
)

// Host-internal collector input. No browser or worker can assert this format,
// fingerprint or source implementation. Numeric MoviePilot IDs have a separate
// snapshot/migration contract and cannot enter this old native-opaque namespace.
type legacySiteReferenceSelection struct {
	Field   string
	Ordinal int
	Target  builtin.SiteReference
}
type legacySiteControlObservation struct {
	Fingerprint, SourceCodeSHA, Instance string
	Generation, Revision                 int64
	Selections                           []legacySiteReferenceSelection
}
type legacySiteControlRun struct {
	Meta  PluginRun
	Input []byte
}
type legacySiteControlSource struct {
	Version         int
	Purpose, ID     string
	Observation     legacySiteControlObservation
	Installation    PluginInstallation
	Config, Watcher []byte
	WatcherRevision int64
	Bindings        []nativeSiteIdentity
	Pending         []legacySiteControlRun
}
type siteReferenceRunTransfer struct{ OldID, NewID, OldInputSHA, NewInputSHA string }
type legacySiteControlTransition struct {
	Version                                                int
	Purpose, Source, Instance                              string
	Generation, BeforeRevision, AfterRevision              int64
	BeforeSHA, AfterSHA, WatcherBeforeSHA, WatcherAfterSHA string
	Transfers                                              []siteReferenceRunTransfer
}

const oldNativeMergeSiteSwitchSHA = "6e0457680aef9b97d9cccdf31d432cebada59ac5024b81e08a424bf921615305"

func legacySiteControlField(key string) (known, custom bool) {
	if slices.Contains([]string{"enable_sites", "search_sites", "rss_sites"}, key) {
		return true, false
	}
	for _, slot := range builtin.SiteControlSlots {
		if slot.Key == key {
			return true, slot.Custom
		}
	}
	return false, false
}

func (a *App) legacySiteControlProjection(ctx context.Context, tx *sql.Tx, source legacySiteControlSource) (builtin.SiteControls, builtin.SiteControls, []nativeSiteIdentity, error) {
	projection, err := a.siteReferenceProjectionTx(ctx, tx, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	selections := map[string]legacySiteReferenceSelection{}
	for _, s := range source.Observation.Selections {
		key := s.Field + ":" + strconv.Itoa(s.Ordinal)
		if _, exists := selections[key]; exists || s.Ordinal < 0 || s.Target.Validate() != nil {
			return nil, nil, nil, bad("PLUGIN_SITE_SOURCE_INVALID", "旧站点引用选择无效或重复")
		}
		selections[key] = s
	}
	used := map[string]bool{}
	bindings := []nativeSiteIdentity{}
	convert := func(prefix string, raw []byte) (builtin.SiteControls, error) {
		if len(raw) == 0 {
			return nil, nil
		}
		var c builtin.SiteControls
		if json.Unmarshal(raw, &c) != nil || c == nil {
			return nil, bad("PLUGIN_SITE_SOURCE_INVALID", "旧站点配置无法读取")
		}
		out := c.Clone()
		keys := make([]string, 0, len(c))
		for key := range c {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			raw := c[key]
			known, custom := legacySiteControlField(key)
			if !known {
				continue
			}
			var opaque []string
			if json.Unmarshal(raw, &opaque) != nil {
				return nil, bad("PLUGIN_SITE_SOURCE_INVALID", "来源不是旧原生字符串协议")
			}
			refs := make([]builtin.SiteReference, 0, len(opaque))
			for i, old := range opaque {
				lookup := prefix + "." + key + ":" + strconv.Itoa(i)
				selection, exists := selections[lookup]
				if !exists {
					return nil, bad("PLUGIN_SITE_REFERENCE_UNMAPPED", "旧站点引用缺少逐项命名空间选择")
				}
				used[lookup] = true
				target := selection.Target
				if target.Native > 0 {
					v, ok := projection.byOpaque[old]
					if !ok || v.SourceID != target.Native {
						return nil, bad("PLUGIN_SITE_REFERENCE_UNMAPPED", "旧原生引用与目标编号不符")
					}
					bindings = append(bindings, v)
				} else if !custom || target.Custom != old {
					return nil, bad("PLUGIN_SITE_REFERENCE_UNMAPPED", "自定义引用必须显式保留原字符串")
				}
				refs = append(refs, target)
			}
			out.SetReferences(key, refs)
		}
		if err := out.Validate(); err != nil {
			return nil, err
		}
		return out, nil
	}
	config, err := convert("config", source.Config)
	if err != nil {
		return nil, nil, nil, err
	}
	watcher, err := convert("watcher", source.Watcher)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(used) != len(selections) {
		return nil, nil, nil, bad("PLUGIN_SITE_SOURCE_INVALID", "来源选择含未绑定字段")
	}
	return config, watcher, bindings, nil
}

func (a *App) captureLegacySiteControlSourceTx(ctx context.Context, tx *sql.Tx, o legacySiteControlObservation) (legacySiteControlSource, error) {
	s := legacySiteControlSource{Version: 1, Purpose: "native-opaque-site-controls/v0", Observation: o}
	if !pluginStorageDigest.MatchString(o.Fingerprint) || o.SourceCodeSHA != oldNativeMergeSiteSwitchSHA || o.Generation < 1 || o.Revision < 1 {
		return s, bad("PLUGIN_SITE_SOURCE_INVALID", "旧原生实现来源未确认")
	}
	in := &s.Installation
	in.InstanceID = o.Instance
	if err := tx.QueryRowContext(ctx, `SELECT plugin_id,version,generation,installed,desired_enabled FROM plugin_installations WHERE instance_id=?`, o.Instance).Scan(&in.PluginID, &in.Version, &in.Generation, &in.Installed, &in.DesiredEnabled); err != nil {
		return s, err
	}
	if in.PluginID != "MergeSiteSwitch" || in.Version != "1.2" || in.Generation != o.Generation {
		return s, bad("PLUGIN_SITE_SOURCE_INVALID", "旧原生实例不匹配")
	}
	var revision int64
	var sealed []byte
	if err := tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='config' AND item_key='main'`, o.Instance).Scan(&revision, &sealed); err != nil {
		return s, err
	}
	if revision != o.Revision {
		return s, bad("PLUGIN_REVISION_CONFLICT", "来源配置已变化")
	}
	var err error
	s.Config, err = a.pluginStore.unseal("config", o.Instance, "main", sealed)
	if err != nil {
		return s, err
	}
	var gen int64
	err = tx.QueryRowContext(ctx, `SELECT generation,config_revision,config FROM plugin_site_control_watchers WHERE instance_id=?`, o.Instance).Scan(&gen, &s.WatcherRevision, &sealed)
	if err == nil {
		if gen != o.Generation {
			return s, bad("PLUGIN_SITE_SOURCE_INVALID", "旧监听器代际不符")
		}
		s.Watcher, err = a.pluginStore.unseal("site_controls_watch", o.Instance, "main", sealed)
	} else if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		return s, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+pluginRunColumns+` FROM plugin_runs WHERE instance_id=? AND state IN ('pending','running','reconcile_pending') ORDER BY created_at,id`, o.Instance)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		r, e := a.pluginStore.scanPluginRun(rows)
		if e != nil {
			rows.Close()
			return s, e
		}
		if r.State != "pending" || !slices.Contains([]string{"init", "reload"}, r.Action) {
			rows.Close()
			return s, bad("PLUGIN_SITE_PROTOCOL_BUSY", "请先完成旧执行恢复和收尾，再转换引用协议")
		}
		input := append([]byte(nil), r.Input...)
		r.Input = nil
		s.Pending = append(s.Pending, legacySiteControlRun{Meta: r, Input: input})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return s, err
	}
	_, _, s.Bindings, err = a.legacySiteControlProjection(ctx, tx, s)
	return s, err
}

func (a *App) registerLegacySiteControlSource(ctx context.Context, o legacySiteControlObservation) (string, error) {
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM native_site_reference_protocols WHERE instance_id=? AND generation=?`, o.Instance, o.Generation).Scan(&count); err != nil {
		return "", err
	}
	if count != 0 {
		return "", bad("PLUGIN_SITE_SOURCE_INVALID", "已有协议不能由旧格式声明替换")
	}
	s, err := a.captureLegacySiteControlSourceTx(ctx, tx, o)
	if err != nil {
		return "", err
	}
	s.ID = a.pluginStore.noticeID("legacy-site-controls-source", s)
	sealed, err := a.Store.Seal(s)
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_site_reference_sources VALUES(?,?,?,?) ON CONFLICT(source_id) DO NOTHING`, s.ID, o.Instance, o.Generation, sealed); err != nil {
		return "", err
	}
	var actual legacySiteControlSource
	if err = tx.QueryRowContext(ctx, `SELECT sealed FROM native_site_reference_sources WHERE source_id=? AND instance_id=? AND generation=?`, s.ID, o.Instance, o.Generation).Scan(&sealed); err != nil {
		return "", err
	}
	if a.Store.Unseal(sealed, &actual) != nil || !reflect.DeepEqual(s, actual) {
		return "", bad("PLUGIN_SITE_SOURCE_INVALID", "旧格式来源记录不一致")
	}
	return s.ID, tx.Commit()
}

func (a *App) transitionLegacySiteControlSource(ctx context.Context, id string) (legacySiteControlTransition, error) {
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	var result legacySiteControlTransition
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var sealed []byte
	var s legacySiteControlSource
	if err = tx.QueryRowContext(ctx, `SELECT sealed FROM native_site_reference_sources WHERE source_id=?`, id).Scan(&sealed); err != nil {
		return result, err
	}
	if a.Store.Unseal(sealed, &s) != nil || s.Version != 1 || s.Purpose != "native-opaque-site-controls/v0" || s.ID != id {
		return result, bad("PLUGIN_SITE_SOURCE_INVALID", "旧格式来源无法校验")
	}
	copy := s
	copy.ID = ""
	if a.pluginStore.noticeID("legacy-site-controls-source", copy) != id {
		return result, bad("PLUGIN_SITE_SOURCE_INVALID", "旧格式来源身份不符")
	}
	err = tx.QueryRowContext(ctx, `SELECT sealed FROM native_site_reference_transitions WHERE source_id=? AND instance_id=? AND generation=?`, id, s.Observation.Instance, s.Observation.Generation).Scan(&sealed)
	if err == nil {
		if a.Store.Unseal(sealed, &result) != nil || result.Version != 1 || result.Purpose != "site-reference-transition/v1" || result.Source != id || result.Instance != s.Observation.Instance || result.Generation != s.Observation.Generation {
			return result, bad("PLUGIN_SITE_SOURCE_INVALID", "引用转换收据无法校验")
		}
		return result, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	current, err := a.captureLegacySiteControlSourceTx(ctx, tx, s.Observation)
	if err != nil {
		return result, err
	}
	current.ID = s.ID
	if !reflect.DeepEqual(current, s) {
		return result, bad("PLUGIN_SNAPSHOT_STALE", "旧配置、站点身份或待执行任务已变化，转换未应用")
	}
	config, watcher, bindings, err := a.legacySiteControlProjection(ctx, tx, s)
	if err != nil {
		return result, err
	}
	if !reflect.DeepEqual(bindings, s.Bindings) {
		return result, bad("PLUGIN_SNAPSHOT_STALE", "站点绑定已变化")
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return result, err
	}
	stored, err := a.pluginStore.saveValueTx(ctx, tx, s.Observation.Instance, "config", "main", s.Observation.Revision, raw)
	if err != nil {
		return result, err
	}
	result = legacySiteControlTransition{Version: 1, Purpose: "site-reference-transition/v1", Source: id, Instance: s.Observation.Instance, Generation: s.Observation.Generation, BeforeRevision: s.Observation.Revision, AfterRevision: stored.Revision, BeforeSHA: tokenHash(string(s.Config)), AfterSHA: tokenHash(string(raw)), WatcherBeforeSHA: tokenHash(string(s.Watcher))}
	if watcher != nil {
		b, _ := json.Marshal(watcher)
		result.WatcherAfterSHA = tokenHash(string(b))
		sealed, err = a.pluginStore.seal("site_controls_watch", s.Observation.Instance, "main", b)
		if err != nil {
			return result, err
		}
		var changed sql.Result
		if changed, err = tx.ExecContext(ctx, `UPDATE plugin_site_control_watchers SET config=? WHERE instance_id=? AND generation=? AND config_revision=?`, sealed, s.Observation.Instance, s.Observation.Generation, s.WatcherRevision); err != nil {
			return result, err
		}
		if n, e := changed.RowsAffected(); e != nil || n != 1 {
			return result, bad("PLUGIN_SNAPSHOT_STALE", "旧监听器未完成原子转换")
		}
	}
	if err = a.pluginStore.writeSiteReferenceProtocolTx(ctx, tx, s.Installation, id); err != nil {
		return result, err
	}
	// The replacement admission uses the ordinary protocol gate. Its proof and
	// the final transfer list are built inside this one transaction; neither a
	// provisional receipt nor the replacement can be seen before commit.
	sealed, err = a.Store.Seal(result)
	if err != nil {
		return result, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_site_reference_transitions VALUES(?,?,?,?)`, id, result.Instance, result.Generation, sealed); err != nil {
		return result, err
	}
	for _, old := range s.Pending {
		var inv plugins.Invocation
		if json.Unmarshal(old.Input, &inv) != nil || inv.Action != old.Meta.Action {
			return result, bad("PLUGIN_SITE_SOURCE_INVALID", "旧执行输入无法校验")
		}
		transfer := siteReferenceRunTransfer{OldID: old.Meta.ID, OldInputSHA: tokenHash(string(old.Input))}
		var changed sql.Result
		if changed, err = tx.ExecContext(ctx, `UPDATE plugin_runs SET state='canceled',error_code='PLUGIN_REFERENCE_PROTOCOL_TRANSFERRED',updated_at=? WHERE id=? AND state='pending' AND instance_id=? AND version=? AND generation=? AND config_revision=?`, now(), old.Meta.ID, s.Observation.Instance, old.Meta.Version, old.Meta.Generation, old.Meta.ConfigRevision); err != nil {
			return result, err
		}
		if n, e := changed.RowsAffected(); e != nil || n != 1 {
			return result, bad("PLUGIN_SNAPSHOT_STALE", "旧执行未完成原子转移")
		}
		if old.Meta.Version == s.Installation.Version && old.Meta.Generation == s.Observation.Generation && old.Meta.ConfigRevision == s.Observation.Revision && s.Installation.Installed && s.Installation.DesiredEnabled {
			if !siteReferenceJSONEqual(inv.Config, s.Config) {
				return result, bad("PLUGIN_SITE_SOURCE_INVALID", "旧执行配置快照不符")
			}
			next, e := a.pluginStore.enqueueRunTx(ctx, tx, s.Observation.Instance, old.Meta.Action, inv.Input)
			if e != nil {
				return result, e
			}
			transfer.NewID, transfer.NewInputSHA = next.ID, tokenHash(string(next.Input))
		}
		result.Transfers = append(result.Transfers, transfer)
	}
	sealed, err = a.Store.Seal(result)
	if err != nil {
		return result, err
	}
	changed, err := tx.ExecContext(ctx, `UPDATE native_site_reference_transitions SET sealed=? WHERE source_id=? AND instance_id=? AND generation=?`, sealed, id, result.Instance, result.Generation)
	if err != nil {
		return result, err
	}
	if n, e := changed.RowsAffected(); e != nil || n != 1 {
		return result, bad("PLUGIN_SITE_SOURCE_INVALID", "引用转换收据未完整提交")
	}
	return result, tx.Commit()
}

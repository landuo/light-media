package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/schedule"
	"light-media/internal/plugins/statistic"
)

func (p *PluginStore) siteStatisticReceipt(ctx context.Context, run PluginRun) (json.RawMessage, error) {
	var sealed []byte
	err := p.store.DB.QueryRowContext(ctx, `SELECT result FROM plugin_site_statistic_operations WHERE run_id=? AND instance_id=? AND generation=? AND config_revision=?`, run.ID, run.InstanceID, run.Generation, run.ConfigRevision).Scan(&sealed)
	if err != nil {
		return nil, err
	}
	return p.unseal("site_statistic_result", run.InstanceID, run.ID, sealed)
}
func (p *PluginStore) saveSiteStatisticReceiptTx(ctx context.Context, tx *sql.Tx, run PluginRun, value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	sealed, err := p.seal("site_statistic_result", run.InstanceID, run.ID, raw)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO plugin_site_statistic_operations VALUES(?,?,?,?,?)`, run.ID, run.InstanceID, run.Generation, run.ConfigRevision, sealed)
	return raw, err
}

func (p *PluginStore) recoverSiteStatisticRuns(ctx context.Context) error {
	rows, err := p.store.DB.QueryContext(ctx, `SELECT r.id,r.instance_id,r.generation,r.config_revision FROM plugin_runs r JOIN plugin_installations i ON i.instance_id=r.instance_id WHERE i.plugin_id='SiteStatistic' AND r.state='reconcile_pending'`)
	if err != nil {
		return err
	}
	runs := []PluginRun{}
	for rows.Next() {
		var run PluginRun
		if err = rows.Scan(&run.ID, &run.InstanceID, &run.Generation, &run.ConfigRevision); err != nil {
			break
		}
		runs = append(runs, run)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, run := range runs {
		result, err := p.siteStatisticReceipt(ctx, run)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err = p.store.DB.ExecContext(ctx, `UPDATE plugin_runs SET state='pending',updated_at=? WHERE id=? AND state='reconcile_pending'`, now(), run.ID); err != nil {
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

// Called under pluginMu. Collection itself runs in a host job, so no network
// request or external message delivery holds this shared lifecycle lock.
func (a *App) pluginSiteStatisticHost(ctx context.Context, installation PluginInstallation, run *PluginRun, method string, raw json.RawMessage) (json.RawMessage, error) {
	if installation.PluginID != "SiteStatistic" || run == nil {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "站点统计仅允许当前插件执行调用")
	}
	stored, err := a.pluginStore.Config(ctx, run.InstanceID)
	if err != nil {
		return nil, err
	}
	var config builtin.SiteStatisticConfiguration
	if !pluginStrictJSON(stored.Value, &config) || config.Validate() != nil {
		return nil, bad("INVALID_PLUGIN_CONFIG", "站点统计配置无法读取")
	}
	if method == "site.statistics.configure" {
		if string(raw) != "{}" || (run.Action != "init" && run.Action != "reload") {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "只能登记当前站点统计配置")
		}
		// PluginManager gates all source event callbacks with get_state(), then
		// SiteStatistic.send_msg additionally tests notify_type and site_id='*'.
		effect := func(ctx context.Context, tx *sql.Tx, current PluginRun) error {
			return a.pluginStore.configureEventSubscriptionTx(ctx, tx, current, "notify", []string{"site.refreshed"}, 0, config.Enabled && config.NotifyType != "")
		}
		if config.OnlyOnce {
			at := time.Now()
			definition := pluginScheduleDefinition{ID: "SiteStatistic.once", Name: "站点数据统计服务", Handler: "refresh", Trigger: schedule.Spec{Kind: "date", Timezone: identifiersTimezone(), RunAt: schedule.FormatTime(at.Add(3 * time.Second))}}
			if err := a.pluginStore.configurePluginSchedulesWithEffect(ctx, run, "once", []pluginScheduleDefinition{definition}, at, map[string]json.RawMessage{"onlyonce": json.RawMessage(`false`)}, effect); err != nil {
				return nil, err
			}
		} else if run.Action == "reload" {
			if err := a.pluginStore.configurePluginSchedulesWithEffect(ctx, run, "once", nil, time.Now(), nil, effect); err != nil {
				return nil, err
			}
		} else if err := a.pluginStore.configureEventSubscription(ctx, *run, "notify", []string{"site.refreshed"}, 0, config.Enabled && config.NotifyType != ""); err != nil {
			return nil, err
		}
		return json.RawMessage(`{"configured":true}`), nil
	}
	switch method {
	case "site.statistics.refresh":
		var input struct {
			OccurrenceID string `json:"occurrence_id"`
		}
		if run.Action != "service" || !pluginStrictJSON(raw, &input) || input.OccurrenceID == "" {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "站点刷新必须来自绑定的一次性任务")
		}
		definition, err := a.pluginStore.pluginScheduledInvocation(ctx, *run, input.OccurrenceID)
		if err != nil {
			return nil, err
		}
		if definition.ID != "SiteStatistic.once" || definition.Handler != "refresh" {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "站点刷新服务不匹配")
		}
		if result, err := a.pluginStore.siteStatisticReceipt(ctx, *run); err == nil {
			return result, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		tx, err := a.Store.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		job, err := enqueueSiteRefreshTx(ctx, tx)
		if err != nil {
			return nil, err
		}
		result, err := a.pluginStore.saveSiteStatisticReceiptTx(ctx, tx, *run, map[string]any{"queued": true, "job_id": job.ID})
		if err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		select {
		case a.jobWake <- struct{}{}:
		default:
		}
		return result, nil
	case "site.statistics.notify":
		var requested, expected struct {
			BatchID string `json:"batch_id"`
		}
		var invocation plugins.Invocation
		if run.Action != "event" || !config.Enabled || config.NotifyType == "" || !pluginStrictJSON(raw, &requested) || requested.BatchID == "" || json.Unmarshal(run.Input, &invocation) != nil || !pluginStrictJSON(invocation.Input, &expected) || expected.BatchID != requested.BatchID {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "统计通知必须来自当前站点事件批次")
		}
		page, err := a.pluginStore.pluginBatchEvents(ctx, *run, requested.BatchID, 0, 200)
		if err != nil {
			return nil, err
		}
		if !page.Complete || len(page.Events) != 1 || page.Events[0].Type != "site.refreshed" {
			return nil, bad("PLUGIN_BATCH_SCOPE_DENIED", "站点统计事件批次无效")
		}
		if result, err := a.pluginStore.siteStatisticReceipt(ctx, *run); err == nil {
			return result, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if page.Events[0].SiteID != "*" {
			return a.commitSiteStatisticNotice(ctx, *run, nil, config.NotifyType, time.Now().In(shanghai()))
		}
		var notice *statistic.Notification
		snapshot, err := a.siteStatisticsSnapshot(ctx)
		if err != nil {
			return nil, err
		}
		if page.Events[0].SiteID == "*" {
			selected, err := statistic.Select(snapshot)
			if err != nil {
				return nil, bad("SITE_STATISTICS_CORRUPT", "站点统计数据无效")
			}
			notice = statistic.BuildNotification(selected, config.NotifyType, snapshot.Now.Format("2006-01-02"))
		}
		return a.commitSiteStatisticNotice(ctx, *run, notice, config.NotifyType, snapshot.Now)
	default:
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "站点统计操作未获授权")
	}
}

func (a *App) commitSiteStatisticNotice(ctx context.Context, run PluginRun, notice *statistic.Notification, mode string, at time.Time) (json.RawMessage, error) {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	sent := false
	if notice != nil {
		var sealed []byte
		var revision int64
		err = tx.QueryRowContext(ctx, `SELECT revision,sealed FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key='last_notify'`, run.InstanceID).Scan(&revision, &sealed)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		var last struct {
			Date        string `json:"date"`
			Type        string `json:"type"`
			Fingerprint string `json:"fingerprint"`
		}
		if err == nil {
			raw, err := a.pluginStore.unseal("data", run.InstanceID, "last_notify", sealed)
			if err != nil {
				return nil, err
			}
			if json.Unmarshal(raw, &last) != nil {
				return nil, bad("PLUGIN_DATA_CORRUPT", "站点统计通知去重记录无效")
			}
		}
		if last.Date != at.Format("2006-01-02") || last.Type != mode || last.Fingerprint != notice.Fingerprint {
			notification := Notification{ID: "plugin_notice_" + run.ID, Title: notice.Title, Message: a.clean(notice.Text), MType: "站点", CreatedAt: now()}
			if err = txPut(ctx, tx, "notification", notification.ID, notification); err != nil {
				return nil, err
			}
			data, _ := json.Marshal(map[string]string{"date": at.Format("2006-01-02"), "type": mode, "fingerprint": notice.Fingerprint, "time": at.Format("15:04:05")})
			if _, err = a.pluginStore.saveValueTx(ctx, tx, run.InstanceID, "data", "last_notify", revision, data); err != nil {
				return nil, err
			}
			sent = true
		}
	}
	result, err := a.pluginStore.saveSiteStatisticReceiptTx(ctx, tx, run, map[string]bool{"notified": sent})
	if err != nil {
		return nil, err
	}
	return result, tx.Commit()
}

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"time"

	"light-media/internal/plugins/builtin"
	"light-media/internal/plugins/schedule"
)

const privateIdentifiersURLs = "[已保存的私有订阅地址；保持此内容可保留，清空可删除]"

func remoteIdentifiersPrivate(raw string) bool {
	for _, line := range strings.Split(raw, "\n") {
		u, err := url.Parse(strings.TrimSpace(line))
		if err != nil {
			return true
		}
		if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return true
		}
		for _, part := range strings.Split(u.Path, "/") {
			if len(part) >= 32 && !strings.Contains(part, ".") {
				return true
			}
		}
	}
	return false
}

func identifiersTimezone() string {
	if zone := os.Getenv("TZ"); zone != "" && zone != "Local" {
		if _, err := time.LoadLocation(zone); err == nil {
			return zone
		}
	}
	return "Asia/Shanghai"
}

func (a *App) pluginIdentifiersHost(ctx context.Context, installation PluginInstallation, run *PluginRun, method string, raw json.RawMessage) (json.RawMessage, error) {
	if run == nil || installation.PluginID != "RemoteIdentifiers" {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "识别词调用范围无效")
	}
	stored, err := a.pluginStore.Config(ctx, run.InstanceID)
	if err != nil {
		return nil, err
	}
	var config builtin.RemoteIdentifiersConfiguration
	if json.Unmarshal(stored.Value, &config) != nil {
		return nil, bad("INVALID_PLUGIN_CONFIG", "共享识别词配置无效")
	}
	switch method {
	case "identifiers.configure":
		if string(raw) != "{}" || (run.Action != "init" && run.Action != "reload") {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "只能根据当前配置登记识别词服务")
		}
		definitions := []pluginScheduleDefinition{}
		cron := config.Cron
		if cron == "" {
			cron = builtin.RemoteIdentifiersCron
		}
		if config.Enable {
			definitions = append(definitions, pluginScheduleDefinition{ID: "RemoteIdentifiers", Name: "获取远端识别词", Handler: "refresh", Trigger: schedule.Spec{Kind: "cron", Timezone: identifiersTimezone(), Cron: cron}})
		}
		if err := a.pluginStore.configurePluginSchedules(ctx, *run, "services", definitions, time.Now()); err != nil {
			return nil, err
		}
		if config.OnlyOnce {
			// Original date job is three seconds after initialization, including
			// time spent waiting in the queue. Persisting
			// its anchor and flag together means retry cannot create a second job.
			at := time.Now()
			date := pluginScheduleDefinition{ID: "RemoteIdentifiers.once", Name: "获取远端识别词", Handler: "refresh", Trigger: schedule.Spec{Kind: "date", Timezone: identifiersTimezone(), RunAt: schedule.FormatTime(at.Add(3 * time.Second))}}
			if err := a.pluginStore.configurePluginSchedulesAndPatch(ctx, run, "once", []pluginScheduleDefinition{date}, time.Now(), map[string]json.RawMessage{"onlyonce": json.RawMessage(`false`)}); err != nil {
				return nil, err
			}
		} else if run.Action == "reload" {
			if err := a.pluginStore.configurePluginSchedules(ctx, *run, "once", nil, time.Now()); err != nil {
				return nil, err
			}
		}
		return json.RawMessage(`{"configured":true}`), nil
	case "identifiers.refresh":
		var input struct {
			OccurrenceID string `json:"occurrence_id"`
		}
		if !pluginStrictJSON(raw, &input) || input.OccurrenceID == "" || run.Action != "service" {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "识别词执行缺少绑定的定时任务")
		}
		definition, err := a.pluginStore.pluginScheduledInvocation(ctx, *run, input.OccurrenceID)
		if err != nil {
			return nil, err
		}
		if definition.Handler != "refresh" || (definition.ID != "RemoteIdentifiers" && definition.ID != "RemoteIdentifiers.once") {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "识别词服务处理器不匹配")
		}
		if result, err := a.pluginStore.identifiersUpdateReceipt(ctx, *run); err == nil {
			return result, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		client := identifiersClient()
		defer client.CloseIdleConnections()
		fetcher := identifiersFetcher{client: client}
		remote, err := fetcher.fetch(ctx, config)
		if err != nil {
			return nil, err
		}
		count, err := a.replaceRemoteIdentifiersForRun(ctx, *run, remote)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"applied": true, "remote_count": len(remote) - 1, "total_count": count})
	default:
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "识别词操作尚未实现")
	}
}

func (p *PluginStore) identifiersUpdateReceipt(ctx context.Context, run PluginRun) (json.RawMessage, error) {
	var total, remote int
	if err := p.store.DB.QueryRowContext(ctx, `SELECT total_count,remote_count FROM recognition_word_updates WHERE run_id=? AND instance_id=? AND generation=? AND config_revision=?`, run.ID, run.InstanceID, run.Generation, run.ConfigRevision).Scan(&total, &remote); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"applied": true, "remote_count": remote, "total_count": total})
}

// Network collection is read-only. Word publication and this receipt share a
// transaction, so a crash has two distinguishable outcomes and no unknown
// external write. Confirmed publications finish without downloading again.
func (p *PluginStore) recoverIdentifiersRuns(ctx context.Context) error {
	rows, err := p.store.DB.QueryContext(ctx, `SELECT r.id,r.instance_id,r.generation,r.config_revision FROM plugin_runs r JOIN plugin_installations i ON i.instance_id=r.instance_id WHERE i.plugin_id='RemoteIdentifiers' AND r.state='reconcile_pending'`)
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
		result, err := p.identifiersUpdateReceipt(ctx, run)
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

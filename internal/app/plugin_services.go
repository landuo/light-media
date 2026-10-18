package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"light-media/internal/plugins"
)

// PluginServiceInfo is the public projection of a registered service. Its
// encrypted declaration, invocation input and credentials never leave the host.
type PluginServiceInfo struct {
	ID         string `json:"id"`
	InstanceID string `json:"instance_id"`
	ServiceID  string `json:"service_id"`
	Name       string `json:"name"`
	Provider   string `json:"provider"`
	Status     string `json:"status"`
	NextRun    string `json:"next_run"`
	CanRun     bool   `json:"can_run"`
	due        time.Time
	busy       bool
}

// Match MoviePilot's TimerUtils.time_difference: floor seconds, omit zero
// units, and show seconds only when no larger unit is present.
func pluginServiceTimeDifference(due, at time.Time) string {
	seconds := int64(due.Sub(at) / time.Second)
	if seconds <= 0 {
		return ""
	}
	var out strings.Builder
	for _, unit := range []struct {
		seconds int64
		label   string
	}{{86400, "天"}, {3600, "小时"}, {60, "分钟"}} {
		if n := seconds / unit.seconds; n > 0 {
			fmt.Fprintf(&out, "%d%s", n, unit.label)
		}
		seconds %= unit.seconds
	}
	if out.Len() == 0 && seconds > 0 {
		fmt.Fprintf(&out, "%d秒", seconds)
	}
	return out.String()
}

func (p *PluginStore) publicPluginServices(ctx context.Context, at time.Time) ([]PluginServiceInfo, error) {
	tx, err := p.store.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT s.instance_id,s.service_id,s.definition,s.next_due,i.plugin_id,i.version,i.manifest,
 EXISTS(SELECT 1 FROM plugin_schedule_occurrences o JOIN plugin_runs r ON r.id=o.run_id WHERE o.instance_id=s.instance_id AND o.service_id=s.service_id AND r.state='pending'),
 EXISTS(SELECT 1 FROM plugin_schedule_occurrences o JOIN plugin_runs r ON r.id=o.run_id WHERE o.instance_id=s.instance_id AND o.service_id=s.service_id AND r.state='running'),
 EXISTS(SELECT 1 FROM plugin_runs r WHERE r.instance_id=s.instance_id AND r.generation=i.generation AND r.state='reconcile_pending')
 FROM plugin_schedules s JOIN plugin_installations i ON i.instance_id=s.instance_id JOIN plugin_values v ON v.instance_id=s.instance_id AND v.kind='config' AND v.item_key='main'
 WHERE s.active=1 AND i.installed=1 AND i.desired_enabled=1 AND s.generation=i.generation AND s.config_revision=v.revision`)
	if err != nil {
		return nil, err
	}
	items := make([]PluginServiceInfo, 0)
	for rows.Next() {
		var item PluginServiceInfo
		var definitionSealed, manifestSealed []byte
		var pluginID, version string
		var next sql.NullString
		var pending, running, reconcile bool
		if err := rows.Scan(&item.InstanceID, &item.ServiceID, &definitionSealed, &next, &pluginID, &version, &manifestSealed, &pending, &running, &reconcile); err != nil {
			rows.Close()
			return nil, err
		}
		item.busy = pending || running || reconcile
		if !next.Valid && !item.busy {
			continue // An exhausted one-shot remains visible only while outstanding.
		}
		raw, err := p.unseal("schedule", item.InstanceID, item.ServiceID, definitionSealed)
		var definition pluginScheduleDefinition
		if err != nil || !pluginStrictJSON(raw, &definition) || definition.ID != item.ServiceID || definition.Name == "" {
			rows.Close()
			return nil, bad("PLUGIN_SCHEDULE_CORRUPT", "定时服务声明无法读取")
		}
		raw, err = p.unseal("manifest", item.InstanceID, "main", manifestSealed)
		var manifest plugins.Manifest
		if err != nil || !pluginStrictJSON(raw, &manifest) || manifest.Validate() != nil || manifest.PluginID != pluginID || manifest.Version != version {
			rows.Close()
			return nil, bad("PLUGIN_SCHEDULE_CORRUPT", "定时服务提供者无法读取")
		}
		item.ID = item.InstanceID + "_" + item.ServiceID
		item.Name, item.Provider, item.Status = definition.Name, manifest.Name, "等待"
		item.CanRun = next.Valid && !item.busy
		if running {
			item.Status = "正在运行"
		}
		if reconcile {
			item.Status = "等待核对"
		}
		if next.Valid {
			item.due, err = time.Parse(time.RFC3339Nano, next.String)
			if err != nil {
				rows.Close()
				return nil, bad("PLUGIN_SCHEDULE_CORRUPT", "定时服务时间无法读取")
			}
			item.NextRun = pluginServiceTimeDifference(item.due, at)
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].busy != items[j].busy {
			return items[i].busy
		}
		if !items[i].due.Equal(items[j].due) {
			return items[i].due.Before(items[j].due)
		}
		return items[i].ID < items[j].ID
	})
	return items, nil
}

func (a *App) queuePublicPluginService(ctx context.Context, instanceID, serviceID string) (PluginRun, error) {
	run, err := a.pluginStore.enqueueManualPluginService(ctx, instanceID, serviceID, time.Now())
	if err == nil {
		a.wakePlugins()
	}
	return run, err
}

// Called inside a.api, after its standard authentication and POST origin guard.
// Original MoviePilot's GET executor also receives the write-origin guard.
func (a *App) pluginServicesHTTP(w http.ResponseWriter, r *http.Request, path string) bool {
	compat := path
	if strings.HasPrefix(path, "v1/") || strings.HasPrefix(path, "v2/") {
		compat = path[3:]
	}
	switch {
	case (path == "plugins/services" || compat == "dashboard/schedule") && r.Method == http.MethodGet:
		items, err := a.pluginStore.publicPluginServices(r.Context(), time.Now())
		if err != nil {
			a.writeError(w, r, err)
		} else if path == "plugins/services" {
			writeJSON(w, r, items)
		} else {
			// Match ScheduleInfo's original public fields, without private host IDs.
			projection := make([]map[string]any, 0, len(items))
			for _, item := range items {
				projection = append(projection, map[string]any{"id": item.ID, "name": item.Name, "provider": item.Provider, "status": item.Status, "next_run": item.NextRun})
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(projection)
		}
		return true
	case path == "plugins/services/run" && r.Method == http.MethodPost:
		var in struct {
			InstanceID string `json:"instance_id"`
			ServiceID  string `json:"service_id"`
		}
		err := decodeBody(w, r, &in)
		var run PluginRun
		if err == nil {
			run, err = a.queuePublicPluginService(r.Context(), in.InstanceID, in.ServiceID)
		}
		if err != nil {
			a.writeError(w, r, err)
		} else {
			writeJSON(w, r, map[string]any{"accepted": true, "run_id": run.ID, "state": run.State})
		}
		return true
	case compat == "system/runscheduler" && r.Method == http.MethodGet:
		if !sameRequestOrigin(r) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			a.writeError(w, r, &APIError{"ORIGIN_REJECTED", "禁止跨站写入请求", 403})
			return true
		}
		requested := r.URL.Query().Get("jobid")
		items, err := a.pluginStore.publicPluginServices(r.Context(), time.Now())
		var selected *PluginServiceInfo
		if err == nil {
			for i := range items {
				if items[i].ID == requested {
					if selected != nil {
						err = bad("PLUGIN_SERVICE_AMBIGUOUS", "定时服务标识不唯一，请刷新服务列表")
						break
					}
					selected = &items[i]
				}
			}
			if err == nil && selected == nil {
				err = bad("PLUGIN_SERVICE_NOT_FOUND", "定时服务不存在或已停用")
			}
		}
		if err == nil {
			_, err = a.queuePublicPluginService(r.Context(), selected.InstanceID, selected.ServiceID)
		}
		if err != nil {
			a.writeError(w, r, err)
		} else {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		}
		return true
	}
	return false
}

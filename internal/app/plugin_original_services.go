package app

import (
	"context"
	"encoding/json"
	"fmt"
	ab "light-media/internal/plugins/autobackup"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	"light-media/internal/plugins/schedule"
	transfer "light-media/internal/plugins/torrenttransfer"
	"time"
)

// getServices ports the existing plugin get_state/get_service methods without
// calling init again. More compiled implementations are connected in subsequent
// slices; unsupported implementations are reported, never counted as ported.
func (p *originalPluginInstance) getServices(ctx context.Context) ([]pluginScheduleDefinition, error) {
	p.methodMu.Lock()
	defer p.methodMu.Unlock()
	if p.external {
		var definitions []pluginScheduleDefinition
		err := p.readSession(ctx, "get_service", &definitions)
		return definitions, err
	}
	switch p.installation.PluginID {
	case "AutoBackup":
		p.configMu.RLock()
		machine, ok := p.nativeState.(*ab.Plugin)
		p.configMu.RUnlock()
		if !ok || machine == nil {
			return nil, fmt.Errorf("loaded backup object missing")
		}
		trigger, err := machine.ServiceTrigger()
		if err != nil || trigger == nil {
			return nil, err
		}
		return []pluginScheduleDefinition{{ID: "AutoBackup", Name: "自动备份定时服务", Handler: "auto_backup", Trigger: *trigger, MisfireGraceMS: 1000}}, nil
	case "SystemProcess", "MPServerStatus", "PluginUnInstall":
		return nil, nil
	case "TorrentTransfer", "TorrentTransfermusic", "IYUUAutoSeed":
	default:
		return nil, fmt.Errorf("original get_service adapter not yet ported: %s", p.installation.PluginID)
	}
	p.configMu.RLock()
	raw := append(json.RawMessage(nil), p.appliedConfig...)
	nativeState := p.nativeState
	p.configMu.RUnlock()
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	var definitions []pluginScheduleDefinition
	if p.installation.PluginID == "IYUUAutoSeed" {
		cfg, actual := nativeState.(iyuu.Config)
		if !actual {
			var err error
			cfg, err = iyuu.RestoreConfig(input)
			if err != nil {
				return nil, err
			}
		}
		if !cfg.State() {
			return nil, nil
		}
		zone, err := iyuuScheduleTimezone()
		if err != nil {
			return nil, err
		}
		jobs, err := cfg.Services(zone)
		if err != nil {
			return nil, err
		}
		for _, job := range jobs {
			definitions = append(definitions, pluginScheduleDefinition{ID: "IYUUAutoSeed." + job.ID, Name: job.Name, Handler: "auto_seed", Trigger: schedule.Spec{Kind: job.Kind, Cron: job.Cron, Timezone: zone}})
		}
	} else {
		cfg, actual := nativeState.(transfer.Config)
		if !actual {
			var err error
			cfg, err = transfer.RestoreConfig(input)
			if err != nil {
				return nil, err
			}
		}
		if !cfg.State() {
			return nil, nil
		}
		zone, err := transferScheduleTimezone()
		if err != nil {
			return nil, err
		}
		jobs, err := cfg.Services(zone)
		if err != nil {
			return nil, err
		}
		for _, job := range jobs {
			definitions = append(definitions, pluginScheduleDefinition{ID: p.installation.PluginID + "." + job.ID, Name: job.Name, Handler: "transfer", Trigger: schedule.Spec{Kind: job.Kind, Cron: job.Cron, Timezone: zone}})
		}
	}
	return definitions, nil
}

// updateOriginalPluginJobs is the central Scheduler.update_plugin_job entry.
// Private date/interval jobs created by init are not central get_service jobs.
// The retained internal service IDs are the existing Go execution binding IDs.
func (a *App) updateOriginalPluginJobs(ctx context.Context, instance string) {
	a.pluginOriginalServicesMu.Lock()
	_, removeErr := a.Store.DB.ExecContext(ctx, `UPDATE plugin_schedules SET active=0,updated_at=? WHERE instance_id=? AND group_key='original_services'`, now(), instance)
	a.pluginOriginalServicesMu.Unlock()
	if removeErr != nil {
		a.log("ERROR", "plugins", "register_services_failed", "移除插件定时服务失败", "", "")
		return
	}
	a.pluginOriginalServicesMu.Lock()
	defer a.pluginOriginalServicesMu.Unlock()
	loaded, ok := a.pluginRunningInstances.Load(instance)
	if !ok {
		return
	}
	object := loaded.(*originalPluginInstance)
	definitions, err := object.getServices(ctx)
	if err != nil {
		a.log("ERROR", "plugins", "register_services_failed", "读取插件定时服务失败", "", "")
		return
	}
	for _, definition := range definitions {
		if err := a.addOriginalPluginJob(ctx, object.installation, definition); err != nil {
			a.log("ERROR", "plugins", "register_service_failed", "注册插件定时服务失败", "", "")
		}
	}
}

func (a *App) addOriginalPluginJob(ctx context.Context, in PluginInstallation, definition pluginScheduleDefinition) error {
	config, err := a.pluginStore.Config(ctx, in.InstanceID)
	if err != nil {
		return err
	}
	at := time.Now()
	trigger, err := schedule.New(definition.Trigger, at)
	if err != nil {
		return err
	}
	definition.Trigger = trigger.Spec()
	if definition.MisfireGraceMS == 0 {
		definition.MisfireGraceMS = 1000
	}
	raw, err := json.Marshal(definition)
	if err != nil {
		return err
	}
	sealed, err := a.pluginStore.seal("schedule", in.InstanceID, definition.ID, raw)
	if err != nil {
		return err
	}
	var due any
	if first := trigger.First(at); !first.IsZero() {
		due = schedule.FormatTime(first)
	}
	_, err = a.Store.DB.ExecContext(ctx, `INSERT INTO plugin_schedules VALUES(?,?,?,?,?,?,?,1,?,?) ON CONFLICT(instance_id,service_id) DO UPDATE SET group_key=excluded.group_key,generation=excluded.generation,config_revision=excluded.config_revision,definition=excluded.definition,definition_digest=excluded.definition_digest,active=1,next_due=excluded.next_due,updated_at=excluded.updated_at`, in.InstanceID, definition.ID, "original_services", in.Generation, config.Revision, sealed, tokenHash(string(raw)), due, now())
	return err
}

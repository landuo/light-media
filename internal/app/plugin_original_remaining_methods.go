package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	bf "light-media/internal/plugins/brushflow"
	"light-media/internal/plugins/remoteidentifiers"
	"light-media/internal/plugins/schedule"
	"light-media/internal/plugins/subscribegroup"
)

// Called under the original instance's methodMu. These are live member getters,
// not persisted configuration projections or a second initialization path.
func originalRemainingState(ctx context.Context, p *originalPluginInstance) (bool, bool, error) {
	p.configMu.RLock()
	native := p.nativeState
	p.configMu.RUnlock()
	switch p.installation.PluginID {
	case "RemoteIdentifiers":
		if s, ok := native.(*remoteidentifiers.State); ok && s != nil {
			return remoteidentifiers.Truth(s.Enable), true, nil
		}
	case "SubscribeGroup":
		if m, ok := native.(*subscribegroup.Machine); ok && m != nil {
			return m.State(), true, nil
		}
	case "BrushFlowLanduo":
		m, release, err := originalRemainingBrushMachine(ctx, p)
		if err != nil {
			return false, true, err
		}
		defer release()
		return m.Enabled(), true, nil
	default:
		return false, false, nil
	}
	return false, true, fmt.Errorf("%s initialized native member unavailable", p.installation.PluginID)
}

func originalRemainingServices(ctx context.Context, p *originalPluginInstance) ([]pluginScheduleDefinition, bool, error) {
	switch p.installation.PluginID {
	case "SubscribeGroup":
		return nil, true, nil // inherited _PluginBase.get_service: pass
	case "RemoteIdentifiers":
		p.configMu.RLock()
		s, ok := p.nativeState.(*remoteidentifiers.State)
		p.configMu.RUnlock()
		if !ok || s == nil {
			return nil, true, fmt.Errorf("RemoteIdentifiers initialized native member unavailable")
		}
		return originalRemainingIdentifierServices(ctx, p.app, s)
	case "BrushFlowLanduo":
		m, release, err := originalRemainingBrushMachine(ctx, p)
		if err != nil {
			return nil, true, err
		}
		defer release()
		services := m.Services(ctx)
		if len(services) == 0 {
			return nil, true, nil
		}
		zone, err := autoBackupHostTimezone()
		if err != nil {
			return nil, true, err
		}
		out := make([]pluginScheduleDefinition, 0, len(services))
		for _, s := range services {
			trigger := schedule.Spec{Kind: "interval", Timezone: zone, IntervalMS: int64(s.Minutes) * 60000}
			if s.Cron != "" {
				trigger = schedule.Spec{Kind: "cron", Timezone: zone, Cron: s.Cron}
			}
			input, _ := json.Marshal(map[string]string{"task_id": s.TaskID, "operation": s.Operation})
			out = append(out, pluginScheduleDefinition{ID: s.ID, Name: s.Name, Handler: "brushflow", Trigger: trigger, Input: input, MisfireGraceMS: 1000})
		}
		return out, true, nil
	default:
		return nil, false, nil
	}
}

func originalRemainingIdentifierServices(ctx context.Context, a *App, s *remoteidentifiers.State) ([]pluginScheduleDefinition, bool, error) {
	// Source get_service reads only enabled and cron. FileURLs/Task validation is
	// exclusively an execution concern; the private date job is not returned here.
	if !remoteidentifiers.Truth(s.Enable) || !remoteidentifiers.Truth(s.Cron) {
		return nil, true, nil
	}
	cron, ok := s.Cron.(string)
	if !ok {
		return nil, true, bad("INVALID_PLUGIN_SCHEDULE", "识别词定时任务周期无效")
	}
	// CronTrigger.from_crontab has no timezone argument in the source. Its local
	// timezone is a real dynamic dependency, not this plugin's config-table state.
	if a == nil || a.pluginStore == nil {
		return nil, true, identifiersDependency()
	}
	zone, err := a.identifiersSourceTimezone(ctx, true)
	if err != nil {
		return nil, true, err
	}
	if _, err = schedule.ParseCrontab(cron, zone); err != nil {
		return nil, true, err
	}
	return []pluginScheduleDefinition{{ID: "RemoteIdentifiers", Name: "获取远端识别词", Handler: "refresh", Trigger: schedule.Spec{Kind: "cron", Cron: cron, Timezone: zone}}}, true, nil
}

func originalRemainingCommands(_ context.Context, p *originalPluginInstance) ([]map[string]any, bool, error) {
	switch p.installation.PluginID {
	case "BrushFlowLanduo":
		return []map[string]any{}, true, nil
	case "RemoteIdentifiers", "SubscribeGroup":
		return nil, true, nil
	default:
		return nil, false, nil
	}
}

func originalRemainingBrushMachine(ctx context.Context, p *originalPluginInstance) (*bf.Core, func(), error) {
	if p.app == nil {
		return nil, nil, fmt.Errorf("BrushFlowLanduo owner unavailable")
	}
	rt := &p.app.pluginBrushFlow
	rt.mu.Lock()
	o := rt.owners[p.installation.InstanceID+":"+strconv.FormatInt(p.installation.Generation, 10)]
	rt.mu.Unlock()
	if o == nil || o.generation != p.installation.Generation {
		return nil, nil, fmt.Errorf("BrushFlowLanduo initialized owner unavailable")
	}
	if err := o.gate.acquire(ctx, false); err != nil {
		return nil, nil, err
	}
	release := func() { o.gate.release(false) }
	p.configMu.RLock()
	m, ok := p.nativeState.(*bf.Core)
	p.configMu.RUnlock()
	if !ok || m == nil || o.machineSnapshot() != m {
		release()
		return nil, nil, fmt.Errorf("BrushFlowLanduo initialized native member unavailable")
	}
	return m, release, nil
}

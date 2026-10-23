package app

import (
	"context"
	"fmt"
	"strconv"

	"light-media/internal/plugins/schedule"
)

// These adapters read the retained owner machine under its execution gate.
// They never create an owner, restore configuration, or call Init. The manager
// owns get_state filtering before calling get_service.
func originalMachineState(ctx context.Context, p *originalPluginInstance) (bool, bool, error) {
	switch p.installation.PluginID {
	case "CloudflareSpeedTest":
		owner, release, err := originalCloudflareMachineOwner(ctx, p)
		if err != nil {
			return false, true, err
		}
		defer release()
		return owner.machine.State(), true, nil
	case "SeedRescue":
		owner, release, err := originalSeedRescueMachineOwner(ctx, p)
		if err != nil {
			return false, true, err
		}
		defer release()
		return owner.machine.State(), true, nil
	default:
		return false, false, nil
	}
}

func originalMachineServices(ctx context.Context, p *originalPluginInstance) ([]pluginScheduleDefinition, bool, error) {
	switch p.installation.PluginID {
	case "CloudflareSpeedTest":
		owner, release, err := originalCloudflareMachineOwner(ctx, p)
		if err != nil {
			return nil, true, err
		}
		defer release()
		services, err := owner.machine.Services(func(cron string) error {
			_, err := schedule.ParseCrontab(cron, owner.timezone)
			return err
		})
		if err != nil {
			return nil, true, err
		}
		definitions := make([]pluginScheduleDefinition, 0, len(services))
		for _, service := range services {
			definitions = append(definitions, pluginScheduleDefinition{ID: service.ID, Name: service.Name, Handler: "cloudflare", Trigger: schedule.Spec{Kind: "cron", Cron: service.Cron, Timezone: owner.timezone}, MisfireGraceMS: 1000})
		}
		return definitions, true, nil
	case "SeedRescue":
		owner, release, err := originalSeedRescueMachineOwner(ctx, p)
		if err != nil {
			return nil, true, err
		}
		defer release()
		// Services performs the source dependency checks through the machine's
		// existing host. A host admission error must propagate, not become no jobs.
		readCtx, closeRead := originalSeedServiceContext(ctx, p, owner)
		defer closeRead()
		services, err := owner.machine.Services(readCtx)
		if err != nil {
			return nil, true, err
		}
		zone, err := autoBackupHostTimezone()
		if err != nil {
			return nil, true, err
		}
		definitions := make([]pluginScheduleDefinition, 0, len(services))
		for _, service := range services {
			trigger := schedule.Spec{Kind: service.Trigger, Cron: service.Cron, Timezone: zone}
			if service.Trigger == "date" {
				trigger.RunAt = schedule.FormatTime(service.RunAt)
			}
			definitions = append(definitions, pluginScheduleDefinition{ID: service.ID, Name: service.Name, Handler: "seedrescue", Trigger: trigger, MisfireGraceMS: 1000})
		}
		return definitions, true, nil
	default:
		return nil, false, nil
	}
}

func originalCloudflareMachineOwner(ctx context.Context, p *originalPluginInstance) (*pluginCloudflareOwner, func(), error) {
	runtime := &p.app.pluginCloudflare
	runtime.mu.Lock()
	owner := runtime.owners[p.installation.InstanceID+":"+strconv.FormatInt(p.installation.Generation, 10)]
	runtime.mu.Unlock()
	if owner == nil {
		return nil, nil, fmt.Errorf("loaded cloudflare owner missing")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	select {
	case owner.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	release := func() { <-owner.gate }
	if owner.generation != p.installation.Generation || owner.machine == nil {
		release()
		return nil, nil, fmt.Errorf("loaded cloudflare machine missing for generation %d", p.installation.Generation)
	}
	return owner, release, nil
}

func originalSeedRescueMachineOwner(ctx context.Context, p *originalPluginInstance) (*pluginSeedRescueOwner, func(), error) {
	runtime := &p.app.pluginSeedRescue
	runtime.mu.Lock()
	owner := runtime.owners[p.installation.InstanceID+":"+strconv.FormatInt(p.installation.Generation, 10)]
	runtime.mu.Unlock()
	if owner == nil {
		return nil, nil, fmt.Errorf("loaded seed rescue owner missing")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	select {
	case owner.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	release := func() { <-owner.gate }
	if owner.generation != p.installation.Generation || owner.machine == nil {
		release()
		return nil, nil, fmt.Errorf("loaded seed rescue machine missing for generation %d", p.installation.Generation)
	}
	return owner, release, nil
}

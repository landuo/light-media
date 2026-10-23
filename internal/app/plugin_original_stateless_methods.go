package app

import (
	"context"
	"fmt"

	"light-media/internal/plugins/builtin"
)

// The caller owns methodMu. These adapters only read retained member state;
// they never initialize a plugin or reconstruct members from persisted config.
func originalStatelessState(ctx context.Context, p *originalPluginInstance) (bool, bool, error) {
	if !originalStatelessPlugin(p.installation.PluginID) {
		return false, false, nil
	}
	p.configMu.RLock()
	native := p.nativeState
	p.configMu.RUnlock()
	switch p.installation.PluginID {
	case "CategoryEditor":
		if state, ok := native.(*builtin.OriginalCategoryEditor); ok && state != nil {
			return state.State(), true, nil
		}
	case "MediaServerRefresh":
		if state, ok := native.(*builtin.OriginalMediaServerRefresh); ok && state != nil {
			return state.State(), true, nil
		}
	case "SiteStatistic":
		if state, ok := native.(*builtin.OriginalSiteStatistic); ok && state != nil {
			return state.State(), true, nil
		}
	case "MergeSiteSwitch":
		if state, ok := native.(*builtin.OriginalMergeSiteSwitch); ok && state != nil {
			// Original __check_any_follow_enable_sites queries the installed plugin
			// list on EVERY call. This external dependency is intentionally live;
			// the object's own config remains the retained initialized member.
			if p.app == nil || p.app.pluginStore == nil {
				return false, true, fmt.Errorf("MergeSiteSwitch installed dependency source unavailable")
			}
			installed, err := p.app.pluginStore.Installations(ctx)
			if err != nil {
				return false, true, err
			}
			ids := make([]string, 0, len(installed))
			for _, entry := range installed {
				ids = append(ids, entry.PluginID)
			}
			// AnyFollow preserves BrushFlow (not BrushFlowLanduo), and omits CrossSeed.
			return state.State(ids), true, nil
		}
	}
	return false, true, fmt.Errorf("%s initialized native member state unavailable", p.installation.PluginID)
}

func originalStatelessPlugin(id string) bool {
	switch id {
	case "CategoryEditor", "MediaServerRefresh", "MergeSiteSwitch", "SiteStatistic":
		return true
	default:
		return false
	}
}

// CategoryEditor and MediaServerRefresh inherit _PluginBase.get_service's
// pass; MergeSiteSwitch and SiteStatistic define their own pass. Private once
// jobs and event subscriptions are not public service declarations.
func originalStatelessServices(_ context.Context, p *originalPluginInstance) ([]pluginScheduleDefinition, bool, error) {
	return nil, originalStatelessPlugin(p.installation.PluginID), nil
}

// All four original get_command bodies are pass, independently of state.
func originalStatelessCommands(_ context.Context, p *originalPluginInstance) ([]map[string]any, bool, error) {
	return nil, originalStatelessPlugin(p.installation.PluginID), nil
}

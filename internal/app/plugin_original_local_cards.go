package app

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"

	"light-media/internal/plugins"
)

// Fixed get_local_plugins iterates imported classes, asks each current object
// for state, then sorts class metadata by plugin_order. state=installed filters
// that result afterwards. It never asks a repository or infers a class from an
// installed-ID row, and a failed initializer may still have class metadata.
// runningPluginVersion is the build actually executing for an instance. It is
// the only version that can be compared against a repository release.
func (a *App) runningPluginVersion(id string) string {
	value, ok := a.pluginRunningInstances.Load(id)
	if !ok {
		return ""
	}
	object, ok := value.(*originalPluginInstance)
	if !ok {
		return ""
	}
	return object.manifest.Version
}

func (a *App) originalLocalPluginCards(ctx context.Context, installedOnly bool, validate ...func(context.Context) error) ([]pluginCard, error) {
	installed, err := a.originalInstalledPluginIDs(ctx)
	if err != nil {
		return nil, err
	}
	a.pluginOriginalRunningMu.Lock()
	ids := slices.Clone(a.pluginOriginalClassOrder)
	a.pluginOriginalRunningMu.Unlock()
	cards := []pluginCard{}
	for _, id := range ids {
		for _, check := range validate {
			if err := check(ctx); err != nil {
				return nil, err
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value, exists := a.pluginOriginalClasses.Load(id)
		if !exists {
			continue
		}
		class := value.(originalPluginClass)
		var manifest plugins.Manifest
		if err := json.Unmarshal(class.installation.Manifest, &manifest); err != nil {
			return nil, err
		}
		card, err := a.pluginStore.presentInstance(ctx, class.installation, manifest)
		if err != nil {
			return nil, err
		}
		card.Installed, card.Available = slices.Contains(installed, id), true
		card.HasPage = manifest.HasPage
		if running, loaded := a.pluginRunningInstances.Load(id); loaded {
			object := running.(*originalPluginInstance)
			// The loaded object's manifest is the build actually executing. The
			// installation column carries whatever the source index declared at
			// install time, which for older packages was a placeholder 1.0.0 and
			// made every built-in look outdated.
			if version := a.runningPluginVersion(id); version != "" {
				card.Version = version
			}
			state, stateErr := object.getState(ctx)
			if stateErr == nil {
				card.State = state
			} else {
				a.log("ERROR", "plugins", "state_read_failed", "获取插件状态失败", "", "")
			}
		}
		if !installedOnly || card.Installed {
			cards = append(cards, card)
		}
	}
	slices.SortStableFunc(cards, func(a, b pluginCard) int { return cmp.Compare(a.PluginOrder, b.PluginOrder) })
	return cards, ctx.Err()
}

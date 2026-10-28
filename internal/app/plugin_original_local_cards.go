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
func (a *App) originalLocalPluginCards(ctx context.Context, installedOnly bool) ([]pluginCard, error) {
	a.pluginOriginalRunningMu.Lock()
	ids := slices.Clone(a.pluginOriginalClassOrder)
	a.pluginOriginalRunningMu.Unlock()
	cards := []pluginCard{}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value, exists := a.pluginOriginalClasses.Load(id)
		if !exists {
			continue
		}
		class := value.(originalPluginClass)
		in, err := a.pluginStore.Installation(ctx, id)
		if err != nil {
			return nil, err
		}
		var manifest plugins.Manifest
		if err = json.Unmarshal(class.installation.Manifest, &manifest); err != nil {
			return nil, err
		}
		card, err := a.pluginStore.presentInstance(ctx, class.installation, manifest)
		if err != nil {
			return nil, err
		}
		card.Installed, card.Available = in.Installed, true
		card.HasPage = slices.Contains(manifest.Methods, "get_page")
		if running, loaded := a.pluginRunningInstances.Load(id); loaded {
			state, stateErr := running.(*originalPluginInstance).getState(ctx)
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

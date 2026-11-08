package app

import (
	"context"
	"slices"

	"light-media/internal/plugins/artifacts"
)

func (a *App) storeOriginalPluginClass(id string, class originalPluginClass) {
	a.pluginOriginalRunningMu.Lock()
	defer a.pluginOriginalRunningMu.Unlock()
	if _, exists := a.pluginOriginalClasses.Load(id); !exists {
		a.pluginOriginalClassOrder = append(a.pluginOriginalClassOrder, id)
	}
	a.pluginOriginalClasses.Store(id, class)
}

func (a *App) forgetOriginalPluginClassLocked(id string) {
	a.pluginOriginalClasses.Delete(id)
	a.pluginOriginalClassOrder = slices.DeleteFunc(a.pluginOriginalClassOrder, func(value string) bool { return value == id })
}

func (a *App) forgetOriginalPluginClass(id string) {
	a.pluginOriginalRunningMu.Lock()
	defer a.pluginOriginalRunningMu.Unlock()
	a.forgetOriginalPluginClassLocked(id)
}

// get_local_plugins reads state for each imported class, including a loaded
// uninstalled object, in class insertion order (not running-object order).
// A class without a running object still has its own version and installation
// membership; an installation row without an imported class does not.
func (a *App) readOriginalLocalPluginStates(ctx context.Context) error {
	a.pluginOriginalRunningMu.Lock()
	ids := slices.Clone(a.pluginOriginalClassOrder)
	a.pluginOriginalRunningMu.Unlock()
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, exists := a.pluginOriginalClasses.Load(id); !exists {
			continue
		}
		if value, loaded := a.pluginRunningInstances.Load(id); loaded {
			_, err := value.(*originalPluginInstance).getState(ctx)
			if err != nil {
				a.log("ERROR", "plugins", "state_read_failed", "获取插件状态失败", "", "")
			}
		}
	}
	return ctx.Err()
}

func (a *App) originalLocalPluginVersion(ctx context.Context, id string) (string, bool, error) {
	installed, err := a.originalInstalledPluginIDs(ctx)
	if err != nil {
		return "", false, err
	}
	if !slices.Contains(installed, id) {
		return "", false, nil
	}
	value, exists := a.pluginOriginalClasses.Load(id)
	if !exists {
		return "", false, nil
	}
	class := value.(originalPluginClass)
	version := class.installation.Version
	if class.isClone {
		metadata, err := a.pluginStore.instanceMetadata(ctx, a.Store.DB, id)
		if err != nil {
			return "", false, err
		}
		if metadata.DisplayVersion != "" {
			version = metadata.DisplayVersion
		}
	}
	return version, true, nil
}

// _process_plugin_info asks the current object once for each declaration,
// even when that declaration was read from the helper metadata cache or later
// loses source selection. Release-list downloads alone do not do this.
func (a *App) readOriginalMarketPluginStates(ctx context.Context, declarations []artifacts.Discovery, observed ...map[string]bool) {
	for _, entry := range declarations {
		if ctx.Err() != nil {
			return
		}
		if value, exists := a.pluginRunningInstances.Load(entry.PluginID); exists {
			state, err := value.(*originalPluginInstance).getState(ctx)
			if err != nil {
				a.log("ERROR", "plugins", "state_read_failed", "获取插件状态失败", "", "")
			} else if len(observed) > 0 && observed[0] != nil {
				observed[0][entry.RepositoryURL+"\x00"+entry.PluginID] = state
			}
		}
	}
}

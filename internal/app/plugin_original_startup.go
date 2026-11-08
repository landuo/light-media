package app

import (
	"context"
	"light-media/internal/plugins"
	"os"
	"slices"
	"sort"
	"strings"
	"time"
)

// initializeOriginalPlugins is the installed-list manager init_config entry:
// stop loaded objects, then start every installed class in its actual manifest plugin_order.
// get_state controls event/service availability, never whether init is called.
func (a *App) initializeOriginalPlugins(ctx context.Context) error {
	a.pluginOriginalRunningMu.Lock()
	running := append([]string(nil), a.pluginOriginalRunningOrder...)
	a.pluginOriginalRunningMu.Unlock()
	for _, instance := range running {
		if err := a.stopOriginalPlugin(ctx, instance); err != nil {
			return err
		}
	}
	// stop(None) clears imported classes even when their initializer failed
	// before creating a running object. Namespace and installed rows survive.
	a.pluginOriginalRunningMu.Lock()
	a.pluginOriginalClasses.Clear()
	a.pluginOriginalClassOrder = nil
	a.pluginOriginalRunningMu.Unlock()
	ids, err := a.originalInstalledPluginIDs(ctx)
	if err != nil {
		return err
	}
	installed, err := a.originalInstalledPackages(ctx, ids)
	if err != nil {
		return err
	}
	// Resolve actual installed executable metadata before sorting; neither an ID
	// allowlist nor an unauthenticated UI/config value defines class order.
	orders := make(map[string]int, len(installed))
	implementations := make(map[string]pluginImplementation, len(installed))
	loadable := installed[:0]
	for _, in := range installed {
		implementation, err := a.resolvePluginImplementation(ctx, in)
		if err != nil {
			a.log("ERROR", "plugins", "load_failed", "插件实现读取失败，继续加载其他插件", "", "")
			continue
		}
		orders[in.InstanceID] = implementation.Manifest.PluginOrder
		implementations[in.InstanceID] = implementation
		loadable = append(loadable, in)
	}
	installed = loadable
	sort.SliceStable(installed, func(i, j int) bool {
		return orders[installed[i].InstanceID] < orders[installed[j].InstanceID]
	})
	// Only a declared manager-control consumer can use the finite code-removal
	// primitive. Other existing class sets retain their ordinary start sequence.
	preload := false
	for _, implementation := range implementations {
		if slices.Contains(implementation.Manifest.Capabilities, plugins.PluginManagerControlCapability) {
			preload = true
			break
		}
	}
	if !preload {
		for _, in := range installed {
			a.startOriginalPlugin(ctx, in.InstanceID)
		}
	} else {
		type imported struct {
			object *originalPluginInstance
			class  originalPluginClass
		}
		preloaded := make([]imported, 0, len(installed))
		// A Go executable must already be open to survive later removal of its code
		// by an earlier initializer, as the original pre-imported Python class does.
		// This short-lived startup plan never survives the current init_config call.
		for _, in := range installed {
			implementation := implementations[in.InstanceID]
			if !implementation.External {
				continue
			}
			object, class, err := a.prepareOriginalPluginObject(ctx, in, implementation)
			if err == nil {
				_, err = object.originalSessionClient(ctx, plugins.Runner{Timeout: 10 * time.Second})
			}
			if err != nil {
				a.log("ERROR", "plugins", "load_failed", "插件实现预加载失败，继续加载其他插件", "", "")
				continue
			}
			preloaded = append(preloaded, imported{object, class})
		}
		for _, entry := range preloaded {
			a.startPreparedOriginalPlugin(ctx, entry.object, entry.class)
		}
	}
	for _, in := range installed {
		a.updateOriginalPluginJobs(ctx, in.InstanceID)
	}
	a.initOriginalPluginCommands()
	for _, in := range installed {
		a.registerOriginalPluginAPIs(in.InstanceID)
	}
	return nil
}

// _load_selective_plugins scans physical packages before stable plugin_order
// sorting. Membership order (including source duplicates) cannot reorder equal
// classes. File.ReadDir retains native directory order; os.ReadDir sorts it.
func (a *App) originalInstalledPackages(ctx context.Context, ids []string) ([]PluginInstallation, error) {
	// pathlib.exists precedes the empty-target check. Stat needs only search
	// access; it must neither turn EACCES into absence nor require directory read.
	if _, err := os.Stat(a.pluginPackageRoot()); err != nil {
		if sourceOriginalPathAbsent(err) {
			return []PluginInstallation{}, nil
		}
		return nil, err
	}
	if len(ids) == 0 {
		return []PluginInstallation{}, nil
	}
	root, err := os.OpenRoot(a.pluginPackageRoot())
	if err != nil {
		return nil, err
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, err := directory.ReadDir(-1)
	directory.Close()
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[strings.ToLower(id)] = true
	}
	installed := []PluginInstallation{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := entry.Name()
		if !entry.IsDir() || !pluginStorageID.MatchString(id) || !wanted[strings.ToLower(id)] {
			continue
		}
		in, err := a.originalDirectoryInstallation(ctx, id)
		if err == nil {
			installed = append(installed, in)
		}
	}
	return installed, nil
}

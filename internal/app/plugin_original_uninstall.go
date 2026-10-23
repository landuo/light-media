package app

import (
	"context"
	"light-media/internal/plugins/builtin"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func originalPluginUninstallRoute(path, method string) bool {
	p := strings.Split(path, "/")
	return pluginStorageID.MatchString(p[0]) && ((method == http.MethodDelete && len(p) == 1) || (method == http.MethodPost && len(p) == 2 && p[1] == "uninstall"))
}

// Keep the original endpoint's separate operations and partial-failure state:
// installed list -> API -> public jobs -> clone data/code -> folders -> stop.
func (a *App) originalPluginUninstallHTTP(r *http.Request, path string) (any, error) {
	ctx := context.WithoutCancel(r.Context())
	instance := strings.Split(path, "/")[0]
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	if _, err := a.Store.DB.ExecContext(ctx, `UPDATE plugin_installations SET installed=0 WHERE instance_id=?`, instance); err != nil {
		return nil, err
	}
	a.pluginRegisteredAPIs.Delete(instance)
	if _, err := a.Store.DB.ExecContext(ctx, `UPDATE plugin_schedules SET active=0,updated_at=? WHERE instance_id=? AND group_key='original_services'`, now(), instance); err != nil {
		return nil, err
	}
	retained := true
	if value, ok := a.pluginOriginalClasses.Load(instance); ok {
		class := value.(originalPluginClass)
		if class.isClone {
			if _, err := a.Store.DB.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='config'`, instance); err != nil {
				return nil, err
			}
			if err := a.pluginStore.deleteOriginalPluginData(ctx, instance); err != nil {
				return nil, err
			}
			retained = false
			var removeErr error
			if _, _, compiled := builtin.Lookup(class.installation.PluginID); !compiled {
				// Package roots are owned by the artifact manager; deleting this copied code
				// does not remove configuration data paths or source/other instance packages.
				removeErr = os.RemoveAll(filepath.Join(a.pluginPackageRoot(), instance))
			}
			if removeErr == nil {
				removeErr = a.writeOriginalCodeAlias(ctx, class.installation, false)
			}
			if removeErr != nil {
				// Original rmtree failures are logged; the endpoint still removes folders
				// and stops the object, while the retained code may load again later.
				a.log("ERROR", "plugins", "remove_code_failed", "删除插件分身代码别名失败", "", "")
			} else {
				a.pluginOriginalClasses.Delete(instance)
			}
		}
	}
	if err := a.removeOriginalPluginFromFolders(ctx, instance); err != nil {
		a.log("ERROR", "plugins", "remove_folder_failed", "从文件夹中移除插件时出错", "", "")
	}
	if err := a.stopOriginalPlugin(ctx, instance); err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "uninstalled": true, "data_retained": retained}, nil
}

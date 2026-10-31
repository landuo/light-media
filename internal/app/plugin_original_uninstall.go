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
	a.pluginOriginalOperationMu.Lock()
	defer a.pluginOriginalOperationMu.Unlock()
	if !a.admitOriginalBackground() {
		return nil, &APIError{"APP_STOPPING", "服务正在停止", http.StatusServiceUnavailable}
	}
	defer a.wg.Done()
	if err := a.setOriginalPluginInstalled(ctx, instance, false); err != nil {
		return nil, err
	}
	a.unregisterOriginalPluginAPIs(instance)
	a.removeOriginalPluginJobs(instance)
	retained := true
	if value, ok := a.pluginOriginalClasses.Load(instance); ok {
		class := value.(originalPluginClass)
		if class.isClone {
			a.pluginMu.Lock()
			_, deleteErr := a.Store.DB.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='config'`, instance)
			if deleteErr == nil {
				a.invalidateOriginalBaseConfig(instance)
			}
			a.pluginMu.Unlock()
			if deleteErr != nil {
				return nil, deleteErr
			}
			if err := a.pluginStore.deleteOriginalPluginData(ctx, instance); err != nil {
				return nil, err
			}
			retained = false
			var removeErr error
			if _, _, compiled := builtin.Lookup(class.installation.PluginID); class.external || !compiled {
				// A legacy catalog name can now have a real standard artifact.
				// Use the imported implementation identity, not that historical name,
				// while retaining the compiled-alias-only branch's existing behavior.
				// Package roots are owned by the artifact manager; deleting this copied code
				// does not remove configuration data paths or source/other instance packages.
				a.pluginImplementationMu.Lock()
				removeErr = os.RemoveAll(filepath.Join(a.pluginPackageRoot(), instance))
				a.pluginImplementationMu.Unlock()
			}
			if removeErr == nil {
				removeErr = a.writeOriginalCodeAlias(ctx, class.installation, false)
			}
			if removeErr != nil {
				// Original rmtree failures are logged; the endpoint still removes folders
				// and stops the object, while the retained code may load again later.
				a.log("ERROR", "plugins", "remove_code_failed", "删除插件分身代码别名失败", "", "")
			} else {
				a.forgetOriginalPluginClass(instance)
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

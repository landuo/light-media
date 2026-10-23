package app

import (
	"context"
	"light-media/internal/plugins/builtin"
	"net/http"
	"strconv"
	"strings"
)

func originalPluginInstallRoute(path, method string) bool {
	parts := strings.Split(path, "/")
	return method == http.MethodPost && path == "install" || method == http.MethodGet && len(parts) == 2 && parts[0] == "install" && pluginStorageID.MatchString(parts[1])
}

// The existing-code branch of the original install endpoint. Repository code
// acquisition is deliberately not faked by changing the stored version label.
// The Go runtime currently executes only handlers compiled into its executable.
func (a *App) originalPluginInstallHTTP(w http.ResponseWriter, r *http.Request, path string) (any, error) {
	var input struct {
		PluginID       string `json:"plugin_id"`
		Repository     string `json:"repo_url"`
		ReleaseVersion string `json:"release_version"`
		Force          bool   `json:"force"`
	}
	if r.Method == http.MethodPost {
		if err := decodePluginBody(w, r, &input); err != nil {
			return nil, err
		}
	} else {
		input.PluginID = strings.Split(path, "/")[1]
		input.Repository = r.URL.Query().Get("repo_url")
		input.ReleaseVersion = r.URL.Query().Get("release_version")
		if raw := r.URL.Query().Get("force"); raw != "" {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				return nil, bad("INVALID_PLUGIN_JSON", "force必须为布尔值")
			}
			input.Force = value
		}
	}
	if !pluginStorageID.MatchString(input.PluginID) {
		return nil, bad("INVALID_PLUGIN_ID", "插件标识无效")
	}
	ctx := context.WithoutCancel(r.Context())
	a.pluginLifecycleMu.Lock()
	defer a.pluginLifecycleMu.Unlock()
	_, hasClass := a.pluginOriginalClasses.Load(input.PluginID)
	// The existing Go market POST selects code already bundled in this server.
	// It is a local compiled-artifact transport, not a claim that a Python
	// repository was downloaded. The original GET keeps its class/repo rules.
	bundled := false
	if r.Method == http.MethodPost && !hasClass && !input.Force && input.Repository == "" {
		manifest, _, exists := builtin.Lookup(input.PluginID)
		if exists && manifest.PluginID == input.PluginID {
			if input.ReleaseVersion != "" && input.ReleaseVersion != manifest.Version {
				return map[string]any{"success": false, "message": "指定版本不是当前已打包的Go插件制品"}, nil
			}
			if _, err := a.originalDirectoryInstallation(ctx, input.PluginID); err != nil {
				return nil, err
			}
			bundled = true
		}
	}
	if input.Force || (!hasClass && !bundled) {
		if input.Repository == "" {
			return map[string]any{"success": false, "message": "没有传入仓库地址，无法正确安装插件，请检查配置"}, nil
		}
		return map[string]any{"success": false, "message": "当前Go运行时尚未实现仓库插件制品下载和加载，未安装或替换插件代码"}, nil
	}
	if input.Repository != "" {
		// The original branch checks repository compatibility before registration.
		// Until that real acquisition metadata exists, do not assert compatibility.
		return map[string]any{"success": false, "message": "当前Go运行时尚未实现此仓库的插件兼容性检查，未改动已安装列表"}, nil
	}
	// release_version is not consumed by the original already-present-class,
	// non-force branch. Configuration is neither synthesized nor default-filled.
	if _, err := a.Store.DB.ExecContext(ctx, `UPDATE plugin_installations SET installed=1 WHERE instance_id=?`, input.PluginID); err != nil {
		return nil, err
	}
	if _, err := a.reloadOriginalPlugin(ctx, input.PluginID); err != nil {
		return nil, err
	}
	a.registerOriginalPlugin(ctx, input.PluginID)
	return map[string]any{"success": true}, nil
}

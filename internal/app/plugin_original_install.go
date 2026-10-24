package app

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins/artifacts"
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
// Only a verified standard package can be started; legacy native adapters are unavailable.
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
	a.pluginOriginalOperationMu.Lock()
	defer a.pluginOriginalOperationMu.Unlock()
	_, hasClass := a.pluginOriginalClasses.Load(input.PluginID)
	if _, _, native := builtin.Lookup(input.PluginID); native {
		return map[string]any{"success": false, "message": "插件尚未适配新的标准模块，旧执行框架已停用"}, nil
	}
	if input.Force || !hasClass {
		if input.Repository == "" {
			return map[string]any{"success": false, "message": "没有传入仓库地址，无法正确安装插件，请检查配置"}, nil
		}
		if acquireErr := a.acquireOriginalImplementation(ctx, input.PluginID, input.Repository, input.ReleaseVersion, input.Force); acquireErr != nil {
			return map[string]any{"success": false, "message": "插件制品获取失败: " + acquireErr.Error()}, nil
		}

		if _, reloadErr := a.reloadOriginalPlugin(ctx, input.PluginID); reloadErr != nil {
			return nil, reloadErr
		}
		a.registerOriginalPlugin(ctx, input.PluginID)
		return map[string]any{"success": true}, nil
	}
	if input.Repository != "" {
		if _, _, native := builtin.Lookup(input.PluginID); native {
			return map[string]any{"success": false, "message": "此仓库契约不能替换已编译业务宿主"}, nil
		}
		if _, inspectErr := artifacts.InspectCompatible(ctx, artifacts.Request{RepositoryURL: input.Repository, PluginID: input.PluginID, Validate: validatePluginArtifactContract}, &http.Client{}); inspectErr != nil {
			return map[string]any{"success": false, "message": "插件兼容性检查失败: " + inspectErr.Error()}, nil
		}
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

// Only the owned code publication and its trusted version binding are serialized
// against active instance calls. Reload runs after releasing this lock.
func (a *App) acquireOriginalImplementation(ctx context.Context, id, repository, release string, force bool) error {
	a.pluginImplementationMu.Lock()
	defer a.pluginImplementationMu.Unlock()
	// This bounded contract cannot replace compiled native business code. Reject
	// known unsupported targets before requesting any repository metadata.
	if _, _, native := builtin.Lookup(id); native {
		return bad("PLUGIN_HOST_CONTRACT_UNSUPPORTED", "当前独立制品契约不能替换已编译业务宿主")
	}

	acquired, err := artifacts.Acquire(ctx, artifacts.Request{Root: a.pluginPackageRoot(), RepositoryURL: repository, PluginID: id, ReleaseVersion: release, Force: force, Validate: validatePluginArtifactContract}, &http.Client{})
	if err != nil {
		return err
	}
	raw, err := json.Marshal(acquired.Manifest)
	if err != nil {
		return err
	}
	in, err := validatePluginInstallation(PluginInstallation{InstanceID: id, PluginID: acquired.Manifest.PluginID, Version: acquired.Manifest.Version, ManifestSchemaVersion: acquired.Manifest.SchemaVersion, Manifest: raw, DesiredEnabled: true})
	if err != nil {
		return err
	}
	return a.pluginStore.Install(ctx, in)
}

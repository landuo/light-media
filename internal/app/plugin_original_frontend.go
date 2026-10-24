package app

import (
	"context"
	"reflect"
	"slices"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
)

// This descriptor is a projection of the loaded instance and verified package,
// never repository metadata or a worker-selected URL. EntryPath is API-relative
// so the web application applies its own root or HTTPS mount exactly once.
type originalFrontendDescriptor struct {
	InstanceID    string   `json:"instance_id"`
	PluginID      string   `json:"plugin_id"`
	Version       string   `json:"version"`
	PackageSHA256 string   `json:"package_sha256"`
	Contract      string   `json:"contract"`
	Exposes       []string `json:"exposes"`
	Shared        []string `json:"shared"`
	APIContract   string   `json:"api_contract"`
	EntryPath     string   `json:"entry_path"`
}

// Caller holds pluginImplementationMu.RLock. No worker method is called under
// this lock: a pending updater must not cause a recursive RWMutex read deadlock.
func (a *App) originalFrontendLocked(ctx context.Context, instance string) (*originalPluginInstance, artifacts.Acquired, error) {
	missing := func() (*originalPluginInstance, artifacts.Acquired, error) {
		return nil, artifacts.Acquired{}, &APIError{"PLUGIN_FRONTEND_UNAVAILABLE", "插件界面不存在或未加载", 404}
	}
	if !pluginStorageID.MatchString(instance) {
		return missing()
	}
	loaded, ok := a.pluginRunningInstances.Load(instance)
	if !ok {
		return missing()
	}
	object := loaded.(*originalPluginInstance)
	if !object.external || object.manifest.Frontend == nil || object.frontendPackageSHA256 == "" {
		return missing()
	}
	in, err := a.pluginStore.Installation(ctx, instance)
	if err != nil {
		return nil, artifacts.Acquired{}, err
	}
	bound := object.installation
	if bound.InstanceID != instance || bound.PluginID != in.PluginID || bound.Version != in.Version || bound.Generation != in.Generation || !originalObjectStillLoaded(object) {
		return nil, artifacts.Acquired{}, &APIError{"PLUGIN_FRONTEND_STALE", "插件界面版本已变化，请重新加载", 409}
	}
	installed, err := artifacts.ReadInstalled(a.pluginPackageRoot(), instance, func(m plugins.Manifest, contract string) error {
		if m.PluginID != in.PluginID || m.Version != in.Version || contract != object.hostContract || !reflect.DeepEqual(m, object.manifest) {
			return bad("PLUGIN_IMPLEMENTATION_MISMATCH", "实例与实际插件制品不一致")
		}
		return validatePluginArtifactContract(m, contract)
	})
	if err != nil || installed.PackageSHA256 != object.frontendPackageSHA256 || !originalObjectStillLoaded(object) {
		// Do not publish filesystem paths or detailed validation errors to callers.
		return nil, artifacts.Acquired{}, &APIError{"PLUGIN_FRONTEND_INVALID", "插件界面制品校验失败", 409}
	}
	return object, installed, nil
}

func frontendDescriptor(instance string, installed artifacts.Acquired) originalFrontendDescriptor {
	f := installed.Manifest.Frontend
	return originalFrontendDescriptor{
		InstanceID: instance, PluginID: installed.Manifest.PluginID, Version: installed.Manifest.Version,
		PackageSHA256: installed.PackageSHA256, Contract: f.Contract, Exposes: slices.Clone(f.Exposes),
		Shared: slices.Clone(f.Shared), APIContract: f.APIContract,
		EntryPath: "/plugins/" + instance + "/frontend/" + installed.PackageSHA256 + "/assets/" + f.Entry,
	}
}

func (a *App) originalPluginFrontend(ctx context.Context, instance string) (any, error) {
	loaded, ok := a.pluginRunningInstances.Load(instance)
	if !ok || !pluginStorageID.MatchString(instance) {
		return nil, &APIError{"PLUGIN_FRONTEND_UNAVAILABLE", "插件界面不存在或未加载", 404}
	}
	object := loaded.(*originalPluginInstance)
	if !object.external || object.manifest.Frontend == nil {
		return nil, &APIError{"PLUGIN_FRONTEND_UNAVAILABLE", "插件尚未提供标准 Vue 界面", 404}
	}
	mode, dist, _, err := object.originalViewRenderMode(ctx)
	if err != nil {
		return nil, err
	}
	if mode != "vue" || dist != object.manifest.Frontend.Root {
		return nil, &APIError{"PLUGIN_FRONTEND_UNAVAILABLE", "插件渲染模式与已安装界面不一致", 409}
	}
	a.pluginImplementationMu.RLock()
	defer a.pluginImplementationMu.RUnlock()
	current, installed, err := a.originalFrontendLocked(ctx, instance)
	if err != nil {
		return nil, err
	}
	if current != object {
		return nil, &APIError{"PLUGIN_FRONTEND_STALE", "插件界面版本已变化，请重新加载", 409}
	}
	return frontendDescriptor(instance, installed), nil
}

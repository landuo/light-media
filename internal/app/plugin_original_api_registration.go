package app

import (
	"reflect"
	"slices"

	"light-media/internal/plugins"
)

// One immutable registration owns the exact object whose get_api produced its
// routes. The registry is route authority; this lifetime reference is not HTTP
// authentication or a HostCaller grant. Manager.remove does not unregister it.
type originalAPIRegistration struct {
	object       *originalPluginInstance
	installation PluginInstallation
	routes       []plugins.HTTPAPI
	owner        *originalRetainedLease
}

func cloneOriginalAPIRoutes(routes []plugins.HTTPAPI) []plugins.HTTPAPI {
	out := slices.Clone(routes)
	for i := range out {
		out[i].Methods = slices.Clone(out[i].Methods)
		out[i].Query = slices.Clone(out[i].Query)
		out[i].Headers = slices.Clone(out[i].Headers)
	}
	return out
}

// Both refresh and explicit uninstall remove the same registry owner. Dropping
// the last reference can reap a process, so release is always outside APIMu.
func (a *App) unregisterOriginalPluginAPIs(instance string) {
	a.replaceOriginalAPIRegistration(instance, nil)
}

func (a *App) replaceOriginalAPIRegistration(instance string, registration *originalAPIRegistration) {
	a.pluginOriginalAPIMu.Lock()
	var previous any
	var existed bool
	if registration == nil {
		previous, existed = a.pluginRegisteredAPIs.LoadAndDelete(instance)
	} else {
		previous, existed = a.pluginRegisteredAPIs.Swap(instance, registration)
	}
	a.pluginOriginalAPIMu.Unlock()
	if existed {
		old := previous.(*originalAPIRegistration)
		old.object.releaseOriginalRetainedOwner(old.owner)
	}
}

// Selection and the temporary exact-object pin are one short registry/lifetime
// critical section. All routing/auth/DB/RPC work and release happen outside it.
func (a *App) selectOriginalAPIRegistration(instance string) (*originalAPIRegistration, *originalRetainedLease, error) {
	a.pluginOriginalAPIMu.Lock()
	defer a.pluginOriginalAPIMu.Unlock()
	value, ok := a.pluginRegisteredAPIs.Load(instance)
	if !ok {
		return nil, nil, bad("PLUGIN_API_SCOPE_DENIED", "插件路由已变化")
	}
	registration := value.(*originalAPIRegistration)
	owner, err := registration.owner.lifetime.retain(originalRetainedOperation, registration.owner)
	if err != nil {
		return nil, nil, err
	}
	return registration, owner, nil
}

func originalAPIInstallationMatches(bound, current PluginInstallation) bool {
	return bound.PluginID == current.PluginID && bound.InstanceID == current.InstanceID &&
		bound.Version == current.Version && bound.Generation == current.Generation
}

func (a *App) currentOriginalAPIRegistration(registration *originalAPIRegistration, in PluginInstallation, api plugins.HTTPAPI) error {
	if registration == nil || registration.object == nil {
		return bad("PLUGIN_API_SCOPE_DENIED", "插件路由已变化")
	}
	if !originalAPIInstallationMatches(registration.installation, in) ||
		!originalAPIInstallationMatches(registration.object.installation, in) {
		return bad("PLUGIN_SNAPSHOT_STALE", "插件实例已变化")
	}
	a.pluginOriginalAPIMu.Lock()
	defer a.pluginOriginalAPIMu.Unlock()
	current, ok := a.pluginRegisteredAPIs.Load(in.InstanceID)
	if !ok || current != registration {
		return bad("PLUGIN_API_SCOPE_DENIED", "插件路由已变化")
	}
	for _, route := range registration.routes {
		if reflect.DeepEqual(route, api) {
			return nil
		}
	}
	return bad("PLUGIN_API_SCOPE_DENIED", "插件路由已变化")
}

// Already registered routes never fall back to disk or create another object.
// The original session path still performs its current DB generation check.
func resolveOriginalRegisteredAPI(registration *originalAPIRegistration, in PluginInstallation) (pluginImplementation, error) {
	if registration == nil || registration.object == nil ||
		!originalAPIInstallationMatches(registration.installation, in) ||
		!originalAPIInstallationMatches(registration.object.installation, in) {
		return pluginImplementation{}, bad("PLUGIN_SNAPSHOT_STALE", "插件实例已变化")
	}
	object := registration.object
	if object.stopping.Load() {
		return pluginImplementation{}, bad("PLUGIN_NOT_LOADED", "插件对象正在停止")
	}
	if object.external {
		object.sessionMu.Lock()
		loaded := object.session != nil
		object.sessionMu.Unlock()
		if !loaded {
			return pluginImplementation{}, bad("PLUGIN_NOT_LOADED", "插件实例尚未加载")
		}
	}
	return pluginImplementation{Manifest: object.manifest, External: object.external, HostContract: object.hostContract}, nil
}

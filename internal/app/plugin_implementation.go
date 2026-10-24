package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"light-media/internal/plugins/builtin"
)

// This contract has no host capabilities. Repository metadata never grants a
// native owner operation or relaxes an existing compiled business version pin.
const pluginPureSDKContract = "light-media.sdk-pure/v1"
const pluginBaseSDKContract = "light-media.sdk-base/v1"
const pluginResourcesSDKContract = "light-media.sdk-resources/v1"

type pluginImplementation struct {
	Manifest     plugins.Manifest
	Command      plugins.Command
	Digest       string
	External     bool
	HostContract string
}

func validatePluginArtifactContract(m plugins.Manifest, contract string) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if contract != pluginPureSDKContract && contract != pluginBaseSDKContract && contract != pluginResourcesSDKContract {
		return bad("PLUGIN_HOST_CONTRACT_UNSUPPORTED", "插件制品声明的宿主契约尚不受支持")
	}
	for _, capability := range m.Capabilities {
		if !originalStandardCapabilityAllowed(contract, capability) {
			return bad("PLUGIN_HOST_CONTRACT_UNSUPPORTED", "插件宿主能力不属于已知契约")
		}
	}
	if _, _, native := builtin.Lookup(m.PluginID); native {
		return bad("PLUGIN_HOST_CONTRACT_UNSUPPORTED", "不能用独立制品替换未匹配的已编译业务宿主")
	}
	for _, method := range []string{"init", "get_state", "get_service", "get_command", "stop"} {
		if !slices.Contains(m.Methods, method) {
			return bad("PLUGIN_HOST_CONTRACT_UNSUPPORTED", "插件制品缺少实例生命周期方法")
		}
	}
	return nil
}
func (a *App) pluginPackageRoot() string { return filepath.Join(a.Dir, "plugin-packages") }

// resolvePluginImplementation is shared by lifecycle and execution. A present
// but invalid artifact is an error, never a reason to silently run other code.
func (a *App) resolvePluginImplementation(ctx context.Context, in PluginInstallation) (pluginImplementation, error) {
	_ = ctx
	_, codeErr := os.Lstat(filepath.Join(a.pluginPackageRoot(), in.InstanceID))
	codePresent := codeErr == nil
	if codeErr != nil && !errors.Is(codeErr, os.ErrNotExist) {
		return pluginImplementation{}, codeErr
	}
	acquired, err := artifacts.ReadInstalled(a.pluginPackageRoot(), in.InstanceID, validatePluginArtifactContract)
	if err == nil {
		if acquired.Manifest.PluginID != in.PluginID || acquired.Manifest.Version != in.Version {
			return pluginImplementation{}, bad("PLUGIN_IMPLEMENTATION_MISMATCH", "实例与实际插件制品版本不一致")
		}
		return pluginImplementation{Manifest: acquired.Manifest, Command: acquired.Command, Digest: acquired.SHA256, External: true, HostContract: acquired.HostContract}, nil
	}
	if codePresent || !errors.Is(err, os.ErrNotExist) {
		return pluginImplementation{}, err
	}
	if _, _, native := builtin.Lookup(in.PluginID); native {
		return pluginImplementation{}, bad("PLUGIN_ADAPTER_UNAVAILABLE", "插件尚未适配新的标准模块")
	}
	return pluginImplementation{}, bad("PLUGIN_IMPLEMENTATION_UNAVAILABLE", "当前版本没有该插件的标准可执行实现")
}

// An already loaded object survives an unsuccessful force acquisition, just as
// the original running registry survives removal of its code directory. This
// path never starts a process: new starts still resolve and verify disk bytes.
func (a *App) resolvePluginInvocation(ctx context.Context, in PluginInstallation) (pluginImplementation, error) {
	var object *originalPluginInstance
	if call, ok := ctx.Value(originalInitializingObjectKey{}).(*originalInitializationCall); ok && call != nil {
		object = call.object
	}
	if object == nil {
		if value, ok := a.pluginRunningInstances.Load(in.InstanceID); ok {
			object = value.(*originalPluginInstance)
		}
	}
	if object != nil && object.external {
		bound := object.installation
		if bound.InstanceID != in.InstanceID || bound.PluginID != in.PluginID || bound.Version != in.Version || bound.Generation != in.Generation {
			return pluginImplementation{}, bad("PLUGIN_SNAPSHOT_STALE", "插件调用对象已经被替换")
		}
		object.sessionMu.Lock()
		loaded := object.session != nil
		object.sessionMu.Unlock()
		if loaded {
			return pluginImplementation{Manifest: object.manifest, External: true, HostContract: object.hostContract}, nil
		}
	}
	return a.resolvePluginImplementation(ctx, in)
}

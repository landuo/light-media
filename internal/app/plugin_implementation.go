package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"light-media/internal/plugins"
	"light-media/internal/plugins/artifacts"
	"light-media/internal/plugins/builtin"
)

// This contract has no host capabilities. Repository metadata never grants a
// native owner operation, including when its ID matches a legacy catalog item.
const pluginPureSDKContract = "light-media.sdk-pure/v1"
const pluginBaseSDKContract = "light-media.sdk-base/v1"
const pluginResourcesSDKContract = "light-media.sdk-resources/v1"

type pluginImplementation struct {
	Manifest      plugins.Manifest
	Command       plugins.Command
	Digest        string
	PackageSHA256 string // complete installed frontend package identity, not a lifecycle gate
	External      bool
	HostContract  string
	// Builtin marks a compiled-in plugin: the session is an in-process
	// handler, Command is unused, and the manifest identity comes from the
	// linked catalog rather than a disk receipt.
	Builtin bool
	// builtinConcurrent preserves the standalone worker's serve mode: serial
	// classes expect the app's method lock, exactly like the legacy worker.
	builtinConcurrent bool
	newInProcess      func() plugins.InProcessHandler
	frontendReader    artifacts.FrontendReader
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
	// Command/service declarations are optional source getters, not lifecycle
	// methods. Their absence must be checked before discovery reads state.
	for _, method := range []string{"init", "get_state", "stop"} {
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
	metadata, metadataErr := a.pluginStore.instanceMetadata(ctx, a.Store.reader(), in.InstanceID)
	if metadataErr != nil {
		return pluginImplementation{}, metadataErr
	}
	if metadata.SourceInstanceID != "" && !plugins.OriginalPluginClassLoadable(in.InstanceID) {
		return pluginImplementation{}, bad("PLUGIN_CLASS_IMPORT_FAILED", "复制的插件类无法按原实例名称导入")
	}
	if implementation, ok := a.resolveBuiltinImplementation(ctx, in, metadata); ok {
		return implementation, nil
	}
	_, codeErr := os.Lstat(filepath.Join(a.pluginPackageRoot(), in.InstanceID))
	codePresent := codeErr == nil
	if codeErr != nil && !errors.Is(codeErr, os.ErrNotExist) {
		return pluginImplementation{}, codeErr
	}
	// A fresh reader verifies every byte before a process can start. The
	// loaded object can reuse that verification when opening its frontend.
	var frontendReader artifacts.FrontendReader
	acquired, err := frontendReader.ReadInstalled(a.pluginPackageRoot(), in.InstanceID, validatePluginArtifactContract)
	if err == nil && acquired.Manifest.Frontend != nil && in.InstanceID != in.PluginID && acquired.FrontendClass != in.InstanceID {
		acquired, err = artifacts.DeriveInstalledFrontend(ctx, a.pluginPackageRoot(), in.InstanceID, validatePluginArtifactContract)
	}
	if err == nil {
		if acquired.Manifest.PluginID != in.PluginID || acquired.Manifest.Version != in.Version {
			return pluginImplementation{}, bad("PLUGIN_IMPLEMENTATION_MISMATCH", "实例与实际插件制品版本不一致")
		}
		return pluginImplementation{Manifest: acquired.Manifest, Command: acquired.Command, Digest: acquired.SHA256, PackageSHA256: acquired.PackageSHA256, External: true, HostContract: acquired.HostContract, frontendReader: frontendReader}, nil
	}
	if codePresent || !errors.Is(err, os.ErrNotExist) {
		return pluginImplementation{}, err
	}
	if _, _, native := builtin.Lookup(in.PluginID); native {
		return pluginImplementation{}, bad("PLUGIN_ADAPTER_UNAVAILABLE", "插件尚未适配新的标准模块")
	}
	return pluginImplementation{}, bad("PLUGIN_IMPLEMENTATION_UNAVAILABLE", "当前版本没有该插件的标准可执行实现")
}

// resolveBuiltinImplementation resolves a compiled-in plugin class. The
// catalog's manifest is authoritative for identity; an installed package on
// disk contributes only its frontend assets, and any version drift is
// tolerated because startup adoption aligns installation rows to the catalog.
func (a *App) resolveBuiltinImplementation(ctx context.Context, in PluginInstallation, metadata PluginInstanceMetadata) (pluginImplementation, bool) {
	if a.pluginBuiltinDisabled || pluginBuiltinDisabled(os.LookupEnv) {
		return pluginImplementation{}, false
	}
	if metadata.SourceInstanceID != "" {
		// Clone instances keep the disk path until clones are cataloged too.
		return pluginImplementation{}, false
	}
	catalog, err := builtinCatalogLoad()
	if err != nil {
		a.log("ERROR", "plugins", "builtin_catalog_failed", err.Error(), "", "")
		return pluginImplementation{}, false
	}
	entry, ok := catalog[in.PluginID]
	if !ok || entry.manifest.Version != in.Version {
		return pluginImplementation{}, false
	}
	var frontendReader artifacts.FrontendReader
	if acquired, err := (frontendReader).ReadInstalled(a.pluginPackageRoot(), in.InstanceID, func(m plugins.Manifest, contract string) error {
		// Frontend-only read: same class, any version. The backend identity
		// comes from the linked catalog, so receipt drift stays non-fatal.
		if m.PluginID != in.PluginID || contract != pluginResourcesSDKContract {
			return bad("PLUGIN_FRONTEND_MISMATCH", "插件前端包与本体内置版本不一致")
		}
		return nil
	}); err == nil {
		frontendClass := acquired.FrontendClass
		if acquired.Manifest.Frontend != nil && in.InstanceID != in.PluginID && acquired.FrontendClass != in.InstanceID {
			// Clone-class frontend derivation is not reachable here; the
			// class path stays the plain package read.
			frontendClass = acquired.FrontendClass
		}
		_ = frontendClass
		return pluginImplementation{Manifest: entry.manifest, PackageSHA256: acquired.PackageSHA256, External: true, Builtin: true, builtinConcurrent: entry.concurrent, HostContract: pluginResourcesSDKContract, newInProcess: entry.newHandler, frontendReader: frontendReader}, true
	}
	return pluginImplementation{Manifest: entry.manifest, External: true, Builtin: true, builtinConcurrent: entry.concurrent, HostContract: pluginResourcesSDKContract, newInProcess: entry.newHandler}, true
}

// adoptBuiltinVersions aligns persisted installation rows with the compiled-in
// catalog. This is the release train's data plane: shipping a light-media
// build with a newer plugin catalog upgrades every matching installation row
// before any object loads, preserving each row's enabled preference.
func (a *App) adoptBuiltinVersions(ctx context.Context) {
	catalog, err := builtinCatalogLoad()
	if err != nil {
		return
	}
	ids, err := a.originalInstalledPluginIDs(ctx)
	if err != nil {
		return
	}
	for _, instance := range ids {
		in, err := a.pluginStore.Installation(ctx, instance)
		if err != nil || !in.Installed {
			continue
		}
		entry, ok := catalog[in.PluginID]
		if !ok || entry.manifest.Version == in.Version {
			continue
		}
		raw, marshalErr := json.Marshal(entry.manifest)
		if marshalErr != nil {
			continue
		}
		if err := a.pluginStore.Install(ctx, PluginInstallation{
			InstanceID: in.InstanceID, PluginID: in.PluginID, Version: entry.manifest.Version,
			ManifestSchemaVersion: entry.manifest.SchemaVersion, Manifest: raw,
			DesiredEnabled: in.DesiredEnabled,
		}); err != nil {
			a.log("ERROR", "plugins", "builtin_adoption_failed", "内置插件版本对齐失败："+in.PluginID+" "+err.Error(), "", "")
			continue
		}
		a.log("INFO", "plugins", "builtin_adopted", "内置插件已随光影版本对齐："+in.PluginID+" "+in.Version+" -> "+entry.manifest.Version, "", "")
	}
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
			return pluginImplementation{Manifest: object.manifest, PackageSHA256: object.frontendPackageHash(), External: true, HostContract: object.hostContract}, nil
		}
	}
	return a.resolvePluginImplementation(ctx, in)
}

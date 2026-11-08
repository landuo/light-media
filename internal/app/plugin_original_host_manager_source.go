package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"light-media/internal/plugins"
)

// An actual initializer owns this private lease. Continuations inherit this
// exact pointer only while the real initializer is live, never a method name,
// payload field, reused object ID, or a transfer of the parent's deadline.
func (p *originalPluginInstance) beginOriginalInitialization(ctx context.Context, configurationSave bool) (context.Context, func()) {
	call := &originalInitializationCall{object: p, configurationSave: configurationSave}
	call.active.Store(true)
	return context.WithValue(ctx, originalInitializingObjectKey{}, call), func() {
		call.active.Store(false)
		if call.retire.Load() && !originalObjectStillLoaded(p) {
			p.finishOriginalInitialization(false)
		}
	}
}
func originalLiveInitialization(ctx context.Context, p *originalPluginInstance) *originalInitializationCall {
	call, _ := ctx.Value(originalInitializingObjectKey{}).(*originalInitializationCall)
	if call != nil && call.object == p && call.active.Load() {
		return call
	}
	return nil
}
func (a *App) sourceRemoveOriginalPlugin(ctx context.Context, instance string) error {
	loaded, ok := a.pluginRunningInstances.Load(instance)
	if !ok {
		a.forgetOriginalPluginClass(instance)
		return nil
	}
	p := loaded.(*originalPluginInstance)
	if call := originalLiveInitialization(ctx, p); call != nil {
		// Source invokes close -> stop_service before popping its maps. Calling the
		// generic BeginShutdown here would cancel the initializer carrying this RPC.
		p.configMu.Lock()
		p.eventsEnabled = false
		p.configMu.Unlock()
		p.sessionMu.Lock()
		client := p.session
		p.sessionMu.Unlock()
		if client == nil || !client.SupportsConcurrent() {
			return bad("PLUGIN_CONCURRENT_REQUIRED", "自身移除需要实际并发初始化调用")
		}
		for _, method := range []string{"close", "stop_service"} {
			if !slices.Contains(p.manifest.Methods, method) {
				continue
			}
			phaseCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			phaseCtx = context.WithValue(phaseCtx, originalBaseStopKey{}, &originalBaseStopCall{object: p, client: client, businessHook: true})
			callback, revoke := p.originalBaseSessionHost(phaseCtx, "stop", nil)
			_, err := client.CallWithOptions(phaseCtx, method, json.RawMessage(`{}`), callback, plugins.CallOptions{CallerDeadlineOnly: true})
			revoke()
			cancel()
			if err != nil {
				a.log("ERROR", "plugins", "stop_method_failed", "插件停止方法失败", "", "")
				break
			}
		}
		a.pluginOriginalRunningMu.Lock()
		a.forgetOriginalPluginClassLocked(instance)
		a.pluginRunningInstances.Delete(instance)
		a.pluginOriginalRunningOrder = slices.DeleteFunc(a.pluginOriginalRunningOrder, func(id string) bool { return id == instance })
		a.pluginOriginalRunningMu.Unlock()
		call.retire.Store(true)
		// True initializer return and the existing exact-object submission join own
		// physical transport retirement. No source config/data/API/folder purge.
		return nil
	}
	return a.stopOriginalPlugin(ctx, instance)
}
func (a *App) sourceOriginalCode(ctx context.Context, instance, action string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Authenticated manager IDs select only one immediate, host-owned package.
	// Instance packages are already independent for real clones.
	if !pluginStorageID.MatchString(instance) {
		return nil, bad("INVALID_PLUGIN_ID", "插件实例ID无效")
	}
	a.pluginImplementationMu.Lock()
	defer a.pluginImplementationMu.Unlock()
	path := filepath.Join(a.pluginPackageRoot(), instance)
	if action == "code.exists" {
		_, err := os.Stat(path) // source pathlib.exists follows symlinks; stat errors propagate.
		if sourceOriginalPathAbsent(err) {
			return json.RawMessage(`false`), nil
		}
		if err != nil {
			return nil, err
		}
		return json.RawMessage(`true`), nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return json.RawMessage(`null`), nil
	}
	if err != nil {
		return json.RawMessage(`null`), nil
	} // rmtree(ignore_errors=True)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return json.RawMessage(`null`), nil
	}
	_ = os.RemoveAll(path) // finite own package; ordinary FS failures are source-suppressed.
	return json.RawMessage(`null`), nil
}

// Captured Python3.12 pathlib._ignore_error: permissions and I/O failures are
// not absence. Canonical manager IDs separately reject invalid path material.
func sourceOriginalPathAbsent(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.EBADF) || errors.Is(err, syscall.ELOOP)
}

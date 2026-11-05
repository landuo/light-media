package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"light-media/internal/plugins"
)

func originalHostManagerCapability(method string) bool {
	return method == plugins.PluginManagerReadCapability || method == plugins.PluginManagerControlCapability
}

func (a *App) originalHostManagerResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	var request plugins.PluginManagerRequest
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	if request.InstanceID != "" && !pluginStorageID.MatchString(request.InstanceID) {
		return nil, bad("INVALID_PLUGIN_ID", "插件实例ID无效")
	}
	if method == plugins.PluginManagerReadCapability {
		if request.Action != "" || len(request.Config) > 0 || request.Force || request.JobID != "" {
			return nil, bad("INVALID_PLUGIN_VALUE", "状态读取不能指定操作")
		}
		list := []PluginInstallation{}
		switch request.View {
		case "", "installed":
			ids, err := a.originalInstalledPluginIDs(ctx)
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				installation, err := a.pluginStore.Installation(ctx, id)
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return nil, err
				}
				if errors.Is(err, sql.ErrNoRows) {
					installation = PluginInstallation{InstanceID: id, Installed: true}
				}
				list = append(list, installation)
			}
		case "running":
			list = []PluginInstallation{}
			for _, object := range a.originalLoadedPluginSnapshot() {
				list = append(list, object.installation)
			}
		case "classes":
			list = []PluginInstallation{}
			a.pluginOriginalRunningMu.Lock()
			ids := append([]string{}, a.pluginOriginalClassOrder...)
			a.pluginOriginalRunningMu.Unlock()
			for _, id := range ids {
				if value, ok := a.pluginOriginalClasses.Load(id); ok {
					list = append(list, value.(originalPluginClass).installation)
				}
			}
		default:
			return nil, bad("INVALID_PLUGIN_VALUE", "插件列表类型无效")
		}
		result := []plugins.PluginHostState{}
		for _, installation := range list {
			if request.InstanceID != "" && installation.InstanceID != request.InstanceID {
				continue
			}
			if err := scope.validate(ctx); err != nil {
				return nil, err
			}
			state := plugins.PluginHostState{InstanceID: installation.InstanceID, PluginID: installation.PluginID, Version: installation.Version, Installed: installation.Installed}
			if loaded, ok := a.pluginRunningInstances.Load(installation.InstanceID); ok {
				state.Loaded = true
				if request.IncludeState {
					if loaded == scope.object {
						if err := originalHostRequireConcurrent(scope); err != nil {
							return nil, err
						}
					}
					enabled, err := loaded.(*originalPluginInstance).getState(ctx)
					if err != nil {
						return nil, err
					}
					state.Enabled = &enabled
				}
			} else if request.IncludeState {
				enabled := false
				state.Enabled = &enabled
			}
			result = append(result, state)
		}
		if err := scope.validate(ctx); err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	if method != plugins.PluginManagerControlCapability || request.InstanceID == "" {
		return nil, bad("INVALID_PLUGIN_VALUE", "管理操作需要明确实例ID")
	}
	if request.IncludeState || request.View != "" || len(request.Config) > 0 && request.Action != "init" || request.JobID != "" && request.Action != "services.remove" || request.Force && request.Action != "config.delete" && request.Action != "data.delete" {
		return nil, bad("INVALID_PLUGIN_VALUE", "插件管理参数与操作不匹配")
	}
	if len(request.Config) > 0 {
		var config map[string]json.RawMessage
		if json.Unmarshal(request.Config, &config) != nil || config == nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "插件初始化配置必须为对象")
		}
	}
	switch request.Action {
	case "init", "stop", "reload", "uninstall", "services.refresh", "services.remove", "config.delete", "data.delete":
	default:
		return nil, bad("INVALID_PLUGIN_VALUE", "插件管理操作无效")
	}
	if request.InstanceID == scope.installation.InstanceID && (request.Action == "init" || request.Action == "stop" || request.Action == "reload" || request.Action == "uninstall") {
		// A separate executable cannot synchronously stop the process carrying
		// this response. Admission is reported explicitly, and execution waits
		// for the invoking SDK phase to close; it is never called completed.
		if !a.admitOriginalBackground() {
			return nil, bad("APP_STOPPING", "服务正在停止")
		}
		go func() {
			defer a.wg.Done()
			deadline := time.NewTimer(30 * time.Second)
			defer deadline.Stop()
			ticker := time.NewTicker(5 * time.Millisecond)
			defer ticker.Stop()
			for scope.validate(context.Background()) == nil {
				select {
				case <-deadline.C:
					a.log("ERROR", "plugins", "manager_self_call_open", "插件自身管理请求未执行：调用尚未结束", "", "")
					return
				case <-ticker.C:
				}
			}
			if !originalObjectStillLoaded(scope.object) {
				return
			}
			operationCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := a.originalHostManagerControl(operationCtx, request, nil); err != nil {
				a.reportOriginalPluginExecutionError(operationCtx, "system", "插件自身管理操作失败", nil, err)
			}
		}()
		return json.Marshal(plugins.PluginControlResult{Accepted: true, Completed: false})
	}
	var sourceResult json.RawMessage
	if err := a.originalHostManagerControl(ctx, request, &sourceResult); err != nil {
		return nil, err
	}
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	return json.Marshal(plugins.PluginControlResult{Accepted: true, Completed: true, Result: sourceResult})
}

func (a *App) originalHostManagerControl(ctx context.Context, request plugins.PluginManagerRequest, sourceResult *json.RawMessage) error {
	if !a.pluginOriginalOperationMu.TryLock() {
		return bad("PLUGIN_OPERATION_BUSY", "另一插件生命周期操作正在执行，请重试")
	}
	defer a.pluginOriginalOperationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	setResult := func(value string) {
		if sourceResult != nil {
			*sourceResult = json.RawMessage(value)
		}
	}
	setResult("null")
	switch request.Action {
	case "uninstall":
		if !a.admitOriginalBackground() {
			return bad("APP_STOPPING", "服务正在停止")
		}
		defer a.wg.Done()
		_, err := a.uninstallOriginalPlugin(ctx, request.InstanceID)
		return err
	case "init":
		if len(request.Config) > 0 {
			loaded, ok := a.pluginRunningInstances.Load(request.InstanceID)
			if !ok {
				return nil
			}
			object := loaded.(*originalPluginInstance)
			unlock := object.lockOriginalMethod()
			call := &originalInitializationCall{object: object}
			_, err := object.callSession(context.WithValue(ctx, originalInitializingObjectKey{}, call), plugins.Runner{Timeout: 10 * time.Second}, "init", request.Config, nil)
			unlock()
			if err != nil {
				return err
			}
			object.configMu.Lock()
			object.appliedConfig = append(json.RawMessage(nil), request.Config...)
			object.configMu.Unlock()
			return a.updateOriginalPluginEventState(ctx, object)
		}
		return a.initOriginalLoadedPlugin(ctx, request.InstanceID)
	case "stop":
		return a.stopOriginalPlugin(ctx, request.InstanceID)
	case "reload":
		_, err := a.reloadOriginalPlugin(ctx, request.InstanceID)
		return err
	case "services.refresh":
		a.updateOriginalPluginJobs(ctx, request.InstanceID)
		return ctx.Err()
	case "services.remove":
		if request.JobID != "" {
			state := &a.pluginOriginalDirectJobs
			state.mu.Lock()
			defer state.mu.Unlock()
			for key, job := range state.jobs {
				if strings.SplitN(job.timerID, "|", 2)[0] == request.JobID {
					delete(state.jobs, key)
				}
			}
			return nil
		}
		a.removeOriginalPluginJobs(request.InstanceID)
		return nil
	case "config.delete":
		setResult("false")
		if _, ok := a.pluginOriginalClasses.Load(request.InstanceID); !ok && !request.Force {
			return nil
		}
		a.pluginMu.Lock()
		defer a.pluginMu.Unlock()
		_, err := a.Store.DB.ExecContext(ctx, `DELETE FROM plugin_values WHERE instance_id=? AND kind='config'`, request.InstanceID)
		if err == nil {
			a.invalidateOriginalBaseConfig(request.InstanceID)
			setResult("true")
		}
		return err
	case "data.delete":
		setResult("false")
		if _, ok := a.pluginOriginalClasses.Load(request.InstanceID); !ok && !request.Force {
			return nil
		}
		err := a.pluginStore.deleteOriginalPluginData(ctx, request.InstanceID)
		if err == nil {
			setResult("true")
		}
		return err
	}
	return bad("INVALID_PLUGIN_VALUE", "插件管理操作无效")
}

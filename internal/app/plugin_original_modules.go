package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"light-media/internal/plugins"
	"slices"
)

// The original manager keys modules by (instance ID, display name). A slice
// preserves running-object insertion order, including independently named clones.
// SDK declarations replace each bound callable with an executable method name;
// the manager retains the actual object binding and checks its manifest on use.
type originalPluginModuleGroup struct {
	PluginID   string          `json:"plugin_id"`
	PluginName string          `json:"plugin_name"`
	Modules    json.RawMessage `json:"modules"`
	object     *originalPluginInstance
}

func (a *App) originalPluginModules(ctx context.Context, pid string) ([]originalPluginModuleGroup, error) {
	groups := []originalPluginModuleGroup{}
	for _, object := range a.originalLoadedPluginSnapshot() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id := object.installation.InstanceID
		if pid != "" && pid != id || !slices.Contains(object.manifest.Methods, "get_module") {
			continue
		}
		enabled, err := object.getState(ctx)
		if err == nil && !enabled {
			continue
		}
		var raw json.RawMessage
		if err == nil {
			result, callErr := object.callOriginalMethod(ctx, "get_module", nil)
			raw, err = result.Result, callErr
		}
		var name string
		if err == nil {
			name, err = object.originalDisplayName(ctx)
		}
		if err != nil {
			a.logOriginalPluginContext(ctx, "ERROR", "module_declaration_failed", "读取插件模块声明失败")
			continue
		}
		// get_module() or [] still creates the original manager's empty group.
		if !originalViewJSONTruth(raw) {
			raw = json.RawMessage(`[]`)
		}
		groups = append(groups, originalPluginModuleGroup{PluginID: id, PluginName: name, Modules: append(json.RawMessage(nil), raw...), object: object})
	}
	return groups, ctx.Err()
}

// This is the plugin half of ChainBase's module dispatcher, not a replacement
// core provider. The caller passes the module result onward to its real core
// implementation. JSON null means continue; lists concatenate only other lists;
// every other JSON value (including false, 0, "", {}) stops the chain.
//
// Tuples use the explicit SDK envelope; [null, null] remains a list. Arbitrary
// Python model instances and callable objects are not transported as Go values.
func (a *App) invokeOriginalPluginModules(ctx context.Context, method string, initial, args, kwargs json.RawMessage) (json.RawMessage, error) {
	return a.invokeOriginalPluginModulesObserved(ctx, method, initial, args, kwargs, nil)
}

type originalModuleAttempts struct{ Called, Failed int }

func (a *App) invokeOriginalPluginModulesObserved(ctx context.Context, method string, initial, args, kwargs json.RawMessage, attempts *originalModuleAttempts) (json.RawMessage, error) {
	return a.invokeOriginalPluginModuleGroups(ctx, method, initial, args, kwargs, attempts, nil)
}

// Native hosts may need their source-shaped arguments only when a matching
// provider exists. Reuse that exact discovery frame: querying get_state and
// get_module again would add source-visible getter side effects.
func (a *App) invokeOriginalPluginModuleGroups(ctx context.Context, method string, initial, args, kwargs json.RawMessage, attempts *originalModuleAttempts, groups []originalPluginModuleGroup) (json.RawMessage, error) {
	return a.invokeOriginalPluginModuleGroupsWithTitleFilter(ctx, method, initial, args, kwargs, attempts, groups, nil)
}

func (a *App) invokeOriginalPluginModuleGroupsWithTitleFilter(ctx context.Context, method string, initial, args, kwargs json.RawMessage, attempts *originalModuleAttempts, groups []originalPluginModuleGroup, title *originalTitleFilterState) (json.RawMessage, error) {
	result := append(json.RawMessage(nil), initial...)
	if len(result) == 0 {
		result = json.RawMessage(`null`)
	}
	if len(args) == 0 {
		args = json.RawMessage(`[]`)
	}
	if len(kwargs) == 0 {
		kwargs = json.RawMessage(`{}`)
	}
	var positional []json.RawMessage
	var keywords map[string]json.RawMessage
	if method == "" || !json.Valid(result) || json.Unmarshal(args, &positional) != nil || positional == nil || json.Unmarshal(kwargs, &keywords) != nil || keywords == nil {
		return result, bad("PLUGIN_MODULE_INVALID", "插件模块名称、结果或参数无效")
	}
	input, err := json.Marshal(map[string]json.RawMessage{"args": args, "kwargs": kwargs})
	if err != nil {
		return result, err
	}
	if groups == nil {
		groups, err = a.originalPluginModules(ctx, "")
		if err != nil {
			return result, err
		}
	}
	images, _ := ctx.Value(originalMediaImagesStateKey{}).(*originalMediaImagesState)
	if method != "async_obtain_images" && method != "obtain_images" {
		images = nil
	}
	raise := originalViewJSONTruth(keywords["raise_exception"])
	for _, group := range groups {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		var bindings map[string]json.RawMessage
		if json.Unmarshal(group.Modules, &bindings) != nil {
			continue
		}
		binding, exists := bindings[method]
		if !exists || !originalViewJSONTruth(binding) {
			continue
		}
		if title != nil {
			if method != "filter_torrents" {
				return result, bad("PLUGIN_MODULE_INVALID", "输入修改仅属于当前普通标题过滤调用")
			}
			result, err = title.resultJSON()
			if err != nil {
				return result, err
			}
		}
		var accumulated []json.RawMessage
		isEmpty := originalModuleResultEmpty(result)
		isList := !isEmpty && json.Unmarshal(result, &accumulated) == nil
		if !isEmpty && !isList {
			break
		}
		if title != nil {
			input, err = title.callJSON()
			if err != nil {
				return result, err
			}
		}
		if images != nil {
			input, err = images.input(args, keywords)
			if err != nil {
				return result, err
			}
		}
		var handler string
		var returned json.RawMessage
		var titleReturn originalTitleFilterValue
		if json.Unmarshal(binding, &handler) != nil || !slices.Contains(group.object.manifest.Methods, handler) {
			err = bad("PLUGIN_MODULE_INVALID", "插件模块方法未在实际制品中声明")
		} else {
			if attempts != nil {
				attempts.Called++
			}
			value, callErr := group.object.callOriginalMethod(ctx, handler, input)
			returned, err = value.Result, callErr
			if err == nil && images != nil {
				returned, err = images.returned(returned)
			}
			if err == nil && title != nil {
				titleReturn, err = title.decodeReturn(returned)
				current, currentErr := title.resultJSON()
				if currentErr != nil {
					return result, currentErr
				}
				result = current // valid mutation precedes a reported callback failure
			}
		}
		if err != nil {
			if errors.Is(err, plugins.ErrModuleRateLimited) {
				if raise {
					return result, err
				}
				a.logOriginalPluginContext(ctx, "INFO", "module_rate_limited", "插件模块已限流，跳过执行")
				continue
			}
			if attempts != nil {
				attempts.Failed++
			}
			if raise {
				return result, err
			}
			a.logOriginalPluginContext(ctx, "ERROR", "module_execution_failed", "插件模块执行失败")
			a.reportOriginalPluginExecutionError(ctx, "plugin", "插件模块执行失败", map[string]any{"type": "plugin", "plugin_id": group.PluginID, "plugin_name": group.PluginName, "plugin_method": method}, err)
			continue
		}
		if title != nil {
			title.merge(titleReturn)
			result, err = title.resultJSON()
			if err != nil {
				return result, err
			}
			continue
		}
		if isEmpty {
			result = append(json.RawMessage(nil), returned...)
			if len(result) == 0 {
				result = json.RawMessage(`null`)
			}
		} else {
			var additional []json.RawMessage
			if !bytes.Equal(bytes.TrimSpace(returned), []byte("null")) && json.Unmarshal(returned, &additional) == nil {
				accumulated = append(accumulated, additional...)
				result, err = json.Marshal(accumulated)
				if err != nil {
					return result, err
				}
			}
		}
	}
	return result, nil
}

func originalModuleGroupsHaveMethod(groups []originalPluginModuleGroup, method string) bool {
	for _, group := range groups {
		var bindings map[string]json.RawMessage
		if json.Unmarshal(group.Modules, &bindings) == nil && originalViewJSONTruth(bindings[method]) {
			return true
		}
	}
	return false
}

func originalModuleResultEmpty(raw json.RawMessage) bool {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true
	}
	items, tuple := plugins.DecodeModuleResultTuple(raw)
	if !tuple {
		return false
	}
	for _, item := range items {
		if !bytes.Equal(bytes.TrimSpace(item), []byte("null")) {
			return false
		}
	}
	return true
}

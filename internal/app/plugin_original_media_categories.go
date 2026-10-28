package app

import (
	"context"
	"encoding/json"
)

// ChainBase.media_category -> run_module -> media/category. This is the list
// used by the existing rule editor, not the policy used to classify a title.
func (a *App) originalMediaCategories(ctx context.Context) (json.RawMessage, error) {
	result, err := a.invokeOriginalPluginModules(ctx, "media_category", nil, nil, nil)
	if err != nil {
		return nil, err
	}
	var list []json.RawMessage
	empty := originalModuleResultEmpty(result)
	if empty || json.Unmarshal(result, &list) == nil && string(result) != "null" {
		policy, nativeErr := a.currentMediaCategoryPolicy()
		if nativeErr != nil {
			a.reportOriginalPluginExecutionError(ctx, "system", "TheMovieDb发生了错误", map[string]any{"type": "module", "module_id": "TheMovieDbModule", "module_name": "TheMovieDb", "module_method": "media_category"}, nativeErr)
		} else if empty {
			categories := map[string][]string{"电影": {}, "电视剧": {}}
			for kind, title := range map[string]string{"movie": "电影", "tv": "电视剧"} {
				for _, rule := range policy[kind] {
					categories[title] = append(categories[title], rule.name)
				}
			}
			result, _ = json.Marshal(categories)
		}
		// A prior list survives a non-list native result, including a dict.
	}
	if !originalViewJSONTruth(result) {
		return json.RawMessage(`{}`), nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(result, &object) != nil || object == nil {
		return nil, &APIError{"PLUGIN_CATEGORY_INVALID", "插件媒体分类结果不是字典", 500}
	}
	return result, nil
}

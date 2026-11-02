package app

import (
	"context"
	"encoding/json"
	"strings"
)

// Command's plugin table overlays its presets. This is reached only when no
// plugin has declared the command; it does not introduce a public method/event
// dispatcher. Other presets remain explicitly unavailable at the Web ingress.
func (a *App) dispatchOriginalPresetCommand(ctx context.Context, name string, input originalCommandEvent) (bool, error) {
	if name != "/clear_cache" {
		return a.dispatchOriginalSystemCommand(ctx, name, input)
	}
	// EventManager has already accepted this command. Disconnecting the Web
	// request must not cancel the source's separately dispatched command. The
	// ordinary SDK runner still owns its finite per-call timeout.
	ctx = context.WithoutCancel(ctx)
	// Like Command.execute, a command exception goes to MessageHelper instead
	// of escaping its event consumer. Module exceptions are handled separately
	// below, so later modules and the completion notification still run.
	if err := a.clearOriginalModuleCaches(ctx); err != nil {
		a.reportOriginalPluginExecutionError(ctx, "system", "执行命令 /clear_cache 出错", nil, err)
		return true, nil
	}
	_, err := a.postOriginalPluginMessage(ctx, map[string]any{
		"channel": input.Channel, "source": input.Source, "userid": input.User,
		"title": "缓存清理完成！", "save_history": false,
	})
	if err != nil {
		a.reportOriginalPluginExecutionError(ctx, "system", "执行命令 /clear_cache 出错", nil, err)
	}
	return true, nil
}

// ChainBase.clear_cache -> run_module("clear_cache"). False, zero, empty
// strings/objects are results, not fallbacks. None/all-None tuples continue;
// lists invoke every no-argument native module and retain their list result.
func (a *App) clearOriginalModuleCaches(ctx context.Context) error {
	result, err := a.invokeOriginalPluginModules(ctx, "clear_cache", nil, nil, nil)
	if err != nil {
		return err
	}
	if !originalModuleResultEmpty(result) {
		var list []json.RawMessage
		if json.Unmarshal(result, &list) != nil || list == nil {
			return nil
		}
	}
	// These are the native provider-owned cache representations in this host,
	// in the source modules' priority order. Fanart/TVDB have no retained cache
	// here: do not invent disk stores or touch other providers on their behalf.
	// RecommendChain's outer cache is NOT cleared by its modules. Neither are
	// image files, the AniList translation dictionary, site/plugin caches,
	// credentials, configuration, or any durable business record.
	for _, provider := range []struct {
		id, name string
		prefixes []string
	}{
		{"TheMovieDbModule", "TheMovieDb", []string{"browse:", "search:", "directory-identify:", "media:", "season:", "source-detail:", "source-search:", "source-person:", "source-credits:", "explore:v1:themoviedb:", "explore-detail:v1:themoviedb:"}},
		{"DoubanModule", "豆瓣", []string{"explore:v1:douban:", "explore-detail:v1:douban:"}},
		{"BangumiModule", "Bangumi", []string{"explore:v1:bangumi:", "explore-detail:v1:bangumi:"}},
		{"AniListModule", "AniList", []string{"explore:v1:anilist:", "explore-detail:v1:anilist:"}},
	} {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := a.clearOriginalProviderCache(ctx, provider.prefixes); err != nil {
			a.reportOriginalPluginExecutionError(ctx, "system", provider.name+"发生了错误", map[string]any{
				"type": "module", "module_id": provider.id, "module_name": provider.name, "module_method": "clear_cache",
			}, err)
		}
	}
	return nil
}

func (a *App) clearOriginalProviderCache(ctx context.Context, prefixes []string) error {
	conditions := make([]string, len(prefixes))
	args := make([]any, len(prefixes))
	for i, prefix := range prefixes {
		// Fixed host-owned prefixes only. GLOB is case-sensitive, unlike SQLite
		// LIKE, and there is no plugin-supplied SQL, path, region or pattern.
		conditions[i], args[i] = "id GLOB ?", prefix+"*"
	}
	_, err := a.Store.DB.ExecContext(ctx, "DELETE FROM records WHERE kind='cache' AND ("+strings.Join(conditions, " OR ")+")", args...)
	return err
}

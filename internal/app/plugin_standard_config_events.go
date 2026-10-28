package app

import (
	"context"
	"slices"
	"strings"
)

type originalPluginSettingValuesKey struct{}

// The original /setting/{key} and Wiki endpoints publish each saved value;
// /env bulk updates publish only the changed key set. Keep those call paths
// distinct while reusing the existing settings transaction and publication.
func (a *App) saveOriginalPluginSettings(ctx context.Context, settings Settings) error {
	return a.saveSettings(context.WithValue(ctx, originalPluginSettingValuesKey{}, true), settings)
}

// These are the original environment-setting names, not database field names.
// Bulk updates carry changed keys only. The original single-setting paths
// carry their committed plugin-setting values. Both run after publication;
// credential changes publish names only, never their saved values.
func (a *App) emitStandardPluginSettingsChanged(ctx context.Context, before, after Settings) {
	keys := []string{}
	if !slices.Equal(before.PluginMarkets, after.PluginMarkets) {
		keys = append(keys, "PLUGIN_MARKET")
	}
	if !slices.Equal(before.PluginLocalRepoPaths, after.PluginLocalRepoPaths) {
		keys = append(keys, "PLUGIN_LOCAL_REPO_PATHS")
	}
	if before.PluginAutoReload != after.PluginAutoReload {
		keys = append(keys, "PLUGIN_AUTO_RELOAD")
	}
	if originalPluginStatisticShared(before) != originalPluginStatisticShared(after) {
		keys = append(keys, "PLUGIN_STATISTIC_SHARE")
	}
	if before.GitHubProxy != after.GitHubProxy {
		keys = append(keys, "GITHUB_PROXY")
	}
	if before.GitHubToken != after.GitHubToken {
		keys = append(keys, "GITHUB_TOKEN")
	}
	if before.RepoGitHubToken != after.RepoGitHubToken {
		keys = append(keys, "REPO_GITHUB_TOKEN")
	}
	if len(keys) != 0 {
		if single, _ := ctx.Value(originalPluginSettingValuesKey{}).(bool); single {
			values := map[string]any{
				"PLUGIN_MARKET":           strings.Join(after.PluginMarkets, ","),
				"PLUGIN_LOCAL_REPO_PATHS": strings.Join(after.PluginLocalRepoPaths, ","),
				"PLUGIN_AUTO_RELOAD":      after.PluginAutoReload,
				"PLUGIN_STATISTIC_SHARE":  originalPluginStatisticShared(after),
				"GITHUB_PROXY":            after.GitHubProxy,
				"GITHUB_TOKEN":            nil,
				"REPO_GITHUB_TOKEN":       nil,
			}
			for _, key := range keys {
				a.emitStandardBusinessEvent(ctx, "config.updated", map[string]any{"key": []string{key}, "value": values[key], "change_type": "update"})
			}
			return
		}
		a.emitStandardBusinessEvent(ctx, "config.updated", map[string]any{"key": keys, "value": nil, "change_type": "update"})
	}
}

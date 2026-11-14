package app

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"light-media/internal/plugins/artifacts"
)

// Sources are one section of the existing encrypted application Settings.
// There is no second plugin configuration store or installation side effect.
func (a *App) pluginMarketsHTTP(w http.ResponseWriter, r *http.Request) (any, error) {
	if r.Method == http.MethodGet {
		settings := a.Settings()
		sources := settings.PluginMarkets
		if sources == nil {
			sources = []string{}
		}
		local := settings.PluginLocalRepoPaths
		if local == nil {
			local = []string{}
		}
		return map[string]any{"plugin_markets": sources, "plugin_markets_locked": a.pluginMarketOverride != nil, "plugin_local_repo_paths": local, "plugin_auto_reload": settings.PluginAutoReload, "plugin_server_url": settings.PluginServerURL, "plugin_statistic_share": originalPluginStatisticShared(settings), "github_proxy": settings.GitHubProxy, "github_token": settings.GitHubToken, "repo_github_token": settings.RepoGitHubToken, "github_token_set": settings.GitHubToken != "", "repo_github_token_set": settings.RepoGitHubToken != ""}, nil
	}
	if r.Method != http.MethodPut {
		return nil, bad("METHOD_NOT_ALLOWED", "请求方法不支持")
	}
	var input struct {
		Markets              *[]string `json:"plugin_markets"`
		LocalRoots           *[]string `json:"plugin_local_repo_paths"`
		AutoReload           *bool     `json:"plugin_auto_reload"`
		ServerURL            *string   `json:"plugin_server_url"`
		StatisticShare       *bool     `json:"plugin_statistic_share"`
		GitHubProxy          *string   `json:"github_proxy"`
		GitHubToken          *string   `json:"github_token"`
		RepoGitHubToken      *string   `json:"repo_github_token"`
		ClearGitHubToken     bool      `json:"clear_github_token"`
		ClearRepoGitHubToken bool      `json:"clear_repo_github_token"`
	}
	if err := decodePluginBody(w, r, &input); err != nil {
		return nil, err
	}
	if input.Markets != nil && a.pluginMarketOverride != nil && !slices.Equal(*input.Markets, *a.pluginMarketOverride) {
		return nil, bad("PLUGIN_MARKET_ENV_OVERRIDE", "仓库由 PLUGIN_MARKET 环境变量设置，请修改启动环境后重启")
	}
	if input.ServerURL != nil && strings.TrimSpace(*input.ServerURL) != "" {
		if _, err := originalPluginServerURL(*input.ServerURL); err != nil {
			return nil, err
		}
	}
	sources := []string{}
	var requestedSources []string
	if input.Markets != nil {
		requestedSources = *input.Markets
	}
	for _, raw := range requestedSources {
		raw = strings.TrimSpace(raw)
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
			return nil, bad("INVALID_PLUGIN_REPOSITORY", "插件仓库地址无效")
		}
		sources = append(sources, raw)
	}
	var localRoots []string
	if input.LocalRoots != nil {
		localRoots = []string{}
		for _, raw := range *input.LocalRoots {
			if strings.TrimSpace(raw) == "" {
				continue
			}
			root, err := artifacts.NormalizeLocalRoot(raw)
			if err != nil {
				return nil, bad("INVALID_PLUGIN_REPOSITORY", "本地仓库路径无效")
			}
			localRoots = append(localRoots, root)
		}
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	settings := a.Settings()
	if input.Markets != nil {
		settings.PluginMarkets = sources
	}
	if input.LocalRoots != nil {
		settings.PluginLocalRepoPaths = localRoots
	}
	if input.AutoReload != nil {
		settings.PluginAutoReload = *input.AutoReload
	}
	if input.ServerURL != nil {
		value := strings.TrimSpace(*input.ServerURL)
		settings.PluginServerURL = &value
	}
	if input.StatisticShare != nil {
		settings.PluginStatisticShare = input.StatisticShare
	}
	if input.GitHubProxy != nil {
		settings.GitHubProxy = strings.TrimSpace(*input.GitHubProxy)
	}
	if input.ClearGitHubToken {
		settings.GitHubToken = ""
	} else if input.GitHubToken != nil && strings.TrimSpace(*input.GitHubToken) != "" {
		settings.GitHubToken = *input.GitHubToken
	}
	if input.ClearRepoGitHubToken {
		settings.RepoGitHubToken = ""
	} else if input.RepoGitHubToken != nil && strings.TrimSpace(*input.RepoGitHubToken) != "" {
		settings.RepoGitHubToken = *input.RepoGitHubToken
	}
	if err := validateOriginalGitHubSettings(settings); err != nil {
		return nil, err
	}
	var err error
	if input.Markets != nil {
		err = a.saveOriginalPluginSettings(r.Context(), settings)
	} else {
		err = a.saveSettings(r.Context(), settings)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "plugin_markets": settings.PluginMarkets, "plugin_local_repo_paths": settings.PluginLocalRepoPaths, "plugin_auto_reload": settings.PluginAutoReload}, nil
}

func (a *App) originalOnlineMarketDeclarations(ctx context.Context, force bool, observed ...map[string]bool) ([]artifacts.Discovery, []originalMarketSourceStatus) {
	settings := a.Settings()
	out := []artifacts.Discovery{}
	statuses := []originalMarketSourceStatus{}
	results := make([]originalMarketResult, len(settings.PluginMarkets))
	// Fetch concurrently, but consume in configured order so network timing
	// never changes which equal-version repository wins.
	slots := make(chan struct{}, 4)
	var workers sync.WaitGroup
	for i, repository := range settings.PluginMarkets {
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
				results[i] = a.originalMarketSource(ctx, repository, force)
			case <-ctx.Done():
				results[i].status = originalMarketSourceStatus{RepositoryURL: repository, Message: "仓库读取已取消"}
			}
		}()
	}
	workers.Wait()
	for _, result := range results {
		statuses = append(statuses, result.status)
		if !result.status.Success {
			continue
		}
		a.readOriginalMarketPluginStates(ctx, result.discoveries, observed...)
		out = append(out, result.discoveries...)
	}
	return out, statuses
}

func (a *App) originalLocalMarketDeclarations(ctx context.Context, observed ...map[string]bool) ([]artifacts.Discovery, []originalMarketSourceStatus) {
	settings := a.Settings()
	out := []artifacts.Discovery{}
	statuses := []originalMarketSourceStatus{}
	// Local candidates are freshly read and follow configured online repositories
	// so the original equal-version online preference remains deterministic.
	for _, root := range settings.PluginLocalRepoPaths {
		discoveries, err := artifacts.DiscoverLocalRepository(ctx, root, "", validatePluginArtifactContract)
		status := originalMarketSourceStatus{RepositoryURL: root, Success: err == nil, CheckedAt: time.Now().UTC()}
		if err != nil {
			status.Message = "本地仓库索引读取失败或与当前平台不兼容"
		} else {
			if len(discoveries) == 1 {
				status.RepositoryURL = discoveries[0].RepositoryURL
			}
			out = append(out, discoveries...)
		}
		statuses = append(statuses, status)
	}
	selected := selectOriginalMarketDeclarations(out)
	// Fixed get_local_plugin_candidates merges roots before _process_plugin_info;
	// duplicate/local lower-version roots must not repeat a live state getter.
	a.readOriginalMarketPluginStates(ctx, selected, observed...)
	return selected, statuses
}

// Source sync combines online then local; exact-history lookup deliberately
// obtains the local list first and asks only online sources on fallback.
func (a *App) originalMarketDeclarations(ctx context.Context, force bool) ([]artifacts.Discovery, []originalMarketSourceStatus) {
	online, statuses := a.originalOnlineMarketDeclarations(ctx, force)
	local, localStatuses := a.originalLocalMarketDeclarations(ctx)
	return selectOriginalMarketDeclarations(append(online, local...)), append(statuses, localStatuses...)
}

func (a *App) originalPluginMarketHTTP(r *http.Request) (any, error) {
	var force bool
	if raw := r.URL.Query().Get("force"); raw != "" {
		var err error
		force, err = strconv.ParseBool(raw)
		if err != nil {
			return nil, bad("INVALID_PLUGIN_QUERY", "force必须为布尔值")
		}
	}
	_, items, statuses, err := a.originalPluginCatalog(r.Context(), force)
	if err != nil {
		return nil, err
	}
	if r.URL.Query().Get("details") == "true" {
		return map[string]any{"items": items, "sources": statuses}, nil
	}
	return items, nil
}

func (a *App) originalPluginCatalog(ctx context.Context, force bool) ([]pluginCard, []pluginCard, []originalMarketSourceStatus, error) {
	// all_plugins(state=market): local imported classes first, then local
	// repository candidates, then online declarations; selection prefers online
	// on equal versions, independently of the state getter call order.
	locals, err := a.originalLocalPluginCards(ctx, false)
	if err != nil {
		return nil, nil, nil, err
	}
	states := make(map[string]bool)
	localSources, localStatuses := a.originalLocalMarketDeclarations(ctx, states)
	onlineSources, statuses := a.originalOnlineMarketDeclarations(ctx, force, states)
	statuses = append(statuses, localStatuses...)
	if err := ctx.Err(); err != nil {
		return nil, nil, nil, err
	}
	sources := selectOriginalMarketDeclarations(append(onlineSources, localSources...))
	items := a.originalMarketCards(locals, sources, states)
	return locals, items, statuses, nil
}

func (a *App) originalPluginListHTTP(r *http.Request) (any, error) {
	force := false
	if raw := r.URL.Query().Get("force"); raw != "" {
		var err error
		force, err = strconv.ParseBool(raw)
		if err != nil {
			return nil, bad("INVALID_PLUGIN_QUERY", "force必须为布尔值")
		}
	}
	if r.URL.Query().Get("state") == "installed" {
		return a.originalLocalPluginCards(r.Context(), true)
	}
	locals, market, _, err := a.originalPluginCatalog(r.Context(), force)
	if err != nil {
		return nil, err
	}
	if r.URL.Query().Get("state") == "market" {
		return market, nil
	}
	all := []pluginCard{}
	for _, local := range locals {
		if local.Installed {
			all = append(all, local)
		}
	}
	return append(all, market...), nil
}

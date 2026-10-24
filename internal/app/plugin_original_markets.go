package app

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"light-media/internal/plugins/artifacts"
	"light-media/internal/plugins/builtin"
)

// Sources are one section of the existing encrypted application Settings.
// There is no second plugin configuration store or installation side effect.
func (a *App) pluginMarketsHTTP(w http.ResponseWriter, r *http.Request) (any, error) {
	if r.Method == http.MethodGet {
		sources := a.Settings().PluginMarkets
		if sources == nil {
			sources = []string{}
		}
		return map[string]any{"plugin_markets": sources}, nil
	}
	if r.Method != http.MethodPut {
		return nil, bad("METHOD_NOT_ALLOWED", "请求方法不支持")
	}
	var input struct {
		Markets []string `json:"plugin_markets"`
	}
	if err := decodePluginBody(w, r, &input); err != nil {
		return nil, err
	}
	sources := make([]string, 0, len(input.Markets))
	for _, raw := range input.Markets {
		raw = strings.TrimSpace(raw)
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
			return nil, bad("INVALID_PLUGIN_REPOSITORY", "插件仓库地址无效")
		}
		sources = append(sources, raw)
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	settings := a.Settings()
	settings.PluginMarkets = sources
	if err := a.saveSettings(r.Context(), settings); err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "plugin_markets": sources}, nil
}

func (a *App) originalMarketDeclarations(ctx context.Context) []artifacts.Discovery {
	var out []artifacts.Discovery
	for _, repository := range a.Settings().PluginMarkets {
		value, err := artifacts.Discover(ctx, repository, "", validatePluginArtifactContract, &http.Client{Timeout: 30 * time.Second})
		if err != nil {
			a.log("ERROR", "plugins", "market_discovery_failed", "插件仓库元数据读取失败", "", "")
			continue
		}
		out = append(out, value)
	}
	return out
}

// The original listing asks the actual running object's state. Installation
// membership, saved enabled flags and object state are different things.
func (a *App) pluginCards(ctx context.Context, market bool) ([]pluginCard, error) {
	installed, err := a.pluginStore.Installations(ctx)
	if err != nil {
		return nil, err
	}
	items := []pluginCard{}
	seen := map[string]bool{}
	for _, in := range installed {
		seen[in.PluginID] = true
		if market {
			continue
		}
		implementation, resolveErr := a.resolvePluginImplementation(ctx, in)
		manifest := implementation.Manifest
		if resolveErr != nil {
			manifest.PluginID = in.PluginID
			manifest.Name = in.PluginID
			manifest.Version = in.Version
		}
		card, err := a.pluginStore.presentInstance(ctx, in, manifest)
		if err != nil {
			return nil, err
		}
		card.Available = resolveErr == nil && implementation.External
		if !card.Available {
			card.UnavailableReason = "插件尚未适配新的标准模块"
			if resolveErr != nil {
				card.UnavailableReason = "插件代码不可用或标准契约不匹配"
			}
		}
		if loaded, ok := a.pluginRunningInstances.Load(in.InstanceID); ok {
			object := loaded.(*originalPluginInstance)
			if object.external {
				raw, _, e := object.originalViewRead(ctx, "get_state", nil)
				if e == nil {
					card.State = originalViewJSONTruth(raw)
				}
				card.HasPage = slices.Contains(object.manifest.Methods, "get_page")
			} else {
				state, e := object.getState(ctx)
				if e == nil {
					card.State = state
				}
			}
		}
		if implementation.External {
			acquired, e := artifacts.ReadInstalled(a.pluginPackageRoot(), in.InstanceID, validatePluginArtifactContract)
			if e == nil {
				card.RepositoryURL = acquired.RepositoryURL
			}

		}
		items = append(items, card)
	}
	if market {
		for _, manifest := range builtin.Catalog() {
			if !seen[manifest.PluginID] {
				items = append(items, pluginCard{Manifest: manifest, InstanceID: manifest.PluginID, Available: false, UnavailableReason: "插件尚未适配新的标准模块"})
				seen[manifest.PluginID] = true
			}
		}
	}
	for _, source := range a.originalMarketDeclarations(ctx) {
		if market {
			if !seen[source.PluginID] {
				items = append(items, pluginCard{Manifest: source.Current.Manifest, InstanceID: source.PluginID, RepositoryURL: source.RepositoryURL, RepositoryVersion: source.CurrentVersion, Available: true})
				seen[source.PluginID] = true
			}
		} else {
			for n := range items {
				if items[n].PluginID == source.PluginID && items[n].SourceInstanceID == "" && items[n].RepositoryURL == source.RepositoryURL {
					items[n].RepositoryVersion = source.CurrentVersion
					items[n].HasUpdate = items[n].Version != source.CurrentVersion
				}
			}
		}
	}
	return items, nil
}

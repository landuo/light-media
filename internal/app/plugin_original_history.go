package app

import (
	"encoding/json"
	"net/http"
	"strings"

	"light-media/internal/plugins/artifacts"
)

type originalPluginRelease struct {
	Version   string `json:"version"`
	IsCurrent bool   `json:"is_current"`
	IsLatest  bool   `json:"is_latest"`
}

type originalPluginHistory struct {
	SourceDisplayRoute string                        `json:"-"`
	SourceDisplay      *artifacts.PythonDisplay      `json:"-"`
	PluginID           string                        `json:"plugin_id"`
	RepositoryURL      string                        `json:"repo_url"`
	CurrentVersion     string                        `json:"current_version"`
	LatestVersion      string                        `json:"latest_version"`
	History            map[string]string             `json:"history"`
	HistoryJSON        json.RawMessage               `json:"-"`
	ReleaseSupported   bool                          `json:"release_supported"`
	Items              []originalPluginRelease       `json:"items"`
	Rejected           []artifacts.ReleaseDiagnostic `json:"rejected"`
	Source             *originalMarketSourceStatus   `json:"source,omitempty"`
}

func (h originalPluginHistory) MarshalJSON() ([]byte, error) {
	type plain originalPluginHistory
	var current, latest *string
	if h.CurrentVersion != "" {
		current = &h.CurrentVersion
	}
	if h.LatestVersion != "" {
		latest = &h.LatestVersion
	}
	history := artifacts.AuthorHistoryJSON(h.History, h.HistoryJSON)
	if history == nil {
		var err error
		history, err = json.Marshal(h.History)
		if err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal(struct {
		plain
		History        json.RawMessage `json:"history"`
		CurrentVersion *string         `json:"current_version"`
		LatestVersion  *string         `json:"latest_version"`
	}{plain(h), history, current, latest})
	if err != nil {
		return nil, err
	}
	return originalPythonHistoryJSON(h.SourceDisplay, raw, h.SourceDisplayRoute == "releases")
}

func originalPluginHistoryRoute(path, method string) bool {
	parts := strings.Split(path, "/")
	return method == http.MethodGet && len(parts) == 2 && (parts[0] == "history" || parts[0] == "releases")
}

func (a *App) originalPluginHistoryHTTP(r *http.Request, path string) (any, error) {
	parts := strings.Split(path, "/")
	id := parts[1]
	if !pluginStorageID.MatchString(id) {
		return nil, bad("INVALID_PLUGIN_ID", "插件标识无效")
	}
	force, err := originalMarketForceQuery(r, parts[0] == "history")
	if err != nil {
		return nil, err
	}
	out := originalPluginHistory{PluginID: id, History: map[string]string{}, Items: []originalPluginRelease{}, Rejected: []artifacts.ReleaseDiagnostic{}}
	repository := originalMarketQueryString(r, "repo_url")
	if parts[0] == "releases" && repository == "" {
		// plugin_releases returns before asking the manager for a local version.
		return out, nil
	}
	if parts[0] == "history" {
		if err := a.readOriginalLocalPluginStates(r.Context()); err != nil {
			return nil, err
		}
	}
	version, present, err := a.originalLocalPluginVersion(r.Context(), id)
	if err != nil {
		return nil, err
	}
	if parts[0] == "history" && !present {
		return nil, &APIError{"PLUGIN_NOT_INSTALLED", "插件未安装，无法读取已安装版本历史", http.StatusNotFound}
	}
	out.CurrentVersion = version
	if parts[0] == "releases" && artifacts.IsLocalRepository(repository) {
		// Fixed releases calls only the online market/GitHub helper. Local
		// history and local installation are separate operations.
		return out, nil
	}
	var source artifacts.Discovery
	if parts[0] == "history" {
		// The history endpoint has no repo_url parameter in the fixed source.
		// get_local_plugins builds a schema with repo_url=None; first prefer an
		// exact local-repository ID, then the selected online ID. In particular,
		// copied provenance must not redirect a clone to its source's history.
		locals, _ := a.originalLocalMarketDeclarations(r.Context())
		for _, local := range locals {
			if local.PluginID == id {
				source = local
				break
			}
		}
		if source.PluginID == "" {
			candidates, statuses := a.originalOnlineMarketDeclarations(r.Context(), force)
			// Online declarations already passed the original package/repository
			// selector; repeating it could move a replaced ID's insertion position.
			for _, candidate := range candidates {
				if candidate.PluginID == id {
					source = candidate
					break
				}
			}
			for _, status := range statuses {
				if status.RepositoryURL == source.RepositoryURL || (source.PluginID == "" && !status.Success && out.Source == nil) {
					status := status
					out.Source = &status
				}
			}
		}
	} else {
		result := a.originalMarketSource(r.Context(), repository, force)
		out.Source = &result.status
		if !result.status.Success {
			return out, nil
		}
		a.readOriginalMarketPluginStates(r.Context(), result.discoveries)
		for _, entry := range result.discoveries {
			if entry.PluginID == id {
				source = entry
				break
			}
		}
	}
	if source.PluginID != id {
		// Both source endpoints return local/empty metadata when no exact
		// market ID exists. Never substitute another plugin's notes or code.
		return out, nil
	}
	out.RepositoryURL, out.LatestVersion = source.RepositoryURL, source.CurrentVersion
	out.SourceDisplay = source.SourceDisplay
	out.SourceDisplayRoute = parts[0]
	if out.SourceDisplay != nil {
		fields := []string{"history"}
		if parts[0] == "releases" {
			fields = []string{"version"}
		}
		if err := out.SourceDisplay.WireError(fields...); err != nil {
			return nil, originalMarketDisplayFailure()
		}
	}
	if source.History != nil {
		out.History = source.History
		out.HistoryJSON = source.HistoryJSON
	}
	if parts[0] == "history" {
		// Author notes are independent from downloadable versions. The pinned
		// frontend never queries releases or offers a historical-version picker.
		return out, nil
	}
	if !artifacts.IsLocalRepository(repository) && len(source.Releases) > 0 {
		releases := a.originalPluginReleaseSource(r.Context(), repository, force)
		if !releases.status.Success {
			return out, nil
		}
		source = artifacts.Discovery{}
		for _, entry := range releases.discoveries {
			if entry.PluginID == id {
				source = entry
				break
			}
		}
		if source.PluginID == "" {
			return out, nil
		}
	}
	for _, release := range source.Releases {
		version := release.Manifest.Version
		out.Items = append(out.Items, originalPluginRelease{Version: version, IsCurrent: version == out.CurrentVersion, IsLatest: version == out.LatestVersion})
	}
	if source.Rejected != nil {
		out.Rejected = source.Rejected
	}
	out.ReleaseSupported = len(out.Items) > 0
	return out, nil
}

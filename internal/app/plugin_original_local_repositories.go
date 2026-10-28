package app

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"light-media/internal/plugins/artifacts"
)

type originalLocalWatchState struct {
	fingerprints map[string]string
	failures     map[string]string
	roots        []string
}

// MoviePilot watches configured local repositories only in developer/auto
// reload mode. Go artifacts are immutable publications: watch the bounded
// index/package identity, then verify and acquire a fresh owned runtime copy.
// Source edits must be rebuilt before publishing the updated index and package.
func (a *App) runOriginalLocalRepositoryWatcher(ctx context.Context) {
	state := &originalLocalWatchState{}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		a.scanOriginalLocalRepositories(ctx, state)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *App) scanOriginalLocalRepositories(ctx context.Context, state *originalLocalWatchState) []error {
	settings := a.Settings()
	if !settings.PluginAutoReload && !a.pluginDeveloperMode {
		state.fingerprints, state.failures = nil, nil
		state.roots = nil
		return nil
	}
	if !slices.Equal(state.roots, settings.PluginLocalRepoPaths) {
		state.fingerprints, state.failures = nil, nil
		state.roots = slices.Clone(settings.PluginLocalRepoPaths)
	}
	if state.fingerprints == nil {
		state.fingerprints = map[string]string{}
	}
	if state.failures == nil {
		state.failures = map[string]string{}
	}
	var failures []error
	report := func(source string, err error) {
		failures = append(failures, err)
		if state.failures[source] != err.Error() {
			a.log("ERROR", "plugins", "local_reload_failed", "本地插件仓库读取、同步或重载失败，请检查仓库文件与插件状态", "", "")
		}
		state.failures[source] = err.Error()
	}
	var candidates []artifacts.Discovery
	for _, root := range settings.PluginLocalRepoPaths {
		if ctx.Err() != nil {
			return failures
		}
		sources, err := artifacts.DiscoverLocalRepositoryForWatch(ctx, root, "", validatePluginArtifactContract, a.pluginDeveloperMode)
		if err != nil {
			report(root, err)
			continue
		}
		delete(state.failures, root)
		candidates = append(candidates, sources...)
	}
	active := map[string]bool{}
	for _, source := range selectOriginalMarketDeclarations(candidates) {
		active[source.PluginID] = true
		if source.UnavailableReason != "" {
			report(source.RepositoryURL, errors.New(source.UnavailableReason))
			continue
		}
		fingerprint, err := artifacts.LocalFingerprintForWatch(ctx, source.RepositoryURL, "", settings.PluginLocalRepoPaths, validatePluginArtifactContract, a.pluginDeveloperMode)
		if err != nil {
			report(source.RepositoryURL, err)
			continue
		}
		fingerprint = source.RepositoryURL + ":" + fingerprint
		previous, seen := state.fingerprints[source.PluginID]
		state.fingerprints[source.PluginID] = fingerprint
		if !seen || previous == fingerprint {
			continue
		}
		if err := a.reloadOriginalLocalCandidate(ctx, source); err != nil {
			report(source.RepositoryURL, err)
			continue
		}
		delete(state.failures, source.RepositoryURL)
	}
	// A partially written index must not erase the baseline: the next complete
	// publication still needs to be compared with the last readable version.
	if len(failures) == 0 {
		for source := range state.fingerprints {
			if !active[source] {
				delete(state.fingerprints, source)
			}
		}
	}
	return failures
}

func (a *App) reloadOriginalLocalCandidate(ctx context.Context, source artifacts.Discovery) error {
	a.pluginOriginalOperationMu.Lock()
	defer a.pluginOriginalOperationMu.Unlock()
	if !a.admitOriginalBackground() {
		return nil
	}
	defer a.wg.Done()
	if err := ctx.Err(); err != nil {
		return err
	}
	settings := a.Settings()
	if !settings.PluginAutoReload && !a.pluginDeveloperMode {
		return nil
	}
	// Recheck membership while holding the same operation lock as uninstall and
	// clone. A changed local source never installs an uninstalled plugin and never
	// replaces a clone merely because it shares the primary manifest ID.
	in, err := a.pluginStore.Installation(ctx, source.PluginID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !in.Installed {
		return nil
	}
	if err != nil {
		return err
	}
	configured := false
	for _, root := range settings.PluginLocalRepoPaths {
		repo, repoErr := artifacts.LocalRepositoryURL(source.PluginID, root)
		if repoErr == nil && repo == source.RepositoryURL {
			configured = true
			break
		}
	}
	if !configured {
		return errors.New("local repository is no longer configured")
	}
	needsReload, err := a.acquireOriginalImplementationForLocalWatch(ctx, source.PluginID, source.RepositoryURL)
	if err != nil {
		return err
	}
	if !needsReload {
		a.log("INFO", "plugins", "local_resources_synchronized", "本地插件资源已校验并同步，运行对象保持不变", "", "")
		return nil
	}
	object, err := a.reloadOriginalPlugin(ctx, source.PluginID)
	if err != nil {
		return err
	}
	if object == nil {
		return errors.New("local plugin initialization failed after package synchronization")
	}
	a.registerOriginalPlugin(ctx, source.PluginID)
	a.log("INFO", "plugins", "local_reloaded", "本地插件制品已同步并重载", "", "")
	return nil
}

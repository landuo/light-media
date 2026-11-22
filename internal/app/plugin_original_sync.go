package app

import (
	"context"
	"sync"

	"light-media/internal/plugins/artifacts"
)

// is_plugin_exists requires both an actual package and the running object's
// version. Persisted installation metadata alone must not turn failed loading
// or missing code into a successful repair check.
func (a *App) originalPluginCodeMeetsVersion(id, version string) bool {
	a.pluginImplementationMu.RLock()
	defer a.pluginImplementationMu.RUnlock()
	if _, err := artifacts.ReadInstalled(a.pluginPackageRoot(), id, validatePluginArtifactContract); err != nil {
		return false
	}
	loaded, ok := a.pluginRunningInstances.Load(id)
	if !ok {
		return false
	}
	object := loaded.(*originalPluginInstance)
	metadata, err := a.pluginStore.instanceMetadata(context.Background(), a.Store.DB, id)
	if err != nil {
		return false
	}
	localVersion := object.manifest.Version
	if metadata.SourceInstanceID != "" && metadata.DisplayVersion != "" {
		localVersion = metadata.DisplayVersion
	}
	return originalObjectStillLoaded(object) && compareOriginalPluginVersion(localVersion, version) >= 0
}

// PluginManager.sync installs only registered IDs whose actual implementation
// is missing or older than the compatible merged market/local candidate. It
// never installs every marketplace item or substitutes a source-ID release for
// a copied clone. A release declaring the exact clone ID is eligible normally.
// The caller owns pluginOriginalOperationMu, including any subsequent re-init.
func (a *App) syncOriginalPlugins(ctx context.Context) ([]string, error) {
	ids, err := a.originalInstalledPluginIDs(ctx)
	if err != nil {
		return nil, err
	}
	installed := make(map[string]bool, len(ids))
	for _, id := range ids {
		installed[id] = true
	}
	candidates, _ := a.originalMarketDeclarations(ctx, false)
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	pending := []artifacts.Discovery{}
	for _, candidate := range candidates {
		if !installed[candidate.PluginID] || candidate.UnavailableReason != "" {
			continue
		}
		if a.originalPluginCodeMeetsVersion(candidate.PluginID, candidate.CurrentVersion) {
			continue
		}
		pending = append(pending, candidate)
	}
	synced := []string{}
	if len(pending) == 0 {
		return synced, nil
	}
	// Each worker downloads a whole source archive in memory (measured 20.8 MB per
	// in-flight build), so five workers meant a ~104 MB peak on every sync. Two is
	// enough to overlap the network wait without holding that much at once.
	jobs := make(chan artifacts.Discovery)
	var wg sync.WaitGroup
	var results sync.Mutex
	for i := 0; i < min(2, len(pending)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for candidate := range jobs {
				if ctx.Err() != nil {
					continue
				}
				if err := a.acquireOriginalImplementation(ctx, candidate.PluginID, candidate.RepositoryURL, "", true); err != nil {
					a.log("ERROR", "plugins", "sync_failed", "已安装插件制品同步失败，保留其他插件的处理结果", "", "")
					continue
				}
				a.reportOriginalPluginInstallation(ctx, candidate.PluginID, candidate.RepositoryURL)
				results.Lock()
				synced = append(synced, candidate.PluginID)
				results.Unlock()
			}
		}()
	}
	for _, candidate := range pending {
		jobs <- candidate
	}
	close(jobs)
	wg.Wait()
	return synced, ctx.Err()
}

// The original startup first loads installed plugins, then asynchronously syncs
// missing/outdated packages. Any successful sync re-initializes the manager and
// re-registers APIs, public jobs and commands through the same common entry.
// Go dependencies are built into each validated executable; no pip environment
// or Python dependency installer is introduced.
func (a *App) synchronizeOriginalPluginsAtStartup(ctx context.Context) {
	defer a.reportOriginalPluginInstallations(ctx)
	a.pluginOriginalOperationMu.Lock()
	defer a.pluginOriginalOperationMu.Unlock()
	ctx, releaseOperationOwner := a.bindOriginalOperationOwner(ctx)
	defer releaseOperationOwner()
	closed := func() bool {
		a.pluginOriginalBackground.mu.Lock()
		defer a.pluginOriginalBackground.mu.Unlock()
		return a.pluginOriginalBackground.closed
	}
	if ctx.Err() != nil || closed() {
		return
	}
	synced, err := a.syncOriginalPlugins(ctx)
	if err != nil {
		a.log("ERROR", "plugins", "sync_failed", "启动时同步已安装插件失败", "", "")
		return
	}
	if len(synced) == 0 || ctx.Err() != nil || closed() {
		return
	}
	if err = a.initializeOriginalPlugins(ctx); err != nil {
		a.log("ERROR", "plugins", "sync_initialize_failed", "同步后重新初始化插件失败", "", "")
	}
}

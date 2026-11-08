package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync"

	"light-media/internal/plugins"
	native "light-media/internal/plugins/iyuuautoseed/native"
)

// Only in-memory constructor facts are retained, never clients, credentials or
// a worker HostCaller. Configuration fingerprints are not logged or persisted.
type originalSourceConnectionFact struct {
	fingerprint [32]byte
	active      bool
}
type originalSourceConnections struct {
	mu          sync.Mutex
	probeGate   chan struct{}
	initialized bool
	fingerprint [32]byte
	facts       map[string]originalSourceConnectionFact
}

func originalSourceConnectionFingerprint(value any) [32]byte {
	raw, _ := json.Marshal(value)
	return sha256.Sum256(raw)
}
func originalSourceConnectionKind(kind string) string {
	switch kind {
	case "qb", "qbittorrent":
		return "qbittorrent"
	case "tr", "transmission":
		return "transmission"
	}
	return ""
}

// Each kind uses a dict keyed by display name: last value, first insertion
// slot. Native modules initialize all enabled qB wrappers before Transmission.
func originalSourceConnectionConfigurations(configs []DownloaderConfig) []DownloaderConfig {
	out := []DownloaderConfig{}
	for _, kind := range []string{"qbittorrent", "transmission"} {
		slots := map[string]int{}
		for _, config := range configs {
			if !downloaderEnabled(config) || originalSourceConnectionKind(config.Kind) != kind {
				continue
			}
			if index, ok := slots[config.Name]; ok {
				out[index] = config
			} else {
				slots[config.Name] = len(out)
				out = append(out, config)
			}
		}
	}
	return out
}
func (a *App) originalSourceConnectionsGuard(ctx context.Context, fingerprint [32]byte, workerGuard func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if workerGuard != nil {
		if err := workerGuard(ctx); err != nil {
			return err
		}
	}
	if originalSourceConnectionFingerprint(a.Settings().Downloaders) != fingerprint {
		return bad("DOWNLOADER_CHANGED", "下载器配置已变化，请重新调用")
	}
	return nil
}
func (a *App) originalSourceConnectionProbe(ctx context.Context, config DownloaderConfig, fingerprint [32]byte, workerGuard func(context.Context) error) (bool, error) {
	guard := func(ctx context.Context) (err error) {
		defer func() {
			if err != nil {
				err = &originalDownloaderGuardError{cause: err}
			}
		}()
		return a.originalSourceConnectionsGuard(ctx, fingerprint, workerGuard)
	}
	if err := guard(ctx); err != nil {
		return false, err
	}
	kind := originalSourceConnectionKind(config.Kind)
	client, err := native.NewIYUUClient(native.Config{ID: config.ID, Kind: kind, BaseURL: config.BaseURL, Username: config.Username, Password: config.Password}, native.IYUUOptions{APIKey: config.APIKey, Guard: guard})
	if err == nil {
		defer client.Close()
		err = client.SourceConstructorConnected(ctx)
	}
	if current := guard(ctx); current != nil {
		return false, current
	}
	if err != nil && !originalDownloaderNativeError(ctx, err) {
		return false, err
	}
	return err == nil, nil
}
func (a *App) sourceConnectionProbeGate(ctx context.Context) (func(), error) {
	state := &a.pluginOriginalSourceConnections
	state.mu.Lock()
	if state.probeGate == nil {
		state.probeGate = make(chan struct{}, 1)
	}
	gate := state.probeGate
	state.mu.Unlock()
	select {
	case gate <- struct{}{}:
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (a *App) initializeOriginalSourceConnections(ctx context.Context, workerGuard func(context.Context) error) error {
	fingerprint := originalSourceConnectionFingerprint(a.Settings().Downloaders)
	if err := a.originalSourceConnectionsGuard(ctx, fingerprint, workerGuard); err != nil {
		return err
	}
	state := &a.pluginOriginalSourceConnections
	state.mu.Lock()
	ready := state.initialized && state.fingerprint == fingerprint
	state.mu.Unlock()
	if ready {
		return nil
	}
	release, err := a.sourceConnectionProbeGate(ctx)
	if err != nil {
		return err
	}
	defer release()
	return a.updateOriginalSourceConnections(ctx, workerGuard, false)
}

// The gate serializes constructor/reconnect attempts. Network IO never holds
// the facts mutex: a cached active A remains readable while inactive B probes.
func (a *App) updateOriginalSourceConnections(ctx context.Context, workerGuard func(context.Context) error, reconnect bool) error {
	configs := a.Settings().Downloaders
	fingerprint := originalSourceConnectionFingerprint(configs)
	if err := a.originalSourceConnectionsGuard(ctx, fingerprint, workerGuard); err != nil {
		return err
	}
	state := &a.pluginOriginalSourceConnections
	state.mu.Lock()
	rebuild := !state.initialized || state.fingerprint != fingerprint
	previous := state.facts
	state.mu.Unlock()
	if !rebuild && !reconnect {
		return nil
	}
	facts := map[string]originalSourceConnectionFact{}
	for _, config := range originalSourceConnectionConfigurations(configs) {
		fact, exists := previous[config.ID]
		if !rebuild && exists && fact.active {
			facts[config.ID] = fact
			continue
		}
		active, err := a.originalSourceConnectionProbe(ctx, config, fingerprint, workerGuard)
		if err != nil {
			return err
		}
		facts[config.ID] = originalSourceConnectionFact{fingerprint: originalSourceConnectionFingerprint(config), active: active}
	}
	if err := a.originalSourceConnectionsGuard(ctx, fingerprint, workerGuard); err != nil {
		return err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	// A concurrent settings publication may invalidate while a probe is ending.
	if originalSourceConnectionFingerprint(a.Settings().Downloaders) != fingerprint {
		return bad("DOWNLOADER_CHANGED", "下载器配置已变化，请重新调用")
	}
	state.facts = facts
	state.fingerprint = fingerprint
	state.initialized = true
	return nil
}
func (a *App) reloadInitializedOriginalSourceConnections(ctx context.Context, previous, current []DownloaderConfig) {
	if originalSourceConnectionFingerprint(previous) == originalSourceConnectionFingerprint(current) {
		return
	}
	state := &a.pluginOriginalSourceConnections
	state.mu.Lock()
	initialized := state.initialized
	state.initialized = false
	state.mu.Unlock()
	if !initialized {
		return
	}
	_ = a.initializeOriginalSourceConnections(ctx, nil)
}
func (a *App) reconnectOriginalSourceConnections(ctx context.Context) error {
	release, err := a.sourceConnectionProbeGate(ctx)
	if err != nil {
		return err
	}
	defer release()
	return a.updateOriginalSourceConnections(ctx, nil, true)
}
func (a *App) originalHostDownloaderSourceConnection(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var request plugins.DownloaderConnectionRequestV1
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	client, kind, guard, err := a.originalHostDownloaderClient(ctx, scope, request.DownloaderID)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	if err := a.initializeOriginalSourceConnections(ctx, guard); err != nil {
		return nil, err
	}
	configured, err := a.configuredDownloader(request.DownloaderID)
	if err != nil {
		return nil, err
	}
	state := &a.pluginOriginalSourceConnections
	state.mu.Lock()
	fact, exists := state.facts[request.DownloaderID]
	fingerprint := state.fingerprint
	state.mu.Unlock()
	if err := a.originalSourceConnectionsGuard(ctx, fingerprint, guard); err != nil {
		return nil, err
	}
	if exists && fact.fingerprint != originalSourceConnectionFingerprint(configured) {
		return nil, bad("DOWNLOADER_CHANGED", "下载器连接配置已变化，请重新调用")
	}
	return originalHostDownloaderMarshal(plugins.DownloaderSourceConnectionV1{DownloaderConnectionV1: plugins.DownloaderConnectionV1{SchemaVersion: 1, DownloaderID: request.DownloaderID, Kind: kind, Inactive: exists && !fact.active}, Exists: exists})
}

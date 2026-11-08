package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"light-media/internal/systemversion"
)

const originalChatReleaseURL = "https://api.github.com/repos/landuo/light-media/releases"

func (a *App) originalChatVersionText(ctx context.Context) string {
	frontend := a.frontendVersion
	if frontend == "" {
		frontend = appVersion
	}
	// Light-media publishes one release for its bundled frontend and server.
	remote := a.originalChatRelease(ctx)
	return systemversion.Message(remote, remote, appVersion, frontend)
}

func (a *App) originalChatRelease(ctx context.Context) systemversion.Release {
	client, err := a.originalPluginHTTPClient(20*time.Second, false)
	if err != nil {
		return systemversion.Release{}
	}
	defer client.CloseIdleConnections()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, originalChatReleaseURL, nil)
	if err != nil {
		return systemversion.Release{}
	}
	request.Header = originalGitHubHeaders(a.Settings(), "")
	request.Header.Set("Accept", "application/vnd.github+json")
	response, err := client.Do(request)
	if err != nil {
		return systemversion.Release{}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return systemversion.Release{}
	}
	const limit = 4 << 20
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || len(raw) > limit {
		return systemversion.Release{}
	}
	major, _, _ := strings.Cut(appVersion, ".")
	release, err := systemversion.SelectReleaseSeries(raw, "v"+major+".")
	if err != nil || release.Version == nil {
		return systemversion.Release{}
	}
	version := strings.TrimPrefix(*release.Version, "v")
	release.Version = &version
	return release
}

func (a *App) originalChatClearCache(ctx context.Context, reply originalSlashReply) error {
	result, err := a.invokeOriginalPluginModules(ctx, "clear_cache", nil, nil, nil)
	if err != nil {
		return err
	}
	var list []json.RawMessage
	if originalModuleResultEmpty(result) || json.Unmarshal(result, &list) == nil {
		if err := a.clearOriginalChatMediaCaches(ctx); err != nil {
			return err
		}
	}
	return reply.post(ctx, a, "缓存清理完成！", false)
}

func (a *App) clearOriginalChatMediaCaches(ctx context.Context) error {
	cache := &a.originalHostMediaCache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	_, err := a.Store.DB.ExecContext(ctx, `DELETE FROM records WHERE kind='cache' OR (kind='plugin_host_tmdb_cache' AND id='main')`)
	if err != nil {
		return err
	}
	cache.items = map[string]*originalHostMediaCacheEntry{}
	cache.order, cache.clock, cache.dirty = nil, 0, false
	a.originalHostMediaProviderCaches.Range(func(_, value any) bool {
		if provider, ok := value.(*originalHostMediaRecognitionCache); ok {
			provider.mu.Lock()
			provider.items = map[string]*originalHostMediaCacheEntry{}
			provider.order, provider.clock = nil, 0
			provider.mu.Unlock()
		}
		return true
	})
	return nil
}

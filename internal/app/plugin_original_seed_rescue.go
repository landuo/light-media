package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"light-media/internal/plugins"
	iyuu "light-media/internal/plugins/iyuuautoseed"
	sr "light-media/internal/plugins/seedrescue"
)

type originalSeedRescueHTTPKey struct{}

type originalSeedRescueHTTPGrant struct {
	object       *originalPluginInstance
	action, hash string
}

func originalSeedRescueHTTPGrantFor(p *originalPluginInstance, api plugins.HTTPAPI, input plugins.HTTPRequest) *originalSeedRescueHTTPGrant {
	type route struct{ path, method, action string }
	expected := map[string]route{
		"status":   {"/status", http.MethodGet, "get_status"},
		"job":      {"/jobs/{target_hash}", http.MethodGet, "get_job"},
		"scan":     {"/scan", http.MethodPost, "scan_now"},
		"download": {"/jobs/{target_hash}/download", http.MethodPost, "download_candidate"},
		"retry":    {"/jobs/{target_hash}/retry", http.MethodPost, "retry_job"},
		"ignore":   {"/jobs/{target_hash}/ignore", http.MethodPost, "ignore_job"},
	}
	r, ok := expected[api.Key]
	if !ok || api.Path != r.path || input.Method != r.method {
		return nil
	}
	return &originalSeedRescueHTTPGrant{object: p, action: r.action, hash: input.Params["target_hash"]}
}

type originalSeedRescueCall struct {
	action, targetHash string
	applied            json.RawMessage
	config             sr.Configuration
	sites              originalSeedRescueSites
	iyuuOrigin         string
	iyuuMu             sync.Mutex
	iyuuHelper         *iyuu.Helper
	materialMu         sync.Mutex
	material           []originalSeedRescueTorrent
	downloaderMu       sync.Mutex
	downloaderBinding  string
}

type originalSeedRescueTorrent struct {
	digest  [sha256.Size]byte
	content []byte
	site    originalSeedRescueSite
}

func (p *originalPluginInstance) originalSeedRescueCallFor(ctx context.Context, phase string) *originalSeedRescueCall {
	if p.installation.PluginID != sr.PluginID {
		return nil
	}
	var action, target string
	switch phase {
	case "http":
		grant, _ := ctx.Value(originalSeedRescueHTTPKey{}).(*originalSeedRescueHTTPGrant)
		if grant != nil && grant.object == p && p.originalPublicServiceScopeAllowed(ctx) {
			action, target = grant.action, grant.hash
		}
	case "scan":
		service, _ := ctx.Value(originalServiceCallKey{}).(*originalServiceCall)
		if service != nil && service.job != nil && service.job.object == p &&
			service.job.definition.Handler == phase && p.originalPublicServiceScopeAllowed(ctx) {
			action = phase
		}
	case "scan_background":
		lease, _ := ctx.Value(originalSubmittedObjectKey{}).(*originalSubmittedLease)
		if lease != nil && lease.sourceKind == originalSourceAPI &&
			p.originalPrivateSchedulerScopeAllowed(ctx) && p.originalPublicServiceScopeAllowed(ctx) {
			action = phase
		}
	case "get_form", "get_service":
		action = phase
	}
	if action == "" {
		return nil
	}
	p.configMu.RLock()
	raw := copyOriginalBaseJSON(p.appliedConfig)
	p.configMu.RUnlock()
	value, err := decodeSourceJSON(raw)
	if err != nil && len(raw) != 0 {
		return nil
	}
	config := sr.ParseConfig(sourceMap(value))
	secrets := append([]string{config.Token}, config.RSSURLs...)
	p.sourceLogSecrets.remember(secrets)
	return &originalSeedRescueCall{action: action, targetHash: target, applied: raw, config: config,
		iyuuOrigin: p.app.seedRescueIYUUOrigin}
}

func originalSeedRescueCapability(method string) bool {
	switch method {
	case "seed_rescue.options", "seed_rescue.site", "seed_rescue.indexer", "seed_rescue.rss",
		"seed_rescue.iyuu", "seed_rescue.seed_info", "seed_rescue.torrent_url", "seed_rescue.search",
		"seed_rescue.detail", "seed_rescue.download_metadata", "seed_rescue.service",
		"seed_rescue.query_torrents", "seed_rescue.downloading_torrents", "seed_rescue.add_torrent":
		return true
	}
	return false
}

func originalSeedRescueValidate(ctx context.Context, scope originalBaseCallScope) error {
	if err := scope.validate(ctx); err != nil {
		return err
	}
	scope.object.configMu.RLock()
	defer scope.object.configMu.RUnlock()
	if !bytes.Equal(scope.seedRescue.applied, scope.object.appliedConfig) {
		return bad("PLUGIN_SNAPSHOT_STALE", "稀缺保种运行对象或配置已变化")
	}
	return nil
}

func (c *originalSeedRescueCall) allows(operation string) bool {
	switch c.action {
	case "get_form", "get_status":
		return operation == "options"
	case "get_service", "scan_now":
		return operation == "iyuu" || operation == "service"
	case "scan", "scan_background", "download_candidate", "retry_job":
		return true
	}
	return false
}

func (a *App) originalSeedRescueResource(ctx context.Context, scope originalBaseCallScope, operation string, raw json.RawMessage) (json.RawMessage, error) {
	if scope.seedRescue == nil || scope.object == nil || scope.object.app != a ||
		scope.installation.PluginID != sr.PluginID || !scope.seedRescue.allows(operation) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "稀缺保种资源必须绑定到已授权的普通插件入口")
	}
	if err := originalSeedRescueValidate(ctx, scope); err != nil {
		return nil, err
	}
	switch operation {
	case "service", "query_torrents", "downloading_torrents", "add_torrent":
		return a.originalSeedRescueDownloaderResource(ctx, scope, operation, raw)
	}
	return a.originalSeedRescueSiteResource(ctx, scope, operation, raw)
}

func (c *originalSeedRescueCall) projectTorrent(content []byte, site originalSeedRescueSite) ([]byte, error) {
	if len(content) > 8<<20 {
		return nil, bad("PLUGIN_VALUE_TOO_LARGE", "种子超过稀缺保种传输上限")
	}
	value, err := sr.InfoOnlyTorrent(content)
	if err != nil {
		return nil, err
	}
	// Multiple sites can share the same info hash and use different private
	// trackers. Bind each grant to its own bytes outside the hashed info slice.
	grant := id("sr_")
	value = append(value[:len(value)-1], "13:x-seed-rescue"+strconv.Itoa(len(grant))+":"+grant+"e"...)
	digest := sha256.Sum256(value)
	c.materialMu.Lock()
	defer c.materialMu.Unlock()
	if len(c.material) == 8 {
		copy(c.material, c.material[1:])
		c.material = c.material[:7]
	}
	c.material = append(c.material, originalSeedRescueTorrent{digest, bytes.Clone(content), site})
	return value, nil
}

func (c *originalSeedRescueCall) takeTorrent(value []byte) (originalSeedRescueTorrent, bool) {
	digest := sha256.Sum256(value)
	c.materialMu.Lock()
	defer c.materialMu.Unlock()
	for i, grant := range c.material {
		if grant.digest == digest {
			last := len(c.material) - 1
			copy(c.material[i:], c.material[i+1:])
			c.material[last] = originalSeedRescueTorrent{}
			c.material = c.material[:last]
			return grant, true
		}
	}
	return originalSeedRescueTorrent{}, false
}

const originalSeedRescueEffectNamespace = "plugin_seed_rescue_effect"

func (a *App) originalSeedRescueEffectID(kind, key string) string {
	return a.Store.siteIdentityMAC(originalSeedRescueEffectNamespace+"-"+kind, key)
}

func (a *App) beginOriginalSeedRescueEffect(ctx context.Context, scope originalBaseCallScope, kind, effectKey string) (func(bool) error, error) {
	if err := originalSeedRescueValidate(ctx, scope); err != nil {
		return nil, err
	}
	if scope.object.stopping.Load() {
		return nil, bad("PLUGIN_CALL_CLOSED", "稀缺保种正在停止")
	}
	current, err := a.pluginStore.Installation(ctx, scope.installation.InstanceID)
	if err != nil {
		return nil, err
	}
	if !current.Installed || current.Generation != scope.installation.Generation {
		return nil, bad("PLUGIN_SNAPSHOT_STALE", "稀缺保种实例已卸载或替换")
	}
	key := a.originalSeedRescueEffectID(kind, effectKey)
	result, err := a.Store.DB.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at)
		VALUES(?,?,'"started"',?) ON CONFLICT(kind,id) DO UPDATE
		SET payload=excluded.payload,updated_at=excluded.updated_at WHERE CAST(records.payload AS TEXT)='"confirmed"'`, originalSeedRescueEffectNamespace, key, now())
	if err != nil {
		return nil, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if changed == 0 {
		return nil, bad("SEED_RESCUE_EFFECT_UNCERTAIN", "上一次保种提交结果仍待确认")
	}
	return func(confirmed bool) error {
		settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if confirmed {
			return a.Store.Put(settle, originalSeedRescueEffectNamespace, key, "confirmed")
		}
		_, err := a.Store.DB.ExecContext(settle, "DELETE FROM records WHERE kind=? AND id=?", originalSeedRescueEffectNamespace, key)
		return err
	}, nil
}

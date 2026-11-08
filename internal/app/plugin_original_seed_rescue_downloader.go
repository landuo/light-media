package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"light-media/internal/plugins"
	native "light-media/internal/plugins/iyuuautoseed/native"
	sr "light-media/internal/plugins/seedrescue"
)

func (a *App) originalSeedRescueDownloader(ctx context.Context, scope originalBaseCallScope, name string) (DownloaderConfig, bool, error) {
	if name == "" || name != scope.seedRescue.config.Downloader {
		return DownloaderConfig{}, false, bad("PLUGIN_CAPABILITY_DENIED", "下载器不在当前稀缺保种配置中")
	}
	var match DownloaderConfig
	found := false
	for _, ref := range a.Settings().Downloaders {
		if ref.Name != name || !downloaderEnabled(ref) {
			continue
		}
		if found {
			return DownloaderConfig{}, false, bad("DOWNLOADER_CHANGED", "下载器名称没有唯一配置")
		}
		match, found = ref, true
	}
	if !found || match.Kind != "qb" && match.Kind != "qbittorrent" {
		return DownloaderConfig{}, false, nil
	}
	if err := originalSeedRescueValidate(ctx, scope); err != nil {
		return DownloaderConfig{}, false, err
	}
	binding, err := json.Marshal(match)
	if err != nil {
		return DownloaderConfig{}, false, err
	}
	call := scope.seedRescue
	call.downloaderMu.Lock()
	defer call.downloaderMu.Unlock()
	stamp := tokenHash(string(binding))
	if call.downloaderBinding != "" && call.downloaderBinding != stamp {
		return DownloaderConfig{}, false, bad("DOWNLOADER_CHANGED", "稀缺保种下载器配置已变化")
	}
	call.downloaderBinding = stamp
	return match, true, nil
}

func (a *App) originalSeedRescueClient(ctx context.Context, scope originalBaseCallScope, name string) (*native.IYUUClient, DownloaderConfig, error) {
	config, found, err := a.originalSeedRescueDownloader(ctx, scope, name)
	if err != nil || !found {
		return nil, config, err
	}
	guarded := scope
	guarded.validate = func(ctx context.Context) error {
		current, present, err := a.originalSeedRescueDownloader(ctx, scope, name)
		if err != nil {
			return err
		}
		if !present || current.ID != config.ID {
			return bad("DOWNLOADER_CHANGED", "稀缺保种下载器已更换")
		}
		return nil
	}
	client, _, _, err := a.originalHostDownloaderBoundSourceClient(ctx, guarded, config.ID, true, true)
	return client, config, err
}

func (a *App) originalSeedRescueDownloaderResource(ctx context.Context, scope originalBaseCallScope, operation string, raw json.RawMessage) (json.RawMessage, error) {
	if operation == "add_torrent" {
		return a.originalSeedRescueAdd(ctx, scope, raw)
	}
	var request struct {
		Name   string   `json:"name"`
		Hashes []string `json:"hashes,omitempty"`
		Tag    string   `json:"tag,omitempty"`
	}
	if err := originalResourceDecode(raw, &request); err != nil {
		return nil, err
	}
	client, config, err := a.originalSeedRescueClient(ctx, scope, request.Name)
	if err != nil || client == nil {
		return json.RawMessage("null"), err
	}
	defer client.Close()
	switch operation {
	case "service":
		if request.Hashes != nil || request.Tag != "" {
			return nil, bad("INVALID_PLUGIN_VALUE", "下载器连接参数无效")
		}
		connected := client.SeedRescueConnected(ctx) == nil
		if err := originalSeedRescueValidate(ctx, scope); err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Name      string `json:"name"`
			Kind      string `json:"kind"`
			Connected bool   `json:"connected"`
		}{request.Name, "qbittorrent", connected})
	case "query_torrents":
		if request.Tag != "" || len(request.Hashes) == 0 || len(request.Hashes) > 128 {
			return nil, bad("INVALID_PLUGIN_VALUE", "稀缺保种必须指定查询的种子 Hash")
		}
		for _, hash := range request.Hashes {
			if !plugins.DownloaderTaskHashValid(hash) {
				return nil, bad("INVALID_PLUGIN_VALUE", "稀缺保种查询的种子 Hash 无效")
			}
		}
		body, err := client.Torrents(ctx, request.Hashes, false)
		if err != nil {
			return nil, err
		}
		facts, err := originalSeedRescueTorrentFacts(body, request.Hashes)
		if err != nil {
			return nil, err
		}
		if err = a.originalSeedRescueObserveAdds(ctx, scope, config, facts); err != nil {
			return nil, err
		}
		return facts, nil
	case "downloading_torrents":
		if request.Hashes != nil || request.Tag != sr.GlobalTag {
			return nil, bad("PLUGIN_CAPABILITY_DENIED", "稀缺保种仅可查询本插件标签下的下载")
		}
		body, err := client.SeedRescueDownloading(ctx, request.Tag)
		if err != nil {
			return nil, err
		}
		return originalSeedRescueTorrentFacts(body, nil)
	}
	return nil, bad("PLUGIN_CAPABILITY_DENIED", "下载器操作未获授权")
}

func originalSeedRescueTorrentFacts(body json.RawMessage, hashes []string) (json.RawMessage, error) {
	var rows []map[string]json.RawMessage
	if json.Unmarshal(body, &rows) != nil || rows == nil {
		return nil, bad("DOWNLOADER_INVALID_RESPONSE", "稀缺保种种子清单无效")
	}
	for i, row := range rows {
		if hashes != nil {
			var hash string
			if json.Unmarshal(row["hash"], &hash) != nil || !slices.ContainsFunc(hashes, func(expected string) bool { return sameHash(expected, hash) }) {
				return nil, bad("DOWNLOADER_INVALID_RESPONSE", "下载器返回了范围外的种子")
			}
		}
		facts := map[string]json.RawMessage{}
		for _, key := range []string{"hash", "state", "progress", "amount_left"} {
			if value, exists := row[key]; exists {
				facts[key] = value
			}
		}
		rows[i] = facts
	}
	return originalHostDownloaderMarshal(rows)
}

func originalSeedRescueAddKey(config DownloaderConfig, hash string) string {
	return strings.Join([]string{config.ID, originalBrushDaemonAddress(config), strings.ToLower(hash)}, "\x00")
}

func (a *App) originalSeedRescueObserveAdds(ctx context.Context, scope originalBaseCallScope, config DownloaderConfig, facts json.RawMessage) error {
	var rows []struct{ Hash string }
	if err := json.Unmarshal(facts, &rows); err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	if _, ok, err := a.originalSeedRescueDownloader(ctx, scope, config.Name); !ok || err != nil {
		if err != nil {
			return err
		}
		return bad("DOWNLOADER_CHANGED", "稀缺保种下载器已更换")
	}
	for _, row := range rows {
		key := a.originalSeedRescueEffectID("add", originalSeedRescueAddKey(config, row.Hash))
		if _, err := a.Store.DB.ExecContext(ctx, `UPDATE records SET payload='"confirmed"',updated_at=?
			WHERE kind=? AND id=? AND CAST(payload AS TEXT)='"started"'`, now(), originalSeedRescueEffectNamespace, key); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) originalSeedRescueIntent(ctx context.Context, scope originalBaseCallScope, hash string, siteID int) error {
	key := sr.JobsKey
	raw, err := a.pluginStore.originalBaseGetData(ctx, scope.installation.InstanceID, &key)
	if err != nil {
		return err
	}
	var jobs map[string]struct {
		State  string `json:"state"`
		Source string `json:"source_hash"`
		SiteID int    `json:"source_site_id"`
	}
	if json.Unmarshal(raw, &jobs) != nil {
		return bad("SEED_RESCUE_INTENT_INVALID", "稀缺保种任务状态无效")
	}
	for target, job := range jobs {
		if (scope.seedRescue.targetHash == "" || sameHash(scope.seedRescue.targetHash, target)) &&
			job.State == "SOURCE_SUBMITTING" && job.SiteID == siteID && sameHash(job.Source, hash) {
			return nil
		}
	}
	return bad("SEED_RESCUE_INTENT_MISSING", "稀缺保种提交缺少对应的任务意图")
}

func (a *App) originalSeedRescueAdd(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Downloader string   `json:"downloader"`
		Content    []byte   `json:"content"`
		SavePath   *string  `json:"save_path"`
		Category   string   `json:"category"`
		Tags       []string `json:"tags"`
	}
	if err := originalHostDownloaderAddDecode(raw, &request); err != nil {
		return nil, err
	}
	call := scope.seedRescue
	want := call.config.SavePath
	if request.Category != sr.Category || !slices.Equal(request.Tags, []string{sr.GlobalTag}) ||
		(request.SavePath == nil) != (want == nil) ||
		want != nil && (*request.SavePath != *want || !importPath(*want) || browserInternal(*want)) {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "稀缺保种提交的目录、分类或标签未获授权")
	}
	material, ok := call.takeTorrent(request.Content)
	if !ok {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "稀缺保种提交缺少本次取得的种子材料")
	}
	hash, err := sr.InfoHashFromTorrent(material.content)
	if err != nil {
		return nil, err
	}
	initialScope := scope
	scope.validate = func(ctx context.Context) error {
		return a.originalSeedRescueSiteGuard(ctx, initialScope, material.site)
	}
	client, config, err := a.originalSeedRescueClient(ctx, scope, request.Downloader)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return json.RawMessage("false"), nil
	}
	defer client.Close()
	release, err := a.beginWrite()
	if err != nil {
		return nil, err
	}
	defer release()
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	if err := a.originalSeedRescueIntent(ctx, scope, hash, material.site.id); err != nil {
		return nil, err
	}
	// The worker checked before saving its intent. Repeat under the same lock
	// as the effect, so another task or clone cannot race that observation.
	body, err := client.Torrents(ctx, []string{hash}, false)
	if err != nil {
		return nil, err
	}
	facts, err := originalSeedRescueTorrentFacts(body, []string{hash})
	if err != nil {
		return nil, err
	}
	if string(facts) != "[]" {
		if err := a.originalSeedRescueObserveAdds(ctx, scope, config, facts); err != nil {
			return nil, err
		}
		return json.RawMessage("true"), nil
	}
	finish, err := a.beginOriginalSeedRescueEffect(ctx, scope, "add", originalSeedRescueAddKey(config, hash))
	if err != nil {
		return nil, err
	}
	result, err := client.SeedRescueAddDownloading(ctx, native.IYUUAddSpec{
		Torrent: material.content, SavePath: request.SavePath, Category: &request.Category, Tags: request.Tags, IgnoreCategoryCheck: true,
	})
	if err != nil {
		if native.MutationNotDispatched(err) {
			if settleErr := finish(false); settleErr != nil {
				return nil, settleErr
			}
		}
		return nil, err
	}
	if err := finish(true); err != nil {
		return nil, err
	}
	return json.Marshal(result.Accepted)
}

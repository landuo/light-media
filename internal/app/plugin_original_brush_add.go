package app

import (
	"context"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"light-media/internal/metainfo"
	"light-media/internal/plugins"
	native "light-media/internal/plugins/iyuuautoseed/native"
	"light-media/internal/sites"
)

type originalBrushAddTask struct {
	ID         string  `json:"id"`
	SiteID     int64   `json:"site_id"`
	Downloader string  `json:"downloader"`
	Enabled    bool    `json:"enabled"`
	Protected  bool    `json:"del_no_free"`
	SkipTips   bool    `json:"site_skip_tips"`
	SavePath   *string `json:"save_path"`
	Category   *string `json:"qb_category"`
}

type originalBrushStoredConfig struct {
	Enabled bool                   `json:"enabled"`
	Tasks   []originalBrushAddTask `json:"tasks"`
}

func (a *App) originalBrushAddConfigured(ctx context.Context, scope originalBaseCallScope, request plugins.BrushAddRequestV1, candidate originalBrushSourceCandidate) (originalBrushAddTask, DownloaderConfig, error) {
	deny := bad("PLUGIN_CAPABILITY_DENIED", "刷流添加与当前任务配置不符")
	var config originalBrushStoredConfig
	stored, err := a.pluginStore.Config(ctx, scope.installation.InstanceID)
	if err != nil {
		return originalBrushAddTask{}, DownloaderConfig{}, err
	}
	if json.Unmarshal(stored.Value, &config) != nil || !config.Enabled {
		return originalBrushAddTask{}, DownloaderConfig{}, deny
	}
	downloader, err := a.configuredDownloader(request.DownloaderID)
	if err != nil {
		return originalBrushAddTask{}, DownloaderConfig{}, err
	}
	for _, task := range config.Tasks {
		if task.ID != request.TaskID {
			continue
		}
		if !task.Enabled || task.SiteID != candidate.sourceID || task.Downloader != downloader.Name || task.Protected != request.Paused ||
			originalBrushString(task.SavePath) != originalBrushString(request.SavePath) || originalBrushString(task.Category) != originalBrushString(request.Category) ||
			request.SkipChecking != nil || request.SkipCheckingWire != nil || len(request.QBLabels) != 0 || request.IgnoreCategoryCheck != nil && !*request.IgnoreCategoryCheck {
			return originalBrushAddTask{}, DownloaderConfig{}, deny
		}
		return task, downloader, nil
	}
	return originalBrushAddTask{}, DownloaderConfig{}, deny
}

func originalBrushString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

var originalBrushTemporaryTag = regexp.MustCompile(`^[A-Za-z0-9]{10}$`)

func originalBrushTaskTag(task string) string {
	id := []rune(task)
	if len(id) > 8 {
		id = id[:8]
	}
	return "刷流-" + string(id)
}

func (a *App) originalBrushSourceAdd(ctx context.Context, scope originalBaseCallScope, request plugins.BrushAddRequestV1) (json.RawMessage, error) {
	scope.brush.mu.Lock()
	candidate, found := scope.brush.candidates[request.MaterialID]
	if found && candidate.site == request.SiteID {
		delete(scope.brush.candidates, request.MaterialID)
	}
	scope.brush.mu.Unlock()
	if !found || candidate.site != request.SiteID {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "刷流材料不属于当前调用与站点")
	}
	task, downloader, err := a.originalBrushAddConfigured(ctx, scope, request, candidate)
	if err != nil {
		return nil, err
	}
	client, kind, guard, err := a.originalHostDownloaderBoundSourceClient(ctx, scope, request.DownloaderID, true, true)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	tags := []string{"已整理", "刷流", originalBrushTaskTag(task.ID)}
	marker := ""
	if kind == "qbittorrent" && len(request.Tags) == 4 && originalBrushTemporaryTag.MatchString(request.Tags[3]) {
		marker = request.Tags[3]
		tags = append(tags, marker)
	}
	if !slices.Equal(tags, request.Tags) || kind == "qbittorrent" && marker == "" {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "刷流标签与任务身份不符")
	}
	content, magnet, expectedHash, err := a.originalBrushMaterial(ctx, scope, candidate, task.SkipTips)
	if err != nil {
		a.logOriginalPluginContext(ctx, "ERROR", "brush_download_material_failed", "刷流无法从当前站点取得有效种子，请检查站点访问状态")
		return nil, err
	}
	out := plugins.DownloaderMutationV1{SchemaVersion: 1, DownloaderID: request.DownloaderID, Outcome: "rejected"}
	if expectedHash == "" {
		return originalHostDownloaderMutationOutcome(ctx, true, nil, out)
	}
	category := originalBrushString(task.Category)
	spec := native.IYUUAddSpec{Torrent: content, Magnet: magnet, SavePath: task.SavePath, Category: &category, Tags: tags, IgnoreCategoryCheck: true}
	destination, err := client.EffectiveAddDestination(ctx, spec)
	if err != nil {
		a.logOriginalPluginContext(ctx, "ERROR", "brush_download_destination_failed", "无法读取刷流下载器的保存目录，请检查下载器连接和分类设置")
		return nil, err
	}
	if err := a.originalBrushDestinationAllowed(destination.EffectivePath); err != nil {
		a.logOriginalPluginContext(ctx, "ERROR", "brush_download_destination_denied", "刷流保存目录不在光影已挂载的授权目录内，请检查容器挂载和目录设置")
		return nil, err
	}
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	rows, err := client.Torrents(ctx, []string{expectedHash}, false)
	if err != nil {
		return nil, err
	}
	existing, err := originalDownloaderTaskProjection(kind, []string{expectedHash}, rows)
	if err != nil {
		return nil, err
	}
	if len(existing) != 0 {
		return originalHostDownloaderMutationOutcome(ctx, true, nil, out)
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	if err := a.originalBrushDestinationAllowed(destination.EffectivePath); err != nil {
		return nil, err
	}
	var up, down *int64
	if kind == "qbittorrent" {
		up, down = request.UploadLimitBytes, request.DownloadLimitBytes
	}
	added, callErr := client.BrushFlowAdd(ctx, spec, request.Paused, up, down, &destination)
	out.Accepted, out.Outcome, out.AddedIDs = added.Accepted, added.Outcome, added.AddedIDs
	if guardErr := originalDownloaderOutcomeGuard(ctx, guard, callErr, true); guardErr != nil {
		if out.Accepted || out.Outcome == "unknown" {
			out.FailureCode = "CALL_CHANGED_AFTER_MUTATION"
			return originalHostDownloaderMutationOutcome(ctx, true, callErr, out)
		}
		return nil, guardErr
	}
	if !added.Accepted {
		if marker != "" && added.Outcome == "rejected" {
			_, _ = client.BrushFlowControl(ctx, "delete_tags", nil, []string{marker}, nil, nil)
		}
		return originalHostDownloaderMutationOutcome(ctx, true, callErr, out)
	}
	hash := added.Hash
	if marker != "" {
		hash, err = client.PollByTag(ctx, marker)
	}
	if err != nil || !sameHash(expectedHash, hash) {
		if marker != "" && guard(ctx) == nil {
			_, _ = client.BrushFlowControl(ctx, "delete_tags", nil, []string{marker}, nil, nil)
		}
		out.Accepted, out.Outcome, out.FailureCode = false, "unknown", "PARTIAL_MUTATION"
		return originalHostDownloaderMutationOutcome(ctx, true, err, out)
	}
	hash = strings.ToLower(hash)
	out.Hash = hash
	if err = guard(ctx); err == nil {
		err = a.originalBrushRememberOwner(ctx, scope, downloader, task.ID, hash)
	}
	if err != nil {
		out.FailureCode = "CALL_CHANGED_AFTER_MUTATION"
		return originalHostDownloaderMutationOutcome(ctx, true, err, out)
	}
	if marker != "" {
		removed, removeErr := client.RemoveTag(ctx, hash, marker)
		if removeErr != nil || removed.Outcome == "unknown" {
			out.FailureCode = "PARTIAL_MUTATION"
			return originalHostDownloaderMutationOutcome(ctx, true, removeErr, out)
		}
		deleted, deleteErr := client.BrushFlowControl(ctx, "delete_tags", nil, []string{marker}, nil, nil)
		if deleteErr != nil || deleted.Outcome == "unknown" {
			out.FailureCode = "PARTIAL_MUTATION"
			return originalHostDownloaderMutationOutcome(ctx, true, deleteErr, out)
		}
	} else if request.UploadLimitBytes != nil || request.DownloadLimitBytes != nil {
		limited, limitErr := client.PluginHostTransmissionLimits(ctx, hash, originalBrushLimitKB(request.UploadLimitBytes), originalBrushLimitKB(request.DownloadLimitBytes))
		if limitErr != nil || limited.Outcome == "unknown" {
			out.FailureCode = "PARTIAL_MUTATION"
			return originalHostDownloaderMutationOutcome(ctx, true, limitErr, out)
		}
	}
	return originalHostDownloaderMutationOutcome(ctx, true, nil, out)
}

func (a *App) originalBrushDestinationAllowed(path string) error {
	deny := bad("FILE_ROOT_REJECTED", "刷流保存目录不在授权本地范围内")
	for a.sourceAllowed(path) && !browserInternal(path) {
		directory, err := importDir(path, false)
		if err == nil {
			return directory.Close()
		}
		parent := filepath.Dir(path)
		if !errors.Is(err, os.ErrNotExist) || parent == path {
			return deny
		}
		path = parent
	}
	return deny
}

func originalBrushLimitKB(value *int64) *int64 {
	if value == nil {
		return nil
	}
	limit := *value / 1024
	return &limit
}

func (a *App) originalBrushMaterial(ctx context.Context, scope originalBaseCallScope, candidate originalBrushSourceCandidate, skipTips bool) ([]byte, string, string, error) {
	ctx = allowDisabledSiteRequests(ctx)
	site, err := a.siteForRequest(ctx, candidate.site)
	if err != nil || site.Public || a.managedSiteKey(site) != candidate.siteKey {
		return nil, "", "", bad("SITE_CHANGED", "刷流站点在选种后已变化")
	}
	address := candidate.enclosure
	if strings.HasPrefix(address, "magnet:?") {
		hash, err := originalBrushMagnetHash(address)
		return nil, address, hash, err
	}
	var content []byte
	var magnet string
	err = a.withSite(ctx, site, false, func(client *sites.Client) error {
		client.AddRequestGuard(scope.validate)
		var err error
		if candidate.resource != "" {
			content, err = client.BrushTorrent(ctx, candidate.resource, skipTips && site.Name != "天空")
			return err
		}
		base, err := url.Parse(site.BaseURL)
		if err != nil {
			return err
		}
		if strings.HasPrefix(address, "[]") || strings.HasPrefix(address, "[") && site.Kind != "mteam" {
			content, magnet, err = originalBrushDownloadRecipe(ctx, client, base, site, address, skipTips)
			return err
		}
		material, err := url.Parse(address)
		if err != nil || address == "" || material.User != nil {
			return bad("INVALID_PLUGIN_VALUE", "刷流RSS材料地址无效")
		}
		material = base.ResolveReference(material)
		if skipTips && site.Kind == "nexusphp" && site.Name != "天空" {
			query := material.Query()
			query.Set("letdown", "1")
			material.RawQuery = query.Encode()
		}
		content, err = client.ConfiguredMaterial(ctx, material.String())
		return err
	})
	if err != nil {
		return nil, "", "", err
	}
	if magnet != "" {
		hash, err := originalBrushMagnetHash(magnet)
		return nil, magnet, hash, err
	}
	if len(content) == 0 {
		return nil, "", "", nil
	}
	material, err := metainfo.ParseMaterial(content)
	if err != nil {
		return nil, "", "", err
	}
	return content, "", material.Hash, nil
}

func originalBrushMagnetHash(address string) (string, error) {
	u, err := url.Parse(address)
	if err == nil {
		for _, value := range u.Query()["xt"] {
			if value, ok := strings.CutPrefix(strings.ToLower(value), "urn:btih:"); ok {
				if len(value) == 40 && plugins.DownloaderTaskHashValid(value) {
					return value, nil
				}
				if raw, err := base32.StdEncoding.DecodeString(strings.ToUpper(value)); err == nil && len(raw) == 20 {
					return hex.EncodeToString(raw), nil
				}
			}
			if value, ok := strings.CutPrefix(strings.ToLower(value), "urn:btmh:1220"); ok && len(value) == 64 && plugins.DownloaderTaskHashValid(value) {
				return value, nil
			}
		}
	}
	return "", bad("INVALID_PLUGIN_VALUE", "刷流磁力没有明确种子身份")
}

const originalBrushOwnerKind = "brush_source_owner"

type originalBrushOwner struct {
	InstanceID, DownloaderID, DownloaderKey, Hash, TaskID string
	AdoptedPath                                           string `json:",omitempty"`
}

func originalBrushOwnerKey(instance, downloader, hash string) string {
	return originalBrushOwnerPrefix(instance) + tokenHash(downloader+"\x00"+strings.ToLower(hash))
}

func originalBrushOwnerPrefix(instance string) string {
	return tokenHash(instance) + ":"
}

func (a *App) originalBrushRememberOwner(ctx context.Context, scope originalBaseCallScope, downloader DownloaderConfig, task, hash string) error {
	instance := scope.installation.InstanceID
	key := originalBrushOwnerKey(instance, downloader.ID, hash)
	owner := originalBrushOwner{InstanceID: instance, DownloaderID: downloader.ID, DownloaderKey: tokenHash(string(mustCleanSeedJSON(downloader.Config))), Hash: hash, TaskID: task}
	sealed, err := a.pluginStore.seal(originalBrushOwnerKind, instance, key, mustCleanSeedJSON(owner))
	if err != nil {
		return err
	}
	result, err := a.Store.DB.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) SELECT ?,?,?,? FROM plugin_installations WHERE instance_id=? AND plugin_id='BrushFlowLanduo' AND installed=1 ON CONFLICT(kind,id) DO UPDATE SET payload=excluded.payload,updated_at=excluded.updated_at`, originalBrushOwnerKind, key, sealed, now(), instance)
	if err != nil {
		return err
	}
	if updated, err := result.RowsAffected(); err != nil || updated != 1 {
		return bad("PLUGIN_CAPABILITY_DENIED", "刷流实例已移除，无法登记种子归属")
	}
	return nil
}

func (a *App) originalBrushReadOwner(ctx context.Context, scope originalBaseCallScope, downloader DownloaderConfig, hash string) (originalBrushOwner, error) {
	var owner originalBrushOwner
	instance := scope.installation.InstanceID
	key := originalBrushOwnerKey(instance, downloader.ID, hash)
	var sealed []byte
	err := a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind=? AND id=?`, originalBrushOwnerKind, key).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return owner, nil
	}
	if err != nil {
		return owner, err
	}
	plain, err := a.pluginStore.unseal(originalBrushOwnerKind, instance, key, sealed)
	if err != nil || json.Unmarshal(plain, &owner) != nil || owner.TaskID == "" || owner.InstanceID != instance || owner.DownloaderID != downloader.ID || owner.Hash != hash {
		return originalBrushOwner{}, bad("PLUGIN_CAPABILITY_DENIED", "刷流种子归属无法核对")
	}
	if owner.DownloaderKey != tokenHash(string(mustCleanSeedJSON(downloader.Config))) {
		return originalBrushOwner{}, nil
	}
	return owner, nil
}

func (a *App) originalBrushForgetOwners(ctx context.Context, scope originalBaseCallScope, downloader string, hashes []string) error {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, hash := range hashes {
		if _, err := tx.ExecContext(ctx, `DELETE FROM records WHERE kind=? AND id=?`, originalBrushOwnerKind, originalBrushOwnerKey(scope.installation.InstanceID, downloader, hash)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a *App) originalBrushControlAllowed(ctx context.Context, scope originalBaseCallScope, client *native.IYUUClient, kind, downloaderID, action string, hashes []string) error {
	deny := bad("PLUGIN_CAPABILITY_DENIED", "刷流下载器操作不在当前任务范围内")
	if !originalBrushSourceScope(ctx, scope) || len(hashes) == 0 {
		return deny
	}
	switch action {
	case "start":
		if scope.phase != "brush" {
			return deny
		}
	case "pause", "reannounce", "remove_task":
		if scope.phase != "brush" && scope.phase != "check" && scope.phase != "promotion_expiry" {
			return deny
		}
	default:
		return deny
	}
	downloader, err := a.configuredDownloader(downloaderID)
	if err != nil {
		return err
	}
	var legacy []string
	for _, hash := range hashes {
		owner, err := a.originalBrushReadOwner(ctx, scope, downloader, hash)
		if err != nil {
			return err
		}
		if owner.TaskID == "" || owner.AdoptedPath != "" {
			if action == "pause" || action == "reannounce" {
				legacy = append(legacy, hash)
				continue
			}
			return bad("DOWNLOAD_NOT_MANAGED", "此种子不属于当前刷流实例")
		}
		if action == "start" {
			stored, err := a.pluginStore.Data(ctx, scope.installation.InstanceID, "task."+owner.TaskID+".torrents")
			if err != nil {
				return err
			}
			var records map[string]*struct {
				Deleted bool `json:"deleted"`
			}
			if json.Unmarshal(stored.Value, &records) != nil || records[hash] == nil || records[hash].Deleted {
				return bad("DOWNLOAD_NOT_MANAGED", "刷流种子状态尚未保存，禁止启动")
			}
		}
	}
	if len(legacy) != 0 {
		raw, err := client.Torrents(ctx, legacy, false)
		if err != nil {
			return err
		}
		tasks, err := originalDownloaderTaskProjection(kind, legacy, raw)
		if err != nil {
			return err
		}
		if len(tasks) != len(legacy) {
			return bad("DOWNLOAD_NOT_FOUND", "所选刷流任务已变化，请重新读取")
		}
		for _, task := range tasks {
			directory, tags := originalBrushTorrentLocation(task)
			if err := a.originalBrushOwnedTorrent(ctx, scope, downloader, task.Hash, directory, tags, true); err != nil {
				return err
			}
		}
	}
	return nil
}

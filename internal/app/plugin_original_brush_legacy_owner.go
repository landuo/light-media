package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

func originalBrushTorrentLocation(task plugins.DownloaderTaskV1) (string, []string) {
	directory, tags := "", []string{}
	if qb := task.QBittorrent; qb != nil {
		if qb.SavePath != nil {
			directory = *qb.SavePath
		}
		if qb.Tags != nil {
			tags = strings.Split(*qb.Tags, ",")
		}
	}
	if tr := task.Transmission; tr != nil {
		if tr.DownloadDir != nil {
			directory = *tr.DownloadDir
		}
		if tr.Labels != nil {
			tags = *tr.Labels
		}
	}
	return directory, tags
}

func originalBrushLegacyMarker(tags []string) string {
	var organized, brush bool
	marker := ""
	for _, tag := range tags {
		switch tag = strings.TrimSpace(tag); tag {
		case "已整理":
			organized = true
		case "刷流":
			brush = true
		default:
			if strings.HasPrefix(tag, "刷流-") {
				if marker != "" && marker != tag {
					return ""
				}
				marker = tag
			}
		}
	}
	if organized && brush && marker != "刷流-" {
		return marker
	}
	return ""
}

func originalBrushDaemonAddress(configured DownloaderConfig) string {
	address, err := url.Parse(configured.BaseURL)
	if err != nil || address.Host == "" {
		return ""
	}
	kind := configured.Kind
	switch kind {
	case "qb":
		kind = "qbittorrent"
	case "tr":
		kind = "transmission"
	}
	return kind + "\x00" + originalOCROrigin(address) + strings.TrimRight(address.EscapedPath(), "/")
}

// An imported worker record is not a downloader receipt. Require the exact
// same live daemon hash, local root and source-generated task marker before
// adopting it. The global Brush tag alone can cause the original worker to
// manufacture a record during a check and is therefore insufficient proof.
// Callers serialize downloader effects with downloadMu.
func (a *App) originalBrushOwnedTorrent(ctx context.Context, scope originalBaseCallScope, downloader DownloaderConfig, hash, directory string, tags []string, allowAdopt bool) error {
	deny := bad("DOWNLOAD_NOT_MANAGED", "此种子缺少当前刷流实例的可靠归属")
	owner, err := a.originalBrushReadOwner(ctx, scope, downloader, hash)
	if err != nil {
		return err
	}
	if owner.TaskID != "" && owner.AdoptedPath == "" {
		return nil // A confirmed new Add retains its existing stronger receipt.
	}
	if owner.TaskID == "" && !allowAdopt || owner.AdoptedPath != "" && owner.AdoptedPath != directory {
		return deny
	}
	marker := originalBrushLegacyMarker(tags)
	if marker == "" || owner.TaskID != "" && marker != originalBrushTaskTag(owner.TaskID) {
		return deny
	}
	if err := a.originalBrushDestinationAllowed(directory); err != nil {
		return err
	}
	address := originalBrushDaemonAddress(downloader)
	if address == "" {
		return deny
	}
	aliases := map[string]bool{}
	var daemonIDs []string
	nameMatches := 0
	for _, candidate := range a.Settings().Downloaders {
		if candidate.Name == downloader.Name {
			nameMatches++
		}
		if originalBrushDaemonAddress(candidate) != address {
			continue
		}
		aliases[candidate.Name] = true
		daemonIDs = append(daemonIDs, candidate.ID)
		owned, err := a.downloaderTaskOwnership(ctx, candidate.ID)
		if err != nil {
			return err
		}
		if owned.contains(download.Task{Hash: hash, Tags: tags}) {
			return deny // Do not take a task already owned by the normal downloader.
		}
	}
	if nameMatches != 1 {
		return deny
	}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	instance := scope.installation.InstanceID
	key := originalBrushOwnerKey(instance, downloader.ID, hash)
	for _, id := range daemonIDs {
		var conflicting bool
		suffix := tokenHash(id + "\x00" + strings.ToLower(hash))
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM records WHERE kind=? AND id LIKE ? AND id<>?)`, originalBrushOwnerKind, "%:"+suffix, key).Scan(&conflicting)
		if err != nil {
			return err
		}
		if conflicting {
			return deny
		}
	}
	task, err := a.pluginStore.originalBrushLegacyTask(ctx, tx, scope.installation, downloader.Name, aliases, marker, hash)
	if err != nil {
		return err
	}
	if task == "" || owner.TaskID != "" && owner.TaskID != task {
		return deny
	}
	if owner.TaskID != "" {
		return tx.Commit()
	}
	owner = originalBrushOwner{InstanceID: instance, DownloaderID: downloader.ID, DownloaderKey: tokenHash(string(mustCleanSeedJSON(downloader.Config))), Hash: hash, TaskID: task, AdoptedPath: directory}
	sealed, err := a.pluginStore.seal(originalBrushOwnerKind, instance, key, mustCleanSeedJSON(owner))
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO records(kind,id,payload,updated_at) VALUES(?,?,?,?) ON CONFLICT(kind,id) DO NOTHING`, originalBrushOwnerKind, key, sealed, now())
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		return deny // Never replace a pre-existing confirmation or stale binding.
	}
	return tx.Commit()
}

func (p *PluginStore) originalBrushLegacyTask(ctx context.Context, tx *sql.Tx, selected PluginInstallation, downloader string, aliases map[string]bool, marker, hash string) (string, error) {
	type identity struct {
		id         string
		generation int64
		installed  bool
	}
	rows, err := tx.QueryContext(ctx, `SELECT instance_id,generation,installed FROM plugin_installations WHERE plugin_id='BrushFlowLanduo'`)
	if err != nil {
		return "", err
	}
	var instances []identity
	for rows.Next() {
		var item identity
		if err := rows.Scan(&item.id, &item.generation, &item.installed); err != nil {
			rows.Close()
			return "", err
		}
		instances = append(instances, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	claim := ""
	for _, item := range instances {
		config, err := p.originalBrushStoredTasks(ctx, tx, item.id)
		if err != nil {
			return "", err
		}
		for _, task := range config.Tasks {
			if task.ID == "" || task.SiteID <= 0 || originalBrushTaskTag(task.ID) != marker || !aliases[task.Downloader] {
				continue
			}
			matched, err := p.originalBrushLegacyRecord(ctx, tx, item.id, task, hash)
			if err != nil {
				return "", err
			}
			if !matched {
				continue
			}
			if claim != "" || item.id != selected.InstanceID || !item.installed || item.generation != selected.Generation || !config.Enabled || !task.Enabled || task.Downloader != downloader {
				return "", nil // Another instance/task can substantiate this claim.
			}
			claim = task.ID
		}
	}
	return claim, nil
}

func (p *PluginStore) originalBrushStoredTasks(ctx context.Context, tx *sql.Tx, instance string) (originalBrushStoredConfig, error) {
	var config originalBrushStoredConfig
	observed, sealed, err := p.readOriginalBaseConfigRow(ctx, tx, instance)
	if err != nil || !observed.present {
		return config, err
	}
	raw, err := p.unsealBound("config", instance, "main", sealed, observed.valueLimit)
	if err != nil || json.Unmarshal(raw, &config) != nil {
		return config, bad("DOWNLOAD_NOT_MANAGED", "刷流任务配置无法核对")
	}
	return config, nil
}

func (p *PluginStore) originalBrushLegacyRecord(ctx context.Context, tx *sql.Tx, instance string, task originalBrushAddTask, hash string) (bool, error) {
	key := "task." + task.ID + ".torrents"
	var sealed []byte
	err := tx.QueryRowContext(ctx, `SELECT sealed FROM plugin_values WHERE instance_id=? AND kind='data' AND item_key=?`, instance, key).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	raw, err := p.unseal("data", instance, key, sealed)
	var records map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &records) != nil {
		return false, bad("DOWNLOAD_NOT_MANAGED", "刷流种子记录无法核对")
	}
	var record struct {
		TaskID  string `json:"task_id"`
		Site    int64  `json:"site"`
		Deleted *bool  `json:"deleted"`
	}
	if raw := records[hash]; len(raw) != 0 && json.Unmarshal(raw, &record) == nil && record.TaskID == task.ID && record.Site == task.SiteID && record.Deleted != nil && !*record.Deleted {
		return true, nil
	}
	return false, nil
}

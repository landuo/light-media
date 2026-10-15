package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"light-media/internal/download"
	"light-media/internal/metainfo"
	"light-media/internal/sites"
)

// Interactive downloads mirror MoviePilot's Add Download dialog. Recognition
// belongs to the directory organizer; a manual download need not have a media ID.
type NativeDownloadRequest struct {
	SiteID       string `json:"site_id"`
	ResourceID   string `json:"resource_id"`
	Query        string `json:"query"`
	Page         int    `json:"page"`
	DownloaderID string `json:"downloader_id"`
	DownloadPath string `json:"download_path"`
	MediaType    string `json:"media_type"`
	TMDBID       int64  `json:"tmdb_id"`
}
type NativeDownload struct {
	ID                 string `json:"id"`
	SiteID             string `json:"site_id"`
	ResourceID         string `json:"resource_id"`
	ResourceTitle      string `json:"resource_title"`
	DownloaderID       string `json:"downloader_id"`
	DownloadPath       string `json:"download_path"`
	DownloaderSnapshot string `json:"downloader_snapshot"`
	Hash               string `json:"hash"`
	MaterialRef        string `json:"material_ref"`
	MediaType          string `json:"media_type,omitempty"`
	TMDBID             int64  `json:"tmdb_id,omitempty"`
	State              string `json:"state"`
	Error              string `json:"error,omitempty"`
	WorkflowID         string `json:"workflow_id"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
}

func (a *App) addNativeDownload(ctx context.Context, in NativeDownloadRequest) (NativeDownload, error) {
	var out NativeDownload
	site, err := a.site(in.SiteID)
	if err != nil {
		return out, err
	}
	d, err := a.downloader(in.DownloaderID)
	if err != nil {
		return out, err
	}
	client, err := download.New(d.Config)
	if err != nil {
		return out, err
	}
	path := cleanDirectoryInput(in.DownloadPath)
	if path == "" {
		path, err = client.DefaultSavePath(ctx)
		if err != nil {
			return out, err
		}
	}
	if err = safeRoot(path); err != nil {
		return out, err
	}
	if !sourceAllowed(a.Settings(), path) {
		return out, bad("DIRECTORY_SOURCE_NOT_ALLOWED", "保存目录不在容器已授权的文件目录内，请核对容器挂载")
	}
	for _, root := range a.Settings().Roots {
		if _, overlap := sharedDirectoryPath(root.Path, path); overlap {
			return out, bad("DIRECTORY_OVERLAP", "资源目录不能与媒体库目录相同或互相包含")
		}
	}
	if in.TMDBID != 0 && !validMedia(in.MediaType, in.TMDBID) {
		return out, bad("INVALID_MEDIA", "高级选项中的影视类型或TMDB ID无效")
	}
	// Use the frozen search identity rather than accepting arbitrary download URLs.
	if in.Query == "" {
		var hint struct {
			Query string `json:"query"`
			Page  int    `json:"page"`
		}
		if a.Store.Get(ctx, "resource", in.SiteID+":"+in.ResourceID, &hint) == nil {
			in.Query = hint.Query
			in.Page = hint.Page
		}
	}
	if strings.TrimSpace(in.Query) == "" || len([]rune(in.Query)) > 200 || in.Page < 0 || in.Page > 500 {
		return out, bad("INVALID_QUERY", "请重新搜索后选择资源")
	}
	if in.Page == 0 {
		in.Page = 1
	}
	var resource sites.Resource
	var raw []byte
	err = a.withSite(ctx, site, false, func(c *sites.Client) error {
		result, e := c.Search(ctx, in.Query, in.Page)
		for _, v := range result.Resources {
			if v.ID == in.ResourceID {
				resource = v
				break
			}
		}
		if resource.ID == "" {
			if e != nil {
				return e
			}
			return bad("RESOURCE_CHANGED", "该资源已无法确认，请重新搜索")
		}
		raw, e = c.Torrent(ctx, in.ResourceID)
		return e
	})
	if err != nil {
		return out, err
	}
	material, err := metainfo.ParseMaterial(raw)
	if err != nil {
		return out, bad("INVALID_TORRENT", "资源种子无效或包含不安全文件路径")
	}
	ref, err := a.persistMaterial(ctx, raw, material.Hash)
	if err != nil {
		return out, err
	}
	release, err := a.beginWrite()
	if err != nil {
		return out, err
	}
	defer release()
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	var previous []byte
	var superseded *NativeDownload
	err = a.Store.DB.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='native_download' AND lower(json_extract(payload,'$.hash'))=lower(?) AND json_extract(payload,'$.downloader_id')=? AND json_extract(payload,'$.state')!='cancelled' ORDER BY updated_at DESC LIMIT 1`, material.Hash, d.ID).Scan(&previous)
	if err == nil {
		err = json.Unmarshal(previous, &out)
		if err != nil || out.State != "failed" && out.State != "waiting" {
			return out, err
		}
		// Explicitly adding again after failure must create a fresh attempt with
		// the user's current downloader/path choice, rather than report the
		// failed record as a successful submission forever.
		old := out
		superseded = &old
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	out = NativeDownload{ID: id("native_"), SiteID: site.ID, ResourceID: resource.ID, ResourceTitle: resource.Title, DownloaderID: d.ID, DownloadPath: path, DownloaderSnapshot: downloaderSnapshot(d), Hash: material.Hash, MaterialRef: ref, MediaType: in.MediaType, TMDBID: in.TMDBID, State: "planned", WorkflowID: id("wf_"), CreatedAt: now(), UpdatedAt: now()}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if superseded != nil {
		superseded.State, superseded.UpdatedAt = "cancelled", now()
		if err = txPut(ctx, tx, "native_download", superseded.ID, superseded); err != nil {
			return out, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,updated_at=? WHERE kind='native_download' AND json_extract(payload,'$.id')=? AND state IN ('queued','retry_wait','waiting_paused')`, now(), superseded.ID); err != nil {
			return out, err
		}
	}
	if err = pinMaterialTx(ctx, tx, ref, material.Hash); err != nil {
		return out, err
	}
	if err = txPut(ctx, tx, "native_download", out.ID, out); err != nil {
		return out, err
	}
	if _, err = enqueueTx(ctx, tx, "native_download", map[string]string{"id": out.ID}, out.WorkflowID); err != nil {
		return out, err
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	select {
	case a.jobWake <- struct{}{}:
	default:
	}
	return out, nil
}

func (a *App) runNativeDownload(ctx context.Context, job Job, key string) (result error) {
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	var in NativeDownload
	if err := a.Store.Get(ctx, "native_download", key, &in); err != nil {
		return err
	}
	if in.State == "accepted" || in.State == "cancelled" {
		return nil
	}
	defer func() {
		if result != nil {
			in.Error = a.clean(result.Error())
			in.State = "failed"
			var api *APIError
			if errors.As(result, &api) && api.Code == "DOWNLOADER_DISABLED" {
				in.State = "waiting"
			}
			in.UpdatedAt = now()
			_ = a.Store.Put(ctx, "native_download", in.ID, in)
		}
	}()
	if err := a.jobCancelled(ctx, job); err != nil {
		return err
	}
	d, err := a.downloader(in.DownloaderID)
	if err != nil {
		return err
	}
	if downloaderSnapshot(d) != in.DownloaderSnapshot {
		return bad("REVIEW_DOWNLOADER_CHANGED", "下载器配置已改变，请重新添加下载")
	}
	if !sourceAllowed(a.Settings(), in.DownloadPath) {
		return bad("REVIEW_DIRECTORY_CHANGED", "保存目录的容器挂载已改变")
	}
	for _, root := range a.Settings().Roots {
		if _, overlap := sharedDirectoryPath(root.Path, in.DownloadPath); overlap {
			return bad("REVIEW_DIRECTORY_CHANGED", "保存目录已成为媒体库目录，请重新选择下载目录")
		}
	}
	material, err := a.resolveMaterial(ctx, in.MaterialRef, materialPurpose)
	if err != nil {
		return err
	}
	client, err := download.New(d.Config)
	if err != nil {
		return err
	}
	release, err := a.beginWrite()
	if err != nil {
		return err
	}
	defer release()
	in.State = "submitting"
	in.UpdatedAt = now()
	if err = a.Store.Put(ctx, "native_download", in.ID, in); err != nil {
		return err
	}
	added, err := client.Add(ctx, download.AddRequest{Torrent: material, SavePath: in.DownloadPath, Tags: []string{"light-media"}})
	if err != nil {
		return err
	}
	if !sameHash(in.Hash, added.Hash) {
		return bad("DOWNLOAD_HASH_MISMATCH", "下载器返回的种子标识不一致")
	}
	in.Hash = added.Hash
	in.State = "accepted"
	in.Error = ""
	in.UpdatedAt = now()
	a.event(ctx, job, "INFO", "downloads", "accepted", "下载器已接收资源，目录监听负责后续识别整理")
	return a.Store.Put(ctx, "native_download", in.ID, in)
}

func (a *App) downloaderTaskAction(ctx context.Context, downloaderID, hash, action string) error {
	switch action {
	case "start", "pause", "verify", "remove_keep_data":
	default:
		return bad("INVALID_ACTION", "未知下载操作")
	}
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	d, err := a.downloader(downloaderID)
	if err != nil {
		return err
	}
	client, err := download.New(d.Config)
	if err != nil {
		return err
	}
	tasks, err := client.List(ctx)
	if err != nil {
		return err
	}
	actual := ""
	for _, task := range tasks {
		if sameHash(task.Hash, hash) {
			actual = task.Hash
			break
		}
	}
	if actual == "" {
		return bad("DOWNLOAD_NOT_FOUND", "下载任务不存在，请刷新列表")
	}
	if err = client.Control(ctx, actual, action); err != nil {
		return err
	}
	if action == "remove_keep_data" {
		// Cancel own bookkeeping too, so a queued retry cannot re-add the torrent.
		tx, e := a.Store.DB.BeginTx(ctx, nil)
		if e != nil {
			return e
		}
		defer tx.Rollback()
		for _, kind := range []string{"intent", "native_download"} {
			if _, e = tx.ExecContext(ctx, `UPDATE records SET payload=json_set(payload,'$.state','cancelled','$.updated_at',?),updated_at=? WHERE kind=? AND json_extract(payload,'$.downloader_id')=? AND lower(json_extract(payload,'$.hash'))=lower(?)`, now(), now(), kind, d.ID, hash); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,updated_at=? WHERE kind IN ('download','native_download') AND json_extract(payload,'$.id') IN (SELECT id FROM records WHERE kind IN ('intent','native_download') AND json_extract(payload,'$.downloader_id')=? AND json_extract(payload,'$.state')='cancelled') AND state IN ('queued','retry_wait','waiting_paused')`, now(), d.ID); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM reservations WHERE intent_id IN (SELECT id FROM records WHERE kind='intent' AND json_extract(payload,'$.state')='cancelled')`); e != nil {
			return e
		}
		return tx.Commit()
	}
	a.log("INFO", "downloads", action, "管理员操作下载器中的任务", "", "")
	return nil
}

package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"light-media/internal/download"
	"light-media/internal/plugins"
	native "light-media/internal/plugins/iyuuautoseed/native"
)

type originalDownloaderDeleteFact struct {
	Hash      string
	Tags      []string
	Directory string
	Files     []native.File
	Manifests map[string][]browserStamp
}

// This bridge shares the host's ownership, root, no-symlink, hardlink and
// snapshot primitives. Unlike the download page, it also admits owned completed
// tasks: the fixed plugin wrapper explicitly deletes seeding task data.
func (a *App) originalHostDownloaderDeleteFiles(ctx context.Context, scope originalBaseCallScope, raw json.RawMessage) (json.RawMessage, error) {
	var request plugins.DownloaderDeleteFilesRequestV1
	if err := originalBrushDownloaderBatchDecode(ctx, scope, raw, &request); err != nil {
		return nil, err
	}
	if request.Validate() != nil {
		return nil, bad("INVALID_PLUGIN_VALUE", "删除数据必须指定下载器和非空准确Hash")
	}
	brush := scope.object != nil && scope.object.installation.PluginID == "BrushFlowLanduo"
	if request.BrushOwnership != brush || brush && (!originalBrushSourceScope(ctx, scope) || scope.phase != "brush" && scope.phase != "check" && scope.phase != "promotion_expiry") {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "刷流删除不在当前调用范围内")
	}
	pathAllowed := a.sourceAllowed
	if brush {
		pathAllowed = a.brushSourceAllowed
	}
	hashes, _ := (plugins.DownloaderTasksRequestV1{DownloaderID: request.DownloaderID, Hashes: request.Hashes}).CanonicalSelection()
	a.downloadMu.Lock()
	defer a.downloadMu.Unlock()
	a.importMu.Lock()
	defer a.importMu.Unlock()
	reader, kind, baseGuard, err := a.originalHostDownloaderMutationClient(ctx, scope, request.DownloaderID)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	configured, err := a.configuredDownloader(request.DownloaderID)
	if err != nil {
		return nil, err
	}
	readOwnership := func() (downloaderTaskOwnership, error) {
		if request.BrushOwnership {
			return nil, nil
		}
		return a.downloaderTaskOwnership(ctx, request.DownloaderID)
	}
	checkOwnership := func(ctx context.Context, owned downloaderTaskOwnership, hash, directory string, tags []string, allowAdopt bool) error {
		if request.BrushOwnership {
			return a.originalBrushOwnedTorrent(ctx, scope, configured, hash, directory, tags, allowAdopt)
		} else if !owned.contains(download.Task{Hash: hash, Tags: tags}) {
			return bad("DOWNLOAD_NOT_MANAGED", "此任务不属于当前宿主，禁止删除数据")
		}
		return nil
	}
	readManifest := func(ctx context.Context, path string) ([]browserStamp, error) {
		manifest, err := browserManifest(ctx, path)
		if request.BrushOwnership && errors.Is(err, os.ErrNotExist) {
			// A paused add can be visible in the daemon before it creates its
			// first file. Freeze absence and check again before deletion.
			return nil, nil
		}
		return manifest, err
	}
	downloaderBindings := func() []download.Config {
		out := []download.Config{}
		for _, configured := range a.Settings().Downloaders {
			out = append(out, configured.Config)
		}
		return out
	}
	frozenBindings := downloaderBindings()
	readFacts := func() ([]originalDownloaderDeleteFact, error) {
		body, err := reader.Torrents(ctx, hashes, false)
		if err != nil {
			return nil, err
		}
		tasks, err := originalDownloaderTaskProjection(kind, hashes, body)
		if err != nil {
			return nil, err
		}
		if len(tasks) != len(hashes) {
			return nil, bad("DOWNLOAD_NOT_FOUND", "所选任务已变化，请重新读取")
		}
		owned, err := readOwnership()
		if err != nil {
			return nil, err
		}
		byHash := map[string]plugins.DownloaderTaskV1{}
		for _, task := range tasks {
			byHash[task.Hash] = task
		}
		facts := []originalDownloaderDeleteFact{}
		for _, hash := range hashes {
			task, ok := byHash[hash]
			if !ok {
				return nil, bad("DOWNLOAD_NOT_FOUND", "所选任务已变化，请重新读取")
			}
			directory, tags := originalBrushTorrentLocation(task)
			if err := checkOwnership(ctx, owned, hash, directory, tags, true); err != nil {
				return nil, err
			}
			if !importPath(directory) || !pathAllowed(directory) || browserInternal(directory) {
				return nil, bad("FILE_ROOT_REJECTED", "任务目录不在当前授权资源目录内")
			}
			files, err := reader.PluginHostFiles(ctx, hash)
			if err != nil {
				return nil, err
			}
			if len(files) == 0 {
				return nil, bad("DOWNLOADER_INVALID_RESPONSE", "不能删除文件范围未知的任务")
			}
			fact := originalDownloaderDeleteFact{Hash: hash, Tags: tags, Directory: directory, Files: files, Manifests: map[string][]browserStamp{}}
			for _, file := range files {
				path := filepath.Join(directory, file.Path)
				if !importPath(path) || !within(directory, path) || path == directory || !pathAllowed(path) || browserInternal(path) {
					return nil, bad("FILE_ROOT_REJECTED", "任务文件超出授权资源目录")
				}
				if err := a.browserMutationAllowed(ctx, path); err != nil {
					return nil, err
				}
				manifest, err := readManifest(ctx, path)
				if err != nil {
					return nil, err
				}
				if len(manifest) != 1 && !(request.BrushOwnership && manifest == nil) {
					return nil, bad("INVALID_FILE", "下载器文件必须是普通文件")
				}
				fact.Manifests[path] = manifest
			}
			facts = append(facts, fact)
		}
		if err := a.originalHostDownloaderProtectSharedFiles(ctx, scope, request.DownloaderID, hashes, facts); err != nil {
			return nil, err
		}
		return facts, nil
	}
	frozen, err := readFacts()
	if err != nil {
		return nil, err
	}
	// A daemon can change its layout independently of the host's locks. Read
	// exact selections twice, then guard the frozen local manifests on every
	// transport dispatch, including immediately before the selected mutation.
	current, err := readFacts()
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(frozen, current) {
		return nil, bad("FILE_SNAPSHOT_CHANGED", "任务文件或快照已变化，未执行删除")
	}
	guard := func(ctx context.Context) error {
		if err := baseGuard(ctx); err != nil {
			return err
		}
		if !reflect.DeepEqual(frozenBindings, downloaderBindings()) {
			return bad("DOWNLOADER_CHANGED", "下载器引用已变化，未执行删除")
		}
		owned, err := readOwnership()
		if err != nil {
			return err
		}
		for _, fact := range frozen {
			// Preserve the original task's ownership snapshot, including its tag;
			// durable ownership may be revoked while this call is in progress.
			if err := checkOwnership(ctx, owned, fact.Hash, fact.Directory, fact.Tags, false); err != nil {
				return err
			}
			if !pathAllowed(fact.Directory) || browserInternal(fact.Directory) {
				return bad("FILE_ROOT_REJECTED", "资源目录授权已变化")
			}
			for path, manifest := range fact.Manifests {
				if !pathAllowed(path) {
					return bad("FILE_ROOT_REJECTED", "资源目录授权已变化")
				}
				if err := a.browserMutationAllowed(ctx, path); err != nil {
					return err
				}
				actual, err := readManifest(ctx, path)
				if err != nil {
					return err
				}
				if !reflect.DeepEqual(actual, manifest) {
					return bad("FILE_SNAPSHOT_CHANGED", "任务文件快照已变化，未执行删除")
				}
			}
		}
		return nil
	}
	if err = guard(ctx); err != nil {
		return nil, err
	}
	client, err := native.NewIYUUClient(native.Config{ID: configured.ID, Kind: kind, BaseURL: configured.BaseURL, Username: configured.Username, Password: configured.Password}, native.IYUUOptions{APIKey: configured.APIKey, Guard: guard})
	if err != nil {
		return nil, err
	}
	defer client.Close()
	receipt, err := client.PluginHostRemoveOwnedFiles(ctx, hashes)
	out := plugins.DownloaderDeleteFilesV1{SchemaVersion: 1, DownloaderID: request.DownloaderID, Accepted: receipt.Accepted, Outcome: receipt.Outcome, DaemonApplied: receipt.Accepted && receipt.Outcome == "confirmed"}
	if err != nil {
		out.FailureCode = "DOWNLOADER_OUTCOME_UNKNOWN"
		if receipt.Outcome == "rejected" {
			out.FailureCode = "DOWNLOADER_NOT_DISPATCHED"
		}
		return originalHostDownloaderMarshal(out)
	}
	// Once acknowledged by the daemon, a bookkeeping or scope failure is a
	// successful external deletion plus a local failure, never a retry proof.
	if err = baseGuard(ctx); err != nil {
		out.FailureCode = "PLUGIN_CALL_CHANGED_AFTER_DELETE"
		return originalHostDownloaderMarshal(out)
	}
	if request.BrushOwnership {
		err = a.originalBrushForgetOwners(ctx, scope, request.DownloaderID, hashes)
	} else {
		err = a.originalHostDownloaderCancelDeleted(ctx, request.DownloaderID, hashes)
	}
	if err != nil {
		out.FailureCode = "DOWNLOAD_BOOKKEEPING_FAILED"
		return originalHostDownloaderMarshal(out)
	}
	out.BookkeepingCompleted = true
	return originalHostDownloaderMarshal(out)
}

// Reuse filesystem identity comparison for bind aliases. A selected daemon's
// deletion must not remove a file still named by a different task, including a
// task on another currently configured downloader. Unknown relevant file lists
// block deletion instead of being treated as an empty source set.
func (a *App) originalHostDownloaderProtectSharedFiles(ctx context.Context, scope originalBaseCallScope, selectedID string, selected []string, facts []originalDownloaderDeleteFact) error {
	paths := []string{}
	for _, fact := range facts {
		for path := range fact.Manifests {
			paths = append(paths, path)
		}
	}
	for _, configured := range a.Settings().Downloaders {
		// Trusted host safety read includes stopped instances; this is never
		// reachable as a public disabled downloader query.
		client, kind, guard, err := a.originalHostDownloaderBoundClient(ctx, scope, configured.ID, false)
		if err != nil {
			return err
		}
		body, err := client.Torrents(ctx, nil, false)
		if err != nil {
			client.Close()
			return bad("FILE_DOWNLOADER_UNVERIFIED", "无法核对其他任务的文件引用，未执行删除")
		}
		var rows []map[string]json.RawMessage
		if json.Unmarshal(body, &rows) != nil || rows == nil {
			client.Close()
			return bad("DOWNLOADER_INVALID_RESPONSE", "无法核对其他任务的文件引用")
		}
		hashes := []string{}
		key := "hash"
		if kind == "transmission" {
			key = "hashString"
		}
		for _, row := range rows {
			var hash string
			if json.Unmarshal(row[key], &hash) != nil || !plugins.DownloaderTaskHashValid(hash) {
				client.Close()
				return bad("DOWNLOADER_INVALID_RESPONSE", "其他下载任务Hash无效")
			}
			hashes = append(hashes, strings.ToLower(hash))
		}
		tasks, err := originalDownloaderTaskProjection(kind, hashes, body)
		if err != nil {
			client.Close()
			return err
		}
		err = func() error {
			defer client.Close()
			for _, task := range tasks {
				skip := false
				if configured.ID == selectedID {
					for _, hash := range selected {
						if task.Hash == hash {
							skip = true
							break
						}
					}
				}
				if skip {
					continue
				}
				directory := ""
				if task.QBittorrent != nil && task.QBittorrent.SavePath != nil {
					directory = *task.QBittorrent.SavePath
				}
				if task.Transmission != nil && task.Transmission.DownloadDir != nil {
					directory = *task.Transmission.DownloadDir
				}
				if !importPath(directory) {
					return bad("FILE_DOWNLOADER_UNVERIFIED", "其他下载任务的保存目录无法核对")
				}
				relevant := false
				for _, path := range paths {
					if _, overlap := sharedDirectoryPath(directory, path); overlap {
						relevant = true
						break
					}
				}
				if !relevant {
					continue
				}
				files, err := client.PluginHostFiles(ctx, task.Hash)
				if err != nil || len(files) == 0 {
					return bad("FILE_DOWNLOADER_UNVERIFIED", "相关下载任务的文件范围无法核对")
				}
				for _, file := range files {
					for _, path := range paths {
						if _, overlap := sharedDirectoryPath(filepath.Join(directory, file.Path), path); overlap {
							return bad("FILE_DOWNLOADER_SOURCE_PROTECTED", "任务文件仍被其他下载或做种任务引用，未执行删除")
						}
					}
				}
			}
			return guard(ctx)
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

// Same durable cancellation as downloaderTaskAction: selected intents and
// retries cannot re-add successfully deleted tasks. One transaction covers the
// entire exact selection; unrelated downloaders and hashes are untouched.
func (a *App) originalHostDownloaderCancelDeleted(ctx context.Context, id string, hashes []string) error {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, hash := range hashes {
		for _, kind := range []string{"intent", "native_download"} {
			if _, err = tx.ExecContext(ctx, `UPDATE records SET payload=json_set(payload,'$.state','cancelled','$.updated_at',?),updated_at=? WHERE kind=? AND json_extract(payload,'$.downloader_id')=? AND length(json_extract(payload,'$.hash')) IN (40,64) AND (lower(json_extract(payload,'$.hash'))=lower(?) OR (length(json_extract(payload,'$.hash'))!=length(?) AND substr(lower(json_extract(payload,'$.hash')),1,40)=lower(?)))`, now(), now(), kind, id, hash, hash, hash[:40]); err != nil {
				return err
			}
		}
		selected := `SELECT id FROM records WHERE kind IN ('intent','native_download') AND json_extract(payload,'$.downloader_id')=? AND json_extract(payload,'$.state')='cancelled' AND length(json_extract(payload,'$.hash')) IN (40,64) AND (lower(json_extract(payload,'$.hash'))=lower(?) OR (length(json_extract(payload,'$.hash'))!=length(?) AND substr(lower(json_extract(payload,'$.hash')),1,40)=lower(?)))`
		if _, err = tx.ExecContext(ctx, `UPDATE jobs SET state='cancelled',generation=generation+1,updated_at=? WHERE kind IN ('download','native_download') AND json_extract(payload,'$.id') IN (`+selected+`) AND state IN ('queued','retry_wait','waiting_paused')`, now(), id, hash, hash, hash[:40]); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM reservations WHERE intent_id IN (`+selected+`)`, id, hash, hash, hash[:40]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

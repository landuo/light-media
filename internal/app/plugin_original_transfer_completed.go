package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"light-media/internal/download"
	"light-media/internal/plugins"
)

// TransferChain.__default_callback marks a torrent only after its registered
// tasks end. Native import jobs group physical tasks, so check their committed
// callbacks after the existing job transaction, including failed callbacks.
// A false/scalar transfer result never invokes the source callback.
func (a *App) finishOriginalTransferTorrent(ctx context.Context, job Job, state string) error {
	if job.Kind != "import" || (state != "succeeded" && state != "failed") {
		return nil
	}
	var payload struct {
		ID    string   `json:"id"`
		Files []string `json:"file_ids"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	var plan ImportPlan
	if err := a.Store.Get(ctx, "import_plan", payload.ID, &plan); err != nil {
		return err
	}
	if plan.IntentID == "" {
		return nil
	}
	selected, err := selectImportFiles(plan, payload.Files)
	if err != nil || len(selected) == 0 {
		return err
	}
	for _, index := range selected {
		file := plan.Files[index]
		if file.Status == "already_imported" {
			continue // Source all-history-hit fallback.
		}
		var callback originalTransferCallback
		if err := a.Store.Get(ctx, "original_transfer_callback", plan.ID+"/"+file.ID, &callback); errors.Is(err, sql.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
	}
	var intent Intent
	if err := a.Store.Get(ctx, "intent", plan.IntentID, &intent); err != nil {
		return err
	}
	return a.markOriginalTorrentCompleted(ctx, intent)
}

func (a *App) originalTorrentTasksDone(ctx context.Context, hash string) (bool, error) {
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT json_extract(i.payload,'$.hash') FROM jobs j
		JOIN records p ON p.kind='import_plan' AND p.id=json_extract(j.payload,'$.id')
		JOIN records i ON i.kind='intent' AND i.id=json_extract(p.payload,'$.intent_id')
		WHERE j.kind='import' AND j.state NOT IN ('succeeded','failed','cancelled')`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var other string
		if err := rows.Scan(&other); err != nil {
			return false, err
		}
		if sameHash(hash, other) {
			return false, nil
		}
	}
	return true, rows.Err()
}

func (a *App) markOriginalTorrentCompleted(ctx context.Context, intent Intent) error {
	if intent.Hash == "" || intent.State == "cancelled" {
		return nil
	}
	done, err := a.originalTorrentTasksDone(ctx, intent.Hash)
	if err != nil || !done {
		return err
	}
	// A source module does not grant access to a different configured daemon.
	if err := a.guardDownloaderSnapshot(ctx, intent); err != nil {
		return err
	}
	d, err := a.downloader(intent.DownloaderID)
	if err != nil {
		return err
	}
	client, err := download.New(d.Config)
	if err != nil {
		return err
	}
	kwargs := map[string]any{"status": nil, "hashs": intent.Hash, "downloader": d.Name, "include_all_tags": false}
	result, err := a.originalTransferDownloaderModule(ctx, d, "list_torrents", kwargs, func() (any, error) {
		raw, err := client.SourceTorrentSnapshots(ctx, intent.Hash)
		if err != nil {
			a.log("ERROR", "downloads", "torrent_completion_query_failed", a.clean(err.Error()), intent.WorkflowID, "")
			return []map[string]any{}, nil // Source protocol helper logs and returns no rows.
		}
		return a.coreDeletedTorrentSnapshots(ctx, d, intent.Hash, raw)
	})
	if err != nil {
		return err
	}
	complete, err := originalTorrentDownloadCompleted(result)
	if err != nil || !complete {
		return err
	}
	// The list call can register new work. Repeat the source job-view check.
	done, err = a.originalTorrentTasksDone(ctx, intent.Hash)
	if err != nil || !done {
		return err
	}
	_, err = a.originalTransferDownloaderModule(ctx, d, "transfer_completed", map[string]any{"hashs": intent.Hash, "downloader": d.Name}, func() (any, error) {
		if err := a.guardDownloaderSnapshot(ctx, intent); err != nil {
			return nil, err
		}
		tasks, err := client.List(ctx)
		if err != nil {
			return nil, err
		}
		owned, err := a.downloaderTaskOwnership(ctx, d.ID)
		if err != nil {
			return nil, err
		}
		for _, task := range tasks {
			if sameHash(task.Hash, intent.Hash) && owned.contains(task) {
				tags := []string{"已整理"}
				if coreDownloaderKind(d.Kind) == "transmission" {
					// get_torrent_tags strips labels; set_torrent_tag writes a set.
					seen := map[string]bool{}
					tags = nil
					for _, tag := range append(append([]string{}, task.Tags...), "已整理") {
						tag = strings.TrimSpace(tag)
						if !seen[tag] {
							tags = append(tags, tag)
							seen[tag] = true
						}
					}
				}
				if err := client.SetTorrentTags(ctx, intent.Hash, tags); err != nil {
					// Both pinned protocol helpers catch write errors. The module
					// wrapper still returns None; no SystemError is broadcast.
					a.log("ERROR", "downloads", "torrent_completion_tag_failed", a.clean(err.Error()), intent.WorkflowID, "")
				}
				return nil, nil
			}
		}
		return nil, bad("REVIEW_DOWNLOAD_OWNERSHIP", "整理完成任务不再属于本应用")
	})
	return err
}

func originalTorrentDownloadCompleted(raw json.RawMessage) (bool, error) {
	if !originalModuleResultTruthy(raw) {
		return false, nil
	}
	var rows []json.RawMessage
	if json.Unmarshal(raw, &rows) != nil {
		var tuple bool
		rows, tuple = plugins.DecodeModuleResultTuple(raw)
		if !tuple {
			return false, bad("PLUGIN_MODULE_RESULT_INVALID", "下载完成检查没有收到任务序列")
		}
	}
	if len(rows) == 0 {
		return false, nil
	}
	for _, row := range rows {
		var fields map[string]json.RawMessage
		if json.Unmarshal(row, &fields) != nil || fields == nil {
			return false, bad("PLUGIN_MODULE_RESULT_INVALID", "下载任务进度无效")
		}
		progress, exists := fields["progress"]
		if !exists {
			return false, bad("PLUGIN_MODULE_RESULT_INVALID", "下载任务缺少进度")
		}
		if !originalModuleResultTruthy(progress) {
			return false, nil
		}
		var number float64
		if json.Unmarshal(progress, &number) != nil {
			return false, bad("PLUGIN_MODULE_RESULT_INVALID", "下载任务进度不是数值")
		}
		if number < 100 {
			return false, nil
		}
	}
	return true, nil
}

// Native downloader providers also follow ChainBase list accumulation and
// exception notification. The completion caller ignores the final value.
func (a *App) originalTransferDownloaderModule(ctx context.Context, d DownloaderConfig, method string, kwargs map[string]any, native func() (any, error)) (json.RawMessage, error) {
	encoded, err := json.Marshal(kwargs)
	if err != nil {
		return nil, err
	}
	result, err := a.invokeOriginalPluginModuleGroups(ctx, method, nil, json.RawMessage(`[]`), encoded, nil, nil)
	if err != nil {
		return nil, err
	}
	var list []json.RawMessage
	accumulate := json.Unmarshal(result, &list) == nil && list != nil
	if !originalModuleResultEmpty(result) && !accumulate {
		return result, nil
	}
	value, err := native()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		module := "Qbittorrent"
		if strings.EqualFold(coreDownloaderKind(d.Kind), "transmission") {
			module = "Transmission"
		}
		a.reportOriginalPluginExecutionError(ctx, "system", module+"发生了错误", map[string]any{"type": "module", "module_id": module, "module_name": module, "module_method": method}, err)
		value = nil
	}
	encoded, err = json.Marshal(value)
	if !accumulate {
		return encoded, err
	}
	var extra []json.RawMessage
	if json.Unmarshal(encoded, &extra) == nil {
		list = append(list, extra...)
	}
	return json.Marshal(list)
}

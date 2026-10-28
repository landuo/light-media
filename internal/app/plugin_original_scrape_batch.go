package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// TransferChain registers before queueing and flushes only after registration
// closes and all tasks finish. Existing durable jobs supply the pending set;
// failed/cancelled jobs finish it, while retries and paused jobs remain pending.
type originalScrapeBatchKey struct{}
type originalScrapeTarget struct {
	Event  json.RawMessage `json:"event"`
	Assets []string        `json:"assets"`
}
type originalScrapeBatch struct {
	ID      string                 `json:"id"`
	Closed  bool                   `json:"closed"`
	Flushed bool                   `json:"flushed"`
	Targets []originalScrapeTarget `json:"targets"`
}

func originalScrapeBatchID(job Job) string {
	var payload struct {
		Batch string `json:"original_scrape_batch"`
	}
	_ = json.Unmarshal(job.Payload, &payload)
	return payload.Batch
}

// An explicit retry after a finished batch is a new transfer attempt. Keep
// automatic retry_wait in the original pending set, but never reopen and replay
// already delivered targets when the administrator retries a terminal job.
func (a *App) retryOriginalScrapeJob(ctx context.Context, job Job) (sql.Result, error) {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	payload := job.Payload
	if key := originalScrapeBatchID(job); key != "" {
		batch, err := readOriginalScrapeBatch(ctx, tx, key)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil && batch.Flushed {
			var fields map[string]json.RawMessage
			if err = json.Unmarshal(payload, &fields); err != nil {
				return nil, err
			}
			key = id("scrape_batch_")
			fields["original_scrape_batch"], err = json.Marshal(key)
			if err != nil {
				return nil, err
			}
			payload, err = json.Marshal(fields)
			if err != nil {
				return nil, err
			}
			if err = txPut(ctx, tx, "original_scrape_batch", key, originalScrapeBatch{ID: key, Closed: true}); err != nil {
				return nil, err
			}
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET payload=?,state='queued',attempt=0,generation=generation+1,run_at=?,error_code='',error_message='',updated_at=? WHERE id=? AND generation=? AND state=?`, payload, time.Now().Unix(), now(), job.ID, job.Generation, job.State)
	if err != nil {
		return nil, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return result, nil
	}
	return result, tx.Commit()
}

// These existing cleanup jobs complete the same local publication. Carry its
// ordinary scrape context through them; native-only legacy jobs keep their old
// behavior. A cleanup failure must not register a successful scrape target.
func deferOriginalScrapeTargetTx(ctx context.Context, tx *sql.Tx, parent Job, kind, key string, raw json.RawMessage, enabled bool) error {
	if enabled && len(raw) == 0 {
		return nil
	}
	var jobID string
	var payloadRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT id,payload FROM jobs WHERE kind=? AND json_extract(payload,'$.id')=? ORDER BY created_at DESC LIMIT 1`, kind, key).Scan(&jobID, &payloadRaw); err != nil {
		return err
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(payloadRaw, &payload); err != nil {
		return err
	}
	batchID := originalScrapeBatchID(parent)
	if batchID == "" {
		batchID = id("scrape_batch_")
		if err := txPut(ctx, tx, "original_scrape_batch", batchID, originalScrapeBatch{ID: batchID, Closed: true}); err != nil {
			return err
		}
	}
	payload["original_scrape_batch"], _ = json.Marshal(batchID)
	payload["original_scrape_deferred"] = json.RawMessage("true")
	if enabled {
		payload["original_metadata_event"] = raw
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE jobs SET payload=? WHERE id=?`, encoded, jobID)
	return err
}

func completeOriginalDeferredScrapeTx(ctx context.Context, tx *sql.Tx, job Job, assetID string) (bool, error) {
	var payload struct {
		Deferred bool            `json:"original_scrape_deferred"`
		Event    json.RawMessage `json:"original_metadata_event"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return false, err
	}
	if !payload.Deferred {
		return false, nil
	}
	if len(payload.Event) == 0 {
		return true, nil // need_scrape=false remains false after cleanup.
	}
	return recordOriginalScrapeTargetTx(ctx, tx, job, payload.Event, assetID)
}

func readOriginalScrapeBatch(ctx context.Context, tx *sql.Tx, key string) (originalScrapeBatch, error) {
	var raw []byte
	var batch originalScrapeBatch
	err := tx.QueryRowContext(ctx, `SELECT payload FROM records WHERE kind='original_scrape_batch' AND id=?`, key).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &batch)
	}
	return batch, err
}

// Preserve first-seen root order, first truthy metadata and stable unique files.
// The fixed key is (storage,path), including its trailing slash.
func (batch *originalScrapeBatch) add(raw json.RawMessage, assetID string) error {
	value, err := decodeSourceJSON(raw)
	if err != nil {
		return err
	}
	incoming := sourceMap(value)
	item := sourceMap(incoming["fileitem"])
	if item == nil {
		return bad("PLUGIN_EVENT_INVALID", "刮削目标目录缺失")
	}
	index := -1
	var target map[string]any
	for i, row := range batch.Targets {
		v, err := decodeSourceJSON(row.Event)
		if err != nil {
			return err
		}
		candidate := sourceMap(v)
		dir := sourceMap(candidate["fileitem"])
		if dir["storage"] == item["storage"] && dir["path"] == item["path"] {
			index, target = i, candidate
			break
		}
	}
	if index < 0 {
		target = map[string]any{"fileitem": item, "meta": incoming["meta"], "mediainfo": incoming["mediainfo"], "file_list": []any{}, "overwrite": false}
		batch.Targets = append(batch.Targets, originalScrapeTarget{})
		index = len(batch.Targets) - 1
	}
	for _, key := range []string{"meta", "mediainfo"} {
		if !sourceSubscriptionTruth(target[key]) {
			target[key] = incoming[key]
		}
	}
	files, _ := target["file_list"].([]any)
	seen := map[string]bool{}
	for _, file := range files {
		seen[sourceText(file)] = true
	}
	if sourceSubscriptionTruth(incoming["file_list"]) {
		list, ok := incoming["file_list"].([]any)
		if !ok {
			return bad("PLUGIN_EVENT_INVALID", "刮削文件列表必须是路径数组")
		}
		for _, file := range list {
			path, ok := file.(string)
			if !ok {
				return bad("PLUGIN_EVENT_INVALID", "刮削文件路径必须是字符串")
			}
			if path != "" && !seen[path] {
				files = append(files, path)
				seen[path] = true
			}
		}
	}
	target["file_list"] = files
	batch.Targets[index].Event, err = json.Marshal(target)
	if err != nil {
		return err
	}
	for _, id := range batch.Targets[index].Assets {
		if id == assetID {
			return nil
		}
	}
	if assetID != "" {
		batch.Targets[index].Assets = append(batch.Targets[index].Assets, assetID)
	}
	return nil
}

func recordOriginalScrapeTargetTx(ctx context.Context, tx *sql.Tx, job Job, raw json.RawMessage, assetID string) (bool, error) {
	key := originalScrapeBatchID(job)
	if key == "" || len(raw) == 0 {
		return false, nil
	} // Existing pre-migration jobs.
	batch, err := readOriginalScrapeBatch(ctx, tx, key)
	if err != nil {
		return false, err
	}
	if batch.Flushed {
		return true, nil
	} // Recovery cannot replay a completed batch.
	if err = batch.add(raw, assetID); err != nil {
		return false, err
	}
	return true, txPut(ctx, tx, "original_scrape_batch", key, batch)
}

func flushOriginalScrapeBatchTx(ctx context.Context, tx *sql.Tx, key string, close bool) ([]json.RawMessage, error) {
	if key == "" {
		return nil, nil
	}
	batch, err := readOriginalScrapeBatch(ctx, tx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil || batch.Flushed {
		return nil, err
	}
	if close {
		batch.Closed = true
	}
	var pending int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind IN ('import','reorganize_cleanup','quality_cleanup') AND json_extract(payload,'$.original_scrape_batch')=? AND state NOT IN ('succeeded','failed','cancelled','needs_review')`, key).Scan(&pending); err != nil {
		return nil, err
	}
	var events []json.RawMessage
	if batch.Closed && pending == 0 {
		for _, target := range batch.Targets {
			if len(target.Assets) == 0 {
				continue
			}
			payload := map[string]any{"id": target.Assets[0], "original_metadata_assets": target.Assets, "original_metadata_event": target.Event}
			if _, err = enqueueTx(ctx, tx, "scrape", payload, key); err != nil {
				return nil, err
			}
			events = append(events, target.Event)
		}
		batch.Flushed = true
	}
	return events, txPut(ctx, tx, "original_scrape_batch", key, batch)
}

func (a *App) broadcastOriginalScrapeBatch(ctx context.Context, events []json.RawMessage) {
	for _, event := range events {
		a.broadcastOriginalPluginEventCoreState(ctx, "metadata.scrape", event, true)
	}
}

func (a *App) closeOriginalScrapeBatch(ctx context.Context, key string, close bool) error {
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	events, err := flushOriginalScrapeBatchTx(ctx, tx, key, close)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	a.broadcastOriginalScrapeBatch(ctx, events)
	return nil
}

func (a *App) recoverOriginalScrapeBatches(ctx context.Context) error {
	rows, err := a.Store.DB.QueryContext(ctx, `SELECT id FROM records WHERE kind='original_scrape_batch' AND json_extract(payload,'$.flushed')=0 ORDER BY updated_at,id`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, key := range ids {
		if err = a.closeOriginalScrapeBatch(ctx, key, true); err != nil {
			return err
		}
	}
	return nil
}

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
)

func originalSharedHistoryCapability(method string) bool {
	return method == plugins.TransferHistoryBySourceCapability || method == plugins.TransferHistoryByDestinationCapability
}

// Only completed native attempts are source history rows. A newly queued retry
// must not hide its last completed source record. This is a read-only view over
// the same plans, jobs, callbacks and deletion receipts as the native UI.
const sharedTransferHistoryCTE = transferHistoryBaseCTE + `, source_attempts AS (
 SELECT e.* FROM executed_files e
 LEFT JOIN records m ON m.kind='import_meta' AND m.id=e.plan_id
 LEFT JOIN json_each(m.payload,'$.shared_history') frozen ON frozen.key=json_extract(e.file,'$.id')
 WHERE (json_extract(frozen.value,'$.final')=1 AND json_extract(frozen.value,'$.job_id')=e.job_id)
 OR (COALESCE(json_extract(frozen.value,'$.job_id'),'')='' AND e.file_rank=1 AND e.status!='pending')
 UNION ALL SELECT * FROM auxiliary_files WHERE file_rank=1 AND status!='pending'
), completed_history AS (
 SELECT *,
 FIRST_VALUE(json_extract(file,'$.id')) OVER (PARTITION BY resource_source ORDER BY first_execution_key,json_extract(file,'$.id')) AS history_id,
 ROW_NUMBER() OVER (PARTITION BY resource_source ORDER BY execution_key DESC,json_extract(file,'$.id')) AS history_rank
 FROM source_attempts
), source_history AS (
 SELECT * FROM completed_history h WHERE history_rank=1 AND NOT EXISTS (
 SELECT 1 FROM records hidden JOIN attempts old ON hidden.id=json_extract(old.file,'$.id')
 WHERE hidden.kind='transfer_history_hidden' AND old.resource_source=h.resource_source
 AND (CASE WHEN COALESCE(json_extract(hidden.payload,'$.execution_key'),'')!=''
 THEN json_extract(hidden.payload,'$.execution_key')>=h.execution_key
 ELSE julianday(hidden.updated_at)>=julianday(h.executed_at) END)
 )
) `

func (a *App) originalSharedHistoryResource(ctx context.Context, scope originalBaseCallScope, method string, raw json.RawMessage) (json.RawMessage, error) {
	column, frozenColumn, storageColumn := "source", "src", "src_storage"
	switch method {
	case plugins.TransferHistoryBySourceCapability:
	case plugins.TransferHistoryByDestinationCapability:
		column, frozenColumn, storageColumn = "target", "dest", "dest_storage"
	default:
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "历史资源方法未获授权")
	}
	values, decodeErr := decodeSharedHistoryQuery(raw, frozenColumn)
	if decodeErr != nil {
		return nil, decodeErr
	}
	path, storage := values[frozenColumn], values["storage"]
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	var planRaw, fileRaw []byte
	var frozenRaw sql.NullString
	var id, status string
	// The query operand is bound even for null/empty input; no path cleaning,
	// case folding, stat or filesystem root access is performed for a DB lookup.
	var operand any
	if path != nil {
		operand = *path
	}
	storageFilter, storageValue := false, ""
	if storage != nil && *storage != "" {
		storageFilter, storageValue = true, *storage
	}
	err := a.Store.DB.QueryRowContext(ctx, sharedTransferHistoryCTE+`SELECT h.plan,h.file,h.history_id,h.status,frozen.value FROM source_history h
 LEFT JOIN records m ON m.kind='import_meta' AND m.id=h.plan_id
 LEFT JOIN json_each(m.payload,'$.shared_history') frozen ON frozen.key=json_extract(h.file,'$.id')
 WHERE (CASE WHEN json_type(frozen.value,'$.row')='object' THEN json_extract(frozen.value,'$.row.`+frozenColumn+`') ELSE json_extract(h.file,'$.`+column+`') END) IS ?
 AND (?=0 OR (CASE WHEN json_type(frozen.value,'$.row')='object' THEN json_extract(frozen.value,'$.row.`+storageColumn+`') ELSE 'local' END)=?)
 ORDER BY h.first_execution_key,json_extract(h.file,'$.id') LIMIT 1`, operand, storageFilter, storageValue).Scan(&planRaw, &fileRaw, &id, &status, &frozenRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return json.RawMessage(`null`), nil
	}
	if err != nil {
		return nil, err
	}
	var plan ImportPlan
	var file PlanFile
	if err = json.Unmarshal(planRaw, &plan); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(fileRaw, &file); err != nil {
		return nil, err
	}
	row := sharedHistoryLegacyTransfer(plan, file, status)
	if frozenRaw.Valid {
		var frozen sharedTransferHistorySnapshot
		if err = json.Unmarshal([]byte(frozenRaw.String), &frozen); err != nil {
			return nil, err
		}
		row = frozen.Row
		if !frozen.Final {
			row.Status = sharedHistoryPtr(status == "success")
		}
	}
	row.ID = id
	if err := scope.validate(ctx); err != nil {
		return nil, err
	}
	return json.Marshal(row)
}

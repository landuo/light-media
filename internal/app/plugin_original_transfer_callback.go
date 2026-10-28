package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// A source callback belongs to one physical task, including subtitles/audio.
// Its history commits before its ordinary completion/failure broadcast. The
// existing asset transaction still independently verifies grouped ownership.
type originalTransferCallback struct {
	PlanID       string   `json:"plan_id"`
	ParentFileID string   `json:"parent_file_id"`
	JobID        string   `json:"job_id"`
	File         PlanFile `json:"file"`
}

type originalTransferCallbackError struct{ cause error }

func (e *originalTransferCallbackError) Error() string { return e.cause.Error() }
func (e *originalTransferCallbackError) Unwrap() error { return e.cause }

func (a *App) completeOriginalTransferCallback(ctx context.Context, job Job, plan ImportPlan, primaryID string, file PlanFile, source originalTransferFileContext, success bool, message string) error {
	if len(source.Meta) == 0 || len(source.Media) == 0 {
		return nil
	}
	key := plan.ID + "/" + file.ID
	var prior originalTransferCallback
	if err := a.Store.Get(ctx, "original_transfer_callback", key, &prior); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	info, called, err := a.originalRecordedTransferModule(ctx, plan, file)
	if err != nil {
		return err
	}
	if called {
		// False/scalar/dict are distinct source outcomes: they never enter
		// __default_callback. A rejected physical success is not completed.
		if info == nil || (info["success"] == true) != success {
			return nil
		}
	} else {
		var failure error
		if !success {
			failure = bad("TRANSFER_FAILED", message)
		}
		result, err := a.originalNativeTransferModuleInfo(ctx, file, plan, failure)
		if err != nil {
			return err
		}
		info = result["__plugin_transfer_info"].(map[string]any)
		var meta importMetadata
		if err := a.Store.Get(ctx, "import_meta", plan.ID, &meta); err != nil {
			return err
		}
		if meta.OriginalTransfer == nil {
			meta.OriginalTransfer = map[string]originalTransferFileContext{}
		}
		meta.OriginalTransfer[file.ID] = source
		kwargs, err := a.originalTransferModuleArguments(ctx, plan, file, meta)
		if err != nil {
			return err
		}
		if file.DirectoryDigest == "" {
			info["fileitem"] = kwargs["fileitem"]
		}
		if err := originalTransferApplyNativeFlags(info, kwargs, success); err != nil {
			return err
		}
		// The direct native path contains typed FileItems; the module-dispatch
		// path already has decoded JSON maps. Normalize through the same DTO
		// before extracting target_item for history and ordinary events.
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		info, err = originalTransferModuleInfo(raw)
		if err != nil {
			return err
		}
	}
	historyFile := file
	if file.ID != primaryID {
		historyFile.ID = "file_" + tokenHash(key)[:32]
		historyFile.ParentFileID = primaryID
	}
	historyFile.OriginalTransferHistory = true
	historyFile.Target = sourceText(sourceMap(info["target_item"])["path"])
	if historyFile.DirectoryDigest != "" {
		historyFile.Target = strings.TrimSuffix(historyFile.Target, "/")
		// TransferHistoryOper persists task.fileitem.size. For native Blu-ray
		// this is the immediate STREAM sum, not the full ownership tree size.
		// Keep the frozen plan snapshot unchanged for execution and deletion.
		historyFile.Size = 0
		if source.Item != nil {
			_ = json.Unmarshal(source.Item.Fields["size"], &historyFile.Size)
		}
		var receipt importReceipt
		if err := a.Store.Get(ctx, "import_receipt", key, &receipt); err == nil {
			if receipt.DirectoryStreamSize != nil {
				historyFile.Size = *receipt.DirectoryStreamSize
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	historyFile.Method = sourceText(info["transfer_type"])
	if historyFile.Method == "link" {
		historyFile.Method = "hardlink"
	}
	historyFile.Status, historyFile.Reason = "imported", ""
	if !success {
		historyFile.Status, historyFile.Reason = "error", sourceText(info["message"])
		if historyFile.Reason == "" {
			historyFile.Reason = "未知错误"
		}
	}
	callback := originalTransferCallback{PlanID: plan.ID, ParentFileID: primaryID, JobID: job.ID, File: historyFile}
	tx, err := a.Store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = qualityRecord(ctx, tx, "original_transfer_callback", key, &prior); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = txPut(ctx, tx, "original_transfer_callback", key, callback); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	history, err := a.transferHistoryItem(ctx, historyFile.ID)
	if err != nil {
		return err
	}
	a.emitOriginalTransferResult(ctx, plan, file, source, success, message, history.ID, info)
	return nil
}

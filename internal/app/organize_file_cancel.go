package app

import "context"

func (a *App) organizeImportFileCancelled(ctx context.Context, job Job, fileID string) error {
	if err := a.jobCancelled(ctx, job); err != nil {
		return err
	}
	var cancelled bool
	if err := a.Store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM records WHERE kind='organize_file_cancel' AND id=?)`, job.ID+"/"+fileID).Scan(&cancelled); err != nil {
		return err
	}
	if cancelled {
		return bad("ORGANIZE_FILE_CANCELLED", "此文件已从整理队列移除")
	}
	return nil
}

package plugins

import (
	"context"
	"errors"
)

// DeleteFiles is separate from Control.remove_task: this explicit capability
// crosses the host's current ownership, local-root and file-snapshot boundary.
const DownloaderDeleteFilesCapability = "downloader.tasks.delete_files"

type DownloaderDeleteFilesRequestV1 struct {
	DownloaderID   string   `json:"downloader_id"`
	Hashes         []string `json:"hashes"`
	BrushOwnership bool     `json:"brush_ownership,omitempty"`
}

type DownloaderDeleteFilesV1 struct {
	SchemaVersion        int    `json:"schema_version"`
	DownloaderID         string `json:"downloader_id"`
	Accepted             bool   `json:"accepted"`
	Outcome              string `json:"outcome"`
	DaemonApplied        bool   `json:"daemon_applied"`
	BookkeepingCompleted bool   `json:"bookkeeping_completed"`
	FailureCode          string `json:"failure_code,omitempty"`
}

func (r DownloaderDeleteFilesRequestV1) Validate() error {
	hashes, err := (DownloaderTasksRequestV1{DownloaderID: r.DownloaderID, Hashes: r.Hashes}).CanonicalSelection()
	if err != nil || len(hashes) == 0 {
		return errors.New("delete-files requires explicit downloader and nonempty exact hashes")
	}
	return nil
}

func (d Downloaders) DeleteFiles(ctx context.Context, request DownloaderDeleteFilesRequestV1) (DownloaderDeleteFilesV1, error) {
	var out DownloaderDeleteFilesV1
	if err := request.Validate(); err != nil {
		return out, err
	}
	if err := d.hostRequest(ctx, DownloaderDeleteFilesCapability, request, &out); err != nil {
		return out, err
	}
	if out.SchemaVersion != 1 || out.DownloaderID != request.DownloaderID || out.Outcome != "confirmed" && out.Outcome != "unknown" && out.Outcome != "rejected" || out.DaemonApplied && (!out.Accepted || out.Outcome != "confirmed") || out.BookkeepingCompleted && !out.DaemonApplied {
		return DownloaderDeleteFilesV1{}, errors.New("invalid delete-files receipt")
	}
	return out, nil
}

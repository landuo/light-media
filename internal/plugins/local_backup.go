package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
)

const LocalBackupCapability = "backup.local"

// Path and Retain describe the running AutoBackup instance's applied settings.
// The host independently checks both values and the destination grant.
type LocalBackupRequestV1 struct {
	Path   string          `json:"path"`
	Retain json.RawMessage `json:"retain,omitempty"`
}

// Counts keep Python's arbitrary precision and signed retention arithmetic.
// Only the finished archive's path crosses the worker boundary.
type LocalBackupResultV1 struct {
	SchemaVersion  int         `json:"schema_version"`
	ArchivePath    string      `json:"archive_path"`
	ArchiveCreated bool        `json:"archive_created"`
	BackupCount    int         `json:"backup_count"`
	DeleteCount    json.Number `json:"delete_count"`
	RemainingCount json.Number `json:"remaining_count"`
}

type LocalBackup struct{ Host HostCaller }

func (b LocalBackup) Run(ctx context.Context, request LocalBackupRequestV1) (LocalBackupResultV1, error) {
	var result LocalBackupResultV1
	err := hostBusinessCall(ctx, b.Host, LocalBackupCapability, request, &result)
	if err == nil && (result.SchemaVersion != 1 || result.BackupCount < 0 || result.DeleteCount == "" || result.RemainingCount == "" ||
		result.ArchiveCreated != (result.ArchivePath != "") || result.ArchiveCreated && !filepath.IsAbs(result.ArchivePath)) {
		err = errors.New("invalid local backup result")
	}
	return result, err
}

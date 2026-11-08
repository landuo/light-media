package plugins

import (
	"context"
	"errors"
	"path/filepath"
)

const StorageLocalReadGrantCapability = "storage.files.local_read_grant"

// StorageLocalReadGrantV1 identifies one regular file in a current local root.
// Before using bytes, consumers must verify the open file matches the grant
// and verify its identity again after reading.
type StorageLocalReadGrantV1 struct {
	SchemaVersion    int    `json:"schema_version"`
	Root             string `json:"root"`
	RelativePath     string `json:"relative_path"`
	Device           uint64 `json:"device"`
	Inode            uint64 `json:"inode"`
	Size             int64  `json:"size"`
	ModifiedUnixNano int64  `json:"modified_unix_nano"`
}

func (s Storage) LocalReadGrant(ctx context.Context, path string) (StorageLocalReadGrantV1, error) {
	request := struct {
		Path string `json:"path"`
	}{Path: path}
	var grant StorageLocalReadGrantV1
	if err := hostServiceCall(ctx, s.Host, StorageLocalReadGrantCapability, request, &grant); err != nil {
		return StorageLocalReadGrantV1{}, err
	}
	if grant.SchemaVersion != 1 || grant.Size < 0 || !filepath.IsAbs(grant.Root) || grant.Root == "/" || filepath.Clean(grant.Root) != grant.Root ||
		!filepath.IsLocal(grant.RelativePath) || grant.RelativePath == "." || filepath.Clean(grant.RelativePath) != grant.RelativePath || filepath.Join(grant.Root, grant.RelativePath) != path {
		return StorageLocalReadGrantV1{}, errors.New("invalid local file read grant")
	}
	return grant, nil
}

package plugins

import (
	"context"
	"encoding/json"
)

const StorageExistsCapability = "storage.files.exists"
const StorageListCapability = "storage.files.list"
const StorageDeleteCapability = "storage.files.delete"
const ScrapeCleanRemoveCapability = "storage.files.scrape_clean_remove"

// Storage uses the host's configured local roots and current invocation. A
// FileItem is descriptive data; it does not itself grant filesystem authority.
type Storage struct{ Host HostCaller }

type StorageFilesRequest struct {
	FileItem  *ModuleFileItem `json:"fileitem"`
	Recursion bool            `json:"recursion,omitempty"`
}

func (s Storage) Exists(ctx context.Context, item *ModuleFileItem) (*bool, error) {
	var value *bool
	err := hostServiceCall(ctx, s.Host, StorageExistsCapability, StorageFilesRequest{FileItem: item}, &value)
	return value, err
}

func (s Storage) List(ctx context.Context, item *ModuleFileItem, recursion bool) ([]*ModuleFileItem, error) {
	var value []*ModuleFileItem
	err := hostServiceCall(ctx, s.Host, StorageListCapability, StorageFilesRequest{FileItem: item, Recursion: recursion}, &value)
	return value, err
}

func (s Storage) Delete(ctx context.Context, item *ModuleFileItem) (*bool, error) {
	var value *bool
	err := hostServiceCall(ctx, s.Host, StorageDeleteCapability, StorageFilesRequest{FileItem: item}, &value)
	return value, err
}

// RawItem permits forward-compatible FileItem fields while keeping the host
// operation vocabulary finite.
func (r StorageFilesRequest) RawItem() json.RawMessage {
	raw, _ := json.Marshal(r.FileItem)
	return raw
}

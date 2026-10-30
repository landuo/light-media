package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
)

const (
	LocalStorageExistsCapability = "storage.local.exists"
	LocalStorageListCapability   = "storage.local.list"
	LocalStorageDeleteCapability = "storage.local.delete_file"
)

// LocalStorageRequest retains the complete source FileItem, including explicit
// nulls. Missing fields use NewModuleFileItem's source constructor defaults.
// This producer slice accepts standard LocalStorage/RemoveLink values, not all
// possible Pydantic coercions (e.g. nonfinite/Unicode-number modify_time).
// Paths confer no authority: the host restricts all calls to its current managed
// local roots, excludes internal/private directories and never follows links.
type LocalStorageRequest struct {
	FileItem  *ModuleFileItem `json:"fileitem"`
	Recursion bool            `json:"recursion,omitempty"`
}

// SharedLocalStorage actively invokes the host StorageChain. This differs from
// StorageBase (a plugin-provided module) and Base.GetDataPath (private files).
// List/Delete return source JSON because installed module overrides may return
// nullable or other scalar values; callers can decode normal lists/bools without
// silently coercing an override. The ordinary native list retains FileItem fields.
type SharedLocalStorage struct{ Host HostCaller }

func (s SharedLocalStorage) call(ctx context.Context, method string, in LocalStorageRequest) (json.RawMessage, error) {
	if s.Host == nil {
		return nil, errors.New("local storage requires an active host caller")
	}
	if e := ValidateLocalStoragePathEncoding(in.FileItem); e != nil {
		return nil, e
	}
	raw, e := json.Marshal(in)
	if e != nil {
		return nil, e
	}
	return s.Host(ctx, method, raw)
}
func (s SharedLocalStorage) Exists(ctx context.Context, item *ModuleFileItem) (bool, error) {
	raw, e := s.call(ctx, LocalStorageExistsCapability, LocalStorageRequest{FileItem: item})
	if e != nil {
		return false, e
	}
	var out bool
	e = json.Unmarshal(raw, &out)
	return out, e
}
func (s SharedLocalStorage) List(ctx context.Context, item *ModuleFileItem, recursion bool) (json.RawMessage, error) {
	return s.call(ctx, LocalStorageListCapability, LocalStorageRequest{FileItem: item, Recursion: recursion})
}
func (s SharedLocalStorage) DeleteFile(ctx context.Context, item *ModuleFileItem) (json.RawMessage, error) {
	return s.call(ctx, LocalStorageDeleteCapability, LocalStorageRequest{FileItem: item})
}

// ValidateLocalStoragePathEncoding checks the original JSON token before Go
// string decoding can silently replace a lone surrogate with U+FFFD. The host
// repeats this check; SDK validation is never filesystem authority. Ordinary
// Unicode scalars (including escaped surrogate pairs) remain unchanged.
func ValidateLocalStoragePathEncoding(item *ModuleFileItem) error {
	if item == nil {
		return nil
	}
	storage, exists := item.Fields["storage"]
	if exists {
		points, e := DecodeSystemConfigJSONString(storage)
		if e != nil || len(points) != 5 || points[0] != 'l' || points[1] != 'o' || points[2] != 'c' || points[3] != 'a' || points[4] != 'l' {
			return nil
		}
	}
	raw, exists := item.Fields["path"]
	raw = bytes.TrimSpace(raw)
	if !exists || len(raw) == 0 || raw[0] != '"' {
		return nil
	}
	points, e := DecodeSystemConfigJSONString(raw)
	if e != nil {
		return errors.New("invalid local filesystem path encoding")
	}
	for _, r := range points {
		if r >= 0xd800 && r <= 0xdfff {
			return errors.New("local filesystem paths require Unicode scalar values")
		}
	}
	return nil
}

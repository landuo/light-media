package mediaservervendors

import (
	"context"
	"encoding/json"

	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
)

// Nil preserves original None (not authenticated); false is a real scan result.
// Refresh logs retain the existing redacted operation/severity/class boundary,
// not raw library names, URLs, authentication tokens or vendor exception text.
func (v *Ugreen) RefreshRootLibrary(ctx context.Context) (*bool, error) {
	return v.RefreshRootLibraryWithScanMode(ctx, json.RawMessage(`null`))
}
func (v *Ugreen) RefreshRootLibraryWithScanMode(ctx context.Context, mode json.RawMessage) (*bool, error) {
	v.operation.Lock()
	defer v.operation.Unlock()
	return v.refreshRootLibrary(ctx, mode)
}
func (v *Ugreen) refreshRootLibrary(ctx context.Context, mode json.RawMessage) (*bool, error) {
	authenticated, err := v.authenticated(ctx)
	if err != nil || !authenticated {
		return nil, err
	}
	api, err := v.refreshAPI()
	if err != nil || api == nil {
		return nil, err
	}
	v.mu.RLock()
	libraries := ejCopy(v.state.Libraries)
	v.mu.RUnlock()
	if !ejTruth(libraries) {
		if _, err := v.refreshLoadLibraries(ctx); err != nil {
			return nil, err
		}
		v.mu.RLock()
		libraries = ejCopy(v.state.Libraries)
		v.mu.RUnlock()
	}
	v.mu.RLock()
	scanType := v.state.ScanType
	v.mu.RUnlock()
	if !ejNull(mode) {
		scanType, err = ugreenScanType(mode, json.RawMessage(`null`))
		if err != nil {
			return nil, err
		}
	}
	cached, err := ejObject(libraries)
	if err != nil {
		return nil, ejSource("AttributeError")
	}
	all := true
	for _, id := range cached.keys {
		v.report("refresh_root_library", "info", "RefreshLibrary")
		ok, err := v.refreshScanLibrary(ctx, id, scanType)
		if err != nil {
			return nil, err
		}
		// Source builds every result before all(): do not short-circuit on false.
		all = all && ok
	}
	return &all, nil
}

func (v *Ugreen) RefreshLibraryByItems(ctx context.Context, items []mediaserverruntime.ValidatedRefreshItem) (*bool, error) {
	return v.RefreshLibraryByItemsWithScanMode(ctx, items, json.RawMessage(`null`))
}
func (v *Ugreen) RefreshLibraryByItemsWithScanMode(ctx context.Context, items []mediaserverruntime.ValidatedRefreshItem, mode json.RawMessage) (*bool, error) {
	v.operation.Lock()
	defer v.operation.Unlock()
	authenticated, err := v.authenticated(ctx)
	if err != nil || !authenticated {
		return nil, err
	}
	api, err := v.refreshAPI()
	if err != nil || api == nil {
		return nil, err
	}
	v.mu.RLock()
	scanType := v.state.ScanType
	v.mu.RUnlock()
	if !ejNull(mode) {
		scanType, err = ugreenScanType(mode, json.RawMessage(`null`))
		if err != nil {
			return nil, err
		}
	}
	// Real shared CPython string-set semantics, not insertion/sorted/Go-map order.
	// That dependency has its own source/runtime gate; no fake fallback is used.
	ids, err := NewSourceStringSet()
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		owned, err := item.OwnedCopy()
		if err != nil {
			return nil, err
		}
		id, err := v.refreshMatchLibrary(ctx, owned.TargetPath)
		if err != nil {
			return nil, err
		}
		if id == nil {
			return v.refreshRootLibrary(ctx, mode)
		}
		points, err := plugins.DecodeSystemConfigJSONString(id)
		if err != nil {
			return nil, err
		}
		if err := ids.AddCodepoints(points); err != nil {
			return nil, err
		}
	}
	for _, points := range ids.ValuesCodepoints() {
		id, err := plugins.EncodeSystemConfigJSONString(points)
		if err != nil {
			return nil, err
		}
		v.report("refresh_library_by_items", "info", "RefreshLibrary")
		ok, err := v.refreshScanLibrary(ctx, id, scanType)
		if err != nil {
			return nil, err
		}
		if !ok {
			return v.refreshRootLibrary(ctx, mode)
		}
	}
	value := true
	return &value, nil
}
func (v *Ugreen) refreshMatchLibrary(ctx context.Context, path json.RawMessage) (json.RawMessage, error) {
	if ejNull(path) {
		return nil, nil
	}
	normalized, err := ugreenRefreshNormalizePath(path)
	if err != nil {
		return nil, err
	}
	normalizedJSON, err := plugins.EncodeSystemConfigJSONString(normalized)
	if err != nil {
		return nil, err
	}
	v.mu.RLock()
	pathsRaw := ejCopy(v.state.LibraryPaths)
	v.mu.RUnlock()
	if !ejTruth(pathsRaw) {
		if _, err := v.refreshLoadLibraries(ctx); err != nil {
			return nil, err
		}
		v.mu.RLock()
		pathsRaw = ejCopy(v.state.LibraryPaths)
		v.mu.RUnlock()
	}
	paths, err := ejObject(pathsRaw)
	if err != nil {
		return nil, ejSource("AttributeError")
	}
	for _, id := range paths.keys {
		match, err := ugreenRefreshIsSubpath(normalizedJSON, paths.values[string(id)])
		if err != nil {
			return nil, err
		}
		if match {
			return ejCopy(id), nil
		}
	}
	return nil, nil
}

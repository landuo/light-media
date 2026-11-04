package app

import (
	"context"
	"encoding/json"
	"errors"

	"light-media/internal/plugins"
	"light-media/internal/plugins/mediaserverruntime"
	"light-media/internal/plugins/mediaservervendors"
)

func originalMediaServerRefreshCapability(method string) bool {
	return method == plugins.MediaServerRefreshRootCapability || method == plugins.MediaServerRefreshItemsCapability
}

// Enter only after the ordinary resource router has checked the exact method's
// manifest grant, full worker identity, current callback and persistent owner.
// Resolve the retained object directly; never look up its old name in a new
// config generation or substitute the currently registered vendor.
func (a *App) originalMediaServerRefreshResource(ctx context.Context, current mediaserverruntime.ReferenceCurrent, owner *originalMediaServerReferenceOwner, references *mediaserverruntime.ObjectReferences, method string, raw json.RawMessage) (json.RawMessage, error) {
	if err := originalMediaServerRefreshCurrent(ctx, current); err != nil {
		return nil, err
	}
	var reference string
	var scanMode json.RawMessage
	var items []mediaserverruntime.ValidatedRefreshItem
	if method == plugins.MediaServerRefreshRootCapability {
		var input plugins.MediaServerRefreshRootRequest
		if err := originalResourceDecode(raw, &input); err != nil {
			return nil, err
		}
		reference, scanMode = input.Reference, input.ScanMode
	} else if method == plugins.MediaServerRefreshItemsCapability {
		var input plugins.MediaServerRefreshItemsRequest
		if err := originalResourceDecode(raw, &input); err != nil {
			return nil, err
		}
		reference, scanMode = input.Reference, input.ScanMode
		var err error
		items, err = originalMediaServerRefreshItems(input.Items)
		if err != nil {
			return nil, err
		}
	} else {
		return nil, bad("PLUGIN_CAPABILITY_DENIED", "媒体刷新方法未获授权")
	}
	vendor, err := references.Resolve(ctx, current, reference)
	if err != nil {
		return nil, err
	}
	if err = originalMediaServerRefreshCurrent(ctx, current); err != nil {
		return nil, err
	}
	// Only the original Ugreen method declares scan_mode. Do not silently ignore
	// an explicitly supplied keyword for any other real vendor.
	if len(scanMode) != 0 {
		if _, ok := vendor.(*mediaservervendors.Ugreen); !ok {
			return originalMediaServerServiceResult(ctx, current, nil, &mediaserverruntime.SourceError{Class: "TypeError"})
		}
	}
	var value any
	if method == plugins.MediaServerRefreshRootCapability {
		value, err = a.originalMediaServerRefreshRoot(ctx, vendor, scanMode)
	} else {
		value, err = a.originalMediaServerRefreshItemsCall(ctx, current, vendor, items, scanMode)
	}
	// Current/cancellation outranks either an ordinary source error or a decoded
	// successful value. No table lock spans the real vendor/HTTP operation.
	if check := originalMediaServerRefreshCurrent(ctx, current); check != nil {
		return nil, check
	}
	if err != nil {
		return originalMediaServerServiceResult(ctx, current, nil, err)
	}
	result, err := originalMediaServerRefreshProjection(ctx, current, owner, value)
	return originalMediaServerServiceResult(ctx, current, result, err)
}

func originalMediaServerRefreshItems(input []plugins.MediaServerRefreshItem) ([]mediaserverruntime.ValidatedRefreshItem, error) {
	output := make([]mediaserverruntime.ValidatedRefreshItem, len(input))
	for i, item := range input {
		var kind mediaserverruntime.RefreshMediaType
		switch item.Type {
		case plugins.MediaServerRefreshTypeNone:
			kind = mediaserverruntime.RefreshMediaTypeNone
		case plugins.MediaServerRefreshTypeMovie:
			kind = mediaserverruntime.RefreshMediaTypeMovie
		case plugins.MediaServerRefreshTypeTV:
			kind = mediaserverruntime.RefreshMediaTypeTV
		case plugins.MediaServerRefreshTypeCollection:
			kind = mediaserverruntime.RefreshMediaTypeCollection
		case plugins.MediaServerRefreshTypeUnknown:
			kind = mediaserverruntime.RefreshMediaTypeUnknown
		default:
			return nil, bad("INVALID_PLUGIN_VALUE", "媒体刷新枚举无效")
		}
		owned, err := (mediaserverruntime.ValidatedRefreshItem{Title: item.Title, Year: item.Year, Type: kind, Category: item.Category, TargetPath: item.TargetPath}).OwnedCopy()
		if err != nil {
			return nil, bad("INVALID_PLUGIN_VALUE", "媒体刷新已验证字段无效")
		}
		if string(owned.TargetPath) != "null" {
			points, err := plugins.DecodeSystemConfigJSONString(owned.TargetPath)
			if err != nil {
				return nil, bad("INVALID_PATH", "媒体刷新路径编码无效")
			}
			for _, point := range points {
				if point >= 0xd800 && point <= 0xdfff {
					return nil, bad("INVALID_PATH", "媒体刷新路径不能替换未配对代理码点")
				}
			}
		}
		output[i] = owned
	}
	return output, nil
}

func originalMediaServerRefreshProjection(ctx context.Context, current mediaserverruntime.ReferenceCurrent, owner *originalMediaServerReferenceOwner, value any) (plugins.MediaServerRefreshResult, error) {
	if err := originalMediaServerRefreshCurrent(ctx, current); err != nil {
		return plugins.MediaServerRefreshResult{}, err
	}
	switch result := value.(type) {
	case nil:
		return plugins.MediaServerRefreshResult{Kind: "none"}, nil
	case bool:
		copied := result
		return plugins.MediaServerRefreshResult{Kind: "bool", Boolean: &copied}, nil
	case *bool:
		if result == nil {
			return plugins.MediaServerRefreshResult{Kind: "none"}, nil
		}
		copied := *result
		return plugins.MediaServerRefreshResult{Kind: "bool", Boolean: &copied}, nil
	}
	if !mediaservervendors.IsPlexRefreshLibraryObject(value) {
		return plugins.MediaServerRefreshResult{}, errors.New("unsupported concrete media refresh result")
	}
	table, err := owner.libraryTable()
	if err != nil {
		return plugins.MediaServerRefreshResult{}, err
	}
	reference, err := table.retain(ctx, current, value)
	if err != nil {
		return plugins.MediaServerRefreshResult{}, err
	}
	return plugins.MediaServerRefreshResult{Kind: "plex_library", Library: &plugins.MediaServerLibraryObject{Reference: reference}}, nil
}

func (a *App) originalMediaServerRefreshRoot(ctx context.Context, vendor mediaserverruntime.Vendor, mode json.RawMessage) (any, error) {
	switch actual := vendor.(type) {
	case *mediaservervendors.Emby:
		return actual.RefreshRootLibrary(ctx)
	case *mediaservervendors.Jellyfin:
		return actual.RefreshRootLibrary(ctx)
	case *mediaservervendors.Plex:
		return actual.RefreshRootLibrary(ctx)
	case *mediaservervendors.TrimeMedia:
		return actual.RefreshRoot(ctx)
	case *mediaservervendors.ZSpace:
		return actual.RefreshRootLibrary(ctx)
	case *mediaservervendors.Ugreen:
		if len(mode) != 0 {
			return actual.RefreshRootLibraryWithScanMode(ctx, append(json.RawMessage(nil), mode...))
		}
		return actual.RefreshRootLibrary(ctx)
	}
	return nil, errors.New("media refresh requires one of the actual six vendor objects")
}

func (a *App) originalMediaServerRefreshItemsCall(ctx context.Context, current mediaserverruntime.ReferenceCurrent, vendor mediaserverruntime.Vendor, items []mediaserverruntime.ValidatedRefreshItem, mode json.RawMessage) (any, error) {
	switch actual := vendor.(type) {
	case *mediaservervendors.Emby:
		return actual.RefreshLibraryByItems(ctx, items)
	case *mediaservervendors.Jellyfin:
		return nil, &mediaserverruntime.SourceError{Class: "AttributeError"} // method absent in the fixed source
	case *mediaservervendors.ZSpace:
		return actual.RefreshLibraryByItems(ctx, items)
	case *mediaservervendors.Plex:
		return actual.RefreshLibraryByItems(ctx, items, mediaservervendors.PlexRefreshDependencies{
			Resolve: func(call context.Context, path string) (string, error) {
				return a.originalMediaServerResolveRefreshPath(call, current, path)
			},
			Report: func(d mediaservervendors.PlexRefreshDiagnostic) {
				a.log(d.Level, "mediaservers", "plex-refresh", d.Message, "", "")
			},
		})
	case *mediaservervendors.TrimeMedia:
		return actual.RefreshLibraryByItems(ctx, items, mediaservervendors.TrimeRefreshDependencies{
			Resolve: func(call context.Context, path string) (string, error) {
				return a.originalMediaServerResolveRefreshPath(call, current, path)
			},
		})
	case *mediaservervendors.Ugreen:
		if len(mode) != 0 {
			return actual.RefreshLibraryByItemsWithScanMode(ctx, items, append(json.RawMessage(nil), mode...))
		}
		return actual.RefreshLibraryByItems(ctx, items)
	}
	return nil, errors.New("media refresh requires one of the actual six vendor objects")
}

package mediaservervendors

import (
	"context"
	"encoding/json"
	"light-media/internal/plugins/mediaserverruntime"
	"light-media/internal/sourcejson"
	"time"
)

// Source refresh uses the same mandatory origin-bound adapter and token as the
// actual constructor object. It adds params=, not a second HTTP implementation.
func (v *ZSpace) refreshRequest(ctx context.Context, method string, path json.RawMessage, params []ZSpaceField) (*ZSpaceResponse, error) {
	if err := zspaceCurrent(ctx, v.deps); err != nil {
		return nil, err
	}
	url, err := zspaceConcat(v.host, path)
	if err != nil {
		return nil, err
	}
	headers := []ZSpaceField{{Name: "X-Emby-Authorization", Value: zspaceString(zspaceClientAuthorization)}}
	v.mu.RLock()
	key := zspaceCopy(v.apiKey)
	v.mu.RUnlock()
	if zspaceTruth(key) {
		headers = append(headers, ZSpaceField{Name: "X-Emby-Token", Value: key})
	}
	var copied []ZSpaceField
	if params != nil {
		copied = make([]ZSpaceField, len(params))
	}
	for i, p := range params {
		copied[i] = ZSpaceField{Name: p.Name, Value: zspaceCopy(p.Value)}
	}
	response, err := v.deps.Request(ctx, ZSpaceRequest{Method: method, URL: url, Origin: zspaceCopy(v.host), Headers: headers, Params: copied,
		Timeout: 20 * time.Second, AllowRedirects: true, VerifyCertificate: false, UseSession: false})
	if current := zspaceCurrent(ctx, v.deps); current != nil {
		return nil, current
	}
	return response, err
}
func (v *ZSpace) canRefresh() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return zspaceTruth(v.host) && zspaceTruth(v.apiKey)
}

func (v *ZSpace) RefreshRootLibrary(ctx context.Context) (bool, error) {
	if err := zspaceCurrent(ctx, v.deps); err != nil {
		return false, err
	}
	if !v.canRefresh() {
		return false, nil
	}
	response, err := v.refreshRequest(ctx, "post", zspaceString("emby/Library/Refresh"), nil)
	if err != nil {
		return false, v.caught("Library/Refresh", "debug", err)
	}
	if response != nil && response.StatusCode < 400 {
		return true, nil
	}
	v.report("Library/Refresh", "debug", "NotImplemented")
	return false, nil
}
func (v *ZSpace) refreshLibraryID(ctx context.Context, id json.RawMessage) (bool, error) {
	if err := zspaceCurrent(ctx, v.deps); err != nil {
		return false, err
	}
	if !v.canRefresh() {
		return false, nil
	}
	var idText json.RawMessage
	if len(id) > 0 && id[0] == '"' {
		idText = zspaceCopy(id)
	} else {
		text, e := sourcejson.String(id)
		if e != nil {
			return false, e
		}
		idText = zspaceString(text)
	}
	path, err := zspaceConcat(zspaceString("emby/Items/"), idText)
	if err != nil {
		return false, err
	}
	path, err = zspaceConcat(path, zspaceString("/Refresh"))
	if err != nil {
		return false, err
	}
	response, err := v.refreshRequest(ctx, "post", path, []ZSpaceField{{Name: "Recursive", Value: zspaceString("true")}})
	if err != nil {
		return false, v.caught("Items/Id/Refresh", "debug", err)
	}
	if response != nil && response.StatusCode < 400 {
		return true, nil
	}
	v.report("Items/Id/Refresh", "debug", "NotImplemented")
	return false, nil
}

func (v *ZSpace) seriesID(ctx context.Context, title, year json.RawMessage) (json.RawMessage, error) {
	if !v.canRefresh() {
		return zspaceCopy(zspaceNull), nil
	}
	params := []ZSpaceField{{Name: "IncludeItemTypes", Value: zspaceString("Series")}, {Name: "Fields", Value: zspaceString("ProductionYear")},
		{Name: "StartIndex", Value: json.RawMessage("0")}, {Name: "Recursive", Value: zspaceString("true")}, {Name: "SearchTerm", Value: zspaceCopy(title)},
		{Name: "Limit", Value: json.RawMessage("10")}, {Name: "IncludeSearchTypes", Value: zspaceString("false")}}
	response, err := v.refreshRequest(ctx, "get", zspaceString("emby/Items"), params)
	if err != nil {
		return zspaceCopy(zspaceNull), v.caught("Items", "error", err)
	}
	if response == nil || response.StatusCode >= 400 {
		return zspaceString(""), nil
	}
	raw, err := v.responseJSON(ctx, response)
	if err != nil {
		return zspaceCopy(zspaceNull), v.caught("Items", "error", err)
	}
	fields, err := zspaceObject(raw)
	if err != nil {
		v.report("Items", "error", "AttributeError")
		return zspaceCopy(zspaceNull), nil
	}
	items := fields.get("Items")
	if !zspaceTruth(items) {
		return zspaceString(""), nil
	}
	entries, err := zspaceRefreshIterable(items)
	if err != nil {
		return zspaceCopy(zspaceNull), v.caught("Items", "error", err)
	}
	for _, entry := range entries {
		item, err := zspaceObject(entry)
		if err != nil {
			v.report("Items", "error", "AttributeError")
			return zspaceCopy(zspaceNull), nil
		}
		if !zspaceRefreshEqual(item.get("Name"), title) {
			continue
		}
		if zspaceTruth(year) {
			got, e := sourcejson.String(item.get("ProductionYear"))
			if e != nil {
				return nil, e
			}
			want, e := sourcejson.String(year)
			if e != nil {
				return nil, e
			}
			if got != want {
				continue
			}
		}
		return zspaceCopy(item.get("Id")), nil
	}
	return zspaceString(""), nil
}

func (v *ZSpace) RefreshLibraryByItems(ctx context.Context, input []mediaserverruntime.ValidatedRefreshItem) (bool, error) {
	if err := zspaceCurrent(ctx, v.deps); err != nil {
		return false, err
	}
	if len(input) == 0 {
		return false, nil
	}
	items := make([]mediaserverruntime.ValidatedRefreshItem, len(input))
	for i, item := range input {
		owned, err := item.OwnedCopy()
		if err != nil {
			return false, err
		}
		items[i] = owned
	}
	v.report("refresh_library_by_items", "info", "RefreshStarted")
	ids := []json.RawMessage{}
	for _, item := range items {
		if err := zspaceCurrent(ctx, v.deps); err != nil {
			return false, err
		}
		id, err := v.libraryIDByTypedItem(ctx, item)
		if err != nil {
			return false, err
		}
		if !zspaceTruth(id) {
			continue
		}
		seen := false
		for _, previous := range ids {
			if zspaceRefreshEqual(previous, id) {
				seen = true
				break
			}
		}
		if !seen {
			ids = append(ids, zspaceCopy(id))
		}
	}
	for _, id := range ids {
		if zspaceRefreshEqual(id, zspaceString("/")) {
			return v.RefreshRootLibrary(ctx)
		}
	}
	for _, id := range ids {
		if !zspaceRefreshEqual(id, zspaceString("/")) {
			return v.refreshLibraryID(ctx, id)
		}
	}
	v.report("refresh_library_by_items", "info", "RefreshCompleted")
	return true, nil
}

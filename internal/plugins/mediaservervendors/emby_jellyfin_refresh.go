package mediaservervendors

import (
	"context"
	"encoding/json"
	"net/http"

	"light-media/internal/plugins/mediaserverruntime"
)

func (v *ejVendor) refreshCaught(ctx context.Context, op string, err error) error {
	return v.refreshCaughtLevel(ctx, op, "error", err)
}

func (v *ejVendor) refreshCaughtLevel(ctx context.Context, op, severity string, err error) error {
	if current := v.current(ctx); current != nil {
		return current
	}
	if err == nil {
		return nil
	}
	if ordinary, ok := err.(*mediaserverruntime.SourceError); ok {
		v.report(op, severity, ordinary.Class)
		return nil
	}
	return err
}
func (v *ejVendor) rootRefreshOutcome(ctx context.Context, response *ejHTTPResponse, err error) (*bool, error) {
	no, yes := false, true
	if err != nil {
		return &no, v.refreshCaught(ctx, "Library/Refresh", err)
	}
	if response != nil && response.Status < 400 {
		return &yes, nil
	}
	v.report("Library/Refresh", "info", "MissingResponse")
	if v.kind == "jellyfin" {
		return nil, nil
	} // Original falsey-response fallthrough.
	return &no, nil
}
func (v *ejVendor) refreshRootLibrary(ctx context.Context) (*bool, error) {
	if err := v.current(ctx); err != nil {
		return nil, err
	}
	if !v.stateConfigured() {
		no := false
		return &no, nil
	}
	path := "emby/Library/Refresh"
	if v.kind == "jellyfin" {
		path = "Library/Refresh"
	}
	response, err := v.requestResponse(ctx, http.MethodPost, path, []ejRequestParameter{{Name: "api_key", Value: ejCopy(v.apiKey)}})
	return v.rootRefreshOutcome(ctx, response, err)
}
func (v *ejVendor) stateConfigured() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.state.Configured
}
func (v *Emby) RefreshRootLibrary(ctx context.Context) (bool, error) {
	value, err := v.v.refreshRootLibrary(ctx)
	return value != nil && *value, err
}

// Jellyfin preserves source None separately from False. No by-items method is
// invented; the later host dispatcher must preserve this nullable result.
func (v *Jellyfin) RefreshRootLibrary(ctx context.Context) (*bool, error) {
	return v.v.refreshRootLibrary(ctx)
}
func (v *ejVendor) refreshLibraryID(ctx context.Context, id json.RawMessage) (bool, error) {
	if err := v.current(ctx); err != nil {
		return false, err
	}
	if !v.stateConfigured() {
		return false, nil
	}
	text, err := ejPythonText(id, false)
	if err != nil {
		return false, v.refreshCaught(ctx, "Items/Id/Refresh", err)
	}
	response, err := v.requestResponse(ctx, http.MethodPost, "emby/Items/"+text+"/Refresh", []ejRequestParameter{{Name: "Recursive", Value: json.RawMessage(`"true"`)}, {Name: "api_key", Value: ejCopy(v.apiKey)}})
	if err != nil {
		return false, v.refreshCaught(ctx, "Items/Id/Refresh", err)
	}
	if response != nil && response.Status < 400 {
		return true, nil
	}
	v.report("Items/Id/Refresh", "info", "MissingResponse")
	return false, nil
}
func (v *ejVendor) seriesID(ctx context.Context, title, year json.RawMessage) (json.RawMessage, error) {
	if !v.stateConfigured() {
		return json.RawMessage(`null`), nil
	}
	params := []ejRequestParameter{{"IncludeItemTypes", json.RawMessage(`"Series"`)}, {"Fields", json.RawMessage(`"ProductionYear"`)}, {"StartIndex", json.RawMessage(`0`)}, {"Recursive", json.RawMessage(`"true"`)}, {"SearchTerm", ejCopy(title)}, {"Limit", json.RawMessage(`10`)}, {"IncludeSearchTypes", json.RawMessage(`"false"`)}, {"api_key", ejCopy(v.apiKey)}}
	response, err := v.requestResponse(ctx, http.MethodGet, "emby/Items", params)
	if err != nil {
		return json.RawMessage(`null`), v.refreshCaught(ctx, "Items", err)
	}
	if response == nil || response.Status >= 400 {
		return json.RawMessage(`""`), nil
	}
	raw, err := ejResponseJSON(response.Body, response.ContentType)
	if err != nil {
		return json.RawMessage(`null`), v.refreshCaught(ctx, "Items", err)
	}
	result, err := ejObject(raw)
	if err != nil {
		v.report("Items", "error", "AttributeError")
		return json.RawMessage(`null`), nil
	}
	items := result.get("Items")
	if !ejTruth(items) {
		return json.RawMessage(`""`), nil
	}
	entries, err := ejIter(items)
	if err != nil {
		return json.RawMessage(`null`), v.refreshCaught(ctx, "Items", err)
	}
	for _, entry := range entries {
		fields, e := ejObject(entry)
		if e != nil {
			v.report("Items", "error", "AttributeError")
			return json.RawMessage(`null`), nil
		}
		if !ejEqual(fields.get("Name"), title) {
			continue
		}
		if ejTruth(year) {
			got, e := ejPythonText(fields.get("ProductionYear"), false)
			if e != nil {
				return json.RawMessage(`null`), v.refreshCaught(ctx, "Items", e)
			}
			want, e := ejPythonText(year, false)
			if e != nil {
				return json.RawMessage(`null`), v.refreshCaught(ctx, "Items", e)
			}
			if got != want {
				continue
			}
		}
		return ejCopy(fields.get("Id")), nil
	}
	return json.RawMessage(`""`), nil
}
func (v *Emby) RefreshLibraryByItems(ctx context.Context, input []mediaserverruntime.ValidatedRefreshItem) (bool, error) {
	if err := v.v.current(ctx); err != nil {
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
	v.v.report("refresh_library_by_items", "info", "RefreshStarted")
	ids := []json.RawMessage{}
	for _, item := range items {
		if err := v.v.current(ctx); err != nil {
			return false, err
		}
		id, err := v.v.libraryIDByTypedItem(ctx, item)
		if err != nil {
			return false, err
		}
		if !ejTruth(id) {
			continue
		}
		seen := false
		for _, before := range ids {
			if ejEqual(before, id) {
				seen = true
				break
			}
		}
		if !seen {
			ids = append(ids, ejCopy(id))
		}
	}
	for _, id := range ids {
		if ejEqual(id, json.RawMessage(`"/"`)) {
			return v.RefreshRootLibrary(ctx)
		}
	}
	for _, id := range ids {
		if !ejEqual(id, json.RawMessage(`"/"`)) {
			return v.v.refreshLibraryID(ctx, id)
		}
	}
	v.v.report("refresh_library_by_items", "info", "RefreshCompleted")
	return true, nil
}

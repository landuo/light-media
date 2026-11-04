package mediaservervendors

import (
	"bytes"
	"context"
	"encoding/json"

	"light-media/internal/plugins"
)

// A separate real capability leaves the accepted constructor/session interface
// unchanged. UgreenConcreteAPI implements all three actual HTTP operations;
// a constructor-only recording API is not silently promoted to refresh support.
type ugreenRefreshAPI interface {
	MediaList(context.Context) (json.RawMessage, error)
	FolderPage(context.Context, int, int) (json.RawMessage, error)
	Scan(context.Context, json.RawMessage, int) (bool, error)
	refreshDetails(context.Context) (ugreenRefreshDetails, error)
}

var _ ugreenRefreshAPI = (*UgreenConcreteAPI)(nil)

type ugreenRefreshDetails struct {
	host               string
	token, staticToken json.RawMessage
	isUGK              bool
}

func (a *UgreenConcreteAPI) refreshDetails(ctx context.Context) (ugreenRefreshDetails, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.current(ctx); err != nil {
		return ugreenRefreshDetails{}, err
	}
	return ugreenRefreshDetails{a.host, ejCopy(a.token), ejCopy(a.staticToken), a.isUGK}, nil
}

func (v *Ugreen) refreshAPI() (ugreenRefreshAPI, error) {
	v.mu.RLock()
	api := v.api
	v.mu.RUnlock()
	if api == nil {
		return nil, nil
	}
	actual, ok := api.(ugreenRefreshAPI)
	if !ok || ugreenAPINil(actual) {
		return nil, &EmbyJellyfinBoundaryError{Kind: "Ugreen real refresh API unavailable"}
	}
	return actual, nil
}

// Python str keeps string code points intact; no filesystem or URL operation.
func ugreenRefreshPythonString(raw json.RawMessage) (json.RawMessage, error) {
	if value := bytes.TrimSpace(raw); len(value) > 0 && value[0] == '"' {
		points, err := plugins.DecodeSystemConfigJSONString(value)
		if err != nil {
			return nil, err
		}
		return plugins.EncodeSystemConfigJSONString(points)
	}
	value, err := ejPythonText(raw, false)
	if err != nil {
		return nil, err
	}
	return plugins.EncodeSystemConfigJSONString([]rune(value))
}
func ugreenRefreshSet(object *ejMembers, key, value json.RawMessage) {
	if _, ok := object.values[string(key)]; !ok {
		object.keys = append(object.keys, ejCopy(key))
	}
	object.values[string(key)] = ejCopy(value)
}
func ugreenRefreshObjectJSON(object ejMembers) json.RawMessage {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, key := range object.keys {
		if i > 0 {
			out.WriteByte(',')
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(object.values[string(key)])
	}
	out.WriteByte('}')
	return json.RawMessage(out.Bytes())
}

// Complete original __load_library_paths. Its 200-page limit is source-owned,
// not a new response/body cap. This method returns local paths and does not
// publish them: get_librarys assigns only after the whole load returns.
func (v *Ugreen) refreshLoadLibraryPaths(ctx context.Context) (json.RawMessage, error) {
	if err := ugreenCurrent(ctx, v.deps); err != nil {
		return nil, err
	}
	api, err := v.refreshAPI()
	if err != nil {
		return nil, err
	}
	paths := ejMembers{values: map[string]json.RawMessage{}}
	if api == nil {
		return json.RawMessage(`{}`), nil
	}
	page := 1
	for ; page <= 200; page++ {
		data, err := ugreenCall(ctx, v, func() (json.RawMessage, error) { return api.FolderPage(ctx, page, 100) })
		if err != nil {
			return nil, err
		}
		if !ejTruth(data) {
			break
		}
		object, err := ejObject(data)
		if err != nil {
			return nil, ejSource("AttributeError")
		}
		folders := object.get("folder_arr")
		if !ejTruth(folders) {
			folders = json.RawMessage(`[]`)
		}
		rows, err := ejIter(folders)
		if err != nil {
			return nil, err
		}
		for _, raw := range rows {
			folder, err := ejObject(raw)
			if err != nil {
				return nil, ejSource("AttributeError")
			}
			id, path := folder.get("media_lib_set_id"), folder.get("path")
			if !ejNull(id) && ejTruth(path) {
				key, err := ugreenRefreshPythonString(id)
				if err != nil {
					return nil, err
				}
				value, err := ugreenRefreshPythonString(path)
				if err != nil {
					return nil, err
				}
				ugreenRefreshSet(&paths, key, value)
			}
		}
		if ejTruth(object.get("is_last_page")) {
			break
		}
	}
	if page > 200 {
		v.report("library_paths", "warning", "PaginationLimit")
	}
	return ugreenRefreshObjectJSON(paths), nil
}

// Complete original __scan_library; its concrete Scan supplies op_type=2.
// Public refresh methods serialize their whole operation before using this.
func (v *Ugreen) refreshScanLibrary(ctx context.Context, id json.RawMessage, scanType int) (bool, error) {
	if err := ugreenCurrent(ctx, v.deps); err != nil {
		return false, err
	}
	api, err := v.refreshAPI()
	if err != nil {
		return false, err
	}
	if api == nil {
		return false, nil
	}
	if scanType == 0 {
		v.mu.RLock()
		scanType = v.state.ScanType
		v.mu.RUnlock()
	}
	return ugreenCall(ctx, v, func() (bool, error) { return api.Scan(ctx, ejCopy(id), scanType) })
}

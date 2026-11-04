package mediaservervendors

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"

	"light-media/internal/plugins"
	"light-media/internal/sourcejson"
)

func ugreenRefreshContains(raw json.RawMessage, needle string) (bool, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		hay, err := plugins.DecodeSystemConfigJSONString(raw)
		if err != nil {
			return false, err
		}
		want := []rune(needle)
		for i := 0; i+len(want) <= len(hay); i++ {
			if slices.Equal(hay[i:i+len(want)], want) {
				return true, nil
			}
		}
		return false, nil
	}
	items, err := ejIter(raw)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if len(bytes.TrimSpace(item)) > 0 && bytes.TrimSpace(item)[0] == '"' {
			text, err := plugins.DecodeSystemConfigJSONString(item)
			if err != nil {
				return false, err
			}
			if slices.Equal(text, []rune(needle)) {
				return true, nil
			}
		}
	}
	return false, nil
}
func ugreenRefreshLibraryType(name, path json.RawMessage) (json.RawMessage, error) {
	if !ejTruth(name) {
		name = ugreenKey("")
	}
	if !ejTruth(path) {
		path = ugreenKey("")
	}
	tv, err := ugreenRefreshContains(path, "电视剧")
	if err != nil {
		return nil, err
	}
	if !tv {
		for _, key := range []string{"剧", "综艺", "动漫", "纪录片"} {
			tv, err = ugreenRefreshContains(name, key)
			if err != nil {
				return nil, err
			}
			if tv {
				break
			}
		}
	}
	if tv {
		return ugreenKey("电视剧"), nil
	}
	movie, err := ugreenRefreshContains(path, "电影")
	if err != nil {
		return nil, err
	}
	if !movie {
		movie, err = ugreenRefreshContains(name, "电影")
		if err != nil {
			return nil, err
		}
	}
	if movie {
		return ugreenKey("电影"), nil
	}
	return ugreenKey("未知"), nil
}

// get_librarys constructs only these original model fields. All remaining
// MediaServerLibrary optional fields default to None. Name and item_count are
// the only constructed fields not already constrained to string/list-of-string
// by the preceding original helpers. Keep validation AFTER cache publication.
func ugreenRefreshLibraryModel(id, name, path, kind, count, images, link json.RawMessage) (json.RawMessage, error) {
	if !ejNull(name) {
		if len(bytes.TrimSpace(name)) == 0 || bytes.TrimSpace(name)[0] != '"' {
			return nil, ejSource("ValidationError")
		}
		if _, err := plugins.DecodeSystemConfigJSONString(name); err != nil {
			return nil, err
		}
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(count))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	number, err := sourcejson.ModelInteger(value)
	if err != nil {
		return nil, ejSource("ValidationError")
	}
	none := json.RawMessage(`null`)
	return ugreenAPIObject("server", ugreenKey("ugreen"), "id", id, "item_id", none, "server_id", none,
		"name", name, "path", path, "type", kind, "item_count", json.RawMessage(number.String()),
		"image", none, "image_list", images, "link", link, "server_type", ugreenKey("ugreen"), "use_cookies", json.RawMessage(`false`)), nil
}

// Full refresh-reachable get_librarys(hidden=False), not a cache-only shortcut.
// Its public hidden-filter branch is outside this refresh call graph; no new
// public libraries API or fake successful model constructor is supplied.
func (v *Ugreen) refreshLoadLibraries(ctx context.Context) ([]json.RawMessage, error) {
	authenticated, err := v.authenticated(ctx)
	if err != nil || !authenticated {
		return nil, err
	}
	api, err := v.refreshAPI()
	if err != nil || api == nil {
		return nil, err
	}
	media, err := ugreenCall(ctx, v, func() (json.RawMessage, error) { return api.MediaList(ctx) })
	if err != nil || ejNull(media) {
		return nil, err
	}
	pathsRaw, err := v.refreshLoadLibraryPaths(ctx)
	if err != nil {
		return nil, err
	}
	paths, err := ejObject(pathsRaw)
	if err != nil {
		return nil, err
	}
	if err := ugreenCurrent(ctx, v.deps); err != nil {
		return nil, err
	}
	v.mu.Lock()
	v.state.LibraryPaths = ejCopy(pathsRaw)
	v.state.Libraries = json.RawMessage(`{}`)
	v.mu.Unlock()
	libraries := ejMembers{values: map[string]json.RawMessage{}}
	rows, err := ejIter(media)
	if err != nil {
		return nil, err
	}
	result := []json.RawMessage{}
	for _, raw := range rows {
		lib, err := ejObject(raw)
		if err != nil {
			return nil, ejSource("AttributeError")
		}
		id, err := ugreenRefreshPythonString(lib.get("media_lib_set_id"))
		if err != nil {
			return nil, err
		}
		name := lib.get("media_name")
		if !ejTruth(name) {
			name = ugreenKey("")
		}
		path := paths.values[string(id)]
		if len(path) == 0 {
			path = json.RawMessage(`null`)
		}
		kind, err := ugreenRefreshLibraryType(name, path)
		if err != nil {
			return nil, err
		}
		images := []json.RawMessage{}
		for _, field := range []string{"poster_paths", "backdrop_paths"} {
			values := lib.get(field)
			if !ejTruth(values) {
				values = json.RawMessage(`[]`)
			}
			paths, err := ejIter(values)
			if err != nil {
				return nil, err
			}
			for _, raw := range paths {
				image, err := v.refreshResolveImage(ctx, raw)
				if err != nil {
					return nil, err
				}
				if ejTruth(image) {
					images = append(images, ejCopy(image))
				}
			}
		}
		count := lib.get("video_count")
		if !ejTruth(count) {
			count = json.RawMessage(`0`)
		}
		cached := ugreenAPIObject("id", id, "name", name, "path", path, "type", kind, "video_count", count)
		ugreenRefreshSet(&libraries, id, cached)
		if err := ugreenCurrent(ctx, v.deps); err != nil {
			return nil, err
		}
		v.mu.Lock()
		v.state.Libraries = ugreenRefreshObjectJSON(libraries)
		v.mu.Unlock()
		imageList, err := json.Marshal(images)
		if err != nil {
			return nil, err
		}
		link, err := v.refreshRootURL(ctx)
		if err != nil {
			return nil, err
		}
		model, err := ugreenRefreshLibraryModel(id, name, path, kind, count, imageList, link)
		if err != nil {
			return nil, err
		}
		result = append(result, model)
	}
	return result, nil
}

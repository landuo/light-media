package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"light-media/internal/plugins"
)

func ownedStorageOperation(ctx context.Context, base plugins.Base, raw json.RawMessage, root, mode string) (json.RawMessage, error) {
	var call plugins.StorageOperationCall
	if json.Unmarshal(raw, &call) != nil || call.Key != "owned-local" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, errors.New("invalid owned storage invocation")
	}
	allowed := func(path string) bool {
		rel, e := filepath.Rel(root, path)
		return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && filepath.IsAbs(path) && filepath.Clean(path) == path
	}
	if call.Path == "" && call.Operation == "list" && call.FileItem != nil {
		var path string
		if json.Unmarshal(call.FileItem.Fields["path"], &path) == nil {
			call.Path = filepath.Clean(path)
		}
	}
	if !allowed(call.Path) || call.PublicationPath != "" && !allowed(call.PublicationPath) {
		return nil, errors.New("owned storage path outside fixture root")
	}
	key := "observations"
	stored, e := base.GetData(ctx, &key)
	if e != nil {
		return nil, e
	}
	var rows []json.RawMessage
	if len(stored) > 0 && string(stored) != "null" && json.Unmarshal(stored, &rows) != nil {
		return nil, errors.New("invalid owned observations")
	}
	row, _ := json.Marshal(map[string]any{"type": "storage." + call.Operation, "data": json.RawMessage(raw)})
	rows = append(rows, row)
	if e = base.SaveData(ctx, key, rows); e != nil {
		return nil, e
	}
	item := func(path string) (json.RawMessage, error) {
		stat, e := os.Lstat(path)
		if os.IsNotExist(e) {
			return json.RawMessage(`null`), nil
		}
		if e != nil || stat.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("owned storage unreadable item")
		}
		ext := strings.TrimPrefix(filepath.Ext(path), ".")
		typ := "file"
		var size any = stat.Size()
		var extension any = ext
		if stat.IsDir() {
			typ = "dir"
			size = nil
			extension = nil
		}
		return json.Marshal(map[string]any{"storage": "local", "path": path, "type": typ, "name": filepath.Base(path), "basename": strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), "extension": extension, "size": size, "modify_time": float64(stat.ModTime().UnixNano()) / 1e9, "children": []any{}})
	}
	switch call.Operation {
	case "get_folder":
		if e = os.MkdirAll(call.Path, 0750); e != nil {
			return nil, e
		}
		return item(call.Path)
	case "get_item", "get_item_strict":
		return item(call.Path)
	case "list":
		if mode == "late-list-error" && filepath.Base(call.Path) == "CERTIFICATE" {
			return nil, errors.New("owned late directory listing failure")
		}
		entries, e := os.ReadDir(call.Path)
		if e != nil {
			return nil, e
		}
		out := []json.RawMessage{}
		for _, entry := range entries {
			raw, e := item(filepath.Join(call.Path, entry.Name()))
			if e != nil {
				return nil, e
			}
			if mode == "directory-names" {
				var fields map[string]any
				if e := json.Unmarshal(raw, &fields); e != nil {
					return nil, e
				}
				if name := map[string]string{"BDMV": "BluRay", "STREAM": "Streams", "00001.m2ts": "feature.m2ts", "index.bdmv": "index-custom.bdmv"}[entry.Name()]; name != "" {
					fields["name"] = name
				}
				raw, e = json.Marshal(fields)
				if e != nil {
					return nil, e
				}
			}
			out = append(out, raw)
		}
		return json.Marshal(out)
	case "delete":
		if mode == "delete-false" {
			return json.RawMessage(`false`), nil
		}
		var path string
		if call.FileItem == nil || json.Unmarshal(call.FileItem.Fields["path"], &path) != nil || !allowed(path) {
			return nil, errors.New("invalid owned delete")
		}
		if e = os.Remove(path); e != nil {
			return nil, e
		}
		return json.RawMessage(`true`), nil
	case "link", "copy":
		var source string
		if call.FileItem == nil || json.Unmarshal(call.FileItem.Fields["path"], &source) != nil || !allowed(source) || !strings.HasPrefix(filepath.Base(call.PublicationPath), ".light-media-") {
			return nil, errors.New("invalid owned publication")
		}
		if mode == "error" {
			return nil, errors.New("owned storage failure")
		}
		if mode == "fail-aux" && (filepath.Ext(source) == ".srt" || filepath.Ext(source) == ".flac") {
			return json.RawMessage(`false`), nil
		}
		if mode == "false" {
			return json.RawMessage(`false`), nil
		}
		if mode == "lie" {
			return json.RawMessage(`true`), nil
		}
		if call.Operation == "link" {
			e = os.Link(source, call.PublicationPath)
		} else {
			input, err := os.Open(source)
			if err != nil {
				return nil, err
			}
			defer input.Close()
			output, err := os.OpenFile(call.PublicationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
			if err != nil {
				return nil, err
			}
			_, e = io.Copy(output, input)
			if e == nil && mode == "corrupt" {
				_, e = output.WriteAt([]byte("X"), 0)
			}
			if e == nil {
				e = output.Sync()
			}
			output.Close()
		}
		if e != nil {
			return nil, e
		}
		return json.RawMessage(`true`), nil
	}
	return nil, errors.New("unsupported owned local storage operation")
}

package app

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"light-media/internal/plugins"
	"light-media/internal/transferextensions"
)

// StorageChain.list_files also belongs to the real file-browser entry point.
// The existing metadata caller alone cannot stand in for this host operation.
func (a *App) originalBrowserStorageList(ctx context.Context, path string, classifier transferextensions.Classifier) ([]browserEntry, bool, bool, error) {
	groups, err := a.originalPluginModules(ctx, "")
	if err != nil {
		return nil, false, true, err
	}
	if !originalModuleGroupsHaveMethod(groups, "list_files") {
		return nil, false, false, nil
	}
	item, err := originalLocalFileItem(path)
	if err != nil {
		return nil, false, true, err
	}
	encoded, _ := json.Marshal(item)
	value, err := decodeSourceJSON(encoded)
	if err != nil {
		return nil, false, true, err
	}
	fields := sourceMap(value)
	raw, err := a.originalMetadataStorageGroups(ctx, "list_files", map[string]any{"fileitem": fields, "recursion": false}, func() (any, error) {
		return a.originalMetadataNativeList(ctx, fields)
	}, groups)
	if err != nil {
		return nil, false, true, err
	}
	var rows []json.RawMessage
	// The fixed storage endpoint calls .sort on every truthy result before
	// response validation. A terminal tuple therefore fails at that caller.
	if _, tuple := plugins.DecodeModuleResultTuple(raw); tuple {
		return nil, false, true, originalBrowserStorageInvalid()
	}
	if json.Unmarshal(raw, &rows) != nil || string(raw) == "null" {
		return nil, false, true, originalBrowserStorageInvalid()
	}
	truncated := len(rows) > 10000
	if truncated {
		rows = rows[:10000]
	}
	entries := make([]browserEntry, 0, len(rows))
	for _, row := range rows {
		entry, err := a.originalBrowserStorageEntry(ctx, path, row, classifier)
		if err != nil {
			return nil, false, true, err
		}
		entries = append(entries, entry)
	}
	return entries, truncated, true, nil
}

func originalBrowserStorageInvalid() error {
	return &APIError{"PLUGIN_MODULE_RESULT_INVALID", "存储插件没有返回可用于当前本地目录的文件列表", 500}
}

func (a *App) originalBrowserStorageEntry(ctx context.Context, directory string, raw json.RawMessage, classifier transferextensions.Classifier) (browserEntry, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return browserEntry{}, originalBrowserStorageInvalid()
	}
	text := func(key, fallback string) (string, error) {
		value, exists := fields[key]
		if !exists {
			return fallback, nil
		}
		if string(value) == "null" {
			return "", nil
		}
		var decoded string
		if json.Unmarshal(value, &decoded) != nil {
			return "", originalBrowserStorageInvalid()
		}
		return decoded, nil
	}
	storage, err := text("storage", "local")
	if err != nil || storage != "local" {
		return browserEntry{}, originalBrowserStorageInvalid()
	}
	path, err := text("path", "/")
	path = strings.TrimSuffix(path, "/")
	if err != nil || !importPath(path) || filepath.Dir(path) != directory || browserInternal(path) {
		return browserEntry{}, originalBrowserStorageInvalid()
	}
	kind, err := text("type", "")
	if err != nil || kind != "file" && kind != "dir" {
		return browserEntry{}, originalBrowserStorageInvalid()
	}
	name, err := text("name", "")
	if err != nil {
		return browserEntry{}, err
	}
	entry := browserEntry{Name: name, Path: path, Type: kind}
	entry.sourceNatural, err = originalBrowserNaturalKey(name)
	if err != nil {
		return entry, originalBrowserStorageInvalid()
	}
	if value := fields["size"]; len(value) > 0 && string(value) != "null" {
		decoded, err := decodeSourceJSON(value)
		if err != nil {
			return entry, originalBrowserStorageInvalid()
		}
		integer, err := originalCatalogPersonInteger(decoded)
		if err != nil {
			return entry, originalBrowserStorageInvalid()
		}
		entry.Size, err = integer.Int64()
		if err != nil {
			return entry, originalBrowserStorageInvalid()
		}
	}
	if value := fields["modify_time"]; len(value) > 0 && string(value) != "null" {
		seconds, err := originalRatingFloat(value)
		if err != nil || seconds < -62135596800 || seconds > 253402300799 {
			return entry, originalBrowserStorageInvalid()
		}
		whole := math.Floor(seconds)
		entry.Modified = time.Unix(int64(whole), int64((seconds-whole)*1e9)).UTC().Format(time.RFC3339)
	}
	entry.Video, _ = classifier.Media(transferextensions.PathFile(path))
	entry.Protected = a.browserStaticProtection(path)
	// Read metadata can be supplied by a module, but existing local operations
	// still require the real guarded file and their current inode snapshot.
	if stat, err := os.Lstat(path); err != nil {
		entry.Protected = "本地文件当前不可访问"
	} else if stat.Mode()&os.ModeSymlink != 0 || !stat.Mode().IsRegular() && !stat.IsDir() {
		entry.Protected = "符号链接或特殊文件禁止操作"
	}
	return entry, nil
}

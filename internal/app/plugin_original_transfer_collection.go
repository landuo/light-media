package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"light-media/internal/plugins"
	"light-media/internal/transferextensions"
)

// Transfer's sibling consumer iterates tuples as well as lists. It must not use
// the browser endpoint's list-only/sort contract or metadata's FileItem parser
// for the intermediate parent: the source passes that value straight through.
func (a *App) originalTransferSiblingItems(ctx context.Context, path string) ([]map[string]any, error) {
	if listings, ok := ctx.Value(originalTransferDirectoryListingsKey{}).(map[string][]map[string]any); ok {
		if entries, exists := listings[filepath.Dir(path)]; exists {
			return entries, nil
		}
	}
	item, err := originalLocalFileItem(path)
	if err != nil {
		return nil, err
	}
	parent, err := a.originalMetadataStorage(ctx, "get_parent_item", map[string]any{"fileitem": item}, func() (any, error) {
		return a.originalMetadataLocalItem("local", filepath.Dir(path))
	})
	if err != nil || !originalModuleResultTruthy(parent) {
		return nil, err
	}
	raw, err := a.originalMetadataStorage(ctx, "list_files", map[string]any{"fileitem": parent, "recursion": false}, func() (any, error) {
		value, err := originalMetadataStorageItem(parent)
		if err != nil || value == nil {
			return nil, err
		}
		return a.originalMetadataNativeList(ctx, value)
	})
	if err != nil || !originalModuleResultTruthy(raw) {
		return nil, err
	}
	entries, tuple := plugins.DecodeModuleResultTuple(raw)
	if !tuple && json.Unmarshal(raw, &entries) != nil {
		return nil, bad("PLUGIN_MODULE_RESULT_INVALID", "整理存储模块没有返回可迭代的文件项")
	}
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if !originalModuleResultTruthy(entry) {
			continue
		}
		value, err := originalMetadataStorageItem(entry)
		if err != nil {
			return nil, err
		}
		if sourceText(value["type"]) == "file" {
			items = append(items, value)
		}
	}
	return items, nil
}

func originalTransferItemClass(item map[string]any) transferextensions.File {
	file := transferextensions.File{Type: sourceText(item["type"])}
	if extension, ok := item["extension"].(string); ok {
		file.Extension = &extension
	}
	return file
}

func originalImportFileClass(file ImportFileRequest) transferextensions.File {
	if file.originalItem == nil {
		return transferextensions.PathFile(file.Path)
	}
	var kind string
	var extension *string
	_ = json.Unmarshal(file.originalItem.Fields["type"], &kind)
	_ = json.Unmarshal(file.originalItem.Fields["extension"], &extension)
	// originalItem is private and supplied by the guarded directory collector;
	// ordinary directories never become task candidates there.
	return transferextensions.File{Type: kind, Extension: extension, BlurayKnown: kind == "dir", Bluray: kind == "dir"}
}

func originalTransferItemName(item map[string]any) string {
	if name := sourceText(item["name"]); name != "" {
		return name
	}
	return filepath.Base(sourceText(item["path"]))
}

func originalTransferBlockedExtra(path string) bool {
	for _, part := range []string{"/@Recycle/", "/#recycle/", "/.", "/@eaDir"} {
		if strings.Contains(path, part) {
			return true
		}
	}
	return false
}

func originalTransferFrozenItem(value map[string]any) (*plugins.ModuleFileItem, error) {
	return plugins.NewModuleFileItem(value)
}

func originalTransferSourceItem(source originalTransferFileContext, file PlanFile) (*plugins.ModuleFileItem, error) {
	if source.Item == nil {
		return originalLocalFileItem(file.Source)
	}
	var path, storage string
	if json.Unmarshal(source.Item.Fields["path"], &path) != nil {
		return nil, bad("PLUGIN_TRANSFER_CONTEXT", "冻结文件项缺少有效路径")
	}
	if file.DirectoryDigest != "" {
		path = strings.TrimSuffix(path, "/")
	}
	if path != file.Source || json.Unmarshal(source.Item.Fields["storage"], &storage) != nil || storage != "local" && storage != "" {
		return nil, bad("PLUGIN_TRANSFER_CONTEXT", "冻结的本地文件项与实际整理文件不一致")
	}
	return source.Item.Clone(), nil
}

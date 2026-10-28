package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"light-media/internal/plugins"
)

func (a *App) originalDirectoryStreamSize(ctx context.Context, root string) (*int64, error) {
	path := filepath.Join(root, "BDMV", "STREAM")
	pair := originalTransferStorageFromContext(ctx)
	var item *plugins.ModuleFileItem
	var err error
	if pair != nil && pair.Source != nil {
		raw, e := pair.Source.call(ctx, "get_item", nil, path, nil, "")
		if e != nil {
			return nil, e
		}
		if !originalModuleResultTruthy(raw) {
			return nil, nil
		}
		fields, e := originalMetadataStorageItem(raw)
		if e != nil {
			return nil, e
		}
		item, err = plugins.NewModuleFileItem(fields)
	} else {
		item, err = originalLocalFileItem(path)
		if os.IsNotExist(err) {
			return nil, nil
		}
	}
	if err != nil {
		return nil, err
	}
	var rows []json.RawMessage
	if pair != nil && pair.Source != nil {
		raw, e := pair.Source.call(ctx, "list", item, "", nil, "")
		if e != nil {
			return nil, e
		}
		if !originalModuleResultTruthy(raw) {
			size := int64(0)
			return &size, nil
		}
		var tuple bool
		rows, tuple = plugins.DecodeModuleResultTuple(raw)
		if !tuple && json.Unmarshal(raw, &rows) != nil {
			return nil, bad("PLUGIN_STORAGE_INVALID", "原盘 STREAM 未返回文件项列表")
		}
	} else {
		var fields map[string]any
		raw, _ := json.Marshal(item)
		_ = json.Unmarshal(raw, &fields)
		list, e := a.originalMetadataNativeList(ctx, fields)
		if e != nil {
			return nil, e
		}
		for _, entry := range list {
			raw, e := json.Marshal(entry)
			if e != nil {
				return nil, e
			}
			rows = append(rows, raw)
		}
	}
	var size int64
	for _, row := range rows {
		fields, e := originalMetadataStorageItem(row)
		if e != nil {
			return nil, e
		}
		if fields == nil || fields["size"] == nil {
			return nil, bad("PLUGIN_STORAGE_INVALID", "原盘 STREAM 文件项缺少大小")
		}
		n, e := originalResourceInteger(fields["size"])
		if e != nil {
			return nil, e
		}
		size += n
	}
	return &size, nil
}

func (a *App) originalTransferIsBluray(ctx context.Context, item map[string]any) (bool, error) {
	if sourceText(item["type"]) != "dir" {
		return false, nil
	}
	for _, name := range []string{"BDMV", "CERTIFICATE"} {
		child, err := a.originalMetadataGetFileItem(ctx, sourceText(item["storage"]), filepath.Join(sourceText(item["path"]), name))
		if err != nil {
			return false, err
		}
		if child != nil {
			return true, nil
		}
	}
	return false, nil
}

func (a *App) originalNativeDirectoryModuleInfo(ctx context.Context, file PlanFile, plan ImportPlan, out map[string]any, failure error) (map[string]any, error) {
	var receipt importReceipt
	if err := a.Store.Get(ctx, "import_receipt", plan.ID+"/"+file.ID, &receipt); err != nil {
		return nil, err
	}
	if receipt.DirectoryException != "" && failure != nil {
		// TransHandler's outer except constructs a new TransferInfo. Physical
		// prefix receipts survive, but none of their lists belong to this DTO.
		out["fileitem"], out["transfer_type"], out["need_notify"] = nil, nil, false
		out["message"] = receipt.DirectoryException
		return map[string]any{plugins.ModuleTransferInfoKey: out}, nil
	}
	item, err := originalLocalFileItem(file.Source)
	if err != nil {
		return nil, err
	}
	if receipt.DirectoryStreamSize != nil {
		item.Fields["size"], _ = json.Marshal(*receipt.DirectoryStreamSize)
	}
	out["fileitem"] = item
	out["file_list"], out["file_list_new"] = []string{}, []string{}
	for _, relative := range receipt.DirectoryCompleted {
		out["file_list"] = append(out["file_list"].([]string), filepath.Join(file.Source, relative))
		out["file_list_new"] = append(out["file_list_new"].([]string), filepath.Join(file.Target, receipt.directoryTarget(relative)))
	}
	if failure != nil {
		out["message"] = a.clean(failure.Error())
	} else {
		target, err := originalLocalFileItem(file.Target)
		if err != nil {
			return nil, err
		}
		out["target_item"], out["target_diritem"] = target, target
		out["need_scrape"] = importBool(plan.Scraping, true)
	}
	return map[string]any{plugins.ModuleTransferInfoKey: out}, nil
}

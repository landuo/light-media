package app

import (
	"context"
	"slices"
)

// Unlike move, softlink does not remove the source tree. Reuse its ordinary
// frozen-tree checks and directory prefix mapping, binding only the actor's
// identity so remaining members cannot silently change provider on recovery.
type importDirectoryStorageSoftlinkState struct {
	InstanceID string `json:"instance_id"`
	Key        string `json:"key"`
}

func (a *App) bindDirectoryStorageSoftlink(ctx context.Context, receipt *importReceipt) error {
	var storage *originalTransferStorage
	if pair := originalTransferStorageFromContext(ctx); pair != nil {
		storage = pair.Source
	}
	prior := receipt.DirectoryStorageSoftlink
	if prior != nil {
		if storage == nil || storage.definition.InstanceID != prior.InstanceID || storage.definition.Key != prior.Key {
			return bad("REVIEW_STORAGE_SOFTLINK", "目录未完成成员的源存储选择已变化")
		}
	} else if receipt.State == "planned" {
		if storage == nil {
			return nil
		}
	} else if storage != nil {
		return bad("REVIEW_STORAGE_SOFTLINK", "原生目录未完成成员不切换到新源存储")
	} else {
		return nil
	}
	if storage.object == nil || storage.definition.Methods["softlink"] == "" || !slices.Contains(storage.object.manifest.Methods, storage.definition.Methods["softlink"]) || !originalObjectStillLoaded(storage.object) {
		return bad("PLUGIN_STORAGE_METHOD", "目录源存储缺少可调用方法：softlink")
	}
	if prior == nil {
		receipt.DirectoryStorageSoftlink = &importDirectoryStorageSoftlinkState{InstanceID: storage.definition.InstanceID, Key: storage.definition.Key}
		return a.Store.Put(ctx, "import_receipt", receipt.ID, receipt)
	}
	return nil
}

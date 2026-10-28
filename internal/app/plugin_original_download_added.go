package app

import (
	"context"
	"encoding/json"

	"light-media/internal/plugins"
)

// DownloadChain submits this ordinary module call before broadcasting the
// accepted download. Its result is ignored and it must not hold the HTTP/job
// response open. The existing App lifetime waits for this concrete SDK call;
// no new queue, executor or subtitle business service is introduced.
func (a *App) submitOriginalDownloadAdded(ctx context.Context, snapshot *downloadContextSnapshot) {
	if snapshot == nil || snapshot.Original == nil || snapshot.OriginalDownloadDir == "" || snapshot.OriginalMaterialRef == "" {
		return
	}
	// Freeze the value passed to the background call. The source shares a Python
	// Context object; the Go ABI deliberately transports its complete JSON value.
	full, err := json.Marshal(snapshot.Original)
	if err != nil {
		a.logOriginalPluginContext(ctx, "ERROR", "download_added_submit_failed", "提交下载成功后处理失败")
		return
	}
	directory, material := snapshot.OriginalDownloadDir, snapshot.OriginalMaterialRef
	if !a.admitOriginalBackground() {
		a.logOriginalPluginContext(ctx, "ERROR", "download_added_submit_failed", "提交下载成功后处理失败")
		return
	}
	go func() {
		defer a.wg.Done()
		ctx := context.WithoutCancel(ctx)
		groups, err := a.originalPluginModules(ctx, "")
		if err == nil && !originalModuleGroupsHaveMethod(groups, "download_added") {
			return
		}
		if err == nil {
			var content []byte
			content, err = a.resolveMaterial(ctx, material, materialPurpose)
			if err == nil {
				var encoded json.RawMessage
				encoded, err = plugins.ModuleResultBytes(content)
				if err == nil {
					kwargs, encodeErr := json.Marshal(map[string]any{"context": json.RawMessage(full), "download_dir": directory, "torrent_content": encoded})
					err = encodeErr
					if err == nil {
						_, err = a.invokeOriginalPluginModuleGroups(ctx, "download_added", nil, json.RawMessage(`[]`), kwargs, nil, groups)
					}
				}
			}
		}
		if err != nil {
			a.logOriginalPluginContext(ctx, "ERROR", "download_added_failed", "执行下载成功后处理失败")
		}
	}()
}

# 受管目录追加发布与上游基线同步（第二十二阶段）

## 基线与上游同步

- 远程 `origin/main` 前进到 `8307f90e3d8556588d2426dc15cf1159c85cea69`（BrushFlow 下载列表经 qBittorrent 5 登录路径）。本轮将该提交的 `internal/app/plugin_original_host_downloader.go` 改动直接应用到当前工作树，未新建提交、未改动 MoviePilot。
- 其余整理历史实现仍基于本分支累积成果；报告中的验证均为本地独占合成材料。

## MoviePilot 语义

MoviePilot 的目录整理没有逐叶所有权索引：目标目录已存在时，`transfer` 仍按计划把成员写入该目录，未涉及的旧条目保留；整理历史按源记录，目标指向同一个目录。不同来源写同一媒体目录因此是允许的，历史各自独立。

之前本项目对「目标根已有 `managed_file` 归属」统一报 `DIRECTORY_MERGE_OWNED_TARGET`，对 MP 来说是额外防御。本轮按上述语义开放**同一受管目录的追加发布**，同时保留逐叶归属事实。

## 实现

1. [import_directory_merge.go](</Users/accidia/projects/light-media/internal/app/import_directory_merge.go>) 不再一律拒绝受管根，改为调用新增的 [admitOwnedDirectoryAppend](</Users/accidia/projects/light-media/internal/app/import_directory_merge_owned.go>)：
   - 根必须是已登记的目录资产（`Kind=video`、目录快照、`AssetID` 非空），实际目录 inode 与登记快照一致，防止目标被替换后继续写；
   - 资产必须同路径、同 Root、状态 `present/external`；
   - 计划媒体键与季必须与资产一致，避免把不同影视塞进同一目录。
2. 追加复用原目录资产：`importReceipt` 新增 `AppendAssetID`/`MediaKey`；发布收据记录被复用的资产，[import_run.go](</Users/accidia/projects/light-media/internal/app/import_run.go>) 不再为同一路径生成第二个资产，而是合并集数与集范围、保留原 `CreatedAt`/`SourcePath`/`IntentID`/尺寸下限。
3. 归属仍按真实发布：新收据的 `SharedDirectory` 只包含本次实际发布的成员，既有成员属于原收据/资产，根 `managed_file` 不被改写。删除追加历史只移除本次成员，旧成员与旧历史不受影响。
4. 已有成员若属于其它资产，仍以 `DIRECTORY_MERGE_OWNED_TARGET` 明确失败——跨资产覆盖需要真正的成员转移语义，本轮不伪造。外部插件整树 softlink 的冻结/认领路径保持原有边界；原生逐叶 softlink 追加走同一准入。

## 验证

- [追加主样本](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round22-append.log>)：`TestDirectoryMergeAppendsIntoOwnedRootAndKeepsPriorMembers`（copy、hardlink）通过 **5.923s**。真实首轮目录导入 → 同一目标的第二来源追加 → 旧成员字节不变、新成员落地、仅一个新资产、两条独立历史；随后删除追加历史只删本次成员，旧成员与旧历史保留。
- 原生软链接追加机制样本：首个目录发布后以其真实收据登记目录资产，再以 softlink 追加 `BDMV/STREAM/00002.m2ts`；新成员为真实软链接、旧成员不变、收据仅登记新成员且 `AppendAssetID` 复用原资产。
- 不同影视身份追加被拒绝（`IMPORT_CONFLICT`），收据不产生所有权。
- 目录/共享/历史相关回归 `TestImportDirectoryMerge|TestSharedDirectory|TestDirectoryMerge|TestDirectoryOverwrite|TestDirectoryStorageSoftlink|TestImportMoveDirectory|TestImportSoftlink|TestImportMove|TestHistoryUpsert|TestTransferHistory`：PASS **91.230s**（[日志](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round22-append-regression.log>)）。
- 扩大整合 **343.561s**、全仓编译、Linux amd64 交叉编译与 diff 检查通过；未执行生产下载器、NAS 或云盘验证。

## 边界

- 跨资产同名成员覆盖转移、外部整树 softlink 对既有根的增量认领、来源对自动订阅链的进一步联调与最终全量联调仍需继续；不把本轮追加等同于全部共享根场景完成。
- 未提交、未推送、未部署；测试使用独占临时目录，结束后清理。

# 跨资产同名成员接管（第二十四阶段）

## MoviePilot 语义

目录整理写入已存在目标时按目录递归覆盖同名文件，不区分该文件由哪一次整理产生；MoviePilot 没有逐叶所有权索引，因此同名文件被新目录整理替换后，旧记录只保留其历史事实。本项目此前对“叶属于其它资产”统一报 `DIRECTORY_MERGE_OWNED_TARGET`，属于额外防御。本轮按上述语义开放精确接管。

## 实现

1. [import_directory_merge.go](</Users/accidia/projects/light-media/internal/app/import_directory_merge.go>)：目标叶已登记为任意资产时，先要求实际内容与登记快照完全一致；一致则记录 `importDirectoryMergeChild.Replaced` 并允许备份后发布，内容已漂移则 `REVIEW_DIRECTORY_MERGE` 拒绝。
2. [import_directory_merge_owned.go](</Users/accidia/projects/light-media/internal/app/import_directory_merge_owned.go>) 新增 `supersedeReplacedDirectoryMembersTx`：在同一提交事务内
   - 复核目标仍是本次发布结果；
   - 读取并逐字段比对旧 `managed_file` 后按主键删除；
   - 旧资产状态改为 `superseded` 并释放 `asset_slots`；
   - 旧资产其它文件、根记录与其它资产均不触碰。
3. 接手后的叶归属本次目录收据的 `SharedDirectory` 子集；旧收据保留其当次快照，旧历史仍是独立成功记录（与 MP 一致），后续删除旧历史会因快照不符而安全拒绝。

## 验证

- [接管与合并专项](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round24-takeover.log>)：**11.858s PASS**
  - `TestDirectoryMergeTakesOverAnotherAssetsLeaf`：先真实单文件整理产生资产 A，再以目录整理覆盖同名叶；目标字节为新内容，旧 `managed_file` 删除，旧资产 `superseded` 且槽为 0，目录资产唯一，两条历史各自成功。
  - `TestDirectoryMergeReplacesOwnedMemberWithinSameAsset`：同资产成员替换仍正常。
  - `TestDirectoryMergeRejectsOwnedMemberDriftAndForeignAsset`：同资产漂移、跨资产漂移均拒绝，失败后不写入新内容。
  - `TestDirectoryMergeAppendsIntoOwnedRootAndKeepsPriorMembers`、原生 softlink 追加机制样本保持通过。
- [目录与历史回归](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round24-directory-regression.log>)：`TestImportDirectoryMerge|TestDirectoryMerge|TestSharedDirectory|TestDirectoryOverwrite|TestDirectoryStorageSoftlink|TestDirectorySoftlink|TestImportMoveDirectory|TestHistoryUpsert|TestTransferHistory|TestAutomaticDirectoryRecognition` PASS **70.752s**。
- 扩大整合 **355.631s**、全仓编译、Linux amd64 交叉编译与 diff 检查通过。

## 边界

- 旧资产被接管后其旁车记录仍保留（文件未变），不自动删除、不转移到新资产；与 MP 的“只写目标文件”一致。
- 外部插件整树 softlink 的增量认领、订阅刷新来源联调与最终整体联调仍未完成。
- 未提交、未推送、未部署；验证为独占合成材料，不代表 NAS／云盘或生产验收。

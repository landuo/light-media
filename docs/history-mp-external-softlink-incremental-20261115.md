# 外部整树软链接增量认领（第二十六阶段）

## MoviePilot 语义

外部存储模块的目录软链接在 MoviePilot 中同样是“把计划成员写入目标目录”：目标已存在时照常创建链接，旧条目保留。此前本项目的外部目录软链接只允许目标不存在（`TargetAbsent`）并按整树双向映射认领，已有目标一律拒绝，属于额外防御。

## 实现

1. [plugin_original_transfer_softlink_directory.go](</Users/accidia/projects/light-media/internal/app/plugin_original_transfer_softlink_directory.go>)：冻结阶段允许目标已存在。
   - 受管根仍先经过第二十二阶段的 `admitOwnedDirectoryAppend`（真实目录 inode、同资产、同媒体键/季），未登记根直接允许；
   - 扫描并记录调用前的 `Existing`（根身份、条目快照、链接目标），模块只能新增/重写为指向原文件的链接。
2. 新增 [plugin_original_transfer_softlink_directory_append.go](</Users/accidia/projects/light-media/internal/app/plugin_original_transfer_softlink_directory_append.go>) `adoptExistingExternalSoftlinkDirectory`：
   - 校验目标根 inode、来源树、既有条目（目录身份、普通文件快照、链接目标）全部未变；
   - 只把“调用前不存在、调用后是指向对应原文件的真实软链接”的叶登记为本次 `Members`，新建父目录登记为 `Directories`；
   - 映射之外的普通/链接新增条目拒绝；模块产生的空目录作为副作用保留但不认领；
   - `receipt.AppendAssetID` 复用受管根资产，收据 `Existing` 保留既有条目供校验，不参与删除/重基准。
3. [import_softlink_directory.go](</Users/accidia/projects/light-media/internal/app/import_softlink_directory.go>)：`snapshotOwnedSoftlinkDirectory` 接受 `Existing` 中未认领的条目（要求身份/快照/链接不变），缺失或额外改动仍报错；删除、重整等消费者只遍历 `Members`/`Directories`，不会删除既有成员。
4. 外部模块收据创建时记录 `MediaKey`；重复发布（已 `published` 的收据）不再重新冻结证明，避免覆盖既有 `Existing`/成员集合。

## 验证

- [专项](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round26-external-adoption.log>) **3.681s PASS**，`TestOriginalTransferModuleActualSDKDirectorySoftlinkAdoption` 四个分支：
  - `success`、`asset-sql-cold`：目标不存在时的原有整树认领与冷恢复保持；
  - `counterfeit-copy`：复制冒充软链接仍拒绝；
  - `existing-target`：目标预先存在 `foreign.txt`，真实 SDK 模块写入全部叶链接后，仅新链接成为成员（5 个），`foreign.txt` 保持原快照且不被认领，根为真实目录，无伪造子收据、一次模块调用、一个完成回调；再次发布不重新冻结既有条目。
- [外部软链/目录/历史回归](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round26-external-adoption-regression.log>)：PASS **113.324s**。
- 扩大整合 **363.389s**、全仓编译、Linux amd64 交叉编译与 diff 检查通过。

## 边界

- 增量认领只接受“新增链接 + 既有条目不变”；模块若改写既有普通文件或既有链接指向，仍拒绝，不伪造内容等价。
- 受管根复用资产的场景已由准入覆盖，但本轮新增用例以未登记既有根为主；受管根外部增量与原生逐叶追加共用同一准入。
- 未提交、未推送、未部署；验证为独占合成材料，不代表 NAS／云盘或生产模块验收。

# 第十五阶段：原生蓝光/原盘目录软链接

后续[第十六阶段](<history-mp-softlink-external-storage-files-20261115.md>)已继续接通普通本地文件的外部transfer认领与source storage actor。目录提供方/actor和UI仍未齐，本页保留当时边界。

重新 fetch 后在同一工作树继续，HEAD/origin/main 均为 `4a222f0fa0c06848e7924e6614654a38e999fb19`。原 MoviePilot/Frontend 只读；未提交、推送、部署或改写生产媒体、下载器。

## 实际实现

[目录发布器](<../internal/app/plugin_original_transfer_directory_publish.go>)沿既有 MP 目录任务逐文件发布，不把整根目录做成一个符号链接：

- 目标根和必要父目录是真实目录，每个原成员是真实软链接；小附加文件不受普通视频最小大小筛选丢失。
- 沿原文件列表递归，不额外复制空目录；源中的空 CERTIFICATE 仍保留，目标不为无成员的目录创建占位。
- 保留原 FileItem、实际成员名映射、完整 file_list/file_list_new、STREAM 大小与单一目录资产/历史，不按每个 m2ts 建独立历史。
- 先持久化父节点与真实子发布收据，再继续下一个成员；中断后复用精确已发布 inode，不用同文本认领未知 entry，不重做已发布成员。

[目录证明与检查](<../internal/app/import_softlink_directory.go>)复用 receipt/managedFile 的 Softlink 证明，增补真实目录节点、成员 entry 与普通 referent：

- 聚合 Snapshot 是实际目标树 entry 的大小/摘要；Asset.Size 仍是原目录有效载荷字节，SDK STREAM 大小另按原字段保存。
- 可用性核对完整目标树和每个引用文件；发布前/进行中核对冻结原树。发布完成后源目录新增无关 NFO 不使已有链接假失效。
- 删除阶段可以检查已冻结成员的已知缺失子集，以恢复原 DirectoryRemovalStarted；未知、替换成员仍阻止继续。
- [新事实扫描器](<../internal/app/plugin_original_transfer_directory_snapshot.go>)只为这些精确证明收集事实；通用 snapshotOriginalDirectory/importOpen/browserStat 仍不跟随任意 symlink。

## 删除与重整

[目录生命周期](<../internal/app/import_softlink_directory_lifecycle.go>)沿原历史四种删除选择和隔离收据清理已核对 entry，逐层 unlink/rmdir，不递归跟随引用实体：

- 仅目标与断链目标删除均保留原实体；两端删除继续沿原源快照及下载器归属流程处理源。
- 历史重新整理到另一目的路径先清理精确旧目录，所有新成员直接引用原资源树；保留来源和稳定历史记录。
- 直接从受管目录原位更正复用真实目录/成员 inode，继承匹配的真实旧成员映射收据，不伪造完成列表。
- 普通文件 V3 覆盖入口继续拒绝目录资产，不把新增目录 proof 混入仅支持文件 unlink 的覆盖支路。目录替换不冒称完整覆盖能力已实现。

## 目录刮削

[元数据目录日志](<../internal/app/plugin_original_metadata_directory.go>)继续冻结原成员与每个真实 staged write；[旁车写入](<../internal/app/plugin_original_metadata_write.go>)只在已绑定目录内处理已知成员：

- 新 NFO 写在库内目录，成为显式普通文件成员；媒体成员仍是原软链。
- 若原目录中已有链接 NFO，默认不覆盖则保留；显式 overwrite 只交换并 unlink 旧 NFO entry，绝不打开源 NFO 写入。
- 结束时验证全部原成员和已知 Writes，原子更新 root managedFile 及对应发布收据的完整 tree/proof；不扫描认领未知新文件。
- 元数据存储列表通过聚合 owner 描述精确成员，不因此创建新子资产，也不放宽通用文件管理。
- SQL 登记失败后，实际写出的 NFO 由原持久日志恢复。刮削入口在严格新树校验前恢复已有日志，避免把自己的已知写入误判为未知改动；遵守原任务取消和写入 lease。

## 验证

[真实手动入口/SDK测试](<../internal/app/import_softlink_directory_test.go>)执行原生目录任务，并在第一个真实 child published 后取消任务、关闭应用再重试；检查单资产/历史、STREAM=12、3个完整成员、tiny.txt、空目录语义、源字节及已发布 inode 保持。新目录发布和旧 copy/hardlink/move 目录回归通过 **9.405s**，见[目录发布回归](<../work/history-mp-alignment-20261115/softlink-directory-regression.log>)。

[消费者测试](<../internal/app/import_softlink_directory_lifecycle_test.go>)实际领取/完成手动与刮削任务，验证目标/断链/两端删除、历史新路径与原位重整、新 NFO、链接 NFO 的默认保留和显式替换，以及源树字节不变。对应[定向软链接及方式检查](<../work/history-mp-alignment-20261115/round15-softlink-final.log>)通过 **19.727s**。

另实际注入元数据 managedFile SQL 提交失败，确认 NFO 已写、日志已持久化，关闭重开应用后运行原 scrape job，验证 owner/receipt/proof 原子恢复、日志完成、源不变；[冷恢复](<../work/history-mp-alignment-20261115/round15-directory-metadata-recovery.log>)通过 **1.05s**。

外部 transfer 声明 softlink 却实际 copy 的 SDK 反例现在明确失败，不再把普通文件误登记为所选软链接方式。外部软链接结果认领尚未实现，这不是相关提供方已支持的证明。

[扩大整合](<../work/history-mp-alignment-20261115/round15-integration.log>)通过 **264.364s**，包含全部新增目录生命周期、NFO冷恢复、此前普通软链接/V3库存及原有整理/历史/元数据回归。全仓仅编译检查、Linux/amd64测试交叉编译及diff检查通过；未执行Linux二进制，交叉产物已清理。[结构化证据](<../testdata/history-mp-alignment-20261115/native-softlink-directory-evidence.json>)。以上为独占合成材料＋真实本地 Go/SQLite/文件系统/独立 SDK，不是 NAS、完整新增 softlink 下载协议或生产媒体验证。

## 仍需继续

存储 actor、外部 transfer/delete_file 的软链接接管、方式 UI/浏览器验证以及 provider-native 普通网页历史身份继承仍未齐。本轮未开放软链接选择项，无前端改动；全局默认继续固定硬链接。整体目标保持进行中。

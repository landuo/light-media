# 受管成员替换与自动目录来源识别（第二十三阶段）

## MoviePilot 语义

1. 目录整理写入已存在的目标目录时，MoviePilot 直接覆盖同名成员（按目录递归，不走普通单文件覆盖事件），不会因为目标文件曾被本应用登记而拒绝。
2. 自动目录整理与手动整理共用同一识别链：`RECOGNIZE_SOURCE` 为豆瓣／Bangumi／AniList 时，文件名元数据通过该来源识别，而不是只走 TMDB 匹配。

## 实现

### 受管目录成员替换

- [import_directory_merge.go](</Users/accidia/projects/light-media/internal/app/import_directory_merge.go>)：目标叶若已登记为**同一追加资产**的 `managed_file`，且当前内容与登记快照一致，允许按 MP 语义先备份再发布新成员；其它资产的叶、或内容已漂移的叶，仍以 `DIRECTORY_MERGE_OWNED_TARGET` / `REVIEW_DIRECTORY_MERGE` 明确失败。
- 共享目录内的成员本来就以根收据的 `SharedDirectory` 子集记录、逐叶备份额隔离发布；本轮补齐独立登记叶的同资产替换，并保持旧收据快照不被篡改（旧历史仍记录其当次发布事实）。
- 追加复用原目录资产（第二十二阶段），替换后仍只有一个目录资产，根记录与 `asset_slots` 不被重复创建。

### 自动目录来源识别

- 新增 [directory_provider_identity.go](</Users/accidia/projects/light-media/internal/app/directory_provider_identity.go>)：全局来源非 `themoviedb` 时，`directoryFileIdentity` 用文件名元数据经 `recognize_media`（保留插件 name.recognize/模块覆盖）→ `obtain_images` → provider DTO 投影，得到 `ProviderSource/ProviderID` 的私有身份；电视剧沿用文件名季集，电影不需要编号。
- `ImportRequest` 新增私有 `originalDirectoryProvider`，仅在自动目录规则识别成功后授予 `providerImport()`；普通网页 JSON 不能伪造该授权。
- NFO 已给出明确 TMDB 身份的文件仍走原有本地身份，不因全局来源改变而重新猜测。

## 验证

- [成员替换与来源](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round23-member-replacement-source.log>)：**11.517s PASS**
  - `TestDirectoryMergeReplacesOwnedMemberWithinSameAsset`：真实两轮目录导入，同资产成员被替换为新字节，根资产仍唯一，新收据成员快照为新内容。
  - `TestDirectoryMergeRejectsOwnedMemberDriftAndForeignAsset`：独立登记叶被外部改动、或归属其它资产时均拒绝，失败后目标不含新内容。
  - `TestAutomaticDirectoryRecognitionUsesGlobalProviderSource`：保存 `recognition_source=douban` 与真实目录规则后，`directoryFileIdentity` 得到 `douban:704` 且无 TMDB ID；`previewImport` 计划媒体为来源身份；实际执行后历史记录 `provider_source=douban`、`provider_id=704`、`id=0`，目标字节与源一致。
- [自动来源与目录回归](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round23-source-directory-regression.log>)：`TestAutomaticDirectoryRecognition|TestDirectory|TestImportDirectoryMerge|TestDirectoryMerge|TestAutomaticDisc|TestSharedDirectory|TestMediaRecognitionSource|TestManualOrganize|TestDirectoryStorageSoftlink` PASS **66.080s**。
- 扩大整合 **351.646s**、全仓编译、Linux amd64 交叉编译与 diff 检查通过。

## 边界

- 跨资产（不同资产）同名成员的所有权转移已在第二十四阶段[跨资产接管](<history-mp-cross-asset-leaf-takeover-20261115.md>)实现：快照一致时替换并作废旧资产；外部插件整树 softlink 的增量认领、来源对订阅刷新链的进一步联调与最终整体联调继续。
- 未提交、未推送、未部署；验证为独占合成材料，不扩称 NAS／云盘或生产验收。

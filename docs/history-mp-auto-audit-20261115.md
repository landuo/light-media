# 自动整理与历史：第四阶段源码核对

这是第四阶段发现的差异记录，不是生产下载器或NAS验收。原MoviePilot仅只读参考。**第五阶段下列五项已落地并通过合并回归；历史描述保留以说明变更依据，当前状态见文末。**

## 已保持的核心契约

- 预览只保存内部计划，不创建执行历史。目录 `organize_file` 在识别/准备阶段不冒充已执行；实际提升为import任务后保存pending，确定失败也可提升为失败import。这是本项目持久执行模型与MP结果型历史的差别，符合“显示最新实际提交”的用户约定。
- 下载完成的多文件计划逐文件入队；已有任何import任务的文件不会因下一次轮询再次提交，包括失败和取消，未受影响的同包文件仍可继续。
- 取消精确作用于执行fileID；稳定历史ID不替代执行ID。取消不删除媒体文件，不复活其他旧任务；内部计划和恢复收据仍保留。
- 一般整理失败不自动指数重试，仅插件刷新、下载器停用、暂停等明确暂态有独立恢复规则。

本项目入口：[目录入队](<../internal/app/organize_directory_queue.go>)、[逐文件准备](<../internal/app/organize_file_queue.go>)、[下载完成](<../internal/app/search_download.go>)、[逐文件实际提交](<../internal/app/import_file_submission.go>)、[历史写事务](<../internal/app/transfer_history_write.go>)、[取消标记](<../internal/app/organize_file_cancel.go>)。

## 第四阶段发现的明确差异

### 1. 自动目录的已有历史跳过

MP `monitor/dispatcher.py:178-188` 按源路径+存储查询任意已有历史；`chain/transfer.py:3413-3454` 再去重，成功/失败均跳过。删除历史后可以重新进入自动处理。

本项目 [目录观察](<../internal/app/directory_filesystem.go>) 按快照、规则fingerprint和识别evidence判断是否重置；源内容、规则或NFO变化可能再次入队，入队只查活动任务，不查终态历史。仅删除历史却不会解除旧observation终态。`TestDirectoryFileAmbiguousIdentityNeedsReviewAndNFOCanResolve` 明确固定了“NFO修正后自动重试”的旧行为。

后续应统一自动源历史gate与删除后的再次资格；调整上述测试契约，而不是保留互相矛盾的两套语义。保留稳定时间、下载完整性核验以及用户主动手动重整。

### 2. 自动目录失败的已知上下文

目录prepare在取得媒体或计划后失败，仍调用不带known plan的失败包装器；相较手动路径，已知标题、ID、分类、模式、目标及季集可能丢失。应复用 [已有失败保存 helper](<../internal/app/organize_manual.go>) 传已得到的事实；纯未知身份不伪造ID。

### 3. 下载计划形成前的确定性失败

`planCompletedDownload` 在部分识别、材料/覆盖核验或preview错误时只写intent错误和日志，未形成逐文件失败历史。已有完整源文件清单后的确定性失败需补齐；下载器连接失败、尚未完成等暂态不应伪造成“已尝试文件整理失败”。已有计划的blocked/admission失败路径已保存实际失败任务。

### 4. 目录识别前取消的观察状态

取消 `organize_file` 后，directory_file可能仍是queued且PlanID为空；扫描只从import任务找PlanID，导致观察记录长期显示queued。队列实际取消已成功。最小修复是同步取消终态及原因，不增加新的竞态框架；用取消后普通连续两轮poll的顺序测试即可。

### 5. 下载时冻结的自定义分类

分类是媒体事实，不等于是否创建分类目录。第四阶段已将识别DTO category、完整本地分类证据与目录开关解耦，保存到ImportPlan及历史快照，不在GET重新分类，也不为关闭分类目录额外请求元数据。

原版还会继承 `download_history.media_category`（`chain/transfer.py:1826-1841,2358-2359`）。本项目分类位于加密下载上下文 `Original.Media.category`，并非下载器torrent.category。非TMDB provider已有DTO传入路径；普通TMDB完成下载目前未传该冻结分类。应复用已核验的下载context，仅传已知分类，不用旧DTO覆盖当前媒体身份，也不新增无意义的catalog.Media/Intent大字段。

## 既有验证入口

- `TestDirectoryFileNewStableVideoImportsOnceAndRetainsSource`
- `TestDirectoryFileRuleChangeBlocksQueuedPublication`
- `TestDirectoryFileIncompleteDownloaderNeverImports`
- `TestDirectoryFileAmbiguousIdentityNeedsReviewAndNFOCanResolve`
- `TestMPOrganizePackIsolatesBlockedFailedAndCancelledFiles`
- `TestHistoryParityV031QueueCancellationIsExactAndPreservesFiles`
- `TestHistoryUpsertQueueCancelAddressesOldExecutionNotLatestHistory`
- `TestManualOrganizeDirectoryQueuePreparesAndReplays`
- `TestImportQueueColdRecoveryKeepsStagedLink`

这些名字定位第四阶段行为；执行证据按主报告各阶段分别记录。

## 第五阶段处理结果

1. [目录历史门控](<../internal/app/directory_history.go>) 在观察和实际入队事务按原源读取已保存current。已有成功、失败或实际提交中的历史都跳过；修改NFO或规则不绕过失败历史。仅记录删除后重新经过稳定时间/下载完整性检查，不恢复旧执行。
2. [目录准备](<../internal/app/organize_directory_queue.go>) 逐阶段保留已知上下文，调用已有失败保存helper。未识别时保留解析标题/年份/季集但不造ID；完整计划提交失败保留媒体、分类、目标、方式。原HEAD两个queue文件的overlay实测重现元数据全部变空，修改后同一测试通过。
3. [下载准备失败](<../internal/app/download_preparation_failure.go>) 仅完整源集合上的明确语义错误冻结一次failed计划，保留Intent归属，复用现有逐文件admission。连接/元数据503、未完整、大小不一致、缺文件及普通IO错误不生成失败历史；quality未选文件缺失不阻断已选文件。
4. [目录观察](<../internal/app/directory_filesystem.go>) 同步识别前organize_file取消状态及原因。普通连续两轮poll验证保持cancelled、无新任务/执行历史、源文件不变。
5. [下载分类](<../internal/app/import_provider.go>) 从匹配下载ID/hash的加密上下文读取分类，内部传给ImportRequest；分类目录开/关均保存冻结分类，开时使用该目录层；旧DTO标题/type/id不替换当前身份。另发现原版SDK MediaInfo投影需继续同步plan.Category，已单列于[剩余选项](<history-mp-remaining-options-20261115.md>)。

原NFO自动重试测试已明确改为 `TestDirectoryFileAmbiguousIdentityRequiresHistoryDeletionBeforeAutomaticRetry`，并实际执行删除后新识别及硬链接，不是删除旧断言或跳过失败。新增 [目录测试](<../internal/app/directory_history_test.go>) 与 [下载测试](<../internal/app/download_preparation_failure_test.go>) 覆盖上述契约；第五阶段合并目录/历史/手动/下载回归通过。

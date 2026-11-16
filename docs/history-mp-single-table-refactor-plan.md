# 整理历史严格单表重构计划

目标：按 MoviePilot `TransferHistory` 的单表语义重写整理历史，移除 `transfer_history_attempts`、`transfer_history_dirty`、`transfer_history_dirty_sources`、`transfer_history_index_version` 及全部历史触发器投影。成功/失败完成时按 `src` 删除旧行并插入新行（新 ID）；预览与运行中任务不写历史；整理队列保持独立。

## 目标表

单表 `transfer_history`，字段对齐 MP：

- `id INTEGER PRIMARY KEY AUTOINCREMENT`
- `src`, `src_storage`, `src_fileitem`(JSON)
- `dest`, `dest_storage`, `dest_fileitem`(JSON)
- `mode`, `type`, `category`, `title`, `year`
- `tmdbid`, `imdbid`, `tvdbid`, `doubanid`, `bangumiid`, `anilistid`
- `media_source`, `media_id`
- `seasons`, `episodes`, `image`
- `downloader`, `download_hash`
- `status`(0/1), `errmsg`, `date`
- `files`(JSON), `episode_group`

内部执行引用放进 `src_fileitem`/`dest_fileitem`/`files` 的 JSON 扩展键（plan_id、file_id、asset_id、root_id、snapshot、softlink/shared proof），不新增历史专用表。索引与 MP 一致：`src`、`title`、`tmdbid`、`download_hash`、`(status,date)`、`(date,id)`、`(media_source,media_id)`。

## 写入

新增 `writeTransferHistoryCompletion(tx, plan, file, status, errmsg, job, callback)`：

1. 解析 `src`：主文件用 `file.Source`，附属文件用其自身 source；`organizeSource` 的 move 成功后重整入口仍按 MP 使用 `dest` 作为下一次 src（读取时计算，不写库）。
2. `DELETE FROM transfer_history WHERE src=?`（MP `add_force`）。
3. `INSERT` 新行，`id` 自增；`date` 用完成时间；`files` 保存本次实际提交的文件清单。
4. 同事务写入，失败随任务事务回滚。

调用点：`completeOriginalTransferCallback`（主文件/附属文件，成功与失败）、`import_run` 原生完成、`recordManualOrganizeFailurePlan`、目录队列失败。运行中/排队/预览不写。

## 读取与删除

- 列表：`SELECT ... FROM transfer_history` + 状态/搜索/排序/分页，默认 50。
- 详情/选择：只按自增 `id`；不再解析 execution file ID 别名。
- 整理入口：按当前行 `id` 读取 `src_fileitem`/`dest_fileitem` 与内部 JSON 证明；旧 ID 失效。
- 删除：按行内 FileItem 与证明执行四范围删除，然后删行；无隐藏标记、无 attempts。
- 回调/重试：通过计划/文件 ID 查询当前行（JSON 扩展键）；查不到则说明该执行已被新提交取代，按 MP 语义不再回写历史。
- 队列：继续读 `jobs`，不依赖历史表；历史页不再显示 pending。

## 迁移（生产停机）

1. 停 `light-media`，备份 `light-media.db` 与 compose。
2. 建 `transfer_history` 新表；从 `transfer_history_current` 按 `resource_source` 取最新行，映射为 MP 字段（保留原 `history_id` 作为一次性 `id` 或重新分配自增 ID）。
3. 校验行数：新表 = 旧 current 行数；抽样核对 src/dest/status/media/files。
4. 删除 `transfer_history_attempts/dirty/dirty_sources/index_version` 与旧 `transfer_history_current`，删除历史触发器。
5. 启动新版本，验证历史列表、单条/批量重整、删除、回调、软链、库存与队列。
6. 失败回滚：恢复 DB 备份与旧二进制/compose。

## 阶段

1. 单表 schema + 双写：新表与旧投影并行，回调/完成路径写新表，读取仍走旧表；对比字段。
2. 切换读取到新表；删除旧表读取代码；保留双写对照一个测试周期。
3. 删除旧表/触发器/attempts；更新 `transferHistoryItem` 扫描、删除、回调、重试、软链消费者。
4. 更新前端类型/选择/删除与全部稳定 ID/pending 测试；新增“重整换 ID”测试。
5. 生产迁移演练（本地副本 + NAS 副本），再做停机迁移与部署。

## 验收

- schema 查询只剩一张历史表及 MP 索引。
- 同一 src 连续成功/失败重整后历史 ID 变化，旧行不存在。
- 运行中任务只出现在队列，不出现在历史。
- 四范围删除、回调事件、软链删除、库存/订阅消费者通过真实夹具。
- NAS 迁移行数与抽样一致，历史页/按钮/分页正常。

# records 单表拆分实施记录（按 MoviePilot 每功能单表）

日期：2026-11-16
方案：`docs/records-split-mp-tables-plan.md`
范围：`internal/app` 存储层 + schema 版本 1 → 2 + 迁移 + 兼容层

---

## 1. 做了什么

1. `records(kind,id,payload,updated_at)` 不再是表。物理存储拆成 **15 张 MoviePilot 对齐表 + 43 张自有功能表 + `plugin_record` 过渡命名空间**，全部由 `internal/app/records_tables.go` 里的单一规格生成。
2. 新增 `migrateRecordsV2`：启动时把 v1 的每一行按 kind 搬到目标表，建立子表行（`import_plan_file`、`import_receipt_member`），把表重命名为 `records_frozen_v1`，写入 `migrations.version=2`。
3. `Store.Put/Get/List/Delete`、`txPut` 改为按 kind 路由；未知 kind 落到 `plugin_record`，保持插件兼容。
4. 生产代码里 **301 处手写 SQL 全部改写**：不再有 `FROM records` / `INTO records` / `UPDATE records`（迁移代码本身除外）。
5. 兼容视图在收尾阶段已删除：测试与生产代码全部改为直接访问功能表，`sqlite_master` 中不再有 `records` 表或视图。
6. 站点与管理员写操作同步投影到 MP 的 `site`、`user` 表。
7. 新增 CLI 与报告：`CheckRecordsMigration` 输出每 kind 行数、目标表、缺失表、未知 kind。

## 2. 表结构

### 2.1 MoviePilot 对齐表（列名/类型/索引与 MP 一致）

`user` `userconfig` `systemconfig` `plugindata` `site` `siteicon` `sitestatistic` `siteuserdata`
`message` `subscribe` `subscribehistory` `downloadhistory` `downloadfiles` `downloadfailure`
`transferhistory`（上一轮已完成） `mediaserveritem` `workflow` `agentchat` `agenttask` `passkey`

扩展列（每张表统一，写明用途）：

| 列 | 用途 |
| --- | --- |
| `uid TEXT UNIQUE` | light-media 的业务键；MP 的 `id` 保持 `INTEGER PRIMARY KEY AUTOINCREMENT` |
| `payload JSON` | 规范化记录本体，供尚未列化的字段与迁移回滚使用 |
| `updated_at TEXT` | 原 `records.updated_at` 语义 |
| `record_kind TEXT` | 多 kind 合表时的判别列（`intent`/`native_download`/`notification` 等） |

列投影由 `AFTER INSERT` / `AFTER UPDATE OF payload` 触发器完成，使用
`CASE WHEN json_valid(NEW.payload) THEN json_extract(...) END`，因此密封密文
（非 JSON 字节）不会让写入失败，缺字段也不会触发 `NOT NULL`。

### 2.2 自有功能表（43 张）

按域拆分：站点状态/统计/站内信、订阅事件与收据、下载预览与选择、素材、排除规则、
导入计划与回执（含子表）、库资产与证明、整理队列、目录监控、历史收据、
删种收据、插件兼容状态、通知/审计/缓存/去重锁。

`plugin_record(kind,id,payload,updated_at)` 保留旧 `(kind,id)` 形状，只服务两类数据：

1. 加密命名空间值（`subscription_history`、`subscription_quality_*`、`site_message_collection` 等），AAD 绑定 kind，搬表就要重封，收益为零；
2. 尚未建模的插件自定义 kind。

## 3. kind → 表（关键映射）

| kind | 表 | 说明 |
| --- | --- | --- |
| `subscription` | `subscribe` | MP 全列 + `uid`/`revision`/`note` 扩展 |
| `intent` / `native_download` | `downloadhistory` | `record_kind` 区分；`uid` 原样保留 |
| `download_added` | `download_accepted` | 密封的接受回执，单独成表，避免被下载历史查询 `json_extract` 扫到 |
| `notification` / `message` | `message` | `read` 扩展列 |
| `config` / `control` | `systemconfig` | `key` 由记录键投影 |
| `ui_preferences` | `userconfig` | `username='admin'` |
| `site_icon` | `siteicon` | 密文放 `payload` |
| `site_stats_latest` | `sitestatistic` | |
| `site_connection` | `site_state` | 冷却态单独落 `site_cooldown`，避免同站点 ID 覆盖 | |
| `site_stats_history` / `site_access_statistics` | `site_statistic_event` | |
| `asset` | `library_asset` | |
| `asset_identity` / `asset_presence` | `library_asset_proof` / `library_asset_presence` | 分开避免键覆盖 |
| `managed_file` | `managed_file` | `asset_id`/`path`/`sha256` 索引 |
| `import_plan` | `import_plan` + `import_plan_file` | 文件数组拆子表 |
| `import_receipt` | `import_receipt` + `import_receipt_member` | 目录成员拆子表 |
| `organize_queue_member` | `organize_queue_member` | 原 5 个表达式索引换成普通复合索引 |
| `directory_file` / `directory_file_state` | `directory_file` / `directory_file_state` | |
| `transfer_history_hidden` / `reorganize_cleanup` | `history_hidden` / `history_cleanup` | |
| `core_download_removal` / `_root` / `_chain` | `download_removal_receipt` / `_root` / `_chain` | 三种收据分表 |
| `cache` | `cache_entry` | `expires_at` 可清理 |
| `plugin_host_*` | `plugin_host_state` | |
| 其他加密命名空间 | `plugin_record` | 保持 `(kind,id)` |

完整 78 项见 `records_tables.go` 的 `recordKinds`。

## 4. 迁移与回滚

1. 启动 `OpenStore`：建表（`recordDDL`）→ `migrateRecordsV2` → 订阅别名回填 → 建视图与写触发器。
2. 迁移在单事务内完成：逐行 upsert → 子表物化 → 行数复核（`records` 行数必须等于读到的行数）→ `ALTER TABLE records RENAME TO records_frozen_v1`。
3. 空库直接 `DROP TABLE records`，不保留冻结表。
4. 回滚：恢复迁移前的 `light-media.db` 备份并用旧二进制启动；`records_frozen_v1` 保留一版，供抽样核对。
5. 重复执行安全：目标表按主键 upsert；`migrations.version=2` 后迁移直接跳过。

## 5. 验证

| 项 | 结果 |
| --- | --- |
| `go build ./...` | 通过 |
| `go vet ./...` | 通过 |
| `internal/storetest` | 迁移、路由、投影、密封、别名回填、索引命中、SQL 预检 7 组测试通过 |
| SQL 字面量预检 | 900 条 SQL（生产 + 测试）在 v2 schema 上 `Prepare` 通过（捕获并修复 20 余处残留 kind/uid 列引用） |
| 残留引用检查 | `TestNoLegacyRecordsReferences` 断言生产与测试代码中不存在 `FROM/INTO/UPDATE/JOIN records` |
| 拼接 SQL 预检 | `ValidateAssembledSQL` 覆盖整理历史 CTE、任务摘要、整理队列、通知、订阅别名，全部 `Prepare` 通过 |
| 测试夹具 | 93 处 `ON records` 触发器与 54 处断言改为落在目标表；`internal/app` 夹具测试仍需 OWNED_* 二进制才能实跑 |
| 本地迁移演练 | 24,200 行合成库（14 类 kind + 密封密文 + 未知 kind）：迁移 5.1s；行数全部对齐；1,200 条抽样 payload 字节一致；400 条密文完全一致；`--apply` 重复执行无副作用 |
| 生产库演练 | NAS 部署库副本 44,455 行 / 487MB / 47 张目标表：迁移 21.6s；逐表行数与预检零差异；1,811 条抽样 payload 字节一致；26 条密封收据完全一致；`records` 对象为 0 |
| NAS 生产迁移 | 2026-11-16 13:42 停机迁移：56.3s，逐表零差异，冻结 44,455 行；启动后目录监控报 `malformed JSON`，定位为 `download_added` 密封回执混在 `downloadhistory`，拆出 `download_accepted` 并加 schema v3 修复迁移，重启后监控错误为 0 |
| 生产复核（sealed 收据读） | 迁移后订阅检查报 `sql: no rows in result set`；原因是 `source_subscription_origin` / `subscription_acquisition` / `subscription_quality_*` 已落 `plugin_record`，读取仍查空的专表。8 处查询改回 `plugin_record`，新增 `TestNoOrphanSealedTables` 守门；重跑后该错误归零 |
| 迁移后打开耗时 | v2 库 0.14s、空库 0.10s（兼容触发器删除后回到基线） |
| `cmd/...` | 全部通过（含真实 Store 的迁移 CLI 测试） |
| 前端 | 无改动（API 未变） |

## 6. 与方案的偏差（明确说明）

1. **保留了 `payload` 扩展列**。方案要求纯列化；本轮为保证 301 处调用点一次性切换正确，`payload` 作为规范记录保留，热字段由触发器投影并建索引。后续可按表逐步删除 `payload`。
2. **`records` 视图已移除**：过渡期曾用只读视图 + INSTEAD OF 写触发器支撑旧夹具，收尾时把 54 处测试 SQL 全部改成功能表访问后删除，未留下任何 `records` 对象。
3. **密封值集中到 `plugin_record`**：`subscription_quality_*`、`subscription_history`、`source_*_settings`、`site_message_collection` 等以 kind 为 AAD 的密文，搬表需要重封，本轮保持原命名空间。
4. **`site` / `user` 为镜像写入**：设置保存与管理员创建时同步 MP 表；读路径仍走加密 config，切换读是下一步。
5. **`mediaserveritem` / `workflow` / `agentchat` / `agenttask` / `passkey`** 已按 MP 建表但无写入方（对应功能未实现），结构与索引齐全。

## 7. 迁移操作

停机迁移步骤、校验 SQL 与回滚见 `docs/records-split-mp-migration-runbook.md`。

## 8. 后续工作（按优先级）

1. ~~测试改 typed 表并删除兼容视图~~（已完成）。
2. `site` 读路径切换：`Settings().Sites` 改为读 `site` 表，`config` 只保留非站点字段。
3. `user` 读路径切换（登录鉴权走 `user`）。
4. 逐表删除 `payload`，把剩余 JSON 字段列化。
5. 给 `cache_entry`、`download_preview`、`removal_preview` 加 TTL 清理任务。

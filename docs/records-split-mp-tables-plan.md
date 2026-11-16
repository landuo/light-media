# records 单表拆分方案（按 MoviePilot 每功能单表 1:1）

状态：已评审并实施（2026-11-16），实施结果见 `docs/records-split-mp-tables-delivery.md`
基线：`landuo/light-media` 当前工作树 `feat-records-mp-mp-records-mp`
参照实现：`/Users/accidia/projects/MoviePilot`（只读，`config/user.db` 实库 DDL 与 `app/db/models/*` 双向核对）

---

## 1. 结论摘要

1. 现 `records(kind,id,payload,updated_at)` 一张表承载 **78 个业务 kind**（代码扫描口径），其中 **70 个 kind 出现在手写 SQL** 中；非测试代码共 **147 个文件**、**301 处**直接读写 `records`（`FROM records` 235、`JOIN records` 19、`INTO records` 39、`UPDATE records` 8）。
2. 15 个部分表达式索引只覆盖少数热路径，其余全部退化为「按 kind 扫全量 + `json_extract` 过滤 + 临时 B 树排序」。缓存、预览、收据、逐文件行、插件加密收据和业务主体同表，行数与 kind 数量线性叠加，`ANALYZE` 统计被跨域噪音污染。
3. MoviePilot 的做法是**一功能一张强类型表**（20 张，见 §3），每张表有主键、`NOT NULL`、类型约束和覆盖查询的复合索引；`TransferHistory` 已完成同样的对齐（`docs/history-mp-single-table-refactor-plan.md`）。
4. 本方案：MP 已有的功能域**照搬 MP 表结构**（15 张：`user/subscribe/subscribehistory/downloadhistory/downloadfiles/downloadfailure/transferhistory/message/site/siteicon/sitestatistic/siteuserdata/systemconfig/userconfig/plugindata`）；MP 没有模型化的内部流程（导入计划、回执、整理队列、目录监控等）**按同一原则拆成强类型自有表**（43 张）；`records` 表在迁移完成后冻结，最终删除。
5. 对外 API、前端路由、插件命名空间与加密 AAD 全部保持不变；改动集中在存储层与 301 处 SQL 调用点。

---

## 2. 现状问题（量化）

| 维度 | 现状 | 影响 |
| --- | --- | --- |
| 表结构 | 4 列，payload 为 JSON BLOB | 无类型约束、无外键、无 `NOT NULL` 校验 |
| kind 数量 | 78 个（缓存、预览、收据、业务主体混用） | 单表行数 = 全部域之和 |
| 手写 SQL | 301 处（非测试） | 每次查询都要 `json_extract` 解码 |
| 索引 | 15 个部分表达式索引 | 未覆盖 `hash`、`state`、`date`、`media_key+date`、`downloader_id` 等高频条件 |
| 排序 | 大量 `ORDER BY updated_at DESC` | 命中不了索引即全 kind 排序后再 `LIMIT` |
| 写入 | 整行 payload 重写 | 一行内容变化重写整块 JSON，WAL 放大 |
| 保留策略 | 预览/缓存/回执无 TTL 清理 | 无限增长，历史页、搜索页逐渐变慢 |
| 迁移 | schema 版本 1，无升级通道 | 拆分必须自带版本 2 迁移 |

典型坏查询（现状）：

```sql
-- 未命中索引，扫全部 intent 行并解码 JSON
SELECT payload FROM records
 WHERE kind='intent' AND json_extract(payload,'$.state') NOT IN ('imported','cancelled');
-- 跨 kind 聚合，只能全表扫
SELECT json_extract(payload,'$.directory_id'),COUNT(*) FROM records
 WHERE (kind='intent' AND ...) OR (kind='directory_file' AND ...);
```

---

## 3. MoviePilot 表功能清单（20 张，实库 DDL 核对）

| # | 表 | 功能 | 主键 | 关键列 | 主要索引 | 写入时机 | 读取场景 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | `user` | 账号、权限、个性化设置 | `id` 自增 | `name` `hashed_password` `is_active` `is_superuser` `permissions`(JSON) `settings`(JSON) | `name` | 注册/改密/改权限 | 登录、鉴权、用户列表 |
| 2 | `passkey` | WebAuthn 通行密钥 | `id` | `user_id` `credential_id` `public_key` `sign_count` `transports` | `credential_id`(UQ) `user_id` | 注册/使用密钥 | 无密码登录 |
| 3 | `userconfig` | 用户级键值（如 UI 偏好） | `id` | `username` `key` `value`(JSON)，`UNIQUE(username,key)` | 唯一约束 | 用户改设置 | 设置页、个性化 |
| 4 | `systemconfig` | 全局键值配置 | `id` | `key` `value`(JSON) | `key` | 保存系统设置/计数 | 启动加载、增量计数 |
| 5 | `plugindata` | 插件键值数据 | `id` | `plugin_id` `key` `value`(JSON) | `(plugin_id,key)` | 插件 `save_data` | 插件 `get_data` |
| 6 | `site` | 站点定义与账号凭据 | `id` 自增 | `name` `domain` `url` `pri` `rss` `cookie` `ua` `apikey` `token` `proxy` `filter` `render` `public` `note` `limit_*` `timeout` `is_active` `downloader` | `domain` | 站点增删改、cookie 刷新 | 搜索、RSS、站点列表 |
| 7 | `siteicon` | 站点图标缓存 | `id` | `name` `domain` `url` `base64` | `domain` | 拉取图标成功后 | 站点卡片渲染 |
| 8 | `sitestatistic` | 站点连通/请求统计 | `id` | `domain` `success` `fail` `seconds` `lst_state` `lst_mod_date` `note` | `domain` | 每次请求结果累计 | 站点健康度、优先级排序 |
| 9 | `siteuserdata` | 站点账号数据（做种/魔力/消息） | `id` | `domain` `username` `user_level` `bonus` `upload` `download` `ratio` `seeding` `leeching` `seeding_info` `message_unread*` `err_msg` `updated_day/time` | `(domain,updated_day,updated_time)` `(updated_day,id)` | 定时刷新站点数据 | 站点数据页、消息提醒 |
| 10 | `message` | 通知/消息中心 | `id` | `channel` `source` `mtype` `title` `text` `image` `link` `userid` `reg_time` `action` `note` | `(reg_time,id)` | 发通知时 | 消息列表、去重(`source`) |
| 11 | `subscribe` | 订阅主体 | `id` 自增 | `name` `year` `type` `keyword` `tmdbid` `doubanid` `bangumiid` `anilistid` `media_source` `media_id` `season` `poster` `backdrop` `filter` `include` `exclude` `quality` `resolution` `effect` `total_episode` `start_episode` `lack_episode` `note` `state` `last_update` `date` `username` `sites`(JSON) `downloader` `best_version*` `current_priority` `episode_priority`(JSON) `save_path` `search_imdbid` `manual_total_episode` `custom_words` `media_category` `filter_groups`(JSON) `episode_group` | `name` `tmdbid` `doubanid` `bangumiid` `anilistid` `mediaid` `media_source` `media_id` `(media_source,media_id)` `state` `username` `(type,date)` | 添加/修改订阅、刷新进度 | 订阅列表、搜索匹配、日历 |
| 12 | `subscribehistory` | 订阅历史（归档，无 `state/lack_episode`） | `id` | 同 `subscribe` 去掉 `state/lack_episode/last_update` | 与 `subscribe` 同族 | 删除/完成订阅时归档 | 历史页、重复订阅校验 |
| 13 | `downloadhistory` | 下载历史（每任务一行） | `id` | `path` `type` `title` `year` `tmdbid*` `media_source` `media_id` `seasons` `episodes` `image` `poster` `downloader` `download_hash` `torrent_*` `userid` `username` `channel` `date` `note` `media_category` `episode_group` `custom_words` | `path` `tmdbid` `bangumiid` `anilistid` `media_source` `media_id` `(media_source,media_id)` `(download_hash,date)` `(date,id)` | 添加下载成功时 | 下载列表、按 hash/path 反查 |
| 14 | `downloadfiles` | 下载任务文件明细 | `id` | `downloader` `download_hash` `fullpath` `savepath` `filepath` `torrentname` `state` | `(download_hash,state)` `savepath` `(fullpath,id)` | 任务文件列表入库 | 整理时定位文件、状态翻转 |
| 15 | `downloadfailure` | 资源失败冷却 | `id` | `fingerprint`(UQ) `type` `title` `tmdbid` `doubanid` `media_source` `media_id` `seasons` `episodes` `site` `site_name` `torrent_id` `torrent_name` `torrent_size` `downloader` `source` `error_message` `retry_count` `first/last_failed_at` `next_retry_at` | `fingerprint`(UQ) `next_retry_at` `(type,tmdbid,doubanid,site)` `(type,media_source,media_id,site)` | 下载失败时 upsert | 搜索过滤冷却资源 |
| 16 | `transferhistory` | 整理历史（每源一行，完成即替换） | `id` | `src` `src_storage` `src_fileitem` `dest` `dest_storage` `dest_fileitem` `mode` `type` `category` `title` `year` 四类外部 ID `media_source` `media_id` `seasons` `episodes` `image` `downloader` `download_hash` `status` `errmsg` `date` `files`(JSON) `episode_group` | `src` `title` `tmdbid` `download_hash` `(status,date)` `(date,id)` `(media_source,media_id)` | 整理完成/失败 | 历史列表、重整、删除 |
| 17 | `mediaserveritem` | 媒体服务器库存缓存 | `id` | `server` `library` `item_id` `item_type` `title` `original_title` `year` `tmdbid` `imdbid` `tvdbid` `path` `seasoninfo`(JSON) `note` `lst_mod_date` | `(server,item_id)`(UQ) `item_id` `title` `tmdbid+item_type` `imdbid` `tvdbid` | 扫描媒体库后 upsert | 库存判断、缺集检测 |
| 18 | `workflow` | 定时/事件工作流 | `id` | `name` `description` `timer` `trigger_type` `event_type` `event_conditions` `state` `current_action` `result` `run_count` `actions` `flows` `context` `execution_config` `execution_state` `add_time` `last_time` | `name` `state` `(trigger_type,state)` | 工作流执行/推进 | 工作流列表、到点触发 |
| 19 | `agentchat` | Agent 会话 | `id` | `session_id` `user_id` `channel` `source` `title` `preview` `agent_messages`(JSON) `display_messages`(JSON) `message_count` `created_at` `updated_at` | `(session_id,user_id)` `(user_id,updated_at,id)` `(channel,updated_at,id)` | 每轮对话 | 会话列表/恢复 |
| 20 | `agenttask` | Agent 定时任务 | `id` | `name` `content` `trigger_type` `cron_expression` `run_at` `enabled` `user_id` `session_id` `last_status` `last_result` `run_count` | `enabled` `(user_id,created_at,id)` | 创建/触发 | 任务调度 |

### 3.1 采纳决策

| MP 表 | 本方案 | 说明 |
| --- | --- | --- |
| `user` | 采纳（替换 `admin`） | 现有 `admin(id=1,username,password_hash)` → `user`；保留单用户约束，`is_superuser=1` |
| `userconfig` | 采纳 | 承接 `ui_preferences`（推荐/探索偏好） |
| `systemconfig` | 采纳 | 承接 `config/main` 中非实体字段 + `source_*_settings` + `task_data_schema_version` + `control` |
| `plugindata` | 采纳 | 承接 `plugin_host_*` 三类宿主机缓存/计数；插件加密数据仍留 `plugin_values`（已独立，不动） |
| `site` | 采纳 | 从 `config.main.sites` JSON 拆出，MP 列 + 扩展列（`enabled`、`options` JSON、`sealed_credentials`） |
| `siteicon` | 采纳 | 承接 `site_icon`（`base64` 列继续存 sealed 值） |
| `sitestatistic` | 采纳 | 承接 `site_stats_latest` |
| `siteuserdata` | 采纳（改名归位） | 与现有 `source_site_account_rows` 合并为 MP 结构，密文列保留 |
| `message` | 采纳 | 承接 `notification` + `message`，新增 `read` 扩展列 |
| `subscribe` | 采纳 | 承接 `subscription`，MP 列 + `uid` 扩展列 |
| `subscribehistory` | 采纳 | 新增归档语义（完成/取消/删除时写入），字段与 MP 一致 |
| `downloadhistory` | 采纳 | 承接 `intent` + `native_download` + `download_added`（三者在 MP 中就是一条下载历史） |
| `downloadfiles` | 采纳 | 承接 `download_review` 的文件行与下载任务文件明细 |
| `downloadfailure` | 采纳 | 新增资源失败冷却（当前仅 `site_cooldown` 与用户 exclusion，无资源级冷却） |
| `transferhistory` | 已完成 | 见 `docs/history-mp-single-table-refactor-plan.md` |
| `mediaserveritem` | 不建 | 当前无媒体服务器全库库存扫描功能；`library_asset` 承担本应用资产索引 |
| `workflow` | 不建 | 现有 `workflow` kind 只是媒体维度去重锁，不是工作流引擎；改名 `workflow_lock` |
| `agentchat` / `agenttask` / `passkey` | 不建 | 对应功能未实现 |

---

## 4. 目标表清单

### 4.1 MP 对齐表（15 张，列/索引按 MP 1:1，扩展列单列标注）

```
user(id INTEGER PK, name, email, hashed_password, is_active, is_superuser, avatar,
     is_otp, otp_secret, permissions JSON, settings JSON)                    -- MP 1:1
userconfig(id INTEGER PK, username, key, value JSON, UNIQUE(username,key))   -- MP 1:1
systemconfig(id INTEGER PK, key, value JSON, INDEX(key))                      -- MP 1:1
plugindata(id INTEGER PK, plugin_id, key, value JSON, INDEX(plugin_id,key))   -- MP 1:1
site(id INTEGER PK, name, domain, url, pri, rss, cookie, ua, apikey, token,
     proxy, filter, render, public, note JSON, limit_interval, limit_count,
     limit_seconds, timeout, is_active, lst_mod_date, downloader,
     + enabled INTEGER, options JSON, sealed_credentials BLOB)               -- MP + 扩展
siteicon(id INTEGER PK, name, domain, url, base64, INDEX(domain))             -- MP 1:1
sitestatistic(id INTEGER PK, domain, success, fail, seconds, lst_state,
     lst_mod_date, note JSON, INDEX(domain))                                  -- MP 1:1
siteuserdata(id INTEGER PK, domain, name, username, userid, user_level, join_at,
     bonus, upload, download, ratio, seeding, leeching, seeding_size,
     leeching_size, seeding_info JSON, message_unread,
     message_unread_contents JSON, err_msg, updated_day, updated_time,
     + sealed BLOB)                                                           -- MP + 密文列
message(id INTEGER PK, channel, source, mtype, title, text, image, link, userid,
     reg_time, action, note JSON, + read INTEGER, + job_id, + workflow_id, + sealed BLOB)
subscribe(id INTEGER PK, ...MP 全列..., + uid TEXT UNIQUE, + revision INTEGER,
     + intent_defaults JSON, + target_scope, + created_at TEXT)
subscribehistory(id INTEGER PK, ...MP 全列...)                                -- MP 1:1
downloadhistory(id INTEGER PK, ...MP 全列..., + state, + progress, + error,
     + workflow_id, + downloader_snapshot, + material_ref, + import_plan_id,
     + uid TEXT UNIQUE, + updated_at)
downloadfiles(id INTEGER PK, downloader, download_hash, fullpath, savepath,
     filepath, torrentname, state, + history_id, + snapshot JSON, + asset_id)
downloadfailure(id INTEGER PK, ...MP 全列...)                                 -- MP 1:1
transferhistory(...)                                                          -- 已存在
```

### 4.2 自有强类型表（43 张，替代 records 中 MP 未模型化的流程）

| 表 | 替代 kind | 主键 | 关键索引 |
| --- | --- | --- | --- |
| `site_state` | `site_connection` `site_cooldown` | `site_id` | `(status,checked_at)` |
| `site_statistic_event` | `site_stats_history` `site_access_statistics` | `id` 自增 | `(site_id,ts)` `(ts)` |
| `site_message_sync` | `site_message_collection` `site_message_history_execution` | `id` | `(site_id,state)` |
| `site_system_error` | `site_system_error` | `id` | `(site_id,ts)` |
| `scheduler_state` | `site_refresh_clock` | `key` | — |
| `subscription_event` | `subscription_added` | `id` | `(subscription_id,ts)` `(event_id)` |
| `subscription_acquisition` | `subscription_acquisition` | `id` | `(subscription_id,state)` `(media_key)` |
| `subscription_receipt` | `subscription_quality_accepted` `_download` `_import` `_cleanup` | `(subscription_id,stage,key)` | `(stage,state)` `(plan_id)` |
| `subscription_origin` | `source_subscription_origin` `source_subscription_environment` | `subscription_id` | — |
| `download_preview` | `download_preview` `download_review` | `id` | `(expires_at)` `(hash)` `(state)` |
| `download_selection` | `download_season_scope` `download_episode_selection` | `(scope_type,key)` | `(intent_id)` |
| `material` | `material_info` | `ref` | `(expires_at)` `(hash)` |
| `material_blob` | `material_blob` | `ref` | — |
| `media_exclusion` | `exclusion` | `(media_key,season,episode)` | `(media_key)` |
| `import_plan` | `import_plan` | `id` | `(state)` `(intent_id)` `(media_key)` `(created_at,id)` |
| `import_plan_file` | `import_plan.files[]` | `(plan_id,file_id)` | `(plan_id,status)` `(source)` |
| `import_receipt` | `import_receipt` | `id` | `(target)` `(state)` `(plan_id)` |
| `import_receipt_member` | `import_receipt.directory_members` | `(receipt_id,path)` | `(asset_id)` |
| `import_meta` | `import_meta` | `id` | `(committed_at)` |
| `managed_file` | `managed_file` | `(asset_id,path)` | `(asset_id)` `(path)` `(sha256)` |
| `library_asset` | `asset` `asset_identity` `asset_presence` | `id` | `(path)` `(root_id,state)` `(media_key)` `(fnos_guid)` |
| `library_candidate` | `library_candidate` `library_candidate_current` | `id` | `(root_id,state)` `(path)` |
| `library_scan` | `library_scan` | `id` | `(root_id,created_at)` |
| `removal_preview` | `removal_preview` | `id` | `(asset_id,state)` `(expires_at)` |
| `organize_queue_member` | `organize_queue_member` | `(group_id,job_id,file_id)` | `(group_id)` `(job_id)` `(meta)` `(media)` `(source)` |
| `organize_listing` | `organize_file_listing` `organize_file_cancel` | `job_id` | `(state)` |
| `directory_file` | `directory_file` `directory_file_state` | `(directory_id,path)` | `(directory_id,last_seen)` `(state)` |
| `directory_import` | `directory_import` | `(directory_id,path)` | `(state)` |
| `directory_overwrite` | `directory_overwrite` | `id` | `(directory_id)` `(state)` |
| `file_operation` | `file_operation` | `id` | `(created_at)` `(state)` |
| `history_receipt` | `history_source_removal` `history_target_removal` `history_parent_cleanup` | `(scope,history_id)` | `(created_at)` |
| `history_hidden` | `transfer_history_hidden` | `history_id` | `(created_at)` |
| `history_cleanup` | `reorganize_cleanup` | `id` | `(state)` |
| `download_removal_receipt` | `core_download_removal` `_root` `_chain` | `id` | `(event_id)` |
| `plugin_original_callback` | `original_transfer_callback` | `id` | `(state)` |
| `plugin_original_failure` | `original_transfer_task_failures` | `(source,hash)` | `(ts)` |
| `plugin_original_batch` | `original_scrape_batch` | `id` | `(state)` |
| `plugin_original_directory` | `original_metadata_directory` | `id` | `(state)` |
| `plugin_host_state` | `plugin_host_tmdb_cache` `plugin_host_media_recognize_count` `plugin_host_setting_presence` | `(instance_id,key)` | `(expires_at)` |
| `settings_lifecycle` | `core_settings_lifecycle` | `key` | — |
| `audit_log` | `audit` `decision` | `id` | `(ts)` `(workflow_id)` |
| `cache_entry` | `cache` | `key` | `(expires_at)` `(namespace)` |
| `workflow_lock` | `workflow` | `media_key` | `(created_at)` |
| `legacy_record_archive` | 未识别 kind 兜底 | `(kind,id)` | — |

### 4.3 直接并入既有表 / 删除的 kind

| kind | 处置 |
| --- | --- |
| `config` | 拆为 `systemconfig` 行 + `site` + 下载器/根目录/目录规则（明文实体表） |
| `task_data_schema_version` | `systemconfig('task_data_schema_version')` |
| `control`（全局暂停态） | `systemconfig('runtime_control')` |
| `source_file_extension_settings` `source_statistics_settings` `source_settings_migration` `source_subscription_environment` | `systemconfig` 对应 key |
| `history_download_file_event` | `downloadfiles.state` + `audit_log` |
| `resource`（资源→媒体提示） | `cache_entry(namespace='resource')` |

---

## 5. kind → 目标表 全量映射（1:1 还原表）

| kind | 现值形态 | 目标表 | 主键/唯一 | 迁移规则 |
| --- | --- | --- | --- | --- |
| `subscription` | `Subscription` JSON | `subscribe` | `uid=id` | 字段映射；`Policy/Schedule/Mappings` 进 `note` JSON 同名键；`State`→`state`；`Revision`→`revision` |
| `subscription_added` | sealed 事件事实 | `subscription_event` | `event_id` | 解密取 `subscription_id/media_id/at`；密文留 `sealed` 列 |
| `subscription_acquisition` | sealed 收据 | `subscription_acquisition` | `id` | 密文原样搬运，AAD 命名空间保持 `subscription_acquisition` |
| `subscription_quality_accepted` `_download` `_import` `_cleanup` | sealed 收据 | `subscription_receipt` | `(subscription_id,stage,key)` | `stage` 取原 kind 后缀；密文原样 |
| `source_subscription_origin` | sealed 来源 | `subscription_origin` | `subscription_id` | 密文原样 |
| `source_subscription_environment` | sealed 环境 | `subscription_origin.environment` | `subscription_id` | 合并为列 |
| `intent` | `Intent` JSON | `downloadhistory` | `uid=id` | `Hash`→`download_hash`；`State/Progress/Error`→扩展列；`Media` 拆 ID 列；`Request`→`note.request` |
| `native_download` | `NativeDownload` JSON | `downloadhistory` | `uid=id` | 同上，`channel='native'` |
| `download_added` | sealed 事实 | `downloadhistory` + `downloadfiles` | `download_hash` | 命中则补 `accepted_id`；未命中建行 `state='added'` |
| `download_review` | `downloadReviewRecord` JSON | `download_preview` | `id` | `state='review'`；文件行进 `downloadfiles` |
| `download_preview` | `DownloadPreview` JSON | `download_preview` | `id` | `state='preview'`；`ExpiresAt` 保留 |
| `download_season_scope` | sealed 范围 | `download_selection` | `id` | `scope_type='season'` |
| `download_episode_selection` | sealed 选择 | `download_selection` | `id` | `scope_type='episode'` |
| `resource` | 提示 JSON | `cache_entry` | `key` | `namespace='resource'` |
| `material_info` `material_blob` | 元数据 + 密文 | `material` `material_blob` | `ref` | 两表同键；`expires_at` 建索引 |
| `exclusion` | 规则 JSON | `media_exclusion` | `(media_key,season,episode)` | 保留 `blocked/reason/at` |
| `asset` `asset_identity` `asset_presence` | `Asset` + 证明 | `library_asset` | `id` | 合并三份 JSON 为列 + `proof` JSON |
| `managed_file` | `managedFile` JSON | `managed_file` | `(asset_id,path)` | 快照列化；`softlink/shared` 进 `proof` JSON |
| `import_plan` | `ImportPlan` JSON | `import_plan` + `import_plan_file` | `id` | `files[]` 拆子表；`warnings` 进 `note` |
| `import_receipt` | `importReceipt` JSON | `import_receipt` + `import_receipt_member` | `id` | `directory_members` 拆子表；其余专列 |
| `import_meta` | 元数据 JSON | `import_meta` | `id` | 列化 |
| `library_candidate` `library_candidate_current` | 候选 + 指针 | `library_candidate` | `id` | `current` 用 `current=1` 标记 |
| `library_scan` | 扫描态 | `library_scan` | `id` | 列化 |
| `removal_preview` | 删除预览 | `removal_preview` | `id` | `state/ext/expires_at` 列化 |
| `organize_queue_member` | 队列成员 | `organize_queue_member` | `(group_id,job_id,file_id)` | 原 5 个索引改为普通复合索引 |
| `organize_file_listing` | 提交标记 | `organize_listing` | `job_id` | 列化 |
| `organize_file_cancel` | 取消标记 | `organize_listing.cancelled_at` | `job_id` | 合并 |
| `directory_file` `directory_file_state` | 观察文件 + 基线 | `directory_file` | `(directory_id,path)` | 基线进列 |
| `directory_import` | 导入守卫 | `directory_import` | `(directory_id,path)` | 列化 |
| `directory_status` | 连接状态 | `site_state` / `directory_import.status` | 键 | 按主体归位 |
| `directory_overwrite` | 策略密文 | `directory_overwrite` | `id` | 密文原样 |
| `file_operation` | 文件操作计划 | `file_operation` | `id` | 列化 |
| `removal_preview` → `history_receipt` | 见上 | | | |
| `history_source_removal` `history_target_removal` `history_parent_cleanup` | 删除收据 | `history_receipt` | `(scope,history_id)` | `scope` 区分来源 |
| `transfer_history_hidden` | 隐藏标记 | `history_hidden` | `history_id` | 列化 |
| `reorganize_cleanup` | 重整清理 | `history_cleanup` | `id` | 列化 |
| `history_download_file_event` | 去重事实 | `downloadfiles` + `audit_log` | — | 幂等键存 `audit_log.event_id` |
| `core_download_removal` `_root` `_chain` | 删种收据 | `download_removal_receipt` | `id` | 三层收据合表，`level` 区分 |
| `original_transfer_callback` | 回调状态 | `plugin_original_callback` | `id` | 列化 |
| `original_transfer_task_failures` | 失败计数 | `plugin_original_failure` | `(source,hash)` | 列化 |
| `original_scrape_batch` | 刮削批次 | `plugin_original_batch` | `id` | 列化 |
| `original_metadata_directory` | 元数据目录 | `plugin_original_directory` | `id` | 列化 |
| `notification` `message` | 通知记录 | `message` | `id` | 投影列 + `sealed` 内容 |
| `audit` `decision` | 审计/决策 | `audit_log` | `id` | 列化 |
| `site_connection` `site_cooldown` | 连接/冷却 | `site_state` | `site_id` | 合并 |
| `site_icon` | sealed 图标 | `siteicon` | `domain` | `base64` 存密文 |
| `site_stats_latest` | 统计快照 | `sitestatistic` | `domain` | 列化 |
| `site_stats_history` `site_access_statistics` | 明细 | `site_statistic_event` | `id` | 列化 |
| `site_message_collection` `site_message_history_execution` | 站内信同步 | `site_message_sync` | `id` | 密文原样 |
| `site_system_error` | 系统错误 | `site_system_error` | `id` | 列化 |
| `site_refresh_clock` | 刷新时钟 | `scheduler_state` | `key` | 列化 |
| `plugin_host_tmdb_cache` | 宿主机缓存 | `plugin_host_state` | `(instance_id,key)` | `expires_at` 列 |
| `plugin_host_media_recognize_count` `plugin_host_setting_presence` | 计数/存在标记 | `plugin_host_state` | `(instance_id,key)` | 列化 |
| `core_settings_lifecycle` | 设置生命周期 | `settings_lifecycle` | `key` | 列化 |
| `cache` | KV + `at` | `cache_entry` | `key` | `namespace` 取 key 前缀 |
| `ui_preferences` | 偏好 | `userconfig` | `(username,key)` | `username=admin` |
| `workflow` | 去重锁 | `workflow_lock` | `media_key` | 列化 |
| `control` | 暂停态 | `systemconfig('runtime_control')` | `key` | JSON 值 |
| `task_data_schema_version` | 版本 | `systemconfig` | `key` | 值 |
| `config` | sealed 全局配置 | `systemconfig` + `site` + 下载器/根/目录表 | — | 解密后按实体拆分，敏感字段重新封装 |
| 其他（未识别） | — | `legacy_record_archive` | `(kind,id)` | 原样保留，导出报告列出 |

---

## 6. 迁移方案（schema version 1 → 2）

### 6.1 版本与建表

- `migrations` 增加 `version=2`；`storeOpen` 检测到 1 时执行 `migrateRecordsV2`。
- 所有 `CREATE TABLE IF NOT EXISTS` 与索引一次性执行，与现有 `nativeSiteIdentitySchema` 等常量同风格。
- 迁移在**单写事务**内完成；`busy_timeout=5000`，迁移期间拒绝新写请求（启动阶段本就无并发）。

### 6.2 五步流程

1. **预检**：`VACUUM INTO` 生成 `light-media-pre-v2-<ts>.db` 备份；统计每种 kind 行数写入迁移报告。
2. **建表**：建 58 张目标表 + 1 张兜底表 + 索引。
3. **回填**：按 kind 分批 `SELECT id,payload,updated_at FROM records WHERE kind=?`，逐行解码 → 写目标表；密文列不解密，原样搬运，AAD 命名空间保持原 kind 字符串。
4. **校验**：每种 kind 迁移前后行数比对；主表抽样字段比对（`subscription.id`、`intent.hash`、`managed_file.path`）；失败即整体回滚事务。
5. **冻结**：`ALTER TABLE records RENAME TO records_frozen_v1`（保留一版用于回滚与抽样），删除其 15 个索引；新代码只读写新表。

### 6.3 切换与回滚

- 切换点即发布：旧二进制 + DB 备份可完整回滚；新版本启动后 `records_frozen_v1` 只读。
- 下一版本删除 `records_frozen_v1` 与 `legacy_record_archive`（导出 CSV 后）。
- 迁移可重入：每张目标表按主键 `ON CONFLICT DO UPDATE`，重复执行不产生重复行。

### 6.4 CLI 支持

- `light-media migrate records --check`：只统计、比对、输出 JSON 报告，不写库。
- `light-media migrate records --apply`：停机执行 + 备份 + 校验。
- 报告字段：`kind`、`source_rows`、`target_table`、`target_rows`、`sealed_rows`、`archived_rows`、`errors`。

---

## 7. 代码改造清单

| 范围 | 现状 | 改造 |
| --- | --- | --- |
| `internal/app/store.go` | `Put/Get/List/Delete` 泛型 | 删除泛型 API；新增 `store_<domain>.go` 强类型仓储（每表一组 `Insert/Update/Get/List/Delete`） |
| 手写 SQL 301 处 | `json_extract(payload,...)` | 改为列引用；每处保留原查询语义与排序键 |
| `internal/app/*_test.go` | 直接 `Store.Put("kind",...)` 造数 | 改为对应仓储的 fixture 构造器；新增迁移往返测试 |
| `internal/app/http*.go` | 输出结构体 | 不改；仅仓储返回同一结构体 |
| `web/` | 消费 API | 不改 |
| 插件 API | `plugin_values` 命名空间 | 不改；仅宿主机缓存改 `plugin_host_state` |
| `internal/sqlitebaseline` | 依赖 schema 版本 | 增加 v2 基线快照测试 |

改造顺序：先建表与双写（不必，见 §6.3 冻结策略）→ 直接按域逐个切换读 → 每个域切换后跑该域回归 → 全部切换完再冻结 `records`。

---

## 8. 性能收益预期

| 场景 | 现状 | 拆分后 |
| --- | --- | --- |
| 订阅列表分页 | 扫全部 `subscription` 行 + JSON 解码 + 排序 | `subscribe(state,date,id)` 索引 seek + 行读取 |
| 下载管理按 hash 查 | `json_extract` 全 kind 扫 | `downloadfiles(download_hash,state)` |
| 整理队列轮询 | `organize_queue_member` 表达式索引 + 交叉 jobs | 复合主键 seek |
| 目录监控差异计算 | `directory_file` 全量解码 | `(directory_id,path)` 主键 seek |
| 通知未读数 | 部分索引 | `message(read)` 计数索引 |
| 站点统计 | `site_stats_latest` 单行 JSON | `sitestatistic(domain)` 直读 |
| 缓存清理 | 无 TTL，永不清 | `cache_entry(expires_at)` 定时删除 |

预期：主要列表接口从 O(kind 行数 × JSON 解码) 降到 O(返回行数)；`records` 单表消失后 `ANALYZE` 统计准确，查询计划稳定。

---

## 9. 风险

| 风险 | 处理 |
| --- | --- |
| 密文 AAD 绑定 kind/key | 迁移不解密、不重封；AAD 命名空间以常量或 `aad_namespace` 列保留原 kind/method 字符串 |
| 加密字段跨表搬运损坏 | 迁移只 `SELECT payload` 原样拷到新表 BLOB 列；校验用解密抽检（`site_icon`、`subscription_acquisition`） |
| 一次性迁移数据量大导致启动慢 | 分批 500 行/事务外批次；CLI 预检给出耗时估算 |
| 旧二进制回滚后看不到新数据 | 回滚依赖 DB 备份；`records_frozen_v1` 保证迁移后可追溯 |
| 301 处 SQL 改写遗漏 | 按 kind 清单逐项打勾；每域切换后 `EXPLAIN QUERY PLAN` 断言无 `SCAN records` |
| MP 列与现有扩展字段冲突 | 扩展列统一后缀/前缀命名，MP 列名与类型不改 |

---

## 10. 分期与验收

| 阶段 | 内容 | 验收 |
| --- | --- | --- |
| P0 | 建表 schema v2 + 迁移器 + CLI 预检 + 备份 | 空库/样本库迁移前后行数一致；`--check` 输出全量报告 |
| P1 | 订阅/下载/通知/站点四个高频域切读 | 对应页面与接口回归通过；查询计划无 `SCAN records` |
| P2 | 整理/导入/目录/历史四个域切读 | 整理队列、手动整理、历史删除、四范围删除回归 |
| P3 | 插件宿主机与配置域切换；`config` 拆分 | 站点/下载器/根目录保存、插件缓存回归 |
| P4 | 冻结并删除 `records`；补基线测试 | `sqlite_master` 无 `records`；`go test ./...` 全绿 |
| P5 | NAS 停机迁移演练 | 生产副本迁移 + 抽样核对 + 回滚演练 |

### 验收硬指标

1. `SELECT name FROM sqlite_master WHERE type='table' AND name IN ('records')` 返回空。
2. 每种 kind 迁移前后行数相等，`legacy_record_archive` 行数为 0（或全部为已说明的废弃 kind）。
3. 订阅列表、下载管理、整理历史、站点统计、通知中心、文件管理六个页面操作与迁移前一致。
4. 关键查询 `EXPLAIN QUERY PLAN` 使用目标索引。
5. 迁移往返测试：v1 fixture → v2 → 断言字段级等价。

---

## 11. 已确认的 5 个决策（2026-11-16 全部按推荐执行）

- **D1 下载域合并**：已合并为 `downloadhistory`，用 `record_kind` 区分 `intent` / `native_download` / `download_added`。
- **D2 subscribe 主键**：已采用「MP `INTEGER` 自增主键 + `uid` 唯一列」，API 继续返回 `uid`，前端与插件无改动。
- **D3 未实现功能的表**：按「1:1 复刻 MoviePilot」改为**全部建表**；`mediaserveritem`/`workflow`/`agentchat`/`agenttask`/`passkey` 结构、列与索引与 MP 一致，暂无写入方。
- **D4 `records` 删除时机**：已冻结为 `records_frozen_v1`，下一版本删除；迁移时表内为零行则直接 `DROP`。
- **D5 缓存/预览保留**：已建带 `expires_at` 的实表（`cache_entry`/`download_preview`/`removal_preview`），清理任务在后续版本接入。

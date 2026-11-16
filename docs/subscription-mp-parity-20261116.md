# 订阅模块与 MoviePilot 功能对照（2026-11-16）

对照基准：`MoviePilot/app/chain/subscribe.py`（4,399 行）、`app/schemas/subscribe.py`、`app/api/endpoints/subscribe.py`（24 个端点）、`MoviePilot-Frontend/src/components/dialog/Subscribe*.vue`
对照对象：`internal/app/subscription*.go` 等 93 个文件、`web/src/components/Subscriptions.vue`

图例：✅ 行为对齐 ｜ 🟡 部分实现/语义不同 ｜ ❌ 缺失 ｜ ➕ 光影额外 ｜ ❓ 代码存在但未验证

---

## 1. 结论摘要

- **核心链路已按 MP 语义重写**：识别 → 站点范围 → 优先级/过滤 → 洗版 → 下载 → 完成判定（插件 check/complete）→ 删除+通知，字段、事件名与 MP 对齐。
- **主要差距集中在"可见性与批处理"**：订阅历史（表+页面+API）、批量暂停/恢复/删除、季详情/文件详情弹窗、手动全量搜索。
- **MP 的社交化能力全缺**：分享、复用、关注、热门订阅、Seerr 请求订阅。
- **识别源收窄**：MP 支持 TMDB/豆瓣/Bangumi/AniList（插件链）；光影字段齐全，但实际识别只走 TMDB 与插件原生 provider。
- **光影超出 MP 的部分**：每订阅自定义周期/窗口/例外日期、单集排除与提前集、集日期、目录规则绑定、洗版批次、卡片统计面板。

---

## 2. 数据与状态

| 功能 | MP 行为 | 光影现状 | 状态 | 证据 |
| --- | --- | --- | --- | --- |
| 订阅字段集 | `Subscribe` 46 列（含 note/current_priority/episode_priority/lack_episode） | `Subscription` 覆盖全部关键字段，另有 `schedule/target_scope/excluded/episode_dates/mappings/revision` | ✅ | `internal/app/types.go` |
| 状态机 | `N` 新建、`R` 订阅中、`P` 待定、`S` 暂停；完成后删除 | `active/paused/completed/cancelled`；电影完成保留 `completed`，TV 完成删除 | 🟡 | `subscription_runtime.go:391,512` |
| 完成后的历史 | 写 `SubscribeHistory` 表，前端"订阅历史"弹窗可删 | 写密封 `subscription_history`（`plugin_record`），**无表、无页面、无 API** | ❌ | `subscription_quality_completion_run.go:285` |
| 多用户归属 | `username`，按用户过滤/权限 | 单管理员，`username` 恒空 | ❌（产品定位） | `subscription_runtime.go` |
| 续订/重置 | `GET /reset/{id}` 重置状态与集数 | `POST /subscriptions/{id}/reset` | ✅ | `http.go:447` |

## 3. 创建订阅

| 功能 | MP 行为 | 光影现状 | 状态 | 证据 |
| --- | --- | --- | --- | --- |
| Web 新建 | `POST /subscribe/`，识别后写入 | `POST /api/subscriptions`，走新增回执+统计链路 | ✅ | `subscription_add_receipt.go` |
| 重复校验 | `exists()` 按媒体+季判断 | `DUPLICATE_SUBSCRIPTION`，同媒体同季拦截 | ✅ | `subscription_runtime.go:429-458` |
| 数量上限 | 无固定上限 | 500 条上限 | ➕ | 同上 |
| 创建即搜索 | 60 秒宽限后进入搜索 | 不隐式搜索，排入下次计划检查 | 🟡 | `subscription_runtime.go:493` |
| 备用识别源 | 豆瓣/Bangumi/AniList/插件 | 网页新增时先解析为 TMDB，唯一命中即落 TMDB 身份；歧义/失败保留 provider 原生 | ✅ | `subscription_add_source.go`、`subscription_source_resolve.go` |
| 插件原生 provider | `media_source/media_id` | `providerNativeMedia` + `recognizeProviderSubscriptionMedia` | ✅ | `subscription_provider_media.go` |
| 消息渠道新建 | 微信/Telegram/Slack/QQ/Lark 等 | Lark 聊天 + Web | 🟡 | `plugin_original_chat_media_subscribe.go` |

## 4. 搜索与匹配

| 功能 | MP 行为 | 光影现状 | 状态 | 证据 |
| --- | --- | --- | --- | --- |
| 站点范围 | `subscribe.sites`，空=全部启用 | `site_ids`，空取 `IndexerSites` | ✅ | `search_parallel.go:40-53` |
| 站点限流 | `site.limit_*` | `site_usage` + `LimitInterval` | ✅ | `site_rate_limit.go` |
| 关键词 / IMDb 搜索 | `keyword` 与 `search_imdbid` 决定搜索域 | 同名字段 + `SourceSearchIMDb` | ✅ | `subscription_quality_source.go:54` |
| 自定义识别词 | `custom_words` 参与识别与过滤 | `subscription_words` 密封存储 + 识别接入 | ✅ | `subscription_words.go` |
| 过滤参数 | quality/resolution/effect/include/exclude/size/min_seeders | 同名 + `codec/languages/subtitles/free_only/min/max_bytes` | ✅➕ | `torrentfilter.Parameters`、`Policy` |
| 做种时间 | `min_seeders_time` | 无对应项 | ❌ | 全仓检索为 0 |
| 过滤规则组 | 订阅/洗版两套默认组 | `filter_groups` + 优先级规则引擎 | ✅ | `priorityrules` |
| 媒体类别 | `media_category` | 同名 + 分类策略 | ✅ | `media_category.go` |
| 剧集组 | `episode_group` | 同名 + TMDB 集组解析 | ✅ | `catalog_episode_groups.go` |
| 全季包优先 | `__download_best_version_with_full_pack_first` | `PreferFullPack` + `ConfirmedFullCoverage` | ✅ | `subscription_quality_snapshot.go:159` |
| 多季整包 | MP 按季独立订阅，无跨季整包 | 明确未接入：`多季整包的跨季下载及整理范围尚未接入` | ❌（自有范围） | `subscription_acquisition_batch.go:664` |
| 集排除/提前集/集日期 | MP 无 | `excluded/allow_early/episode_dates` | ➕ | `Subscription` |

## 5. 下载

| 功能 | MP 行为 | 光影现状 | 状态 | 证据 |
| --- | --- | --- | --- | --- |
| 下载器选择 | `downloader` 或默认 | `downloader_id` 或默认 | ✅ | `plugin_original_download.go:41` |
| 保存路径 | `save_path` | `download_path` + 目录规则 | ✅ | `Subscription` |
| 站点绑定下载器 | `site.downloader` | 后端支持；站点弹窗仍提示"暂不支持站点绑定" | 🟡 | `plugin_original_download.go:43`、`SiteManager.vue` |
| 下载失败冷却 | `DownloadFailure` 指纹+重试时间 | 拒绝记录 + 站点冷却，无独立指纹表 | 🟡 | `subscription_download_rejection.go` |
| 下载/整理通知 | 模板通知 | 模板 + 事件 + 通知中心 | ✅ | `plugin_standard_business_events.go` |

## 6. 洗版

| 功能 | MP 行为 | 光影现状 | 状态 | 证据 |
| --- | --- | --- | --- | --- |
| 洗版开关 | `best_version` / `best_version_full` | 同名字段 + 独立质量工作流 | ✅ | `subscription_quality_*` |
| 集优先级 | `episode_priority`（100 完成） | 同名 + `current_priority` | ✅ | `subscription_quality_state` |
| 总集数增减 | `__prepare_total_episode_change_fields` | `plugin_standard_subscription_decrease.go` | ✅ | 同名文件 |
| 洗版批次 | MP 无 | 批次下载/导入/清理收据 | ➕ | `subscription_quality_batch_workflow_test.go` |

## 7. 完成判定

| 功能 | MP 行为 | 光影现状 | 状态 | 证据 |
| --- | --- | --- | --- | --- |
| 插件完成检查 | `subscribe.completion.check` | 同名事件，含否决与来源 | ✅ | `subscription_quality_completion_run.go:228` |
| 完成事件 | `subscribe.complete` | 同名业务事件 | ✅ | `plugin_standard_business_events.go` |
| 完成删除+通知 | 删除订阅、写历史、发通知 | 删除 `subscribe` 行、密封历史、排队通知 | ✅ | `subscription_quality_completion_run.go:283` |
| 历史可见性 | 表 + 弹窗 + 删除接口 | 仅密封记录 | ❌ | 同上 |
| 缺集计算 | `compute_lack_episode` | `lack_episode` + 目标范围计算 | ✅ | `subscription_completion_ordinary.go` |
| 电影完成态 | 完成即删除 | 标记 `completed` 保留 | 🟡 | `subscription_runtime.go:512` |

## 8. 自动化

| 功能 | MP 行为 | 光影现状 | 状态 | 证据 |
| --- | --- | --- | --- | --- |
| 定时刷新 | 全局 cron 触发 `SubscribeChain.refresh` | 调度 ticker + 每订阅 `schedule` | ✅➕ | `jobs.go:530`、`Schedule` |
| 单条检查 | `GET /search/{id}` | `POST /subscriptions/{id}/check` | ✅ | `http.go:440` |
| 全部检查/搜索 | `GET /search`、`GET /check` | 无手动全量入口 | ❌ | `http.go:438-481` |
| 暂停/恢复 | `PUT /status/{id}` | 同名 | ✅ | `http.go:453` |
| RSS 驱动 | RSS 链参与订阅搜索 | RSS 磁力/聊天 RSS 流程 | 🟡 | `subscription_rss_magnet.go` |
| 站点消息读取 | 站内信/通知 | `site_message_*` 同步与已读 | ✅ | `site_message_collection.go` |

## 9. 界面

| 功能 | MP 行为 | 光影现状 | 状态 | 证据 |
| --- | --- | --- | --- | --- |
| 列表/筛选/排序/分页 | ✅ | ✅ | ✅ | `Subscriptions.vue` |
| 编辑弹窗字段 | 20 项（基础/进阶） | 覆盖 20 项并增加编码/音轨/字幕/体积/发布组/集范围 | ✅➕ | `Subscriptions.vue` |
| 季详情弹窗 | `SubscribeSeasonDialog`（逐集状态） | 季列表：集数/已入库/下载中/缺集，可进入文件详情 | ✅ | `SubscriptionSeasonDialog.vue` |
| 文件详情弹窗 | `SubscribeFilesDialog`（逐集文件） | 逐集标题/播出日期/状态/库文件路径与大小/下载记录 | ✅ | `SubscriptionFilesDialog.vue` |
| 订阅历史弹窗 | `SubscribeHistoryDialog` | 无 | ❌ | — |
| 批量暂停/恢复/删除 | 有 | 无 | ❌ | — |
| 热门订阅 | `GET /popular` + 页面 | 无 | ❌ | — |
| 分享/复用/关注 | `share/fork/follow` | 无 | ❌ | — |
| 日历 | ✅ | ✅ | ✅ | `subscription_calendar.go` |
| 卡片统计面板 | MP 无 | 整部/已入库/下载中/缺集 | ➕ | `Subscriptions.vue` |

## 10. API 与插件兼容

| 功能 | MP | 光影 | 状态 |
| --- | --- | --- | --- |
| 订阅端点 | 24 个（含 history/files/share/fork/follow/popular/seerr） | 7 个（列表/新建/编辑/状态/检查/重置/删除） | ❌ |
| Seerr 请求订阅 | `POST /seerr` | 无 | ❌ |
| 按 mediaid 查询/删除 | `GET/DELETE /media/{id}` | 无 | ❌ |
| 插件事件 | `subscribe.added/complete/check` | 同名事件 | ✅ |
| 插件搜索模块 | `async_batch_search_torrents` 等 | 模块组调用 + 无插件时原生并行 | ✅ |
| 订阅分享插件 | 有 | 无 | ❌ |

---

## 3. 差距分级与建议顺序

**已补齐（2026-11-16）**

- 备用识别源：网页新增订阅时，豆瓣/Bangumi/AniList 来源先经 `catalog.Explorer.Resolve` 解析，唯一命中即保存为 TMDB 订阅（保留原标题与年份），歧义或解析失败继续走插件原生 provider。生产实测：豆瓣 `36847425` → TMDB `1483525`。
- 季详情与文件详情：新增 `GET /api/subscriptions/{id}/seasons`、`GET /api/subscriptions/{id}/files?season=N` 与两个弹窗；逐集状态、库文件路径/大小、下载记录均来自 `asset_slots`/`reservations`/`library_asset`/`downloadhistory`。

**P0（影响日常使用）**
1. 订阅历史：写 `subscribehistory` 表 + `GET/DELETE /subscriptions/history` + 历史弹窗（当前密封记录无法查看）。
2. 批量暂停/恢复/删除（MP 列表页已有）。
3. 手动全量检查（`GET /search` 等价入口）。
4. 备用识别源：豆瓣/Bangumi/AniList 接入识别链（目前仅存字段）。

**P1（功能完整度）**
5. 季详情、文件详情两个弹窗。
6. 热门订阅、分享/复用/关注、Seerr。
7. 多季整包（跨季下载与整理范围）。
8. `min_seeders_time` 做种时间过滤。
9. 站点绑定下载器的前端开关（后端已支持）。

**P2（体验/一致性）**
10. 电影完成语义与 MP 对齐（删除+历史，而非保留 completed）。
11. 多用户与权限（当前单管理员）。
12. 订阅列表逐集状态视图（卡片汇总之外）。

## 4. 复核说明

- 标注 ❓ 的项未在本次逐行验证：站点绑定下载器前端、RSS 在订阅搜索中的实际占比。
- MP 的分享/复用/关注/热门/Seerr 依赖 MP 插件与外部服务；光影未实现，属于功能缺口而非回归。
-MD
gofmt -l $(git diff --name-only | grep '\.go$') 2>/dev/null | head -2; git add docs/subscription-mp-parity-20261116.md && git commit -q -m "Compare the subscription module with MoviePilot

Add a feature-by-feature parity list covering data model, creation,
search, filtering, best-version, downloads, completion, automation, UI
and API surface, with evidence paths and a gap ordering." && git log --oneline -1

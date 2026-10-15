# M0 元数据替代证据核查（2026-10-15）

此前这轮核查只尝试官方公开网页/公开索引，并读取本项目已经保存的有限实测摘要。该轮没有调用未授权 API、没有使用 MoviePilot 的共享默认 TMDB Key、没有修改旧项目或凭据文件。这是历史执行记录，不构成对后续已授权凭据取用的禁止。

2026-10-15 用户最新明确授权：本次任务可以从原 MoviePilot 项目只读取得并使用 TMDB 凭据，同类任务相关凭据也可读取对应旧配置。此授权取代此前我们自行添加的“不得使用旧应用内置公用 Key”限制。主任务已由授权 broker 从原配置只读取用并完成五次官方 API 查询；旧项目保持不变，凭据不回显、不写普通文件或日志。本轮文档核对只读取脱敏取用和实测报告，没有自行读取凭据或发起请求。该授权及实际 API 成功不改变下述公开页面访问限制，也不能把历史索引或缓存改标为标准 API 证据。

## 官方公开页的实际障碍

| 目标公开地址 | 本次访问结果 | 能证明的范围 |
|---|---|---|
| `https://www.themoviedb.org/movie/1602727` | restricted URL；后续官方域检索明确 robots.txt non-retryable block | 未取得电影详情正文，不能核验日期/年份/标题/标准响应 |
| `https://www.themoviedb.org/tv/1418/season/7/episode/17` | not accessible；同域 robots.txt block | 未取得 S07E17 的独立单集 ID 或详情 |
| `https://www.themoviedb.org/tv/206484/season/4/episode/5` | not accessible；同域 robots.txt block | 未取得 S04E05 的独立单集 ID 或详情 |

这是工具返回的实际访问限制，不推断网站没有这些项目，也不声称页面需要登录。停止对被明确标记为不可重试的地址重试，没有切换客户端绕过该限制。

限定官方 domain 的搜索仅返回一条相关的[诛仙既有 watch 页索引](https://www.themoviedb.org/tv/206484/watch?language=zh-HK)，显示 TV206484 与剧名关联，页面索引大约在 1.3 年前抓取且只显示第 3 季信息。证据类型只能是 **search_index_primary_domain**，不是当前 `live_public_page`、captured API response 或 S04E05 的权威确认。没有将它写入标准单集夹具或用于判定新季不存在。

## 已有安装/媒体库记录

Memos 此前按 TMDB/MoviePilot 查询只找到《NAS Docker 容器服务入口》《服务入口与 Lucky 转发关系》，没有匹配 TMDB 配置条目。这仅说明该次知识库查询没有找到对应记录，不证明设备上的所有配置都没有 Key。该轮没有读取/回显密码或共享默认 Key；后续旧项目相关配置的只读取用按上述最新授权执行。

本项目已保存的 `docs/m0-evidence.json` 显示：

- 飞牛既有库查询：夜王 `Movie / tm1602727`、生活大爆炸 `TV / tt1418`、诛仙 `TV / tt206484`。
- 既有生活大爆炸 `tvshow.nfo` 的只读摘要：TMDB `1418`、TVDB `80379`、IMDb `tt0898266`。

这些属于 **installed_library_cache / existing_read_only_nfo** 佐证，说明本地来源标记与既有库有一致性。它们不是官方当前响应，也没有两个目标单集的独立 TMDB episode ID。读取本项目已保存摘要没有重新扫描 NAS 或读取影视内容。

## 结论与后续契约

此前公开页面核查只取得候选作品的本地佐证，没有取得可作为 `live_public_page` 的当前官方详情，也没有两集独立来源 ID，因此当时没有新增声称通过的权威规范夹具。这一历史结论保持不变；后来取得标准身份的证据来自独立官方 API 实测，不来自受限网页、旧索引或飞牛不透明 Trim 查询。

实际脱敏报告 `work/live-metadata-authorized-source.json` 为 `live_request`，五请求全部 `ok`：电影 1602727、父剧 1418/206484、独立单集 972676（TV1418/S07E17）和 7706553（TV206484/S04E05），标题与日期已按固定官方路由核对。`work/metadata-original-project-access.json` 记录用户新授权下从原 MoviePilot 配置只读取用且未保存/回显凭据。凭据的有效性与固定样本标准身份因此已有真实证据，不再保持待实测；具体字段及父上下文边界见 [metadata-contract.md](metadata-contract.md)。

官方 API 文档和 OpenAPI 的一般接口示例仍只能确认契约；未配置探针的历史零请求结果、网页受限、搜索片段、本地缓存以及后续飞牛 provider 查询只返回不透明 Trim ID 的结果均没有改标。后续真实报告各生成五份 v1 `uniqueid` 和 v2 同值 `tmdbid+uniqueid` NFO，在两个新 UUID 媒体库中分别读回七节点、三个实际文件路径和父链/季集日期，两轮各 30 项比较通过。两轮既有三作品有限前后字段及七库配置相同，两个自建库与合计十六文件已精确清理；这些实际结果由主任务报告关联，不来自公开页面绕行。

两轮新库 `trim_id` 全为空，`direct_standard_id_readback=unverified`，不能直接证明服务内部保留标准 uniqueid/tmdbid，也不能断言飞牛完全不支持 TMDB。原方案允许字段兼容说明，M0 采用权威来源 → NFO → 实际文件/父链/日期的有限闭环，而非将标准 ID 直接回读设为新增必过条件。原方案范围复核也撤回服务克隆/DDL 隔离作为额外 M0 硬门槛；全局共享缓存仍未审计，不宣称全局无变化。

M0 **10/10 交付项、6/6 进入 M1 条件在限定范围内通过**。这个结果不将本轮网页限制、不透明查询或缓存佐证改标为权威身份，也不等于完整 P0 应用/生产验收。详见 [m0-final-steps.md](m0-final-steps.md)、[metadata-contract.md](metadata-contract.md) 和 [fnos-identity-isolation.md](fnos-identity-isolation.md)。

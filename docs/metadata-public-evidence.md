# M0 元数据替代证据核查（2026-10-15）

本次只尝试官方公开网页/公开索引，并读取本项目已经保存的有限实测摘要。没有调用未授权 API、没有使用 MoviePilot 的共享默认 TMDB Key、没有修改旧项目或凭据文件。

## 官方公开页的实际障碍

| 目标公开地址 | 本次访问结果 | 能证明的范围 |
|---|---|---|
| `https://www.themoviedb.org/movie/1602727` | restricted URL；后续官方域检索明确 robots.txt non-retryable block | 未取得电影详情正文，不能核验日期/年份/标题/标准响应 |
| `https://www.themoviedb.org/tv/1418/season/7/episode/17` | not accessible；同域 robots.txt block | 未取得 S07E17 的独立单集 ID 或详情 |
| `https://www.themoviedb.org/tv/206484/season/4/episode/5` | not accessible；同域 robots.txt block | 未取得 S04E05 的独立单集 ID 或详情 |

这是工具返回的实际访问限制，不推断网站没有这些项目，也不声称页面需要登录。停止对被明确标记为不可重试的地址重试，没有切换客户端绕过该限制。

限定官方 domain 的搜索仅返回一条相关的[诛仙既有 watch 页索引](https://www.themoviedb.org/tv/206484/watch?language=zh-HK)，显示 TV206484 与剧名关联，页面索引大约在 1.3 年前抓取且只显示第 3 季信息。证据类型只能是 **search_index_primary_domain**，不是当前 `live_public_page`、captured API response 或 S04E05 的权威确认。没有将它写入标准单集夹具或用于判定新季不存在。

## 已有安装/媒体库记录

Memos 本次按 TMDB/MoviePilot 查询只找到《NAS Docker 容器服务入口》《服务入口与 Lucky 转发关系》，没有匹配 TMDB 配置条目。这仅说明该次知识库查询没有找到对应记录，不证明设备上的所有配置都没有 Key。没有读取/回显密码或共享默认 Key。

本项目已保存的 `docs/m0-evidence.json` 显示：

- 飞牛既有库查询：夜王 `Movie / tm1602727`、生活大爆炸 `TV / tt1418`、诛仙 `TV / tt206484`。
- 既有生活大爆炸 `tvshow.nfo` 的只读摘要：TMDB `1418`、TVDB `80379`、IMDb `tt0898266`。

这些属于 **installed_library_cache / existing_read_only_nfo** 佐证，说明本地来源标记与既有库有一致性。它们不是官方当前响应，也没有两个目标单集的独立 TMDB episode ID。读取本项目已保存摘要没有重新扫描 NAS 或读取影视内容。

## 结论与后续契约

三类样本的候选作品身份与文件名已有本地证据，但 **标准 provider 与两集独立来源 ID 的权威绑定仍未验收**。没有取得可作为 `live_public_page` 的当前官方详情，因此没有新增声称通过的权威规范夹具。

官方 API 文档和 OpenAPI 的一般接口示例能确认字段契约，不能替代这三个目标的真实数据。现有 `metadatacheck` 将 `requested_source_id` 与通过的 `source_id` 分开；无自用 Key/Read Access Token 时仍返回 `unverified`、0 请求和非零退出码。要关闭该门槛，需要已授权的自用 TMDB 凭据完成固定五请求验证，或者实际可读取且包含同等独立身份/季集/日期的官方详情证据；不能以搜索片段或本地缓存降低门槛。

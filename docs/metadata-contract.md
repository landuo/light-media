# M0 固定三样本 TMDB 来源 ID 验证

`cmd/metadatacheck` 是标准库 Go 的只读验证工具。它不是元数据搜索/推荐服务，不生成 NFO，不保存完整响应、不调用用户/账户接口。实现阶段及自动化测试只使用 **synthetic HTTP round-trip**；后续主任务在用户明确授权下完成了固定五次官方 API 查询，两类证据分开记录。

## 凭据与调用边界

stdin 只接受一个 JSON 对象，最多 64 KiB。只能配置以下凭据之一：

```json
{"api_key":"由已授权内存broker提供"}
```

或 `{"access_token":"由已授权内存broker提供"}`。Read Access Token 使用 Authorization Bearer Header，v3 API Key 使用官方 api_key query。不会将两者混用。不要在聊天粘贴密钥，不放命令行/环境变量/普通输入文件/Git。未配置 `{}` 返回 `unverified`、零请求和非零退出码，不能记为来源验证通过。

2026-10-15 授权更新：用户已明确授权为本次任务从原 MoviePilot 项目只读取得并使用 TMDB 凭据，同类任务相关凭据也可读取对应旧配置。此授权取代此前我们自行添加的“不得使用旧应用内置公用 Key”限制；不再仅因凭据来自旧应用就要求另行提供自用 Key。由已授权 broker 在内存中取用并经 stdin 传入，不修改旧项目，不回显或保存凭据。授权本身不证明凭据有效、实际请求成功或标准身份已通过。

不接受任意 host、ID、path 或外部 HTTP client 参数。固定 HTTPS `api.themoviedb.org`，正常 TLS 验证，无 cookie jar，所有重定向都拒绝；未知输入字段（含 base_url）被拒绝。没有重试、搜索、翻页或 append_to_response。每次请求最多十秒、响应 1 MiB，总上下文六十秒。

```sh
CGO_ENABLED=0 go build -o work/metadatacheck ./cmd/metadatacheck
# 由已授权内存broker通过stdin提供上述凭据。
```

退出码 0 表示全部检查通过；1 表示未配置/部分失败；2 表示 JSON、输入尺寸或不支持的命令行参数。完整 URL、Authorization、凭据、响应正文和服务端错误文本均不进入报告。

## 固定五个 GET

均加 language=zh-CN：

| 请求 | 确认目标 |
|---|---|
| movie/1602727 | 夜王，电影，文件名年份 2026 |
| tv/1418 | 生活大爆炸，TV |
| tv/1418/season/7/episode/17 | 该父剧的 S07E17 |
| tv/206484 | 诛仙，TV |
| tv/206484/season/4/episode/5 | 该父剧的 S04E05 |

电影/父剧 ID 和目标季集由本次 M0 确认候选提供，真正通过前仍是 requested_source_id。验证响应的稳定 ID、电影 title/TV name、合法日期；电影年份必须 2026，三个作品本地化名称必须与确认目标一致。类型由固定官方 endpoint 与对应字段契约确认；响应若另给 media_type 则必须一致。正常路径共五请求；401/403/429 或已识别业务鉴权错误停止后续请求，剩余目标保持未验证。

单集必须有独立正 ID，不能拿父剧 ID 当单集 ID；season_number/episode_number 须正且等于请求。先验证父剧稳定 ID/名称，再结合固定的父剧 ID 请求路由确认单集上下文。当前官方 OpenAPI 的单集响应没有必需 show_id 字段，因此报告区分 `verified_series_and_fixed_request_route`；若响应额外包含 show_id，还要求其与父剧一致，并记录 `verified_response_show_id_and_request_route`。不宣称缺少 show_id 时直接从响应字段证实了父剧 ID。

报告仅输出每项目稳定状态/原因码、请求来源 ID、通过的来源 ID/类型、季集、日期、年份和安全的本地化标题。单集 source_id 是单集 ID，parent_source_id 才是剧 ID；`tmdb:movie:id`、`tmdb:tv:id` 和 `tmdb:episode:id` 分开。标题必须长度有界、无控制字符且不包含输入凭据；失败响应不回显标题/正文。

## 实际验证与测试

2026-10-15 主任务依据用户新授权，从原 MoviePilot 的 `app/core/config.py` 只读取用已有 TMDB API Key，在内存中经 stdin 传入本探针。没有修改旧项目，没有回显或落盘凭据；实际仅发送到固定官方 TMDB API。脱敏取用记录为 `work/metadata-original-project-access.json`，实际报告为 `work/live-metadata-authorized-source.json`：`evidence=live_request`、五请求、五条 `status=ok`、退出码 0，日志写入成功且无丢弃。这里的通过来自实际查询及固定接口校验，不是授权、编译或模拟测试本身。

| 实测来源身份 | 类型/上下文 | 返回标题 | 日期 |
|---|---|---|---|
| TMDB 1602727 | movie，2026 | 夜王 | 2026-02-14 |
| TMDB 1418 | tv，2007 | 生活大爆炸 | 2007-09-24 |
| TMDB 972676 | episode，TV1418 / S07E17 | 友谊的湍流 | 2014-03-06 |
| TMDB 206484 | tv，2022 | 诛仙 | 2022-08-02 |
| TMDB 7706553 | episode，TV206484 / S04E05 | 偶遇小白 | 2026-09-04 |

两个单集的父上下文为 `verified_series_and_fixed_request_route`，没有冒充响应额外返回 show_id。通过范围仅是这三个作品及两个目标单集的标准来源身份、标题、日期和请求上下文，不代表视频内容、所有国漫集号或整库识别全部通过。

该真实五记录报告已由独立的 [experiments/metadata-nfo](../experiments/metadata-nfo/README.md) 核对并生成五份 canonical NFO。历史 v1 为 `uniqueid` 单字段；当前 v2 当存在合法 `tmdb` UniqueID 时还输出同值 `<tmdbid>`，两个断言取自同一个已验证值，单集均使用自身 ID，不使用父剧 ID。无 TMDB ID 的 custom-only 合成样本不会新增该标签，历史 XML/JPEG 不改写。两轮清单分别为 `work/nfo-real-manifest.json`、`work/nfo-real-tmdbid-manifest.json`。工具自身仍不获取元数据、不把 supplied `live_request` 或哈希当成远端真实性证明，也不做飞牛读回；远端依据保留在独立生产者报告中。此前未配置报告在默认/apply 两模式实跑均被拒绝、零文件创建，该历史失败没有被改标为成功。

主任务分别在独占新库执行了 v1（`24dd83d2-28d9-4052-8ebd-0282d808cf2f`）和 v2（`63a741d5-3dfb-4319-b512-ebd324d81128`）实测。两轮各读到三根作品、两季、两集共七节点；实际接口均提供电影与两个单集的三条文件路径关联，目标路径和父链、作品标题/年份、S07E17/S04E05 及两集日期的 30 项离线比较均通过。每轮文件核对 32 请求、66 条日志；既有三作品前后摘要各 17 请求，实际字段及七库配置相同。两轮库均清理、各八份自建文件按哈希核对后删除，合计两个库/十六文件及自建 staging 已清理，凭据 broker 已退出；结果总览为 `work/real-experiments-final-cleanup.json`。

电影/父剧 NFO 仅编码年份，飞牛相应根日期为该年 01-01，不能用它证明完整发行/首播日期已从 NFO 保留。两轮七节点 `trim_id` 均为空，故 `direct_standard_id_readback=unverified`：不能直接证明 uniqueid/tmdbid 在飞牛内部如何保存，也不能声称接口已返回独立单集标准 ID。M0 按权威来源报告 → 实际 NFO → 服务实际文件路径/父链/季集日期的有限兼容闭环通过；这项直接回读限制单独说明，不增加为必须消除的 M0 缺口，也不推断飞牛完全不支持 TMDB。全局共享缓存仍未审计。

截至本轮收束，M0 **10/10 交付项、6/6 进入 M1 条件在限定范围内通过**；本文件只证明其中固定样本的来源与兼容链路。完整范围、早期独立演员/图片样本及兼容边界见主任务报告和 [m0-final-steps.md](m0-final-steps.md)。最新代码后的 `make check` 与 Linux amd64 主程序/实验构建已由主任务完成，不能据此扩展到完整 P0 应用或生产验收。

测试覆盖固定 origin/五个路由/zh-CN/key 与 Bearer 鉴权、未配置零请求、任意 host 输入拒绝、来源 ID/名称/年份/类型/季集/父上下文不一致、HTTP/业务鉴权失败、限流、404、过大响应、HTML/格式错误、URL/错误正文/标题中的凭据不泄露和重定向拒绝。自动化测试中的单集 ID 与日期 fixture 为合成数据；上表另来自实际五请求报告，没有把 fixture 改标成实测。

依据官方主源：[电影详情](https://developer.themoviedb.org/reference/movie-details)、[剧详情](https://developer.themoviedb.org/reference/tv-series-details)、[集详情](https://developer.themoviedb.org/reference/tv-episode-details)、[应用鉴权](https://developer.themoviedb.org/docs/authentication-application) 和 [官方 OpenAPI](https://developer.themoviedb.org/openapi/tmdb-api.json)。

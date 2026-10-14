# M0 固定三样本 TMDB 来源 ID 验证

`cmd/metadatacheck` 是标准库 Go 的只读验证工具。它不是元数据搜索/推荐服务，不生成 NFO，不保存完整响应、不调用用户/账户接口。实现期间没有实站 API 调用；测试是 **synthetic HTTP round-trip**。

## 凭据与调用边界

stdin 只接受一个 JSON 对象，最多 64 KiB。只能配置以下凭据之一：

```json
{"api_key":"由已授权内存broker提供"}
```

或 `{"access_token":"由已授权内存broker提供"}`。Read Access Token 使用 Authorization Bearer Header，v3 API Key 使用官方 api_key query。不会将两者混用。不要在聊天粘贴密钥，不放命令行/环境变量/普通输入文件/Git。未配置 `{}` 返回 `unverified`、零请求和非零退出码，不能记为来源验证通过。

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

## 待验证与测试

目前未配置用户自用凭据，实际五次来源验证 **pending**；编译及模拟测试不能替代 live_request 证据。获得已授权凭据后由 broker 一次执行并保留脱敏摘要。通过也仅确认这三个样本的标准来源 ID/目标季集，不代表文件内容、所有国漫集号、飞牛 NFO 读取或整库识别全部通过。

后续五记录报告可由独立的 [experiments/metadata-nfo](../experiments/metadata-nfo/README.md) 核对并生成 canonical NFO 计划；它不获取元数据，不把 supplied `live_request` 或哈希当成远端真实性证明，也不做飞牛读回。当前未配置报告在默认/apply两模式实跑均被拒绝、零文件创建，标准身份门槛保持未验证。

测试覆盖固定 origin/五个路由/zh-CN/key 与 Bearer 鉴权、未配置零请求、任意 host 输入拒绝、来源 ID/名称/年份/类型/季集/父上下文不一致、HTTP/业务鉴权失败、限流、404、过大响应、HTML/格式错误、URL/错误正文/标题中的凭据不泄露和重定向拒绝。所有单集 ID 与日期 fixture 为合成数据，未编造实际 API 成功结果。

依据官方主源：[电影详情](https://developer.themoviedb.org/reference/movie-details)、[剧详情](https://developer.themoviedb.org/reference/tv-series-details)、[集详情](https://developer.themoviedb.org/reference/tv-episode-details)、[应用鉴权](https://developer.themoviedb.org/docs/authentication-application) 和 [官方 OpenAPI](https://developer.themoviedb.org/openapi/tmdb-api.json)。

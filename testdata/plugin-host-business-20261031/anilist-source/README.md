# S11 AniList 原生识别交验

固定源 `8a83efeacd3e26b82be495e17d50146e31c96a60`，只迁移插件宿主普通媒体识别分支；这里不迁移整个 AniList 探索/人物/刮削业务，也不新增 AI、工作流或认证。中央唯一对照表负责最终状态。

| 源位置与行为 | 目标位置与实际证据 |
|---|---|
| `app/chain/__init__.py:586–635/678–710` 明确来源仅留对应 ID；无来源按 TMDB/豆瓣/Bangumi/AniList 顺序，均无 ID 时由模块读取默认来源 | `plugin_original_catalog_recognize.go`、`plugin_original_host_media.go`。实际固定源 AST 8 例 `dispatch-oracle.json` 与 Go 逐值比较；标准 SDK 另测无 source+AniList ID、明确 AniList+TMDB ID、默认 AniList+meta。 |
| `app/modules/anilist/__init__.py` `recognize_media`、`_match_by_meta`、`_matches_meta`、`_enrich_people`：ID优先、首个类型/年份匹配项、第一声优、Director大小写、模型生成后季号覆盖 | `internal/app/plugin_original_host_media_anilist.go`。完整标准 SDK/native 18 HTTP 场景与 source AST 比较；含 `format=movie` 在模块筛选为电视剧、模型投影为电影的原始差异，未擅自统一。 |
| `app/modules/anilist/anilist.py` 完整 detail/search GraphQL、代理失败永久切官方、递归中文数据注入、词库仅首次获取；空词库也缓存 | `internal/catalog/source_plugin_anilist.go` 与 App 私有客户端状态。所有请求的域名、路径、方法、完整 GraphQL/variables 均实际比对。两组同 App 连续 `cache=false` 调用证明代理状态、空词库缓存真实保留。 |
| `app/core/context.py:859–1021` 完整 AniList `MediaInfo` 初始化及公用序列化 | `originalHostAniListFields`、既有真实 legacy 标题解析器、共同 `rsscontext` 序列化。35 完整模型源 oracle，包含中文/假名、日期、HTML、原始分数、别名去重、来源 ID、演职员、季/集、Unicode AniDB 数字，以及源异常。不是 Explore 卡片。 |
| `ChainBase.__execute_system_modules:377–414` 捕获普通模块错误；`__handle_system_error:260–284` 提示、事件，然后保留 None | `originalCatalogRecognizeMethod`。实际 SDK 用 null characters 触发源模块错误，结果为 null 且读取真实 AniList 系统错误提示。此用例发现并推动修复公共硬编码错误标题；r6 红证据保留，r8 通过。 |

`oracle.py` 执行原固定 MediaInfo、AniList 模块和 API 的 AST，只有 HTTP 与标题解析结果为明确 owned 边界，不导入旧应用或读取旧服务配置。`dispatch_oracle.py` 执行原固定 ID 选择与真实错误捕获/消息/事件函数。`queries.json` 是固定源字面量查询，SDK 实际请求以 source oracle 的 `request_body` 全量比对。

最新全组 race：`verification/anilist-race-r8.log.gz` **PASS 30.262s**。normal r4 **PASS 16.620s**；8 例 Chain 独立 normal r7 **PASS 1.230s**。标准 worker 实际安装后，通过 SDK→resource→Chain→真实 native provider→实际完整模型序列化。HTTPS 使用测试独有 CA、标准域名验证和仅连接 owned listener 的测试 DialContext；没有关闭 TLS 检查、修改全局信任或访问正式 AniList。这是实际 App/SQLite/外部 worker 的合成协议回放；最终 Linux `cmd/server`、前端浏览器及全 Go/embed 输入封存由 services/root 归档，不能用本组测试替代。

`native-cases.json` 给 Linux driver，`cases` 是扁平 `MediaRecognizeRequest`。`fresh_process=true` 前必须冷启动 App；紧随其后的 `false` 行必须保留同一 App，证明真实客户端生命周期。`cache=false` 不代表重置代理或中文词库。`request_body` 是完整 JSON 断言，不能只检查 HTTP200。

必要适配：Go 使用既有代理/网络策略及宿主 UA；source `PROXY_HOST` 重载用配置指纹+App 生命周期表达。Python traceback/任意错误文本使用既有安全 Go 错误码包络，保留真实模块身份、提示、事件与 None 控制流；JSON SDK 16MiB 是响应边界，不截断集数为任意固定上限。固定源 parser 的 owned 结果边界与 target 真实解析器明确分记。详细源/目标/worker/日志 hash 和失败历史见 `evidence.json`。

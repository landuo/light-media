# M0 飞牛同源元数据查询协议核查

协议研究阶段只读取已安装公开客户端 bundle，未连接实站，未读取服务内置 TMDB Key，也未尝试被拒绝的 TMDB 网页或替代 URL。随后主任务用已授权飞牛账号独立执行了 5 次真实同源请求，脱敏摘要位于 `work/live-fnos-provider.json`；与本节的代码研究及合成测试分开计证。新增独立 `fnosprovidercheck` 不修改原 `metadatacheck` 契约。

## 客户端实证

当前本项目 `work/fnos-public-mdb-client.js` SHA-256 为 `e244e703491c58f2e354be13a2dc00a6354ad352bb6519e90d7e28a188d9f0a6`；view bundle 为 `92a8becea3c705a448301b49fe7368722a136368137cd541986f3c3ffc3a5527`。它们是已安装版本的公开客户端代码，不能据此宣称稳定官方开放 API。

- client 第 3 行定义 `search: POST /scrap/search`，并独立定义写接口 `rescrap: POST /scrap/rescrap`、`batchRescrap: POST /scrap/rescrap/batch`。
- client 同行的搜索 selector 枚举：`TmdbId=tmdb_id`、`ImdbId=imdb_id`、`Title=title`。
- view 第 45 行的 `HK({metaLang,keyword,searchSource,itemGuid})` 构造 `{source,source_keyword,lan}`，itemGuid 存在时添加 `item_guid`，调用 `R.scrap.search` 后将 `.data` 数组作为候选显示。这个读调用不包含 season_number/episode_number。
- 选择候选后的写接口才携带 `item_guid/source/source_id/type/season_number/episode_number/scrap_tv_type/media_guids`；本探针不会调用。
- `GET /season/list/:guid`、`GET /episode/list/:guid`、`GET /movie/:guid`、`GET /tv/:guid` 是 GUID 媒体库视图；`POST /item/getEditDetail {item_guid}` 是本地编辑详情。当前代码没有证明这些是按标准 provider ID 获取当前季集详情的云查询接口。

因此只读 `scrap/search` 是可核查的 provider selector 查询；但仅凭客户端不能声称已经找到标准单集 ID 的读取路由。不能从普通 GUID/trim_id 或裸 `id` 推断 TMDB episode ID，不能用 rescrap 的写副作用获取数据来冒充只读。

## 独立探针

`cmd/fnosprovidercheck` stdin 沿用已有 fnOS Config：host、username、password、access_code。凭据仅通过已授权内存 broker 传入，不能放命令行、普通文件或报告；不会读取服务内置凭据。API 调用总数最多五个：版本 GET、登录 POST、三个 `/scrap/search` POST。

三个固定 body 分别为 `{"source":"tmdb_id","source_keyword":"1602727","lan":"zh-CN"}`、ID 1418、ID 206484。没有额外季集参数、没有任意 endpoint、不传 item_guid 改变库条目。正常 TLS，无 cookie jar，所有重定向拒绝，单次十秒、响应 1 MiB、总六十秒。授权的局域网 HTTP origin 可按已有飞牛配置显式传入，凭据始终只发送该 origin。

报告会分开保存 requested source 与响应 provider/source_id 类型，检查明确类型/标题及日期。只有显式 `tmdb_id` 数值，或响应显式声明 TMDB source 且 source_id 是相同数值，才算标准作品 ID 通过。若两个标准 ID 断言同时提供，必须各自合法且一致；任一空/null/无效或两者冲突均保持未验证，不优先选取一个覆盖另一个。`trim_id` 与前缀字符串是不同的不透明供应商 namespace，不自动去前缀冒充标准 ID，也不要求它与另一个显式外部 TMDB ID 字符串相同。

实际 5 次调用均返回 `source=trim_id`，三作品标题/类型匹配，但没有显式标准 TMDB ID 或目标单集对象，故标准身份 completeness 为 false、总体 partial_evidence。电影候选包含 `first_air_date` 字段，而探针只把电影 release_date/air_date 作为日期候选；没有证明该服务的电影 first_air_date 等同标准电影发行日期。该次 `standard_date_not_returned` 应解读为“未取得语义已确认的电影日期字段”，不能说响应完全没有任何日期字段，也不据此推断电影年份。

如果响应实际包含嵌套 episodes/seasons，只在明确 `tmdb_id` 单集 ID、show_id 与父剧匹配、正季集恰好等于 S07E17/S04E05、日期与标题有效且匹配唯一时记录该目标；裸 id 的 namespace 未证实会保持未验证。这只是严格读取返回数据，不凭造新字段向服务器请求。

结果类型是 `live_fnos_provider_query`，与直连 TMDB `live_request`、本地 `existing_read_only_nfo` 和 `installed_library_cache` 分开。仅作品匹配而缺单集标准 ID 时，本探针仍输出 `partial_evidence`、episode_identity_complete=false，并以非零退出；该份结果不能单独证明标准单集身份。后续主任务已按用户新授权从原 MoviePilot 配置只读取用 TMDB 凭据，固定五次官方查询全部通过，取得两个独立单集 ID，来源证据见 [metadata-contract.md](metadata-contract.md)。因此这条代理查询的局限不再是整个 M0 未完成的标准身份缺口，也不需要额外索要一份自用 Key 才承认实际结果。M1 运行时凭据配置尚需实现，本次有限实验没有提供完整的生产凭据管理功能。

M0 飞牛兼容核对采用权威官方报告 → 实际 NFO → 服务返回的实际文件路径、父链与季集/日期的有限关联。`trim_id` 为空和没有显式标准 ID 字段，只能记录为 `direct_standard_id_readback=unverified`；不能推断服务内部保存了 uniqueid，也不能据此断言飞牛完全不支持 TMDB 字段。原方案允许字段兼容说明，这项直接回读不被增加为额外 M0 硬门槛。公开 provider 查询仍保持原来的不透明 namespace 和 `partial_evidence`，没有被改标为标准身份成功。

后续 v1 `uniqueid` 和 v2 同值 `tmdbid+uniqueid` 两轮独占新库实验均完成七节点、三条实际文件路径及父链/季集日期核对，各 30 项比较通过；既有三作品和七库配置的有限前后摘要相同，两个库与十六文件均已清理。M0 **10/10 交付项、6/6 进入 M1 条件在限定范围内通过**，这些成功依据与本代理五请求的 `partial_evidence` 分开。完整范围及直接标准 ID/全局共享缓存限制见 [m0-final-steps.md](m0-final-steps.md)。

不输出用户名、密码、会话 token、host、完整 URL、签名或完整响应。只显示固定样本、受限类型/provider ID、已认可的字段名称、安全标题/日期与状态。没有媒体库修改、刷新、扫描、重新刮削或目录访问。

```sh
CGO_ENABLED=0 go build -o work/fnos-providercheck ./cmd/fnosprovidercheck
# 由已授权broker通过stdin输入原有飞牛 Config。
```

所有自动测试均为 **synthetic HTTP round-trip**，不是捕获的实站响应。主任务的实际 5 次同源调用另计为 `live_fnos_provider_query`，没有单独证明标准身份；后续官方 API 的五次 `live_request` 是独立来源证据。测试明确验证只读调用 body 与路由、来源 namespace、多来源标准 ID 一致性/非法值、缺单集不猜测、标准单集父/季集边界、鉴权/重定向/过大响应失败和脱敏。此阶段契约不等于飞牛代理已经满足全部标准身份验收，也不因其有限结果否定其他已取得的真实证据。

# M0 飞牛既有作品只读详情基线

`internal/fnos.Probe` 的可选 `capture_sample_details` 默认 false。`cmd/fnoscheck` 的服务器配置直接使用 `fnos.Config`，因此无需新增命令行参数；配置仍由已授权内存 broker 经 stdin 提供，不读取凭据文件。默认模式的请求、输出样本和判定保持原行为。

启用时 `sample_titles` 必须恰好包含 `夜王`、`生活大爆炸`、`诛仙` 各一次，顺序可变；预期类型分别为 `Movie`、`TV`、`TV`。仅针对已经返回的搜索结果中题名和类型完全匹配且唯一的候选，增加一次 `GET /item/:guid`。GUID 必须为 32 位十六进制字符串；搜索结果为空、歧义、截断或 GUID 不合法时不请求详情。每作品至多一条详情请求，三个作品最多三次；沿用原总计 20 请求上限。既有最多八个库的一条抽样、三次搜索和三次详情共最多 18 请求。

安全摘要放在 `search_samples[].details`，与原 `samples` 分开。`status=observed` 必须满足：详情 GUID 精确等于请求 GUID；`ancestor_guid` 为合法 GUID 且属于本轮已经读取的媒体库；类型、题名与候选完全一致；`parent_guid` 字段明确返回空字符串；`trim_id` 字段明确为字符串（可以为空）。核心字段缺失、null、错误类型、外库归属或非空父关系均为 `unverified`，不填造父关系；总体结果也为 `unverified`。这里只核对三个电影/父剧根对象，不递归季集。

摘要只保留实际返回的 GUID、祖先/父关系、类型、题名、Trim ID，以及可选 year、release_date、premiere_date、first_air_date、air_date、season_number、episode_number。年份可以是明确数值或四位数字字符串，归一为整数；不会从日期推年份。可选日期必须为合法 YYYY-MM-DD，季集数值保留服务明确返回的值（含 0），不会由根对象类型补 0。未返回、null 或空日期列入 `missing_fields`；这些正常缺失不会否定已经核对的身份/库归属。明确返回的非法可选值保持未验证，不把类型错误当作字段缺失。

输出沿用已知 username/password/会话 token 及其 URL 编码变体的脱敏和 URL 清除，不输出完整响应、未知字段、演员、图片、profile 或媒体文件；只新增上述 GET，不增加写接口。沿用原请求超时、响应体上限、拒绝重定向和正常 TLS 校验。

这个基线仅支持授权实验前后比较三部既有作品实际返回的有限核心字段。`observed` 不代表全部可选字段存在，不是标准 TMDB ID 验证、独立单集身份、全局共享缓存隔离、全库无变化或 NFO 导入成功的证明。`trim_id` 保持不透明供应商标记，不能去前缀推断标准 ID。实现阶段只运行 synthetic httptest，没有 NAS/实站请求；后续真实前后基线由主任务单独执行和记录，不能混用两类证据。

## 已收束的有限实测

主任务在两个真实来源 NFO 实验前后各执行一次基线，每次 17 请求、三个 `details.status=observed`。v1 为 UUID `24dd83d2-28d9-4052-8ebd-0282d808cf2f`，对应 `work/nfo-real-baseline-before.json` 和 `work/nfo-real-baseline-after.json`；v2 为 `63a741d5-3dfb-4319-b512-ebd324d81128`，对应 `work/nfo-real-tmdbid-baseline-before.json` 和 `work/nfo-real-tmdbid-baseline-after.json`。每轮三作品实际详情完全相同，七个既有库的名称/分类/目录配置相同，未返回的字段仍保留缺失，没有填值。

这是有限基线相同的实测证据，不能证明全局共享 provider/actor/image 缓存隔离或全部既有数据无变化。两轮新库另各完成七节点、三条服务实际文件路径与 30 项 NFO 关联比较，并精确清理两个自建库及十六文件；基线没有替代这些独立验证。两种新库 NFO 的 `trim_id` 均为空，标准来源身份来自五次官方 TMDB 请求，`direct_standard_id_readback=unverified` 作为兼容说明保留，不是新增的 M0 必过条件。

这些有限证据共同支持 M0 **10/10 交付项、6/6 进入 M1 条件在限定范围内通过**，不要求先取得服务克隆/DDL 隔离证明，也不声称该隔离已成立。最终证据与清理汇总见主任务报告、`work/real-experiments-final-cleanup.json` 和 [m0-final-steps.md](m0-final-steps.md)。

合成测试覆盖默认不新增请求、三个固定题名与精确候选、解码查询签名/详情 GET 签名、候选 GUID 路径拒绝、歧义/截断/空结果、篡改详情 GUID、外库 ancestor、缺失/null 父关系、错误题名/类型、可选字段缺失与非法值、脱敏/未知字段不回显及最多 18 请求。

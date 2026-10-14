# M0 逐项验收矩阵

审阅日期：2026-10-15。初次审阅代码基线：`14cb72f`；日志缺口随后按同一 M0 任务在当前工作区补齐，状态更新列明于 D09/G06。本矩阵作者没有调用实站、下载器、飞牛或 NAS；日志实机证据来自主任务执行。本矩阵不替代原需求，不修改总体报告结论。

## 原始要求与判定口径

原始基线为 [design-v1.4.txt](design-v1.4.txt) 第 13 节 M0 行：

> 五站协议样本；两类下载器版本/能力检测；飞牛兼容验证；真实 NAS 路径/硬链接验证；一部电影、一部普通剧及一部常看国漫的命名/NFO 样本；Go+SQLite 内存基线；日志与扩展接口草案。

进入 M1 的原始条件为：

> 五站和双下载器接入可行；飞牛读取样本 NFO；实际挂载与稳定身份成立；确定驱动及基线，接口错误可追踪。

同时遵守原文的开工边界：路径记录下载器/应用/飞牛三方所见目录、文件系统与子卷，实际验证硬链接；不原地改写共享媒体。这里的“通过”只覆盖对应 M0 条件和列明版本/样本，不能扩张为 P0 完整应用验收。

- **通过**：对应 M0 的有限要求已有实际证据；其他阶段的实现不因此提前算完成。
- **部分**：已有可核对事实，但尚不足以把该项全部关闭。
- **未验证**：没有相应实际执行证据，合成单元测试或未配置零请求不能替代。
- 兼容差异必须记录真实行为及可行处理，不把空字段标为读入成功。M0 不要求实现所有未来 UI、所有可选 NFO 字段或 P1 插件运行器。

## M0 交付物

| 编号 | 原始要求 | 已有证据及结论 | 尚未验证 | 证明所需下一步 |
| --- | --- | --- | --- | --- |
| D01 | 五站协议样本 | **通过，有限样本**。`docs/m0-evidence.json.readonly_integrations` 含馒头、憨憨、UBits、家园、1PTBA 的实际鉴权及一页搜索结果；`.metainfo` 含每站一份材料的结构检查。五站实际结果均成功；自动 fixture 仍明确为 synthetic。契约见 `integration-contract.md`、`metainfo-contract.md`。 | 完整资源属性、促销到期、分页、RSS、稳定生产解析不由本次证明。材料没有验证媒体 payload。 | 此项 M0 不需扩大联网请求。后续完整筛选/适配按 M1/M2 处理，不能将只有一页/一材料的证据写成完整站点支持。 |
| D02 | 两类下载器版本/能力检测 | **通过，只读检测范围，分开记录运行**。实际 qB 5.1.2 / WebAPI 2.11.4、Transmission 4.1.3 / JSON-RPC 2.0。历史 TR 实测为 **5 请求、1 个稳定 hash 样本、22 个文件**，证明当次文件读取成功；当前 tracing 运行 `work/live-integrations-with-trace.json` 为 **4 请求、0 样本、0 文件**，阶段 `no_bounded_sample_files_unverified`、状态 `unverified`，仅版本/有限列表已观察。当前 qB 为 6 请求、1 样本、1 文件。能力表区分协议已知、当次读取和写操作验证。 | 当前 TR 文件能力没有新实测样本，不能用旧结果改写本次状态。所有写操作未实测；旧 Transmission 方言只有合成测试。 | M0 历史只读可行性保留；今后如需证明当前 TR 文件能力，另取受控小样本。不得在现有种子上补写操作，或把历史 22 文件冒充 tracing 本次结果。 |
| D03 | 飞牛兼容验证 | **部分；合成字段、条目归属及图片角色子项通过**。实际版本 0.9.8 / mediasrv 0.8.43，登录、库目录、少量作品查询可行。第三次 canonical 实机：7 节点 ancestor/parent/type 归属通过，`synthetic_nfo_fields_v1` 对照通过，普通剧 S01E02、国漫 S02E01 正确；六张不同 SHA、按角色区分尺寸的 poster/fanart 按作品及字段精确匹配。三次实验的差异和清理见下文。 | 自定义 uniqueid 未成为来源 ID；真实 provider/独立单集身份及同一真实三样本 NFO 闭环未验证。TV 根演员为空、年份/演员顺序有规范化。标准身份共享元数据隔离未知，见 `fnos-identity-isolation.md`；UUID 库不等于全局缓存隔离。 | 先核对真实三样本标准身份，并取得可复核的标准 ID 实验隔离条件，再做新 NFO 读回。不能凭新库 GUID 直接导入可能复用既有全局来源身份的元数据。 |
| D04 | 真实 NAS 路径/硬链接 | **通过，列明布局**。宿主授权目录实际 link 成功。又在 UID1000/GID1001、整个父目录 `/vol2/1000/media`→`/media` 的隔离容器内执行两组：download→国漫、Transmission 默认电视剧子目录→国漫；SameFile、源内容保持、改名/解除链接及清理均通过。只读核对 qB Mounts、原生 TR 默认路径、飞牛国漫目录；`.paths` 和 `path-baseline.md` 有证据。 | qB 另一 `/downloads` 挂载、默认 save_path、其他目录/卷、未来生产部署 UID/GID 不由本次证明。 | 本次 M0 的建议应用布局有实际可行性证据，无需测试全部历史目录。M1 显式采用已核对布局与下载目录；若选择其他 save_path/挂载，新增对应最小测试，不能静默套用旧结果。 |
| D05 | 一部电影命名/NFO 样本 | **部分**。实际 `夜王 (2026) - 1080p.mkv` 语法解析通过；飞牛既有条目返回 `tm1602727`。第三次 canonical 的合成 Movie 新 NFO 字段对照通过，但不是该真实电影的导入证据。 | 同一个真实电影的权威来源核对、新 NFO provider ID 读回尚未组成闭环。`.metadata` 当前 unverified、0 请求。 | 验证 movie/1602727 的类型、标题/年份；确认标准 ID 不会影响既有共享状态的隔离条件后，再生成这一电影的小样本 NFO，导入并读回来源 ID/年份/标题。不下载或修改真实视频。 |
| D06 | 一部普通剧命名/NFO 样本 | **部分**。实际 `生活大爆炸 - S07E17 - 第 17 集.mkv` 语法解析通过，飞牛既有父剧 `tt1418`；第三次 canonical 合成普通剧 S01E02 字段对照通过；第二次 namespaced 及仅改 basename 均实测返回 E01，失败保留。 | 真正 S07E17 的独立单集 ID、父剧关联、目标季集和新 NFO 读回尚未验证。真实 filename fixture 不伪造 `media_id/known_episodes`，因此映射仍 needs_review。 | 验证父剧 1418 和 S07E17 标准来源；先确认标准 ID 共享状态隔离，再将已确认目标季集输入映射/NFO 生成并读回父剧、单集 ID、S07E17。不能把父剧 ID 当集 ID。 |
| D07 | 一部常看国漫命名/NFO 样本 | **部分**。实际 `诛仙 - S04E05 - 第 5 集.mkv` 语法解析通过，飞牛父剧 `tt206484`；第三次 canonical 合成国漫 S02E01 字段对照通过。原始/标准编号、冲突/范围外处理已有合成测试。 | 真实诛仙 S04E05 的独立单集 ID、父剧季集与 NFO 尚未闭环。合成 `101–112→S02E01–E12` 不代表任何真实国漫结构。 | 验证父剧 206484 与 S04E05，确认样本对应规则或标准编号，满足标准 ID 共享状态隔离前提后再生成/导入匹配 NFO 并读回。M0 不需实现复杂国漫自动推断或剧集组全自动处理。 |
| D08 | Go+SQLite 内存基线 | **通过，合成数据库基线**。modernc v1.60.1 与 mattn v1.14.52 本机相同负载对照；实际 SQLite 3.53.4。同一 200订阅/5000元数据/50000文件负载在 NAS ext4、1CPU/512MiB、GOMEMLIMIT256MiB 完成；七项正确性检查通过。child wait4 最大 RSS 25.688MiB，容器 cgroup peak 50.293MiB。参见 `.sqlite_quota` 与 `sqlite-baseline.md`。 | 完整应用空闲/活动内存、五站混合流程、72小时负载、整个下载器/飞牛部署组合不由本次证明。 | 此项 M0 不需重复跑完整应用或72小时测试；这些属于应用阶段/M4。主进程、容器和 Go heap 口径继续独立报告。 |
| D09 | 日志草案 | **通过，草案及最小 CLI 接线**。`logging-extension-contract.md` 描述关联字段、白名单、秘密/URL脱敏、单条/总量预算、失败/丢弃可见性及待实现项；`internal/logging` 的独立 Handler 和 Run/HTTP helper 已接入六类 HTTP CLI，stdout/stderr 通过生成 run/target/request ID 关联，真实 CLI synthetic 故障测试及第三次 canonical 全阶段实机日志证明接线实际运行；observe 23 请求/48 事件、images 14 请求/30 事件包含具体语义 operation，零丢弃/写入失败。 | 完整应用文件日志、轮转、检索、持久任务时间线未实现；这些不由当前 CLI 日志证明。 | M0草案/追踪接线已交付，见G06。M1/M3分别实施网页日志中心及完整运行保留，不提前扩大本阶段。 |
| D10 | 扩展接口草案 | **通过，可 review 草案**。v0.1 事件封套、事件类型/事实语义、subject、版本/因果/幂等、能力事实、范围、变更控制、默认拒绝、核心/插件责任和后续 outbox 边界有明确文字与 JSON 示例。 | 没有插件进程、事件存储、host command执行或完整SDK；文字草案不是协议执行/权限安全测试。 | M0 要求是草案，无需把 M5/M6 提前。M1/P0继续收敛类型/错误/实现接口；P1才实现运行器、授予范围和业务插件。 |

## 进入 M1 的条件

| 编号 | 原始条件 | 已有证据与状态 | 尚未验证/阻断 | 所需最小下一步 |
| --- | --- | --- | --- | --- |
| G01 | 五站接入可行 | **通过，有限鉴权/搜索/材料范围**。D01 的五站实际结果支持接入可行性。 | 完整生产适配未实现，但不是这一 M0 可行性条件的额外阻断。 | 保留样本范围，按后续阶段扩展。 |
| G02 | 双下载器接入可行 | **通过，历史只读连接及能力探测范围**。D02 历史 TR 5 请求/1 样本/22 文件、qB 实际样本支持可行性。当前 tracing 的 TR 4 请求/0 样本仅证明版本及有限列表可读，文件阶段明确 `unverified`；两次事实不合并为当次成功。 | 当前 TR 文件读取没有新样本；写操作既未执行也未被当成 verified，不能将协议已知宣传为添加/迁移通过。 | 保留历史可行性与当前未验证状态；M1/M2 另用受控样本验收写路径，M0 不操作生产种子来凑完整能力表。 |
| G03 | 飞牛读取样本 NFO | **部分；合成 NFO 字段子项通过**。第三次 canonical 实机完成 7 节点归属、S01E02/S02E01、日期/标题/有限演员字段及六张图片角色的分别核对。第二次普通剧 E02→E01 的失败证据保留，不因第三次通过而删除。 | 真实三样本标准来源身份与新 NFO 尚未闭环；不能用自定义 ID 空回读替代作品身份。UUID 库/目录不证明 provider、演员或缓存全局隔离。 | 先满足 `fnos-identity-isolation.md` 的可复核隔离前提，再让同一确认作品/季集贯穿命名→元数据→NFO→飞牛读回。 |
| G04 | 实际挂载与稳定身份成立 | **部分，路径和下载器身份已通过**。D04 的三方布局与容器硬链接成立；`TaskIdentity{InstanceID,Hash}` 对大小写规范化并取指纹，历史实际两下载器分别取得合法 hash 样本（当前 tracing TR 为 0 样本，见 D02）；合成测试覆盖跨实例分离，不持久化数值 torrent ID。 | 作品和单集的权威 provider 身份仍未验证；下载器重启后的完整恢复对账、文件资产持久化属于后续实现。 | 关闭 D05–D07 的标准来源身份缺口；不把 M4 重启版本矩阵或整个资产状态机附加为 M0 前置。 |
| G05 | 确定驱动及基线 | **通过，M0 default已选 modernc**。固定模块版本，DefaultOptions 与 NAS wrapper选择 modernc；文档给出部署/静态交叉编译理由并如实保留 cgo更快/较低RSS的对照事实。基线为D08。 | 完整生产数据层尚未实现，不能称两套生产驱动已维护，也不能把 cgo 对照删除等同技术验证。 | M1生产实现只用选定驱动；基准对照作为M0工具保留。应用目标后续验收。 |
| G06 | 接口错误可追踪 | **通过，M0 CLI 范围**。初次审阅时只有JSON目标/阶段错误码，日志未接线；现已接入Run/HTTP helper，每CLI独立256KiB stderr JSONL、stdout run_id/logging健康、每目标trace_target_id、每请求request_id。实际CLI localhost HTTP400/redirect/断连故障及HTTP200业务错误测试验证了关联与脱敏，race通过；第三次 canonical 全阶段实际运行已接入语义日志，observe 23 请求/48 事件、images 14 请求/30 事件，另见下文。 | 没有以日志宣称完整应用时间线或HTTP200业务成功；业务错误仍由原摘要判断。无前端日志中心/生产保留/持久workflow/job，这些属于后续应用阶段。 | 此M0缺口已通过执行补证关闭。后续新增HTTP CLI必须同样接线；新run日志独立，不借旧运行的日志计数冒充。 |

## 三次合成 NFO 实机证据（不合并运行）

| 次序/UUID | 实际结果 | 证明边界与清理 |
| --- | --- | --- |
| 第一次 `97f462b1-b27c-4a31-a912-ace48d6380db` | namespaced 合成样本读回标题、日期、季集和有限演员；每作品 poster/fanart 同字节，六张图片下载字节匹配各作品组。 | 不能区分组内 poster/fanart 误交换；旧输出不能替代后来增加的真实 ancestor/parent 检查。库及自建文件已清理。旧 `.nfo_experiment` 与 `nfo-verification-plan.md` 证据保留。 |
| 第二次 `12567b4f-a170-40c4-acec-79bf497c9e01` | namespaced 布局增加 distinct 图片及 7 节点归属核对；普通剧 XML/文件名期望 S01E02，API 实际 S01E01。只改视频和匹配 NFO basename、单库扫描后仍 E01。 | `work/nfo-distinct-compare-before-rename-current.json` 与 `work/nfo-distinct-compare-after-rename.json` 均为 `FIELDS_MISMATCH`（expected 2 / actual 1）；读取成功不代表字段通过。该库及自建文件也已清理。 |
| 第三次 `06208206-17d8-42e2-b052-9e84c315040d` | canonical 发布目录/文件名，XML 标题/演员/custom ID 仍独立 UUID namespace。`work/nfo-canonical-compare.json` 为 `passed`：普通剧 S01E02、国漫 S02E01，7 节点 ancestor/parent/type 归属核对通过；`work/nfo-canonical-images.json` 六张图按作品+posters/backdrops 分别精确匹配 SHA-256、解码 JPEG 格式及尺寸（poster 48×72、fanart 96×54），六个哈希不同。 | `work/nfo-canonical-observe.json` 保存实际字段，compare 明确 `provider_ids_verified=false`。`work/nfo-canonical-cleanup.json` 确认临时库消失、既有库配置基线不变；`work/nfo-canonical-remote-file-cleanup.json` 核对自建清单并删除 14 个文件、目录不存在。 |

第三次关闭的是**这一合成样本的字段对照、条目归属和图片角色/字节**证明缺口；不是标准 provider 身份验证或所有布局兼容通过。两次布局结果不同尚未定位后端根因，不能断言 UUID 中的 `e01`、某个目录名或 NFO 优先级就是原因。清理及既有库配置基线不变也不能推出共享演员/来源元数据/图片缓存从未受影响；该隔离边界仍按 [fnos-identity-isolation.md](fnos-identity-isolation.md) 记为未知。

## 当前真正阻断与补证顺序

1. **三种代表样本的标准身份与 NFO 闭环**：基线元数据探针真实请求为零；合成 NFO 的字段读取和飞牛既有库查询不能替代同一真实作品及独立单集 ID 的核对。先通过已授权的自用来源 API 或可复核的官方来源获取固定样本身份；标准 ID 导入前还须取得共享状态隔离的可复核条件（独立测试实例、明确的厂商作用域契约或足够的应用级证据），不能仅凭 UUID 库/目录假定全球隔离，再决定最小 NFO 读回路线。原始 M0 不强制某一种获取途径；官方页面也必须确实提供所需的作品/独立单集 ID 与父剧、季集对应，不能把只展示标题的页面当完整身份。不得借用旧应用的内置公用 Token 或猜造 ID。

日志接入已经由代码和执行补证关闭：`cmd/integrationcheck/trace_test.go` 真实执行CLI故障路径；`internal/logging/trace_test.go` 验证HTTP200业务错误、请求/目标关联、固定operation、原样HTTP行为、并发隔离、输出预算与sink失败。主任务第三次 canonical 的 prepare/observe/images/cleanup 全阶段已实际使用接线后的 CLI；`work/nfo-canonical-observe.jsonl` 为 23 请求/48 事件，`work/nfo-canonical-images.jsonl` 为 14 请求/30 事件，摘要均为 logging ok、dropped=0、failed=0。日志中实际出现 `fnos_login`、`fnos_libraries`、`fnos_item_detail`、`fnos_persons`、`fnos_image` 等具体 operation；部分路由仍采用 `http_request`，不宣传全部请求均已有专门标签。早期 distinct 日志仍保留原样，不借新运行的语义标签改写旧证据。真实成功日志不替代故障路径测试，业务成功仍由返回摘要及字段比较判断。

TV 根演员为空、Season/Episode 有演员、year 2024 返回 2024-01-01、演员 order 0/1→1/2 及 custom uniqueid 未采用等兼容发现继续保留。第一次同字节图片的局限保留，第三次 distinct 图片已另行证明角色；第二次 E01 失败也不能被覆盖。完整演员资料/相关推荐页面、所有NFO扩展字段、完整站点筛选、下载/整理端到端、全局暂停、72小时资源验收与P1插件不应成为这次M0逐项验收的额外门槛。

本矩阵的路径和资源条目已使用持久脱敏证据，不需重复实机实验。新增补证应另外记录版本、样本、结果与清理，不能把此处的 `部分/未验证` 仅通过改文字变成 `通过`。

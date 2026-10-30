# Go 宿主运行时与安全边界（当前待验清单）

本表不是整个平台验收，也不是“现有 Go 行为已获批准”的声明。以固定 MoviePilot 后端 `8a83efeacd3e26b82be495e17d50146e31c96a60`、前端 `2829e3b08214348f05ff2f5beae644882eccad58` 为依据。当前状态唯一入口是 `ledger.json`。

## 已核实的用户依据

- `Sentinel_08ce7e2f43048191b8f265afe662e0d9`，2026-10-25 14:39:40 UTC：“不需要考虑用户和权限，这个系统仅单用户使用”。本轮通过原聊天只读接口核回；不等于删除现有登录保护或插件安全隔离。
- `Sentinel_b5a6087277508191a8f7ba54e94238c4`，2026-10-25 15:30:14 UTC：只迁移插件模块及必需前后端，按固定源设计和行为；保留 Go、Linux、本地存储、单用户，优先复用既有宿主；排除工作流/AI及其专用接口、依赖和扩展点。本轮核回全文。必要宿主适配不等于任意行为差异豁免。
- `Sentinel_c8907ed54bdc819180efd0d7ece41391`，2026-10-30 10:20:25 UTC：就明确名称问题答“可以”；名称按普通文本，不复刻 Python 源码改写的转义/引号副作用。
- `Sentinel_426b5ccc2a988191bb8c117f21731522`，2026-10-30 10:59:04 UTC：就描述问题 `Sentinel_3a7ab82157ec8191abaf8244982621b5` 答“按普通文本”。这是单独批准，不扩大到版本、图标或 HTTP 错误。
- `Sentinel_400f89908c24819183b04953918d00f3`，2026-10-30 10:59:27 UTC：“与原版保持一致的行为即可”。其他未批准的非安全差异继续对齐，不再把市场 JSON 500 等待批准当阻塞。

旧 `docs/plugin-framework-current-contract.md` 已退役。`docs/plugin-auth-provider-parity.md` 等历史实现报告中的“已批准”措辞不能单独充当新授权原文。

## 逐项边界与当前处理

| 边界 | 原源证据与用户影响 | 目标现状及处理 | 依据/验收状态 |
|---|---|---|---|
| 单用户会话 | 原 `core/auth.py`、`api/endpoints/auth.py` 使用原用户/JWT；调用者的认证材料与响应不是相同协议 | 保留现有单管理员 Cookie、API token、鉴权；不补多用户/RBAC，也不把整个原用户系统算已移植 | 明确单用户范围。响应形状必须按各接口核实；不能由单用户范围推导所有 JSON 信封都获批准 |
| 执行请求同源检查 | 原 `system/runscheduler` GET 可改变运行状态；目标跨站请求被 403 拒绝 | 保留 `plugin_services.go` 的 Origin/Sec-Fetch-Site 防护与 token 常量时间比较，不为逐字节对齐放开跨站写入 | 安全边界必须保留。当前实际 token/跨站拒绝测试可复用；不是源全鉴权 daemon 等价 |
| 调用作用域、停用/reload 撤销 | 原 `PluginManager.stop`/scheduler 移除声明并不保证能终止任意已运行 Python 方法；目标旧 worker 能力失效并可能取消该进程调用 | 保留当前实例/代际能力和旧 worker 撤销，新的对象必须实际可调用；不让旧调用恢复权限 | 真实服务取消、新 PID、新代成功和冷启动已有证据；源行为差异明确保留，不能称任意旧任务取消语义相同 |
| 回调异常诊断 | 原 `scheduler.py` 882–904、975–1036 将 `str(exception)` 放入事件、通知和进度；空字符串仍是失败 | 旧目标一律泛化文字会丢失普通诊断，属于非安全差异。当前候选增加协商的有界 callback detail，保留 `Error()` 代码协议、host/transport/cancel 不携带文本；宿主先复用敏感信息清洗再展示，不伪造 Python traceback | 协议兼容、原/新 worker、敏感值、空异常及未协商详情撤销已获独立组件验收；最终主树公开 wire / 浏览器仍须收口。安全清洗造成的差异不是普通文本无限披露许可 |
| 本机路径能力 | 原 `LocalStorage.list/get_item/delete` 没有当前宿主 root/private/link/special-file 约束；原某些无效编码路径返回 false/空/null | 保留当前授权根、no-follow、私有目录和根删除等防护。孤立 surrogate 或非法文件名不能经 JSON 替换误指合法 U+FFFD 路径；必要时明确拒绝 | 普通文件/module/native 失败恢复另有实际证据；未跑的权限/身份替换探针不算通过，禁止探针不因提交或默认测试间接执行 |
| 市场展示与制品执行 | 原 Python 元数据的 ID/重复键/字段类型可能不符合 Go 可执行制品格式；原展示并不等于 Go 代码可执行 | 将 display-only 元数据解析与 native executable manifest/path/install 校验分开。普通展示规则按源修复，执行/下载/路径校验不放宽 | 展示差异修复须独立真实 HTTP/浏览器；Python 市场条目不冒充已可安装 Go 插件 |
| 市场请求资源预算 | 独立正常 HTTP 镜像实测：大于 2 MiB 的合法包加无关 padding，源显示一张卡而旧目标为空 | 当前总字节预算保留；这是真实可见差异，不称全市场输入等价，不自动记为用户已批准。不得借忠实迁移取消运行时资源保护 | 必须继续记录完整市场组未闭合的具体边界；有限已验切片不能覆盖该输入 |
| HTTP 序列化失败 | 源不可序列化市场元数据返回 HTTP 500、`text/plain`、`Internal Server Error`；旧候选 JSON 错误体不同 | 按源修复状态与正文；前端兼容失败文本、保留 JSON 响应处理，不把 200 或解析失败当业务成功 | 不再待用户答复。旧 JSON 差异证据保留；当前服务器 d011 和官方浏览器 v3 已通过；有限市场切片于 122fafc7 交付，不能扩为完整09 |
| 统计/评分中心响应预算与凭据 | 源 MoviePilotServerHelper / RequestUtils 对应响应无现有 2 MiB 总预算；原 repo_url 可能含账号和 query | 保留 requestOriginalPluginServer 字节上限与凭据移除，不向测试 peer 发送用户凭据。超预算统计/评分回退、安装报告不标记成功须逐条记录；非敏感 query 同样被整体去除，这是已实证但未批准的普通字段差异；不得把所有 query 都当已知凭据，也不直接取消原安全过滤 | 23/24 新独审发现；不称任意响应大小等价，不从安全凭据清洗推导可删除普通 query |
| 服务公开 HTTP wire | 源实际 ASGI 的 v1/v2 list、Response、422、500、405、307 有不同固定形状；旧 Go host envelope 全面不同 | 专用兼容入口只覆盖固定 /api/v1 与 /api/v2；native /plugins/services 及 unversioned host 扩展保留自身合同，前端显式适配 | 旧 66 个 before 的 18 状态差异与全部 body 差异保留；wire2 source142/QueryParams38、真实 CLI108、独立16,383解码样例和原26条rawquery/slash反例已闭合。当前主树运行/官方浏览器仍未验，不以全局包装“既有”作授权 |
| 组合触发器不推进 | 独审正常 Scheduler/APS 证明 CalendarInterval、AND、OR 及嵌套对象均可达；两个不同周期 Cron 的 AND 在已有 previous-fire 时可能不推进，旧源运行已中止并保留失败 | 正常触发器族必须补齐，不能以“Python 对象”排除。不得为复刻源无限循环而挂住宿主；实现需有明确有界/可取消策略并分别验证普通输入 | `trigger-run03` 的 39 观察、18 实际回调及有限子调用证据已冻结。新 services-triggers 树正在补完整族，主树仍仅三类，38 未闭合；此行是待实现安全合同，不是已验功能或用户批准差异 |
| 分身特殊显示字段与旧 SDK | 实际源 version/icon 字面量可保留孤立 surrogate，name getter 仍正常，而完整 schema/public wire 序列化失败；旧 Go SDK encoding/json 会修复这些码点 | 新候选只在专用内部显示载体中无损保存；native Manifest/能力/身份不改。新 SDK 保原字段名，普通旧 worker 双向兼容另测；旧 worker 对这类新可表达字段不能假称无损，须重编译后才能消费 | metadata05 SDK 合同和实际旧/新 worker 对照正在闭合。该限制不是用户已批准差异；需说明旧目标是否原本可表达该域、现有普通制品是否受影响，不为迁就静默修复丢失源行为 |
| 分身名称/描述 | 原 `core/plugin.py` 的 `re.sub` 与 Python 源字面量可改变字符、导致创建或加载失败 | 名称、可编辑描述按普通文本持久化，UI 外层 trim 单独按源；引号/反斜杠/实际 TAB、冷重启需真实验收 | 两项明确特例。版本、图标的普通字段与特殊字符仍分别核对，不扩大批准 |

## 不用边界掩盖缺项

- 原首页 `AnalyticsScheduler.vue` 是独立的只读服务卡：默认关闭、可从仪表板设置启用、60 秒刷新，显示名称/下次执行/状态。现有“捷径 → 服务”弹窗不能替代它；正在补齐，未因后端可复用而标完整 38/39 通过（scope.discovery.41 属于39，不是 feature41 输入交互）。
- 本机存储共享 `FileItem` 的任意 Pydantic 输入、非有限数/Unicode 数字等，须另按真正源反例收敛；普通 producer 切片不代表全输入转换已闭合。普通远日期 mtime 溢出是产品缺陷，不属于安全豁免。
- 禁止的旧 inode/身份替换/权限试验及 TLS/CA/CONNECT 拦截材料继续仅作历史，不复跑、不改名绕过，不作为新的验收门。
- 本表记录限制，不新增用户问题或暗示已批准所有技术适配。可安全正常对齐的行为继续修；确实不能对齐的安全边界保留并准确披露。

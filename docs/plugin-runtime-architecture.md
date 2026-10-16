# 纯 Go 插件运行与 MoviePilot 插件迁移方案

状态：代码审计与逐插件实现阶段，2026-10-16；不是完整平台或插件迁移完成报告。本文件不包含原始凭据或插件配置值，原 MoviePilot 保持只读。

## 交付含义

沿用已确认的 `design-v1.4.txt` §14：Go 宿主、按需启动的 Go 插件可执行文件、版本化 stdio 协议、SQLite 持久化。不引入 Python、pip、Python 子进程或 Go 动态 `.so`。先使用一个执行协议，不同时建设内嵌和外置两套运行器。

“1:1”按用户实际安装插件的功能、配置项、页面操作和结果逐项对照。MoviePilot 的 Python 包不能直接在此运行；必须用 Go 重新实现其行为并迁移状态。只有界面、配置复制、占位按钮、返回固定成功、模拟数据，都不计作插件实现。已从原运行数据库核查 23 个已安装插件，23/23 实际安装源码已找到；详见 [迁移基线](plugin-installed-migration-baseline.md)。用户再次明确选择全 Go，拒绝 Python 兼容运行环境。

先交付可运行的插件纵向切片，再移植全部已安装插件。通用平台不以“支持任何原版插件”为验收口径；每个原插件有独立迁移条目，未实现或缺依赖明确显示原因，不能显示已迁移/已启用。

## 已有能力与必须补齐的边界

| 代码证据 | 已有能力 | P1 的最小改动 |
| --- | --- | --- |
| `internal/app/extension_contract.go:17,51,123,162` | v1 核心 outbox，事实和业务状态同事务，事件 ID 幂等，按 seq 重放 | 增加持久消费者、投递任务和失败状态；保留 v1 固定事件，不将其改成任意插件 payload |
| `internal/app/store.go:31,96,138,183` | 独占数据目录、单 SQLite 连接、通用 records、AES-GCM 配置加密；启动恢复 running jobs | 增量数据库迁移、插件专属表、绑定插件身份的加密；通用 Put 不适合存插件秘密 |
| `internal/app/jobs.go:15,112,133,204,242` | 入队事务、活跃对象去重、generation、失败退避、取消检查 | 增加 `plugin.run`；核心/插件领取分流；插件运行截止时间、版本固定、外部副作用对账 |
| `internal/app/runtime.go:118` | 两个核心 worker 和调度循环 | 独立一个有界插件 worker，复用 jobs 表；不能用慢插件占满两个核心 worker |
| `internal/app/types.go:248`、`job_summary.go:62` | jobs API 返回 payload，摘要刻意避免凭据/材料 | 插件 job 仅存 run ID、plugin instance ID 和版本引用，禁止保存原配置、参数中的秘密或材料 bytes |
| `internal/app/material_store.go:51,176,208` | 原始种子加密、校验、pin、用途仅 core_download | 宿主内部核对授权和来源后使用；不要向插件开放 resolveMaterial 原始材料通道 |
| `internal/app/http.go:65` | 登录会话、跨站写入检查、统一 API 错误 | 插件管理挂同一认证入口；插件自定义动作不得绕过认证/同源保护 |
| `internal/app/runtime.go:247`、`logs_runtime.go:151` | 核心配置秘密清理，日志字段白名单/轮转 | 增加 plugin_id/version/run_id 等固定字段和插件秘密清理，不能直接记录 stderr/配置/远端响应 |
| `internal/download/client.go:214,244,267` | 添加可暂停、控制/校验/保留数据移除、明确“移动数据”的 Relocate | 高级能力逐项实际验收；Relocate 不能冒充不搬文件的 RelocateExisting，尚无导出种子通道 |

特别说明：当前 jobs 的 `lease_until` 是领取记录，运行中没有通用续租/到期重领循环；进程重启时重新排队。不能声称已经具备跨进程插件租约恢复。初版插件只由单宿主领取，短执行有 deadline；长流程每步持久化并重新入队，禁止持有 SQLite 事务等待插件或网络。

## 原版生命周期对照

| 原 MoviePilot 的实际入口 | Go 中对应行为 |
| --- | --- |
| `app/core/plugin.py:72`：读取 UserInstalledPlugins、按 plugin_order 加载类、init_plugin(config)、get_state 控制事件 | 安装记录决定可用实例；配置修订和 enabled 决定触发；启动恢复宿主持久状态，不执行原版 Python 导入 |
| `app/plugins/__init__.py:53,68,112,120,128`：初始化、状态、配置表单、数据页面、公共服务 | 固定 manifest/描述协议，配置验证、页面模型、动作/服务声明、健康状态 |
| `app/api/endpoints/plugin.py:976`：保存配置，再 init_plugin，再注册服务/命令/API | 配置先验证，密文落盘并产生新 revision；切换新 revision 的声明；应用失败可见，不能把“保存”误报为“运行成功” |
| `app/core/plugin.py:156,619,641`：停事件，close/stop_service，移除实例，重载后发 PluginReload | 禁用先停止新触发，阻止旧 revision 新副作用；在安全边界取消/收尾；重载刷新调度/页面/动作注册，不强杀交接临界步骤 |
| `app/scheduler.py:1197,1251,1338`：以插件 ID 注册/替换/移除 cron/interval/date 服务 | 宿主存储计划，按 due 时间原子入队；插件不自建不可见的无限循环 |
| `app/plugins/__init__.py:241,251,261,272,296,318` | 配置、命名空间 KV、数据目录分别有持久化接口；不能直接给插件完整 DB |
| `app/api/endpoints/plugin.py:648,680,704`、基类的 get_dashboard/get_command/get_api/get_module/get_action 等 | 迁移清单逐项记载配置、页面、仪表盘、命令、API、模块替换和工作流动作；每项真实实现后才标兼容 |
| `app/api/endpoints/plugin.py:993`：普通插件卸载保留配置/数据，分身另处理 | 卸载与“删除配置/数据”分开；默认保留数据，执行中的操作先停/完成安全收尾；分身是独立实例，不靠生成新 Go 源码 |

原版事件有两种语义：`app/core/event.py:139,350` 的广播异步入内存队列；链式事件同步按优先级调用并修改结果。现有 outbox 只能覆盖已提交事实，不能用稍后触发的 job 冒充会修改当前搜索/识别结果的链式 hook。移植插件若依赖链式事件，须在相应核心边界新增**具名、类型明确、有超时的 hook**，并约定可修改字段、排序和错误处理。没有实现该 hook 前，该插件不能标为完整迁移。不要先建任意对象/任意方法的万能 hook 总线。

## 单一运行模型

包内是 Go 可执行文件、manifest、UI 描述/静态资源及内容摘要。官方移植插件可以共用同一 Go runner 的若干固定实现入口，但每个插件的身份、版本、配置和授权仍独立；其调用也通过同一协议，不另开内嵌捷径。插件安装后默认不产生外部业务副作用。只有用户启用的计划/事件规则或手动执行才会启动工作。

manifest 最少包含：`plugin_id`、`version`、`host_api_major/min_minor`、`os/arch`、`entry`、文件 SHA-256、配置/状态 schema 版本、原 MoviePilot ID/版本范围、声明的 actions/events/services、requested capabilities、UI 描述版本。安装先校验身份、版本范围、摘要、文件数量/总大小/路径；拒绝绝对路径、`..`、符号链接、重复路径及越界解压。先写暂存目录并校验，再原子切换版本。摘要证明包完整性，不等于作者可信或签名认证；初期只安装明确选择的可信本地包。

执行用 `exec.CommandContext` 的明确 executable 和固定参数，不经 shell；凭据不进入参数、继承环境或工作目录文件。宿主从执行会话派生 plugin instance、run、version、revision，忽略插件自行填写的身份。stdin/stdout 只承载 JSON-RPC 2.0 消息，双向 method 使用固定白名单。最低协议：

- `plugin.handshake`：协议/插件/实现版本核对。
- `plugin.describe`：配置字段、页面、动作、计划和事件声明，不能执行业务副作用。
- `plugin.run`：动作 ID、非敏感输入引用、触发原因、checkpoint；返回完成/等待/失败和有限结果。
- `host.*`：只暴露当前实际移植插件需要的具名查询或命令，经授权/幂等/暂停校验。
- `plugin.cancel`：协作取消；进程退出兜底。对已提交但结果不明的命令，宿主先对账，不因子进程退出盲目重发。

初始建议硬上限可实现为宿主常量：单 JSON 帧 256 KiB、单次 stdout/stderr 各 2 MiB、握手 5 秒、一次执行 60 秒、全局插件并发 1、每实例并发 1；服务声明和分页读取同样有限。长耗时下载器校验采用“提交校验→保存阶段→稍后观察”的状态机，不能把60秒强制截止理解为允许杀死交接过程。资源上限在验收中用真实负载调整，避免给无边界插件添加无限超时。

平台是**可信本地插件的故障隔离**，子进程不是 OS 安全沙箱。进程拥有应用用户的系统权限时，仍可能自行读文件/联网；不能声称 manifest 已防住恶意本地二进制。宿主 API 执行范围限制是产品契约。开放不可信插件前才另做 OS 身份/挂载/网络隔离，本期不伪装已具备此能力。

## 持久化最小模型

保留现有 records/jobs/outbox；新增以下有唯一约束和索引的小表，避免核心 records.List 500 上限误截断插件数据：

| 表/主键 | 必要内容 |
| --- | --- |
| `plugin_instances(instance_id)` | plugin_id、active version/digest、enabled、desired/applied config revision、runtime state/error、来源原 ID；同一实现可有独立分身实例 |
| `plugin_configs(instance_id,revision)` | 绑定实例/schema/revision 的完整密文、schema version、created_at；授权范围是独立已验证结构，不由任意 config 字段扩大 |
| `plugin_data(instance_id,key)` | 状态 schema/revision、加密值、updated_at；分页/数量/单值上限；历史 data 可能有 passkey，默认按秘密级保护 |
| `plugin_runs(run_id)` | instance/version/package_digest/config_revision/grant_revision、action、trigger、event ID、job ID、密文输入/checkpoint/result、阶段、下一次观察时间；公开部分仅状态摘要 |
| `plugin_deliveries(instance_id,handler_id,event_id)` | 唯一投递、对应 run、状态；另有每实例扫描 cursor。cursor 表示已持久化投递的进度，不表示已成功执行 |
| `plugin_schedules(instance_id,service_id)` | cron/interval/date、timezone、next_due、revision；触发唯一键 `(instance_id,service_id,revision,scheduled_at)` |
| `plugin_operations(instance_id,operation_key)` | 命令类型、规范参数摘要、关联 run、阶段、外部稳定身份、结果；同 key 不同参数拒绝，含秘密的字段加密 |
| `plugin_migrations(source_fingerprint,source_plugin_id,target_instance_id)` | 源版本、转换器版本、校验摘要、各功能/数据迁移状态和错误，不保存明文源配置 |

表和索引在初始化时一次事务建立，不能每个调用临时创建。现 `OpenStore` 只接受核心 migration=1；第一块存储实现采用独立 `plugin_storage_schema=1` 的附加表，不改变核心 v1，未知较高插件 schema 拒绝接入。后续若必须修改核心表，再实现显式原子升级并核对回退兼容性；“保留旧包”不能等同于“数据库任意降级”。本工作不增加数据库备份产品功能。

可以给 `Store` 增加 scoped seal/unseal，复用现有32字节 key，AAD 示例 `light-media/plugin-config/v1/{instance}/{revision}/{schema}`。保持原 Seal/Unseal 的旧配置兼容。对不同实例/用途换行密文应解密失败。插件公开配置投影由 schema 提供的字段/secret 标记生成，秘密只返回 configured 状态。更新以显式 `secret_updates` 的 set/clear 操作区分“保留”“替换”“删除”；不回传原始秘密，也不能把 UI 掩码当真实值写回。需要账号/地址绑定的 secret，在目标改变时执行对应连接器的重新授权规则，不能将旧秘密自动发给任意新地址。

## 触发、并发和恢复

1. 手动操作：验证安装/启用策略、版本、动作输入和授权；同事务写 run + job。写入动作按当前权限检查，不能因配置声明而得到任意目标。
2. 事件：分页扫描核心 outbox；同事务写匹配投递、run/job 和扫描 cursor。提交失败不前进；重复扫描被唯一约束去重。首次启用从当前高水位开始，历史补跑只能显式选择；停用期间的事件默认不补跑，恢复启用时记录跳过范围。已有失败投递仍可见且可手动重试。
3. 调度：以持久 next_due 选择到期项，事务内写唯一触发 run/job 并推进 next_due。采用原插件服务的 cron、时区和 misfire 语义；停机补跑策略显式定义，默认合并一次，不能无声启动数千条过期工作。
4. 领取：两核心 worker 排除 plugin.run，一个插件 worker 只领 plugin.run。claim 选择器和实例串行锁在宿主内控制；数据库仍是唯一持久工作来源，不能放内存 channel 后就算接受。
5. 运行：只读取所固定 revision；版本/hash 不同不能用新实现继续旧 checkpoint，须匹配 converter 或标 needs_review。任何 SQLite transaction 都在外部调用之前提交并释放（当前连接上限1）。
6. 取消/禁用/暂停：停止新自动触发、禁止新副作用。当前执行在阶段边界退出；交接临界阶段由宿主保存恢复/收尾意图并完成必要收尾。每次命令再次查 enabled、revision、授权、generation/global control，不只领取时查一次。
7. 崩溃/超时：命令分为 prepared/submitting/reconcile_pending/confirmed；外部效果与 SQLite 无法原子提交。重启先凭实例+稳定 torrent hash/外部 ID 查询，确认已有结果再推进，不能使用固定三次任务重试代替副作用对账。

`finishJob` 目前会把所有 failed job 写成 v1 core job.failed；增加 plugin.run 时须明确分流，插件失败不能伪装为 core_download/core 来源。保留现有 v1 校验，另用插件运行记录/投递失败表示；真正需要业务插件事件时定义新版本封套，带原始 causation 和 origin，限制自触发递归。

## 宿主接口与辅种/转移边界

只实现实际移植插件需要的接口：分页查询授权站点/下载器/资产、站点搜索、持久 KV、通知、页面数据、具名的辅种/迁移计划及观察/执行。每次调用按当前会话的插件实例、capability、目标 ID 集合和目录范围核对；不接受 plugin_id 自报、不接受任意 URL/RPC/SQL/shell、不输出完整核心 Settings。

凭据由宿主连接器使用；插件拿 handle/reference。站点搜索结果及文件名也可能含敏感内容，结果按实际用途分页返回，不进入日志。种子材料始终由宿主校验、解析、添加到授权下载器，插件可查文件清单/稳定 hash，不能拿包含私有 tracker 的原 bytes。

P1 cross_seed/migrate 是操作目的和文件引用关系，不是永久“做种任务”。不得仅扩大 core_download 的 purpose 校验就宣称完成。专属计划记录来源/目标实例、材料、文件布局、持有者、校验和交接阶段。资源重用不占第二个 acquire 名额；同一目标/材料/目的有唯一约束。

- 辅种必须停止态添加→实际完整校验→确认完整后启动；匹配失败不启动补下载、不改原共享文件。
- 转移必须目标停止态添加/校验完成→源暂停确认→目标启动确认；失败按持久阶段恢复或等待对账。默认保留源任务和数据。
- 所有权不能仅由下载器标签推断；未接管任务只读。需要管理外部已有任务时，先明确接管范围并核对稳定身份及材料。
- `Relocate` 的现有实现确实搬文件，不可用于“只引用现有数据”。缺能力/缺材料应显示不能执行，不能假装支持或绕过校验。

## 管理 API 与原版界面

沿用当前 `/api/` 认证入口，建议固定 `/api/plugins` 管理列表/安装，`/{instance}/config|enable|disable|reload|runs|page|actions` 等具名子路由，`/api/plugin-migrations` 生成/执行迁移计划。读页面为 GET；安装、启停、重载、执行、更新、重置用非 GET 并校验同源。绝不开放 arbitrary method 反射执行。回调 API 若确有原插件需要，单独注册受限路由及认证策略，不能整个插件默认 anonymous。

配置、详情、已装/市场切换、启停、手动运行、服务、日志/历史、卸载保留数据语义按原版逐项实现。配置表单先提供能覆盖当前实际插件的白名单组件和显隐/默认值；Vuetify 描述中未知组件/表达式需明确提示迁移缺口，不能丢字段后仍报保存成功。原版 Vue/federation 页面以本地 Vue 模块重做相同操作；不能直接运行来自迁移包的任意模板表达式或远端脚本来代替功能迁移。

公开响应只提供经过 schema 筛选的 config、状态摘要和页面模型。运行错误保留明确 error_code、阶段、下一步；插件执行出错、没返回结果、stdout 非协议、未验证都不能返回 success。原版插件的更多入口（仪表盘、导航、认证提供者、命令、工作流动作、链式替换）由迁移矩阵决定本期实际必需接口，逐项补齐，不能只做配置弹窗称1:1。

## 迁移步骤与完成证明

1. **只读盘点**：从明确目标取得 UserInstalledPlugins、plugin.ID config、plugindata、config/plugins/ID 数据文件、安装源和版本/摘要；同时确认分身、已禁用插件、实际服务/命令/API/事件/hooks。原版 `SystemConfig`、`PluginData` 均可含秘密，读取用内存流/受限本地快照，不输出聊天/普通日志，不导出整个无关数据库。
2. **逐插件规格**：读取该固定版本代码和页面。记录原 ID/版本、每个表单字段/默认/校验、每个开关和一次性动作、事件/定时规则、依赖、持久数据 schema、外部行为、页面/日志、验收方式。动态 Python/外部命令依赖要有实际 Go 替代，无替代则明确阻塞。
3. **真实移植**：Go 实现动作、查询/状态、页面；添加对应宿主能力，采用合成 HTTP/文件系统夹具验证业务结果。操作时机、批量范围、失败重试及清理语义都要对照，不只翻译函数名。
4. **转换预览**：针对 source ID+version 的 converter 输出字段/数据计数、目标实例映射、unsupported 字段和待办；秘密只显示 configured。不把未认识键静默丢弃；未知值可密封保留在迁移来源记录，但不能视为已生效。
5. **幂等导入**：同事务写密文配置、转换状态和迁移记录；相同 source fingerprint 不重复创建/运行；来源发生变化或目标已有用户修改则提示冲突，不覆盖。原项目所有数据保持只读。
6. **验证后启用**：默认导入禁用，连接/只读结果先校验。一次性开关（例如 onlyonce）、过期 date 计划和通知等不得因导入自动重新执行；按原实际语义显式转为一次运行。启用前显示本次触发范围；已授权迁移可按已确认范围继续，不重复无谓询问。
7. **功能完成**：每个原已装插件均有“Go 实现版本＋配置/数据转换结果＋真实业务行为证据＋UI操作对照”；未通过项是待完成而非成功。合成测试、实服务只读联调、真实写入验收分别标记。实际外部写入在已授权目标/范围内完成，不能以拥有凭据扩展范围。

日志增加固定 `plugin_id/plugin_version/plugin_instance_id/plugin_run_id`，贯通 job/workflow/causation。宿主清理插件秘密的原值与常见编码，限制捕获长度，拒绝记录配置对象/请求体/材料。插件协议和 stderr 按不可信输入验证，不原样入库；无法解析的 stderr 只报告固定错误和字节数。对插件运行状态、积压、重试和迁移错误保持可见。

## 建议实施顺序与文件边界

| 次序 | 直接实施位置 | 必须先通过的验证 |
| --- | --- | --- |
| A：契约/状态 | 新 `internal/plugins/` 的 manifest、协议 DTO/校验；新 `internal/app/plugin_store.go`；独立插件 schema 初始化 | 非法包/不兼容版本、作用域密文互换失败、初始化/事务失败回滚、公开投影不泄漏、保留/清除秘密 |
| B：真实运行纵向切片 | 新 `internal/pluginhost/` 的 stdio runner；`plugin_runtime.go`；jobs 最小分流；一个真 Go 插件 | 子进程实际执行＋host查询/写持久 KV，超时/崩溃/取消/输出超限仍不阻塞核心，旧任务不偷换版本，空闲没有插件进程 |
| C：管理操作/UI | `plugin_http.go`、受限 UI 模型；前端 Plugins 与配置/详情/运行记录组件 | 原版操作对照；未登录/跨站拒绝，保存失败保留表单，启停/重载/卸载正确，真实动作结果和日志可见 |
| D：事件/计划 | `plugin_delivery.go`、`plugin_schedule.go` 与持久表 | commit之前无投递、重复扫描不重复执行、cursor与投递同事务、时区/misfire、重启和禁用规则 |
| E：逐插件移植/数据迁移 | `internal/plugins/<id>/`、`cmd/lm-plugin/` 或独立包；`plugin_migration.go` 和每插件 converter/tests | 对实际安装固定版本的功能矩阵；配置/数据正确且原服务未改；只迁移配置不能通过 |
| F：辅种/迁移的宿主操作 | 新具名 seed plan/operation 状态机和下载器实际缺失能力；相关插件 | 两客户端双向、真实校验、同名同大小不同内容拒绝、重复/超时对账、目标失败恢复源、默认不删数据 |

A/B 之后立即接实际插件切片，不等待通用市场、通用脚本引擎、任意第三方远端 UI 或全量 hook 引擎。D 与 E 按具体插件所需并行展开；最终完成标准是全部已安装插件逐项通过，不是框架接口齐全。资源验收统计宿主和所有子进程的 CPU/RSS/I/O，不能只报告 Go 主进程内存。

存储实现位于 `internal/app/plugin_store.go` 与 `plugin_store_test.go`：独立版本化附加表、准备态安装元数据、全密文配置/KV、修订冲突、迁移 run/收据和原子幂等导入、卸载保留/重置删除。config 和 KV 合并在带 kind 的 `plugin_values` 表，manifest 也加密；存储层本身不启动插件或产生外部操作。App/HTTP 与首个真实插件的后续接入见下节，不代表完整平台已可用。

`go test ./internal/app -run '^TestPluginStore' -count=1` 与对应 `-race` 测试验证真实临时 SQLite、重启、实例/用途/键密文隔离、DB/WAL无合成秘密明文、晚期收据写入失败整体回滚、导入不创建任务、重放不覆盖用户修改、卸载/重装/重置和无关插件不变。这些是存储测试，不是业务插件迁移或生产运行证据。

## 当前已接入的代码与剩余范围

- `internal/plugins/` 已有真实 JSON-RPC 2.0 stdio runner 与 worker SDK；每次在释放配置前核对协议/版本/身份/方法/能力。反向宿主调用绑定实例，stdout/stderr、帧、调用次数与 deadline 均有限制。宿主不继承管理凭据环境，子进程空闲时不常驻，Go worker 软内存限制为 64 MiB；这不是 RSS 实测或 OS 沙箱。
- 官方内置 Go 实现暂随主程序编译，以同一可执行文件的 `--plugin-worker ID` 模式执行，仍走相同进程协议。尚无独立包安装器、市场仓库同步、包摘要/签名及升级回滚，不把内置目录称为兼容所有原版插件包的市场。
- `plugin_runs.go` 已持久化私有输入快照和结果，安装/保存配置与入队同事务；独立单并发插件 worker 不占核心两个 worker。每次宿主写入核对安装 generation、固定版本、配置修订和执行状态。内部配置规范化和清除一次性开关会在同一事务更新同 generation/revision 的排队快照，保留事件输入；用户修改仍取消旧快照。配置/输入/结果不进入公共 run JSON；元数据和日志带插件/实例/执行 ID。
- 原版刷流历史单值只读实测约 7.16 MB，因此 KV 单值上限改为 16 MiB、单次导入 64 MiB；配置、manifest、IPC 执行输入继续 1 MiB。大 KV 后续需宿主分页访问，不扩大单帧到整段历史。所有已安装插件的数据转换尚未执行。
- `CategoryEditor 1.3` 已有原版表单、写入 YAML、自动关闭一次性开关与系统通知。分类引擎已补通用 TMDB 字段、逗号与空白语义、数字范围、字符串范围端点、排除项、Unicode 大写、YAML 别名与合并顺序；额外元数据只保存比较摘要，凭据回显先清理。数字范围按区间比较，不展开大数组；限制 YAML 循环/展开和字段数量。默认模板与实际原版容器 `/app/config/category.yaml` 的 SHA-256 同为 `1095b3aeb315569f541fded31bc77f43fa625cb9ac4844e3a28ba7d7868b2303`，未更改原用户策略。实际硬链接整理测试验证额外条件改变分类结果。
- `CustomHosts 1.2.1` 已有原版字段、IP/IDNA 规范化、重复地址/域名跳过、错误行保留与通知、全非法输入保留旧文件，以及停用恢复原前缀。宿主只开放容器 `/etc/hosts` 的读取和带摘要条件的专属尾段替换；拒绝符号链接与前缀改写，写失败不报成功并关闭启用状态。启动重应用已保存的活动配置，迁移暂存实例不执行。重置/普通卸载沿用原版不自动清除 Hosts 的行为。
- `SystemProcess 1.1` 已有原版配置、PID 倒序详情表格、空状态、CPU/内存/运行时长与仪表板数据接口。采样使用固定版本 [gopsutil v4.26.9](https://github.com/shirou/gopsutil/tree/v4.26.9) 的纯 Go 实现，读取当前进程命名空间，不增加 host PID 挂载；不查询命令行/环境。CPU 首次为 0，后续按两次查询之差计算，PID 重用重置采样；运行时长保留原版 `timedelta.seconds` 的 24 小时取模。未实现仪表板个性化布局前，不将数据接口称作仪表板交付完成。
- 前端使用原版同版本 Vuetify 3.7.3 与 Ace 编辑器，按需加载；配置弹窗 60rem，详情弹窗 80rem、窄屏全屏，并可从详情切换设置。已接 JSON 表格组件，数字 0 不丢失；限制描述规模和组件范围，不执行 HTML/脚本。修正全局输入样式与插件控件双边框/高度冲突。当前组件不足全部 23 个插件，动态表达式/插槽、联邦页面、文件夹/排序/分身、原版完整菜单、完整仪表板等仍待实现。
- HTTP 已接安装目录、已安装列表、表单、CAS 保存、重载、重置、保留数据卸载、执行元数据、只读详情和仪表板数据；经过现有管理员认证和同源检查，页面查询与执行共用单进程限额。通用事件投递、cron、专属 API/命令、完整迁移工具、另外 20 个插件业务及正式服务切换均未完成。

验证包含本地测试/浏览器隔离实例与 NAS 独占容器，尚未部署正式服务、迁移正式插件或停止原版服务。整体目标保持“完整原版插件平台与全部 23 个已安装插件”。

本批 `make check` 通过前端干净安装/类型检查/构建、全部 Go 测试、全部竞态测试、`modernconly` 纯 Go 测试、vet 与模块校验；前端 114 项测试通过。浏览器在独立实例验证插件安装、实际进程详情、切换设置、启用保存和重启后保留配置；1280×720 与 390×844 检查了弹窗、字段和入口。后续表单样式修正已重新构建并在浏览器确认两个控件均为 56px 高度，手机配置全屏可操作；进程初始化重启恢复另外通过定向测试。Ace 所在按需加载块仍有超过 500 KiB 的构建提示；没有将此测试结果称为资源基准。

NAS 的 Hosts 夹具以 UID 1000/GID 1001、只读根文件系统、无外部网络、移除全部 capability 运行，仅独占 Hosts 文件可写。真实 Go 子进程写入后，Go resolver 和 HTTP 客户端通过自建域名连接本容器测试服务器；停用后逐字节恢复前缀，测试通过。另一个同样隔离的容器通过了实际进程、配置、详情/仪表板接口测试；夹具容器均已退出并清理。没有挂载 NAS 进程、媒体目录、原配置或凭据。

正式新服务目前同样以普通用户及只读根文件系统运行，因此上线 Hosts 功能前必须为**新容器单独的 `/etc/hosts` 文件**提供可写 bind mount，先创建普通文件并设为应用 UID 可写，再启动容器；不能把 NAS 宿主机 Hosts 挂为可写，不能为此改用 root、关闭只读根文件系统或扩大 capability。此部署准备尚未作用到正式服务。

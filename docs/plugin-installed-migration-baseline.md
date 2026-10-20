# 已安装插件迁移基线

最新交付（2026-10-21）：新服务已部署 v0.4.6，**二级分类策略 1.3、插件彻底卸载 2.2、共享识别词 2.4.1、系统进程 1.1、MoviePilot服务监控 1.4、自定义Hosts 1.2.1 已完成正式迁移；当前 6/23 完成，其余 17 项未完成**。自定义Hosts保留原启用配置，使用新容器独占可写文件；原插件内存模拟、实际生效文件、配置、原版表单与成功执行记录均核对，旧服务和既有配置/五份偏好保留。详见 [本轮交付](plugin-custom-hosts-delivery.txt)、[最新迁移收据](plugin-custom-hosts-deployment.json)；服务监控及此前交付保留历史记录。下方带旧日期的记录不代表全部可用。

2026-10-20 共享基础补充：[原版请求与调度设置](source-settings-migration.md)已随 v0.4.2 部署，并正式导入 4 条密文记录，区分配置 TZ 和本地时区。代理认证修复亦已部署；共享配置迁移不额外增加插件完成数量。

核查日期：2026-10-16（Asia/Shanghai）。来源：NAS 原版 MoviePilot 的只读 SQLite 查询 UserInstalledPlugins，与 /app/app/plugins、/config/plugins 已安装源码的 AST 元数据对应；未导入配置值或运行数据。23/23 插件源码已找到，合计 36717 行 Python（不含前端静态包）。此清单是迁移范围，不是完成声明。

| ID | 名称 | 原版本 | 原状态 | 数据键数量 | 状态 |
| --- | --- | --- | --- | --- | --- |
| CategoryEditor | 二级分类策略 | 1.3 | 停用 | 0 | 2026-10-20 已正式迁移并冷启动/UI核对；原停用配置保留、安装与收据各1、0任务/KV/新增通知，分类文件未改写；实际编辑/分类及错误缓存行为已在独占实例验证 |
| TorrentTransfer | 自动转移做种 | 1.10.3 | 启用 | 418 | 2026-10-18 已接实际 Go SDK/原生双下载器、原表单、调度、历史、通知、生命周期与冷恢复；配置及418项KV未迁移，未部署 |
| SiteStatistic | 站点数据统计 | 1.9.7 | 启用 | 0 | Go 快照统计、原版表单/卡片/饼图/明细、一次性刷新、持久站内通知及按域名刷新 API 已接入；通知通道、完整账户字段/域名别名、历史/配置正式迁移和仪表板布局仍待完成 |
| RemoveLink | 清理媒体文件 | 2.17 | 停用 | 0 | 已接正式catalog、真实SDK终态调用、连续owner/Guard/Core Effects、配置纠错/目录扩展/重启及卸载；NAS App61项和host44项通过。后续已接精确无hash事件发布/站内历史转交；NoticeMessage路由与外发、正式迁移和部署未完成 |
| MPServerStatus | MoviePilot服务监控 | 1.4 | 启用 | 0 | 2026-10-21 已随 v0.4.5 正式迁移；原配置1条加密导入，既有五份布局/显示偏好保留，原单widget空key对齐。详情/表单/六卡、增量采样/缓存/刷新与冷启动已验证；生产真实公共状态及速率已核对，1次成功初始化、0计划/事件/其他业务运行 |
| PluginUnInstall | 插件彻底卸载 | 2.2 | 停用 | 0 | 2026-10-20 已随v0.4.1部署并正式迁移；原true/true及空选择保留，安装与收据各1、0运行/KV/计划/事件/卸载步骤，实际生产表单已核对；失败后的warm/durable、连续新执行、重装及自卸载已在独占实例验证；原全局顺序/布局仍待后续独立迁移 |
| AutoSignIn | 站点自动签到 | 2.9.14 | 启用 | 24 | 主流程、22类适配、原表单/详情及逐效果StateJournal已迁入正式Go包；实际SQLite分行/owner、内部配置CAS与旧admission归档切换已有NAS证据。实际Manager/调度/消息及Browser/OCR接线、资源按需加载/回收、注册、配置/KV迁移和实机验收未完成 |
| MergeMessageNotify | 聚合消息通知 | 1.20.1 | 启用 | 0 | 主流程/17渠道构造/SDK及SMTP依赖已迁入正式Go包，28项NAS隔离测试通过。原表单页签/子弹窗/点号键已用实际ConfigDialog验证；完整Mako/Apprise、正式宿主/消息生产者及迁移未完成，不在可安装目录 |
| IYUUAutoSeed | IYUU自动辅种 | 2.19 | 启用 | 325 | 2026-10-18 已接正式 Go handler、实际 SDK/原生下载器宿主、持久队列和原日志；默认目录/ATM 落点及无需整理路径映射的原生授权已验证并纳入正式代码，325 项 KV、配置、引用尚未迁移，未部署 |
| SystemProcess | 系统进程 | 1.1 | 启用 | 0 | 2026-10-21 已随 v0.4.3 部署并正式迁移；原配置与同一用户五份仪表板偏好一次事务加密导入，保留来源启用。实际详情、刷新、布局与冷启动已验证；1 次成功初始化，0计划/事件/业务执行，页面读取后配置与五份偏好不变 |
| MediaServerRefresh | 媒体库服务器刷新 | 1.3.3 | 启用 | 0 | 已接 Go 子进程、持久批次、飞牛刷新、停用/重载/卸载/关闭收尾及核心核对交接；已有独占测试库实机证据，正式配置引用映射和迁移尚未执行 |
| RemoteIdentifiers | 共享识别词 | 2.4.1 | 停用 | 0 | 2026-10-20 已随 v0.4.2 部署并正式迁移；保留来源配置缺失、KV/文件/全局识别词缺失，安装与收据各1、0配置/任务/计划/事件。真实页面、Go SDK、认证请求、词表应用及冷启动已在独占实例验证，生产读取默认表单0写入；完整来源全局顺序与布局仍待独立迁移 |
| SubscribeGroup | 订阅规则自动填充 | 2.8.7 | 启用 | 2 | 已接 Go SDK、原表单/历史页、创建及实际下载事件、分类覆盖和下载补空宿主；两套动漫 Go 状态机、订阅局部识别词及下载配置冻结已验证。分块传输及4,500条完整历史的工作器/HTTP/网页已验证；任意合法大历史的流式构树、完整RSS上下文、规则测试工具及正式配置/KV/引用迁移未完成，尚未部署或验收为1:1 |
| AutoBackup | 自动备份 | 2.1.4 | 启用 | 0 | 已接实际App授权、原表单/SDK、持久once/cron/API、一致SQLite快照及密钥恢复、归档/保留/WebDAV、通知队列与生命周期；独立恢复证据、未知执行核对及零效果重试已验证。已进入可安装目录；真实外部通知渠道、原配置及7个历史文件迁移仍未完成 |
| BrushFlowLanduo | 站点刷流-landuo | 5.2.15 | 启用 | 24 | 已接原生执行入口、实际SDK/owner/原表单及source容量快照；真实质量提升下载替换和完成通知继续组合验证，尚未完成正式配置/KV/引用迁移，不计已完成 |
| MergeSiteSwitch | 聚合站点开关 | 1.2 | 停用 | 0 | Go 核心站点范围、原版动态表单、持久跟随及关联配置事务已接入；数字/自定义引用协议、不可变执行输入与已提交保存的丢失回复恢复已验证。旧来源采集、关联插件实际业务联动、正式配置和引用迁移仍未完成 |
| TorrentTransfermusic | 自动转移做种 分身 | 1.10.3 | 启用 | 36 | 2026-10-18 已接独立 Go 实例、原表单及与本体相同业务，配置/历史/执行命名空间独立；原配置及36项KV未迁移，未部署 |
| ScrapeFileClean | 源文件联动清理 | 1.0.7 | 停用 | 0 | 原算法、11字段原表单、Go SDK、67组完整原执行/224组helper/8组watchfiles参考与NAS隔离验证已完成；生产监听、删除宿主和正式迁移未接入，不在可安装目录 |
| LarkMessager | Lark 应用消息通知 | 0.9.1 | 启用 | 2 | 主流程、13个消息模块、API/回调与受控HTTP已迁入正式Go包；1,376条原执行及28项NAS隔离测试通过。实际App持久宿主、可信消息入口、完整动态输入及配置/KV迁移未完成，不在可安装目录 |
| CleanInvalidSeed | 清理无效做种 | 2.1 | 启用 | 0 | 已接正式catalog、真实SDK终态/owner/Core、配置生命周期与调度；下载器写前拒绝与未知写分开留证，源通知路由、私有事件和持久队列已接入。本轮107项联合NAS保留独立证据；原43/148项及mutation98项证据保留。可信远程五命令入口、真实通知渠道/完整依赖及正式迁移未完成 |
| SeedRescue | 稀缺保种 | 1.6.3 | 启用 | 3 | 2026-10-18 已接正式 Go 宿主、真实 SDK、原六 API、持久扫描及原版 Vue 配置/数据/全页工作台和侧栏；配置与3项KV、用户引用/布局未迁移，未部署 |
| CloudflareSpeedTest | Cloudflare IP优选 | 1.5.1 | 停用 | 0 | 2026-10-18 已接正式 Go handler、实际 CLI/下载校验/hosts 宿主、原表单和持久 once/cron/API；统一认证消息命令入口待接，配置及12个文件尚未迁移，未部署 |
| CustomHosts | 自定义Hosts | 1.2.1 | 启用 | 0 | 2026-10-21 已随 v0.4.6 正式迁移；启用配置1条AEAD导入，独占容器Hosts可写挂载，原模拟文件与运行配置一致；生产原版表单和1次成功初始化已核对，0其他运行/计划/KV。真实域名连接及停用恢复在NAS独占容器验证，原服务/Hosts/既有配置与偏好保留 |

包含独立的 TorrentTransfermusic 分身，不与原实例合并。未安装但留有历史配置/数据的旧插件不混入已安装范围；需在迁移包中另行标记。迁移前须按实际 URL/实例/路径建立站点、下载器、媒体服务和目录引用映射。配置及缓存中的凭据只能以应用密文落盘，日志、普通 API 和报告不得含原值。

每项完成须同时取得：对应原版行为对照、实际可执行实现、配置/数据/文件迁移收据、引用映射核验、适当的协议/合成与实机证据。平台界面出现卡片不构成该插件已迁移。

以下为截至2026-10-18的历史阶段：当时原版配置、KV与文件均未导入正式新服务；18 个可安装处理器已有不同程度的实际 Go 行为，不能表述为“18/23 已迁移”。其余 5 个尚未进入可安装目录：ScrapeFileClean、AutoSignIn、MergeMessageNotify、LarkMessager、BrushFlowLanduo；其中前四项已有正式 Go 核心和不同范围的对照验证，完整宿主或依赖仍待接入。完整业务及平台完整交互继续保留在交付范围内。定时触发器和持久队列已接共享识别词、站点统计及清理无效做种；其他具体业务插件注册、公共服务界面和逐项迁移仍需完成，不能因此增加已迁移插件数量。

同日只读核查原版 `/config/plugins/<原插件ID>` 的文件元数据：AutoBackup 有 7 个文件、51,658,600 字节，最大单文件 7,414,774 字节；CloudflareSpeedTest 有 12 个文件、15,871,166 字节，最大单文件 7,930,040 字节。两处均无符号链接或特殊文件，其余 21 个已装 ID 未发现对应数据目录。本次没有读取/导出文件正文，也没有把备份包放入 Git。后续迁移须分别保留这些文件的内容和校验收据，密文落盘；不得把旧可执行文件复制后直接运行，也不能因 KV 为 0 就漏掉文件迁移。

聚合站点开关对 SiteStatistic 的字段更新和真实 Go 初始化已联调，不使用可用性测试替身；保留统计插件启用状态及其他选项。原版 SiteStatistic 1.9.7 未读取 statistic_sites，不能把该字段自行解释为图表/采集过滤条件，实际数据仍按核心启用站点展示。AutoSignIn 的实际 Manager/宿主仍在独立候选接线，IYUUAutoSeed 已接正式 Go 宿主及默认目录/ATM 观测，但整体业务/迁移验收仍待完成；未就绪依赖会阻止整次聚合更新，不能用测试替身的配置/队列结果代替该证据。原版只识别 BrushFlow，不把当前安装的 BrushFlowLanduo 自动视为同一插件。

自定义 API 的已实现行为、24 组原 ASGI 对照、鉴权/恢复/NAS 证据和未完成边界见 [plugin-http-api-verification.md](plugin-http-api-verification.md)。已装源码中的 24 项 API 声明不等于 24 项业务均已实现，本批只接通 SiteStatistic 的按域名刷新；没有增加“已迁移”插件数量。

订阅规则自动填充的源码行为、规则对照和核心事件进度见 [plugin-subscribe-group-port.md](plugin-subscribe-group-port.md)。该插件已加入可执行目录，并在隔离实例实际安装、配置、初始化和执行；这些证据不等于正式用户配置和数据已迁移。原版两个 KV 的只读元数据核查显示 `history` 为列表、8 条、1,992 字节，`history_handle` 为列表、8 条、230 字节；没有读取输出正文或正式导入。当前用户数据规模不构成取消未来大历史传输要求的理由。大消息已证实范围见 [plugin-large-message-audit.md](plugin-large-message-audit.md)，新增卸载宿主及原管理器行为见 [plugin-uninstall-go-port.md](plugin-uninstall-go-port.md)。

优先级规则依赖的 Go 内核、202 组双路径样本、88 组原版排序参考、原版规则管理页、分区保存边界及实际关键词/订阅搜索证据见 [priority-rules-go-port.md](priority-rules-go-port.md)。原用户规则还未正式迁移，完整元数据、独立规则测试工具和所有订阅识别来源仍在范围内。下载资源五字段与季号的已确认行为、算法剩余差异见 [release-resource-metadata-go-port.md](release-resource-metadata-go-port.md)。

附加订阅过滤已接通原版质量/分辨率/特效表达式和编辑控件，313 组原调用参考、真实搜索进度前过滤、保存/重载及边界见 [subscription-additional-filters-go-port.md](subscription-additional-filters-go-port.md)。这是订阅规则插件的核心依赖，不增加已迁移数量；新增分类宿主事务进度见上述专文，下载宿主和原数据迁移仍待完成。

订阅局部识别词、原文保存和自动 Intent 的完整识别配置冻结见 [subscription-recognition-scope.md](subscription-recognition-scope.md)。Rust 与 legacy 两套单标题动漫流水线均已由 Go 执行；两套引擎、实际 wrapper、源错误及字符表证据见资源元数据专文。这些结论不能扩大为完整普通视频身份识别、RSS缓存上下文或原生下载目录监听始终恢复选择快照。

完整 MetaInfo / MetaBase 标题对象、修改方法、保存格式及拼音依赖已移入正式 Go 包，来源、2682 个完整标题参考、源异常和边界见 [metainfo-go-port.md](metainfo-go-port.md)；完整RSS对象纯包已接同一真实入口，110条原链路参考通过，见 [rss-context-full-go-port.md](rss-context-full-go-port.md)；尚未接入 App 消费者。路径识别与 RSS 缓存的剩余边界、实际安装版差异和逐项验收顺序见 [rss-context-go-plan.md](rss-context-go-plan.md)。RSS 标题重建不依赖 MetaInfoPath，二者分别推进和计证。

服务监控的完整原树、固定网络适配、缓存/配置并发和页面验证见 [plugin-server-status-go-port.md](plugin-server-status-go-port.md)。清理媒体文件的已移植纯算法与尚待接入的生产副作用边界见 [plugin-remove-link-go-port.md](plugin-remove-link-go-port.md)。

RemoveLink 连续本地宿主、正常关闭恢复、Linux原生事件、文件身份、逐动作收据及owner配置切换/显式reload见 [plugin-remove-link-host-go-port.md](plugin-remove-link-host-go-port.md)。宿主方法和SQL切换测试尚不等于App管理入口、实际Guard/Effects与SDK恢复已接通。

私有SQL journal的字段、分行加密、同事务updateTx和恢复验证见 [plugin-remove-link-journal-go-port.md](plugin-remove-link-journal-go-port.md)。文件夹管理的原raw响应/路由兼容、20条实际ASGI参考与新NAS联合证据见文件夹专文；用户布局、全部23个插件配置及文件的正式迁移数量仍为0。

插件文件夹/排序的现有可操作交互与原数据格式、额外平台限制和分层验证见 [plugin-organization-go-port.md](plugin-organization-go-port.md)。原用户布局仍未迁移，不能增加已迁移插件数。

源文件联动清理的纯包及与 RemoveLink 不同的 Stop、known_paths、覆盖移动、刮削保护和根目录行为见 [plugin-scrape-file-clean-go-port.md](plugin-scrape-file-clean-go-port.md)。没有把纯算法或 SDK 测试当作生产删除宿主已就绪。

AutoSignIn 的 Tjupt 图像处理参考与纯Go依赖边界见 [plugin-auto-signin-image-hash-go-port.md](plugin-auto-signin-image-hash-go-port.md)、[plugin-auto-signin-jpeg-go-port.md](plugin-auto-signin-jpeg-go-port.md)。已验证JPEG现走专用纯Go解码，明确未覆盖格式仍失败，不以缩放样本或其他格式通过替代整个签到插件完成。

清理无效做种的完整原执行参考、原生属性/包装器、Go SDK与未接宿主边界见 [plugin-clean-invalid-seed-go-port.md](plugin-clean-invalid-seed-go-port.md)。源消息中的“已删除”计数不等于下载器实际成功收据，迁入纯包不表示已清理任何用户种子。

RemoveLink 已接入的原操作、连续owner、deferred终态RPC、身份/生命周期/恢复与NAS证据见 [owner接线](plugin-remove-link-owner-go-port.md)、[终态执行与恢复](plugin-deferred-owner-execution.md)。原配置、数据、文件尚未导入或部署。后续精确无hash事件发布/站内history_saved见[原事件转交](plugin-remove-link-outbox-go-port.md)，NoticeMessage路由与外发仍未完成；不能声称已删种或外发已送达。

CleanInvalidSeed 原生完整对象、7固定调用所需的正式下载器协议扩展与边界见 [下载器依赖](plugin-clean-invalid-seed-native-download.md)。它没有扩展主下载管理展示范围，也不是已清理任何实际种子的证据。

AutoSignIn 正式单一引擎、22类适配、逐效果收据和API/Inventory恢复修正见 [Go主流程与恢复](plugin-auto-signin-go-port.md)。生产SQL宿主仍在接入，独立FileStore和旧末尾checkpoint Machine仅为测试夹具；注册、部署和原数据迁移数量不增加。

AutoSignIn 已有实际 App SQLite 分行加密、原运行记录核验、内部配置 CAS 链和容量预留。13项NAS隔离测试不含尚在开发的外部配置切换/Manager/调度接线，详见 [SQL宿主记录](plugin-auto-signin-sql-go-port.md)；正式迁移数量仍为0。

MergeMessageNotify 正式主流程和 SMTP 依赖的原始对照、28项NAS证据及完整 Mako/Apprise、动态类型、表单和宿主未完成项见 [Go 主流程与 SMTP](plugin-merge-message-go-port.md)。真实 SMTP 回环证明不等于实际账户投递、正式注册或配置迁移。

AutoSignIn 原生配置切换已保存旧 admission 原 Scope/配置链/未投递通知，并在真实 drain 证明后原子转交 owner 文件锁；统一24项NAS测试包含6个切换目标，详见 [配置切换记录](plugin-auto-signin-transition.md)。Manager/barrier/历史handoff尚待实际接线，不能据此增加正式迁移数量。

CleanInvalidSeed 的正式宿主、实际 App journal 与后续29项 admission NAS 验证范围见 [宿主与 journal](plugin-clean-invalid-seed-host.md)。真实文件/下载器租约、Core/barrier、配置生命周期、自动/手动调度及目录安装接线见 [App 宿主记录](plugin-clean-invalid-seed-core.md)。可信远程命令、外发通知及正式迁移仍待完成，没有操作用户种子或媒体。

聚合通知原表单需要的页签、子弹窗、完整字面键和链接已补，655节点检查及跨渠道编辑/一次保存的浏览器证据见 [原版表单交互](plugin-merge-message-form-ui.md)。这不是正式安装、账户发送或完整模板依赖的证据。

Lark 原 App Secret 显隐已映射为固定原生动作并验证 password→text→password 与一次保存保留原值，见 [表单交互](plugin-lark-password-ui.md)。原对象 rules 的 warning 如实保留，不声明前端 required 验证完成；整个 Lark 业务及配置迁移仍未完成。

AutoSignIn 新增可选的逐条收据、缓存与运行头读取接口，保留旧完整 State 兼容路径；本地99项与NAS80项纯包检查通过，见 [读取接口与验证边界](plugin-auto-signin-typed-read-go-port.txt)。这份纯包证据不包含后续实际 SQL 切片；后者的6项真实 App 测试已在148项普通/竞态/NAS联合检查中通过，见 [实际 SQL 读取记录](plugin-auto-signin-typed-sql-go-port.txt)。两者均不证明完整运行内存、资源回收、Manager接线、站点签到或数据迁移完成。

Lark 的正式纯Go算法、原执行参考、来源许可与28项NAS隔离验证见 [Go核心迁入记录](plugin-lark-go-port.txt)。跨执行的持久owner、可信回调授权和消息生产者仍待接入，不能把受控HTTP或SDK通过表述为已发送真实消息或已完成迁移。

2026-10-18 追加：共享 Notification/路由/窗口纯Go核心的15项NAS与1,316条原参考见 [通知核心](notice-message-go-port.txt)。实际 CleanInvalidSeed 源调用的构造校验、完整24字段密文及历史收据鉴权见 [源通知接线](plugin-clean-seed-notice-go-port.txt)；43项相关App正常/race/NAS通过不等于外发已送达。下载器写前拒绝、7个真实SDK恢复分支及58项leaf/40项App的独立冻结NAS合计98项证据见 [写入阶段](plugin-download-mutation-phase-go-port.txt)。原配置/KV/文件正式迁移数量仍为0，尚未启用或部署新增插件。

2026-10-18 追加：实际 Clean 源调用之后的路由、私有 NoticeMessage 事实与核心持久队列，及稳定数字站点映射已合入正式代码。新107项应用测试的普通/竞态/NAS、31项宿主测试及首轮缺样本的失败证据，分别见 [通知接线](plugin-notice-route-go-port.txt)与[站点映射](native-site-identity-go-port.txt)。真实通知渠道、原配置引用接线和正式数据迁移仍未完成；没有部署或启用新增插件。

2026-10-18 追加：实际站点删除的数字身份、私有删除事实及队列验真已接入，112项实际App普通/竞态/NAS联合验证通过，见[站点删除事件](native-site-deleted-go-port.txt)。原版重置的 `*`、AutoSignIn/IYUU实际消费者与配置引用迁移仍未完成；该批次当时可安装处理器为12，原配置/KV/文件正式迁移为0。

2026-10-18 追加：聚合站点开关的数字/自定义引用、旧格式显式转换、原执行输入保留与已提交保存的回复丢失恢复，见[引用协议与验证边界](plugin-site-reference-protocol.txt)。主轮130项与后续27项分别留证，不能相加为不重复测试数量；最终合并页面已实际保存和重开。旧来源生产采集器、关联业务及正式数据迁移仍未完成，不增加已迁移插件数量。

2026-10-18 追加：AutoBackup实际App接线、快照恢复、独立证据与零效果阻止、页面核对以及最终129项NAS组合验证见[自动备份宿主](plugin-auto-backup-host-go-port.txt)。本机129项普通与53项所选竞态分别计证；此前联合竞态超时记录保留。当前可安装处理器为13，完整业务/平台及原23个实例配置/KV/文件的正式迁移仍未完成，不能表述为13个插件已迁移。

2026-10-18 集成更新：提交 aebd1e1 的源码输入、测试范围和未完成边界见 [IYUU/优选 IP 集成报告](plugin-iyuu-cloudflare-integrated-go-port.txt) 与 [逐项验证记录](plugin-iyuu-cloudflare-integrated-verification.json)。此更新未部署；原配置、KV 和文件正式迁移数量仍为 0。aebd1e1 当时的 SeedRescue 原业务核心及 Vue 联邦界面仍在独立候选接入；该项最新状态见下方稀缺保种追加记录。

2026-10-18 IYUU 目录跟进见 [实际目录授权方案与边界](plugin-iyuu-daemon-destinations-go-port.txt) 和 [验证记录](plugin-iyuu-daemon-destinations-verification.json)。不需要把下载器目录加入 App 整理规则；未操作用户既有种子。

2026-10-18 稀缺保种实际 Go 宿主和原版 Vue 联邦界面接入见[功能与验证边界](plugin-seed-rescue-go-port.txt)及[逐项记录](plugin-seed-rescue-verification.json)。760项应用/153项前端及所选竞态分别计证；RSS列表脱敏补充回归与首次失败单独保留。原用户配置、3项KV及全部23个实例正式迁移仍为0，未部署。

2026-10-18 自动转移做种及音乐分身实际 Go 宿主接入见[功能与验证边界](plugin-torrent-transfer-go-port.txt)及[逐项记录](plugin-torrent-transfer-verification.json)。774项应用普通测试和25项所选竞态分别计证；独立实例、一次性/校验恢复及未知提交不重放均有实际SDK/下载器协议测试，原配置及418+36项KV尚未迁移。本批未部署，可安装处理器增至18，全部23个实例的正式迁移仍为0。

2026-10-18 刷流与源文件联动清理原生入口集成：新增两个实际Go宿主和原版配置/应用界面，可安装处理器入口增至20。当前业务范围、验证分层及仍未完成的RSS/TMDB/并行、消费者、Lark/自动签到/聚合通知与完整平台接线见[集成进展](plugin-brush-scrape-native-progress.txt)。入口增加不等于全部业务已完成；本批未部署，原23个实例配置、KV、独立文件正式迁移仍为0。

最新自定义Hosts交付见 [交付记录](plugin-custom-hosts-delivery.txt)，总体实际完成 6/23；服务监控历史见 [服务监控](plugin-server-status-delivery.txt)；此前系统进程见 [历史交付](plugin-system-process-delivery.md)；共享识别词的 v0.4.2 历史交付见 [识别词记录](plugin-remote-identifiers-delivery.md)。

# 已安装插件迁移基线

2026-10-21 RemoveLink 监听诊断补充：仅真实add-watch系统调用失败可生成可信目录归属的本地系统提示，提示/独立收据/journal同事务，未知ACK只读核验且不解除文件闭锁。核心普通/race各60/257、App普通/race/modernconly各134/475通过，vet与23个CLI构建通过；全局初始化归属、partial-watch源差异、远端provider与非Linux监听仍是开发缺口，真实配置/账户验收及部署另列未做。详见 [本轮范围](plugin-remove-link-init-diagnostic.txt) 与 [验证记录](plugin-remove-link-init-diagnostic-verification.json)。正式迁移仍7/23。

2026-10-21 RemoveLink 专项停用导入补充：固定代码版本与调用者提供快照的校验分开标记，新增离线validate/只读dry-run/显式apply、已有授权本地目录映射、加密proof与原子收据、分身冷启动隔离。受影响App及真实CLI普通/race/modernconly均通过，真实来源stdin测试跳过不算生产验收；非local存储及整个配置缺失明确拒绝。详见 [工具范围](plugin-remove-link-reviewed-import.txt) 与 [验证记录](plugin-remove-link-reviewed-import-verification.json)。未读取真实NAS配置或部署，正式迁移仍7/23。

2026-10-21 RemoveLink 同步来源屏障补充：新v3通知在原位置等待可验证的来源结果，已知typed异常按原捕获规则继续，未知结果保持文件写闭锁；独立收据绑定终态，旧v1/v2不升级补发。真实SDK事件/原生宿主、分身、冷恢复和撤销并发已通过受影响普通/race/modernconly验证。远端StorageChain适配、专项停用导入等开发缺口与实际迁移/部署缺口分列，未部署，正式迁移仍7/23。详见 [本轮范围](plugin-remove-link-source-barrier.txt) 与 [验证记录](plugin-remove-link-source-barrier-verification.json)。

2026-10-21 RemoveLink 通知接线补充：新接受的 notification 在原 Commit 授权内冻结域名、信封及最小历史，并以核心收据绑定专属共享 Router 交接；旧v1保持本地历史、不升级补发。分身、退休代次、回滚与未知发送分别验证。原同步通知异常回传清理流程及共享v1终态独立证明仍待完成，未部署、正式迁移仍7/23。详见 [接线范围](plugin-remove-link-notice-routing.txt) 与 [验证记录](plugin-remove-link-notice-routing-verification.json)。

2026-10-21 默认链接补充：SiteStatistic 新通知已复用 AppDomain，并把默认插件链接与站内历史在接受时冻结；旧v1通知不补发、不随设置漂移。采用固定基类片段及公开v2 URL规则的合成对照，未冒称完整安装版URL等价或真实域名迁移。详见 [链接阶段](plugin-site-statistic-default-link.txt) 与 [验证记录](plugin-site-statistic-default-link-verification.json)，正式迁移数仍7/23。

2026-10-21 来源异常分类补充：SiteStatistic 的真实账户采集已对四类固定 oracle 解析异常建立调用级私有来源分类，普通/race 正反例与并发隔离留证；这不是 SystemError/SSE 完整接线，固定 IndexerModule 源码证据仍缺。通知默认插件链接差异另列待补，不计插件正式完成。详见 [分类与边界](plugin-site-statistic-module-failure-classification.txt) 及 [验证记录](plugin-site-statistic-module-failure-verification.json)。

2026-10-21 开发续作：SiteStatistic 已补专属来源绑定的共享通知 Router/持久队列接线，并按精确原版源码修正为通知正常返回后才保存 last_notify；处理中去重、双实例隔离、未知发送不重放与冷恢复终态分别留证。该阶段未部署、未使用真实外发账户，不增加正式完成数，仍为7/23。专项、全项目回归及本机二进制输入限制见 [通知路由说明](plugin-site-statistic-notice-routing.txt) 与 [验证记录](plugin-site-statistic-notice-routing-verification.json)。

2026-10-21 站点索引器目录补充：已取证的 17 行 SitesHelper 模板投影（23 个 host，含 ext_domains 别名）正式内建为 Go 目录并接入站点来源识别，站点地址如 hhan.club、kp.m-team.cc、cspt.top、www.pttime.top 现在按原版 schema 选择适配器并保存注册域；同时修复站点管理把导出 token 误报为已配置凭据的既有回归。详见 [说明](site-indexer-alias-go-port.txt) 与 [逐项验证](site-indexer-alias-verification.json)。这是 SiteStatistic 的模板/别名基础接线，未部署、未访问真实站点，正式完成仍为 7/23。

最新交付（2026-10-21）：新服务已部署 v0.4.8，**二级分类策略1.3、插件彻底卸载2.2、共享识别词2.4.1、系统进程1.1、MoviePilot服务监控1.4、自定义Hosts1.2.1、媒体库服务器刷新1.3.3 已正式迁移，当前7/23已迁移，其余16项继续实施**。媒体库刷新保留原启用和延迟，唯一飞牛引用完成映射；生产表单、首次初始化与当前事件监听位置已核对，无历史重放或刷新调用。媒体连接停用时的表单过滤及选中引用保留已随v0.4.8对齐，完整23项目标继续实施。详见 [本轮交付](plugin-media-server-refresh-delivery.txt)、[部署收据](plugin-media-server-refresh-deployment.json)。下方旧日期记录保留历史阶段。

2026-10-20 共享基础补充：[原版请求与调度设置](source-settings-migration.md)已随 v0.4.2 部署，并正式导入 4 条密文记录，区分配置 TZ 和本地时区。代理认证修复亦已部署；共享配置迁移不额外增加插件完成数量。

2026-10-21 开发集成补充：[订阅、目录整理与消息通知 Go 集成记录](plugin-business-source-integration.txt)已合入正式代码，包含完整回归和实际来源配置的独占迁移验证；本轮未部署、未新增插件正式迁移，完成数仍为 6/23。Lark 等未完成项保留原状态。

核查日期：2026-10-16（Asia/Shanghai）。来源：NAS 原版 MoviePilot 的只读 SQLite 查询 UserInstalledPlugins，与 /app/app/plugins、/config/plugins 已安装源码的 AST 元数据对应；未导入配置值或运行数据。23/23 插件源码已找到，合计 36717 行 Python（不含前端静态包）。此清单是迁移范围，不是完成声明。

| ID | 名称 | 原版本 | 原状态 | 数据键数量 | 状态 |
| --- | --- | --- | --- | --- | --- |
| CategoryEditor | 二级分类策略 | 1.3 | 停用 | 0 | 2026-10-20 已正式迁移并冷启动/UI核对；原停用配置保留、安装与收据各1、0任务/KV/新增通知，分类文件未改写；实际编辑/分类及错误缓存行为已在独占实例验证 |
| TorrentTransfer | 自动转移做种 | 1.10.3 | 启用 | 422（10-21复核） | 已接实际Go SDK/原生双下载器、原表单、调度、历史、通知、生命周期与冷恢复；2026-10-21完成原配置/422项历史和双下载器引用的隔离迁移、离线CLI及真实SDK正常/未知回执冷启动验证，普通/竞态和NAS通过；生产备份目录只读挂载、调度交接及正式迁移未完成，未部署 |
| SiteStatistic | 站点数据统计 | 1.9.7 | 启用 | 0 | 已接Go统计、原表单/卡片/饼图/明细、刷新与API基础；原配置、2133条21列账户档案、17个站点引用及离线CLI已验证。账户日记录/卡片/历史统一读取并加密；做种明细、站内消息采集、原版开关及逐请求授权/取消检查已通过普通/竞态/NAS与独占UI验证；刷新API 19字段与24例原HTTP对照一致。17行已装SitesHelper模板投影已内建并接入站点别名/schema解析，错误DTO与事件语义已对齐。原版通知分类（站点）、专属来源Router/持久队列和完成后去重已接，双实例及冷恢复专项已验证；SystemError、真实外发账户验收、全局选项正式迁移、执行交接和部署仍未完成 |
| RemoveLink | 清理媒体文件 | 2.17 | 停用 | 0 | 已接正式catalog、真实SDK终态调用、连续owner/Guard/Core Effects、配置纠错/目录扩展/重启及卸载；NAS App61项和host44项通过。后续已接精确无hash事件发布/站内历史转交；新v3通知路由、同步来源结果及未知继续闭锁已验证；专项停用导入与本地引用映射工具已验证；已补可信add-watch失败本地系统提示与闭锁收据；远端存储适配、全局初始化/partial-watch差异、非Linux监听仍未完成；真实配置/渠道验收、正式迁移和部署另列待项 |
| MPServerStatus | MoviePilot服务监控 | 1.4 | 启用 | 0 | 2026-10-21 已随 v0.4.5 正式迁移；原配置1条加密导入，既有五份布局/显示偏好保留，原单widget空key对齐。详情/表单/六卡、增量采样/缓存/刷新与冷启动已验证；生产真实公共状态及速率已核对，1次成功初始化、0计划/事件/其他业务运行 |
| PluginUnInstall | 插件彻底卸载 | 2.2 | 停用 | 0 | 2026-10-20 已随v0.4.1部署并正式迁移；原true/true及空选择保留，安装与收据各1、0运行/KV/计划/事件/卸载步骤，实际生产表单已核对；失败后的warm/durable、连续新执行、重装及自卸载已在独占实例验证；原全局顺序/布局仍待后续独立迁移 |
| AutoSignIn | 站点自动签到 | 2.9.14 | 启用 | 24 | 主流程、22类适配、原表单/详情及逐效果StateJournal已迁入正式Go包；实际SQLite分行/owner、内部配置CAS与旧admission归档切换已有NAS证据。实际Manager/调度/消息及Browser/OCR接线、资源按需加载/回收、注册、配置/KV迁移和实机验收未完成 |
| MergeMessageNotify | 聚合消息通知 | 1.20.1 | 启用 | 0 | 主流程/17渠道构造/SDK及SMTP依赖已迁入正式Go包，28项NAS隔离测试通过。原表单页签/子弹窗/点号键已用实际ConfigDialog验证；完整Mako/Apprise、正式宿主/消息生产者及迁移未完成，不在可安装目录 |
| IYUUAutoSeed | IYUU自动辅种 | 2.19 | 启用 | 329（10-21复核） | Go 宿主、双下载器、队列/日志已接；2026-10-21 完成原版配置/329项历史/三类缓存、17站点与双下载器引用的隔离迁移；实际 Go SDK 辅种及冷恢复普通/竞态、离线 CLI 和独占界面验证通过，修正 Token 回填。生产迁移及调度切换未完成，未部署 |
| SystemProcess | 系统进程 | 1.1 | 启用 | 0 | 2026-10-21 已随 v0.4.3 部署并正式迁移；原配置与同一用户五份仪表板偏好一次事务加密导入，保留来源启用。实际详情、刷新、布局与冷启动已验证；1 次成功初始化，0计划/事件/业务执行，页面读取后配置与五份偏好不变 |
| MediaServerRefresh | 媒体库服务器刷新 | 1.3.3 | 启用 | 0 | 2026-10-21 已随v0.4.7正式迁移；启用和延迟保留，唯一飞牛引用加密映射，原表单/冷启动/当前事件监听核对通过；首次初始化1成功，0计划/数据/业务刷新，查看页面不改配置。v0.4.8已补齐核心停用时的表单筛选与原选择保留 |
| RemoteIdentifiers | 共享识别词 | 2.4.1 | 停用 | 0 | 2026-10-20 已随 v0.4.2 部署并正式迁移；保留来源配置缺失、KV/文件/全局识别词缺失，安装与收据各1、0配置/任务/计划/事件。真实页面、Go SDK、认证请求、词表应用及冷启动已在独占实例验证，生产读取默认表单0写入；完整来源全局顺序与布局仍待独立迁移 |
| SubscribeGroup | 订阅规则自动填充 | 2.8.7 | 启用 | 2 | 已接 Go SDK、原表单/历史页、创建及实际下载事件、分类覆盖和下载补空宿主；两套动漫 Go 状态机、订阅局部识别词及下载配置冻结已验证。分块传输及4,500条完整历史的工作器/HTTP/网页已验证；任意合法大历史的流式构树、完整RSS上下文、规则测试工具及正式配置/KV/引用迁移未完成，尚未部署或验收为1:1 |
| AutoBackup | 自动备份 | 2.1.4 | 启用 | 0 | 已接实际App授权、原表单/SDK、持久once/cron/API、一致SQLite快照及密钥恢复、归档/保留/WebDAV、通知队列与生命周期；独立恢复证据、未知执行核对及零效果重试已验证。已进入可安装目录；真实外部通知渠道、原配置及7个历史文件迁移仍未完成 |
| BrushFlowLanduo | 站点刷流-landuo | 5.2.15 | 启用 | 24 | 已接原生执行入口、实际SDK/owner/原表单及source容量快照；真实质量提升下载替换和完成通知继续组合验证，尚未完成正式配置/KV/引用迁移，不计已完成 |
| MergeSiteSwitch | 聚合站点开关 | 1.2 | 停用 | 0 | Go 核心站点范围、原版动态表单、持久跟随及关联配置事务已接入；数字/自定义引用协议、不可变执行输入与已提交保存的丢失回复恢复已验证。旧来源采集、关联插件实际业务联动、正式配置和引用迁移仍未完成 |
| TorrentTransfermusic | 自动转移做种 分身 | 1.10.3 | 启用 | 36 | 独立Go实例、原表单及与本体相同业务；2026-10-21完成原配置/36项历史及独立引用的隔离迁移，与本体同库458条历史不混用；离线CLI、真实SDK正常/未知回执冷启动普通/竞态和NAS通过。生产只读挂载、调度交接及正式迁移未完成，未部署 |
| ScrapeFileClean | 源文件联动清理 | 1.0.7 | 停用 | 0 | 原算法、11字段原表单、Go SDK、67组完整原执行/224组helper/8组watchfiles参考与NAS隔离验证已完成；生产监听、删除宿主和正式迁移未接入，不在可安装目录 |
| LarkMessager | Lark 应用消息通知 | 0.9.1 | 启用 | 2 | 主流程、13个消息模块、API/回调与受控HTTP已迁入正式Go包；1,376条原执行及28项NAS隔离测试通过。实际App持久宿主、可信消息入口、完整动态输入及配置/KV迁移未完成，不在可安装目录 |
| CleanInvalidSeed | 清理无效做种 | 2.1 | 启用 | 0 | 已接正式catalog、真实SDK终态/owner/Core、配置生命周期与调度；下载器写前拒绝与未知写分开留证，源通知路由、私有事件和持久队列已接入。本轮107项联合NAS保留独立证据；原43/148项及mutation98项证据保留。可信远程五命令入口中 /detect_invalid_torrents 已从 Lark 分发接入真实命令事实（管理员门沿用原版消息层），其余四命令、真实通知渠道/完整依赖及正式迁移未完成 |
| SeedRescue | 稀缺保种 | 1.6.3 | 启用 | 3 | 已接 Go 宿主、SDK、原六 API、持久扫描及原版 Vue 工作台；2026-10-21 完成原配置/3项KV/1140任务、17站点与qB旧名引用的隔离迁移及离线CLI；真实GoSDK下载/冷恢复普通和竞态、原版界面及NAS 1核512MiB验证通过，并补齐目标站点搜索。IYUU生产依赖与调度交接、正式迁移未完成，未部署 |
| CloudflareSpeedTest | Cloudflare IP优选 | 1.5.1 | 停用 | 0 | 2026-10-18 已接正式 Go handler、实际 CLI/下载校验/hosts 宿主、原表单和持久 once/cron/API；/cloudflare_speedtest 已冻结在插件命令注册表，但命令 authority（需绑 Lark inbox 事实）未建，入口仍返回明确 PENDING；配置及12个文件尚未迁移，未部署 |
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

2026-10-21 稀缺保种历史导入校验修复及原版真实数据隔离验证见[说明](plugin-seed-rescue-history-import.txt)和[逐项记录](plugin-seed-rescue-history-import-verification.json)。这是迁移基础修复，实际完成仍为 7/23，生产仍为 0.4.8。

2026-10-21 稀缺保种原版编号执行拦截的实际失败复现、普通/竞态及真实历史冷读取补充见[说明](plugin-seed-rescue-reference-gate.txt)和[逐项记录](plugin-seed-rescue-reference-gate-verification.json)。该限制没有代替完整引用映射，未增加已迁移数量。

2026-10-21 IYUU 原版配置/历史和引用迁移工具、真实 Go SDK/qB/TR 独占业务及冷恢复验证见[说明](plugin-iyuu-reviewed-migration.txt)和[逐项记录](plugin-iyuu-reviewed-migration-verification.json)；独占界面、凭据保护及前后数据不变见[补充记录](plugin-iyuu-owned-ui-verification.json)。已合入开发代码，尚未生产迁移/切换，正式完成仍为 7/23。

2026-10-21 稀缺保种完整编号/下载器引用、原数据离线迁移与真实GoSDK/冷恢复、原Vue界面及NAS资源限制验证见[说明](plugin-seed-rescue-reviewed-migration.txt)和[逐项记录](plugin-seed-rescue-reviewed-migration-verification.json)。这是开发交付，尚未生产迁移或切换，正式完成仍7/23。

2026-10-21 自动转移做种与音乐分身的原配置/422+36项历史、独立下载器引用、离线CLI、原表单HTTP及实际Go SDK/qB→TR与冷启动保护，见[迁移说明](plugin-transfer-reviewed-migration.txt)和[逐项验证](plugin-transfer-reviewed-migration-verification.json)。当前生产尚未加入备份目录只读挂载、未切换两边调度，正式完成仍7/23。

2026-10-21 四个已审查迁移实例的执行交接保护见[说明](plugin-execution-handoff.txt)和[逐项验证](plugin-execution-handoff-verification.json)。缺少原程序停止与最终来源核验时，不允许迁入实例执行；普通新建实例保留原流程。真实原版仍运行，未签发真实交接、未改原服务或部署，正式完成仍为7/23。

2026-10-21 站点数据统计的原配置及2133条共享账户历史、完整21列原件、17个站点引用、原取数与后续观测、Go SDK页面和离线迁移工具，见[说明](plugin-site-statistic-account-history.txt)和[逐项验证](plugin-site-statistic-account-history-verification.json)。它已纳入执行交接保护；完整采集、刷新字段/别名、通知链和生产迁移仍待完成，正式完成仍7/23。

2026-10-21 后续补齐账户日记录与核心站点管理读取，见[说明](plugin-site-account-collection.txt)和[逐项验证](plugin-site-account-collection-verification.json)。首次失败保留失败日，重刷失败不覆盖已有日记录；卡片和完整历史可读取导入档案，新采集快照绑定身份加密。导入历史不算当前连接测试通过。完整插件迁移数量仍7/23，生产保持v0.4.8。

2026-10-21 再补充做种明细及站内消息采集，见[说明](plugin-site-account-fields.txt)和[逐项验证](plugin-site-account-fields-verification.json)。原版消息开关完成独占浏览器保存重载验证，连接测试不触发阅读，正式刷新逐请求核对授权和取消。19字段类型及转发不等于错误DTO分支完成；完整SiteStatistic及生产迁移仍待继续，完成数量7/23。

2026-10-21 继续对照刷新API与核心采集的错误数据、None、事件及独立时间戳，见[本轮说明](plugin-site-account-outcomes.txt)和[逐项验证](plugin-site-account-outcomes-verification.json)。原版构造默认值与原生未知观测分开保存；错误数据可以触发刷新事件，连接状态仍独立反映失败。本轮尚未部署，完整SiteStatistic与其余实例仍在继续，正式完成仍7/23。

2026-10-21 MTeam账户采集按原版完整解析流程调整，涉及请求参数与顺序、peer分页、消息原始类型、DTO校验时机及大整数冷恢复，见[本轮说明](plugin-site-mteam-account-flow.txt)和[逐项验证](plugin-site-mteam-account-flow-verification.json)。完整SiteStatistic及原23个实例的剩余迁移仍继续；本轮尚未部署，正式完成仍7/23。

2026-10-21 NexusPHP/憨憨原版账户流程、27个完整请求与DTO参考及真实Go SDK冷恢复见[说明](plugin-site-nexus-account-flow.txt)和[逐项验证](plugin-site-nexus-account-flow-verification.json)。独立源采集器保留原XPath、消息重复/null和摘要累计；原生未知值与DTO默认值继续区分。完整别名、通知链与生产迁移尚未完成，正式完成仍7/23、生产仍v0.4.8。


2026-10-21：日期解析依赖改为本地 Go 分支，205 份语言构造函数延迟到首次查找并以 sync.Once 保护。仅初始化样本 RSS 从约 84 MiB 降至约 40 MiB；不表示首次解析后或生产峰值降低。依赖自身、站点与相关应用正常/race、2,133 条历史及 NAS 隔离验证分别计证，见 `site-dateparser-resource-initialization.txt` 与对应验证报告。未部署，正式迁移仍为 7/23。


2026-10-21：肉丝 RousiPro 单独以 Go 实现账户资料和 Token 消息流程，原版参考采集扩至 31 场景；补原生搜索/签名种子材料获取、API-only 导入和站点 Token 编辑/在途撤销。直接协议、实际插件 SDK、冷恢复及隔离 NAS 分别验证；原生搜索/下载为自有合成协议验证，未访问用户站点或下载器，未部署。详细边界见 `plugin-site-rousi-account-flow.txt` 与验证 JSON；仍为 7/23，不能把本项算作 SiteStatistic 整体迁移完成。

2026-10-21：原版 SITE_MESSAGE 的独立加密迁移、父快照完整性、原生显式选择优先、页面实际值投影及逐请求撤销保护，见[说明](plugin-site-message-settings.txt)与[逐项验证](plugin-site-message-settings-verification.json)。实际来源只做冷推导与 stdin 校验，NAS 使用独占合成业务；未部署、未迁移生产开关，完整插件正式完成仍 7/23。

2026-10-21：AutoSignIn 2.9.14 接入实际 SDK 宿主、分身 owner/历史/调度、原生事件及命令、逐请求撤销和同步通知/事件独立回执，见[说明](plugin-auto-signin-host-source.txt)与[验证](plugin-auto-signin-host-source-verification.json)。完整 App 保留 8 项已确认基线失败；受影响普通/race/modernconly、核心和构建分别计证。Browser/OCR、Tjupt 图像能力和拆分/SOCKS 代理等代码仍待补齐；未迁移真实账户或部署，正式完成仍 7/23。

2026-10-21：AutoSignIn 再接显式/环境代理合并、HTTP/HTTPS/SOCKS 与逐跳撤销，保留原 proxy=false 的 trust_env；修复共享传输取消时握手 socket 回调竞态。详见[说明](plugin-auto-signin-proxy-routing.txt)与[验证](plugin-auto-signin-proxy-routing-verification.json)。同字节完整 App 无新增失败，受影响 race/modernconly、核心及构建通过；Browser/OCR/Tjupt 等仍待完成，未部署，正式完成仍7/23。

- AutoSignIn Tjupt 宿主阶段：已接实际 SDK、可信公共图片/逐跳守卫和环境代理，原答案缓存采用分身隔离 SQL 原字节导入导出，冷恢复及未知结果不自动重发。冻结普通/扩大 race/modernconly/core/vet/23 CLI 已验证；完整 App 保留原8顶层失败，新增0。见 `plugin-auto-signin-tjupt-host.txt` 与 verification JSON。Browser/OCR 等代码缺口仍在，未操作真实账户或部署生产，正式完成数不变。

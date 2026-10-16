# 已安装插件迁移基线

核查日期：2026-10-16（Asia/Shanghai）。来源：NAS 原版 MoviePilot 的只读 SQLite 查询 UserInstalledPlugins，与 /app/app/plugins、/config/plugins 已安装源码的 AST 元数据对应；未导入配置值或运行数据。23/23 插件源码已找到，合计 36717 行 Python（不含前端静态包）。此清单是迁移范围，不是完成声明。

| ID | 名称 | 原版本 | 原状态 | 数据键数量 | 状态 |
| --- | --- | --- | --- | --- | --- |
| CategoryEditor | 二级分类策略 | 1.3 | 停用 | 0 | Go 执行及分类引擎已验证；支持通用 TMDB 条件、范围、YAML 别名/合并；待正式配置迁移 |
| TorrentTransfer | 自动转移做种 | 1.10.3 | 启用 | 418 | 待实现、待迁移、待验证 |
| SiteStatistic | 站点数据统计 | 1.9.7 | 启用 | 0 | Go 快照统计、原版表单/卡片/饼图/明细、一次性刷新、持久站内通知及按域名刷新 API 已接入；通知通道、完整账户字段/域名别名、历史/配置正式迁移和仪表板布局仍待完成 |
| RemoveLink | 清理媒体文件 | 2.17 | 停用 | 0 | 原算法、原表单、Go SDK 纯包及真实临时文件验证已完成；生产监听、删除宿主和正式迁移尚未接入，不在可安装目录 |
| MPServerStatus | MoviePilot服务监控 | 1.4 | 启用 | 0 | Go SDK、固定公共状态探针、跨工作器采样/缓存、原表单/详情/仪表板数据及配置失效已接入；完整仪表板布局、正式配置迁移与部署未完成，编码等平台差异见专文 |
| PluginUnInstall | 插件彻底卸载 | 2.2 | 停用 | 0 | Go 原算法/表单、真实卸载宿主、自卸载收据与恢复、安装/重装及合成网页操作已验证；保留安装版清配置/KV的非强制 guard，待正式迁移和部署 |
| AutoSignIn | 站点自动签到 | 2.9.14 | 启用 | 24 | 待实现、待迁移、待验证 |
| MergeMessageNotify | 聚合消息通知 | 1.20.1 | 启用 | 0 | 待实现、待迁移、待验证 |
| IYUUAutoSeed | IYUU自动辅种 | 2.19 | 启用 | 325 | 待实现、待迁移、待验证 |
| SystemProcess | 系统进程 | 1.1 | 启用 | 0 | Go 进程采样、配置/详情页及仪表板数据已验证；仪表板布局待接入，待正式迁移 |
| MediaServerRefresh | 媒体库服务器刷新 | 1.3.3 | 启用 | 0 | 已接 Go 子进程、持久批次、飞牛刷新、停用/重载/卸载/关闭收尾及核心核对交接；已有独占测试库实机证据，正式配置引用映射和迁移尚未执行 |
| RemoteIdentifiers | 共享识别词 | 2.4.1 | 停用 | 0 | Go 词表获取、定时/一次性执行、实际识别接线和原版配置表单已验证；正式配置及全局识别词迁移仍未执行 |
| SubscribeGroup | 订阅规则自动填充 | 2.8.7 | 启用 | 2 | 已接 Go SDK、原表单/历史页、创建及实际下载事件、分类覆盖和下载补空宿主；两套动漫 Go 状态机、订阅局部识别词及下载配置冻结已验证。分块传输及4,500条完整历史的工作器/HTTP/网页已验证；任意合法大历史的流式构树、完整RSS上下文、规则测试工具及正式配置/KV/引用迁移未完成，尚未部署或验收为1:1 |
| AutoBackup | 自动备份 | 2.1.4 | 启用 | 0 | 待实现、待迁移、待验证 |
| BrushFlowLanduo | 站点刷流-landuo | 5.2.15 | 启用 | 24 | 待实现、待迁移、待验证 |
| MergeSiteSwitch | 聚合站点开关 | 1.2 | 停用 | 0 | Go 核心站点范围、原版动态表单、持久跟随及关联配置事务已接入；关联插件实际业务联动、正式配置和引用迁移仍未完成 |
| TorrentTransfermusic | 自动转移做种 分身 | 1.10.3 | 启用 | 36 | 待实现、待迁移、待验证 |
| ScrapeFileClean | 源文件联动清理 | 1.0.7 | 停用 | 0 | 待实现、待迁移、待验证 |
| LarkMessager | Lark 应用消息通知 | 0.9.1 | 启用 | 2 | 待实现、待迁移、待验证 |
| CleanInvalidSeed | 清理无效做种 | 2.1 | 启用 | 0 | 待实现、待迁移、待验证 |
| SeedRescue | 稀缺保种 | 1.6.3 | 启用 | 3 | 待实现、待迁移、待验证 |
| CloudflareSpeedTest | Cloudflare IP优选 | 1.5.1 | 停用 | 0 | 待实现、待迁移、待验证 |
| CustomHosts | 自定义Hosts | 1.2.1 | 启用 | 0 | Go 写入/停用恢复及实际域名连接已在 NAS 隔离容器验证；待正式可写挂载和配置迁移 |

包含独立的 TorrentTransfermusic 分身，不与原实例合并。未安装但留有历史配置/数据的旧插件不混入已安装范围；需在迁移包中另行标记。迁移前须按实际 URL/实例/路径建立站点、下载器、媒体服务和目录引用映射。配置及缓存中的凭据只能以应用密文落盘，日志、普通 API 和报告不得含原值。

每项完成须同时取得：对应原版行为对照、实际可执行实现、配置/数据/文件迁移收据、引用映射核验、适当的协议/合成与实机证据。平台界面出现卡片不构成该插件已迁移。

当前原版配置、KV 与文件均未导入正式新服务；10 个可安装处理器已有不同程度的实际 Go 行为，不能表述为“10/23 已迁移”。RemoveLink 另有尚未注册的纯 Go 包；其他 13 个插件及平台完整交互继续保留在交付范围内。定时触发器和持久队列已接共享识别词及站点统计的一次性刷新；其他具体业务插件注册、公共服务界面和逐项迁移仍需完成，不能因此增加已迁移插件数量。

同日只读核查原版 `/config/plugins/<原插件ID>` 的文件元数据：AutoBackup 有 7 个文件、51,658,600 字节，最大单文件 7,414,774 字节；CloudflareSpeedTest 有 12 个文件、15,871,166 字节，最大单文件 7,930,040 字节。两处均无符号链接或特殊文件，其余 21 个已装 ID 未发现对应数据目录。本次没有读取/导出文件正文，也没有把备份包放入 Git。后续迁移须分别保留这些文件的内容和校验收据，密文落盘；不得把旧可执行文件复制后直接运行，也不能因 KV 为 0 就漏掉文件迁移。

聚合站点开关对 SiteStatistic 的字段更新和真实 Go 初始化已联调，不使用可用性测试替身；保留统计插件启用状态及其他选项。原版 SiteStatistic 1.9.7 未读取 statistic_sites，不能把该字段自行解释为图表/采集过滤条件，实际数据仍按核心启用站点展示。AutoSignIn、IYUUAutoSeed 的业务仍待实现；未就绪依赖会阻止整次聚合更新，不能用测试替身的配置/队列结果代替该证据。原版只识别 BrushFlow，不把当前安装的 BrushFlowLanduo 自动视为同一插件。

自定义 API 的已实现行为、24 组原 ASGI 对照、鉴权/恢复/NAS 证据和未完成边界见 [plugin-http-api-verification.md](plugin-http-api-verification.md)。已装源码中的 24 项 API 声明不等于 24 项业务均已实现，本批只接通 SiteStatistic 的按域名刷新；没有增加“已迁移”插件数量。

订阅规则自动填充的源码行为、规则对照和核心事件进度见 [plugin-subscribe-group-port.md](plugin-subscribe-group-port.md)。该插件已加入可执行目录，并在隔离实例实际安装、配置、初始化和执行；这些证据不等于正式用户配置和数据已迁移。原版两个 KV 的只读元数据核查显示 `history` 为列表、8 条、1,992 字节，`history_handle` 为列表、8 条、230 字节；没有读取输出正文或正式导入。当前用户数据规模不构成取消未来大历史传输要求的理由。大消息已证实范围见 [plugin-large-message-audit.md](plugin-large-message-audit.md)，新增卸载宿主及原管理器行为见 [plugin-uninstall-go-port.md](plugin-uninstall-go-port.md)。

优先级规则依赖的 Go 内核、202 组双路径样本、88 组原版排序参考、原版规则管理页、分区保存边界及实际关键词/订阅搜索证据见 [priority-rules-go-port.md](priority-rules-go-port.md)。原用户规则还未正式迁移，完整元数据、独立规则测试工具和所有订阅识别来源仍在范围内。下载资源五字段与季号的已确认行为、算法剩余差异见 [release-resource-metadata-go-port.md](release-resource-metadata-go-port.md)。

附加订阅过滤已接通原版质量/分辨率/特效表达式和编辑控件，313 组原调用参考、真实搜索进度前过滤、保存/重载及边界见 [subscription-additional-filters-go-port.md](subscription-additional-filters-go-port.md)。这是订阅规则插件的核心依赖，不增加已迁移数量；新增分类宿主事务进度见上述专文，下载宿主和原数据迁移仍待完成。

订阅局部识别词、原文保存和自动 Intent 的完整识别配置冻结见 [subscription-recognition-scope.md](subscription-recognition-scope.md)。Rust 与 legacy 两套单标题动漫流水线均已由 Go 执行；两套引擎、实际 wrapper、源错误及字符表证据见资源元数据专文。这些结论不能扩大为完整普通视频身份识别、RSS缓存上下文或原生下载目录监听始终恢复选择快照。

完整 MetaInfo / MetaBase 标题对象、修改方法、保存格式及拼音依赖已移入正式 Go 包，来源、2682 个完整标题参考、源异常和边界见 [metainfo-go-port.md](metainfo-go-port.md)；完整RSS对象纯包已接同一真实入口，110条原链路参考通过，见 [rss-context-full-go-port.md](rss-context-full-go-port.md)；尚未接入 App 消费者。路径识别与 RSS 缓存的剩余边界、实际安装版差异和逐项验收顺序见 [rss-context-go-plan.md](rss-context-go-plan.md)。RSS 标题重建不依赖 MetaInfoPath，二者分别推进和计证。

服务监控的完整原树、固定网络适配、缓存/配置并发和页面验证见 [plugin-server-status-go-port.md](plugin-server-status-go-port.md)。清理媒体文件的已移植纯算法与尚待接入的生产副作用边界见 [plugin-remove-link-go-port.md](plugin-remove-link-go-port.md)。

插件文件夹/排序的现有可操作交互与原数据格式、额外平台限制和分层验证见 [plugin-organization-go-port.md](plugin-organization-go-port.md)。原用户布局仍未迁移，不能增加已迁移插件数。

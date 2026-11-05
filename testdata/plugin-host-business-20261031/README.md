# S04–S07 / S12 evidence

> S11 AniList 新增原生识别完整分支交验见 `anilist-source/README.md` / `evidence.json`：35完整模型、18实际SDK/native、8固定Chain ID、默认来源和源异常副作用；race r8 PASS30.262s。它不改变S04–S07/S12原阶段证据，最终Linux/浏览器由services/root独立归档。

> 最新追加交验：S05 已从原9列扩展到既有原生支持的30列，详见 `subscription-expanded-final.json`（固定46列逐列分出16个宿主边界、真实原ORM/SQLite35例、native实际消费/失败/冷热、Linux真实进程）；四flag支持integer/real/text，非0/1按真实SQLite结果truth运行，不再拒绝1.5/非数字字符串。S12 已由17个已装站点扩至固定实际catalog内全部165个现有支持schema（114带category），详见 `category-supported-final.json`，855+10源差分与完整域绑定。旧阶段证据与下述原阶段说明保留，不把它们冒充最新全输入构建。最新Linux是 `process-linux-expanded/` / `build-server-linux-expanded/`，normal app15.814s / SDK0.385s，S05增量race app16.069s / SDK1.698s。

中央唯一对照表继续维护范围与验收状态；本目录只保存该切片的可复查证据，不另建完成总数。源 commit 固定为 `8a83efeacd3e26b82be495e17d50146e31c96a60`。没有引入 Python 产品运行依赖、工作流、AI 或官方等级认证。

## 源行为与实现

| 原版源码及行为 | Go 实现与实际验证 |
|---|---|
| `app/db/site_oper.py` 的 list / list_active / list_order_by_pri、get_userdata、success:225 / fail:266；`app/db/models/site.py` 的独立优先级排序 | `internal/app/plugin_original_host_sites.go`；`internal/plugins/site_host.go`。默认 source ID 顺序，显式优先级顺序，启用过滤；统计包含归档行、按 domain 选择最新有效日后保留该日错误/重复行；沿用成功/失败秒数、note、原有平均分母。7 个固定源 AST case，外部 SDK 实际 SQLite / clone / 刷新事务故障 / 503 路径。 |
| `app/db/subscribe_oper.py` get:174 / list:214 / update:258 / list_by_tmdbid:268；`app/db/__init__.py` update:503 只逐字段赋值并提交 | `plugin_original_host_subscriptions.go`、`subscription_host.go`、`subscription_quality_source.go`。有限九字段 patch，空/等值 patch 不加 revision、不取消任务；显式 JSON null 与省略不同，完整原生 UI save 清除 null 投影。实际外部 SDK + 队列记录 + 原生 HTTP PUT + 重启；不是 mock 返回。 |
| DownloadHistory `get_by_hash`:81 精确 hash、创建日期倒序；TransferHistory `get_by_src`:188 / `get_by_dest`:207 首条；TransferHistoryOper `delete`:189 只删记录 | `plugin_original_host_history.go`、`history_host.go`。实际原生硬链接整理后按 ID/源/目标查询，SQL 故障、仅记录删除、Close/New 恢复，双端文件与内部执行计划保留。下载历史大小写与创建时间排序有独立实际 SDK 验证。 |
| SystemConfigOper set:27 / get:92：缺失 null、初次空值持久化、等值 None、已有值被空值删除但热 cache 仍保留；普通 config.py:422/428 共享识别设置 | `plugin_original_host_settings.go`、`settings_host.go` 与 App 有限热 cache。6 个原 AST setter case分别验证 DB存在性/冷热返回；活跃识别词真正修改解析器；故障不替换已生效词库。共享识别 bool/API URL 及只读成功计数复用宿主。 |
| TorrentsChain browse:402 / rss:434 / TorrentInfo 构造:460–485；RssHelper 与 RequestUtils 编码、Rust 可选 fallback；通用 Spider 分类:188–208、MTorrent / Rousi 参数 | `plugin_original_host_sites*.go`、`internal/sites/plugin_host_resources.go`、`rousi.go`。复用现有站点网络/代理/限流/解析；标题为空跳过、description不投影为subtitle。已有纯 Go Rust RSS / 编码实现加 RSS 私有恢复路径。26 个实际 lxml6.1.1 固定源 replay case；115 个通用分类 case及10个特殊参数 case；Nexus/M-Team/Rousi实际外部 SDK请求参数。 |

## 必要适配与边界

1. Go SDK 使用有限 typed RPC。站点 Cookie、API key、UA、私有 RSS URL 和 enclosure 留在宿主；返回稳定 ID 和安全逻辑字段。RSS enclosure 使用加密绑定站点及配置指纹的 opaque ID，下载只接受该 ID 或已有数字资源 ID；同源、无凭据跨域、scope 取消与配置变化 guard 均保留。原 `get_site` 的秘密字段不直接返回，所需浏览/RSS/材料能力由宿主完成。
2. 原版 subscription DB setter 没有调度副作用。目标只有实际字段变更才增加原生 revision、取消旧 revision 的原生获取任务，避免已冻结的配置在并发执行中继续写入；空/等值 patch 保持原样。九个可空源字段有限保存 `SourceNullFields`，原生完整表单重存清除；不引入通用 ORM null 兼容框架。
3. 全局设置只开放 `CustomIdentifiers`、`RssSites`、`IndexerSites`、`MEDIA_RECOGNIZE_SHARE`、`MEDIA_RECOGNIZE_SHARE_API`；`UserInstalledPlugins`、`MediaRecognizeShareCount` 只读。安装列表写入必须走真实生命周期。环境配置迁到单用户 Settings。词库写入 SQL 失败时保留旧 live matcher；原同步 setter 会先改热 cache再失败，这是目标事务边界的明确差异。
4. RSS/材料请求复用宿主的同源、50MiB、无自动跳转与私有数据边界；HTTP 非200以错误返回，避免将抓取失败计为成功空列表。源高层 RSS 会将普通 False 折叠成 `[]`；typed RPC 明确暴露失败是传输适配。固定源中“RSS Link has expired, You need to get new!”实际返回 False，标准链的 `is None` 续期分支不被该普通响应触发，未虚构自动续期。Go RSS 私有恢复的26例通过不等于通用 lxml全输入兼容。
5. 普通分类静态数据来自 root 在独占只读、无网络容器中执行实际 compiled helper + decrypt/pickle 的17站有限投影，参见 `../plugin-host-integration-20261031/catalog-source/`。projection SHA `839efec5a055781bd40e9f9206829d568ea2ce333fd7acb9df727ce734fd30f2`。对应 helper SHA `8a14c1668778305b9b40bdd5456e6a95afe6ef101b11db02750fbcd4e7ae0b64`，catalog SHA `f6099dcd4f55f99646d53dbe4cec2c3f01ad4ec95929052a4e348a4ad90205c0`，与本次 NAS 实际字节一致，且匹配固定源时间之前 Resources 历史候选。固定主仓 Dockerfile使用 mutable Resources/main，没有固定这些外部制品；时间推断不作为固定配套证明。未知模板的非空分类显式拒绝，未知模板空分类沿用已有宿主。

## 实际执行证据

| 证据 | 结果与分类 |
|---|---|
| `verification/all-current-normal-r2.log` | 正常全组 app12.627s / SDK0.603s。含17个 App 顶层测试（源差分与真实外部 SDK分开）、SDK patch序列化测试。`internal/sites` 本次正则未选测试；已有 RSS 解析/编码独立执行见 `rss-existing-source-parser.log`。 |
| `verification/all-current-race.log` | app89.802s / SDK1.609s；最终资源源代码。worker为 metadata-only `fixtureVersion` 变量引入前的默认1.0行为；最终Linux两版本另行全输入封存，不宣称本race二进制就是后者。 |
| `process-mac-current/result.json` | 实际 cmd/server + 标准独立 Go worker 安装、有限资源调用、失败、原生保存、clone、cold、卸载。只用 owned loopback站点协议与合成历史DB。该 Mac阶段没有两个版本更新；Linux阶段补齐。 |
| `process-linux-final/` | NAS Linux amd64 实际 cmd/server + 已安装两个不同hash/manifest版本的worker。source1.0→1.1强制更新改变实际PID，clone仍1.0且PID不变；两端私有data保留；之后重启cold、查询恢复、卸载API404。失败RSS为502，记录删除前后两端媒体文件相同。历史DB初始行是明确合成数据，不是生产历史迁移或真实公网站点。 |
| `browser/` / `process-mac-r3/` | root实际浏览器：配置表单读/写真实词库，非法key保存报错并保留表单，恢复成功，clone数据隔离。该二进制先于最终RSS/category/null修复，只支持这里明确列出的UI链证据。 |

`build-server-linux-final/`、`build-worker-linux-final/`、`build-worker-linux-final-v2/` 使用共用 `build_bound.py` 记录实际Go/embed/go.mod/go.sum前后输入hash；三者 `changed_inputs=[]`。`process-linux-final/transferred-hashes.log` 核对远端字节，`cleanup.json` 证明自己的测试数据及worker已清理、无遗留owned worker进程；server按root要求暂留供S02复用。

`source-oracle.json`、`rss-fallback-source-probe.json`、`category-source-oracle.json` 是固定原源码 AST 的合成执行证据；没有导入旧服务配置/数据库或重放生产定时任务。关键失败记录也保留：M-Team测试错误使用loopback origin被正确拒绝，后用仅测试transport保留生产origin后通过；null测试读回复用了Go目标对象导致omitted字段残留，重置读回对象后通过，生产数据库当时已正确清除。

完整文件与driver hash、执行范围和剩余限制见 `evidence-index.json`。本目录没有据合成站点协议声称所有真实公网站点均已实测，也没有把实机启动/浏览器证据替代范围内未核实的其他宿主模块。

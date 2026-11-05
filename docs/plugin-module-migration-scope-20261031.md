# MoviePilot v2 插件模块迁移范围：盘点基线

> 续接说明：用户随后已明确“使用完整项目迁移技能继续迁移插件模块”，实施已开始。下文保留盘点时的原始合同与发现；当前状态见唯一对照表和 continuation 顶部，不再以“尚不开始”限制本轮开发。

用户盘点阶段明确范围为**插件模块**，并要求“先看一下需要迁移什么，先不开始迁移”。本轮使用 `migrate-project-faithfully` 完成固定源码、独立发现、源到目标候选对照和范围整理；未进入实现、运行等价验收或部署。

## 固定依据与边界

- 后端：`jxxghp/MoviePilot@8a83efeacd3e26b82be495e17d50146e31c96a60`，用 Git 对象读取，未追随最新 v2。
- 前端：`jxxghp/MoviePilot-Frontend@2829e3b08214348f05ff2f5beae644882eccad58`，同样只读固定对象。
- 目标：`/Users/accidia/projects/light-media`，`main@97476582910dc4b696f7ccd5010f28d823812d40`；本轮实际 `ls-remote origin refs/heads/main` 读回同一 SHA。当前未提交改动原为插件缺口文档/证据，均保留。
- 后端全 Go，沿用光影前端；Linux、本地存储、单用户。复用已有正确实现；不新造 Python 兼容环境，不重新引入退休业务运行框架。
- 排除 AI、工作流及其专用扩展/接口/页面/依赖；继续排除用户此前已撤销的官方等级/RSA认证门禁。普通登录、自定义API/资源鉴权、插件认证 provider、普通事件/命令/定时服务均保留。

**`app/modules` 的31个内置模块不是本次需要全部迁移的对象。** 具体第三方业务插件也不在本轮开发范围。只为插件平台补齐必要宿主合同，优先复用光影既有业务能力。固定平台的 `app/plugins` 仅含基类，没有具体第三方插件；实际依赖仍须由已经固定的安装版插件快照反查，不能仅扫描基类就宣布完整。

## 需要保留、适配和补齐的内容

以下13组是盘点/实施分组，不是13项已实现功能，也不是重新生成验收总数。实现状态仍以 `docs/plugin-module-parity-matrix.md` 为唯一来源；当前已确认清单45/59，原有45项本轮没有重跑验收。

| 分组 | 需要迁移或保持的行为与源依据 | 光影已有实现候选 | 后续工作 |
|---|---|---|---|
| P01 发现与生命周期 | `app/core/plugin.py:72–265`：代码/类/运行对象、初始化、停用、停止、重载、失败清理和启动恢复 | `plugin_original_lifecycle.go`、`plugin_original_registry.go`、`plugin_original_startup.go` | 复用，按实际源顺序和失败状态复核；不能仅确认worker能启动。 |
| P02 仓库、安装与更新 | `app/helper/plugin.py:620–995`、`app/core/plugin.py:1332–1849`：市场/本地仓库、版本选择、Release、兼容性、安装更新卸载、下载/初始化失败保护、统计评分和开发热重载 | `plugin_original_markets.go`、`plugin_original_install.go`、`internal/plugins/artifacts/` | 复用现有Go制品方式，核清与Python导入/pip/源码热重载的必要差异；不擅自改成NAS自动编译。 |
| P03 分身、重置与卸载 | `app/core/plugin.py:1905–2235`、`app/api/endpoints/plugin.py:742–761,993–1030`：身份/代码/配置/KV/文件/前端资源隔离，更新、重置、普通卸载与分身清理差异 | `plugin_instances.go`、`plugin_original_identity.go`、`plugin_original_reset.go`、`plugin_original_uninstall.go`、制品clone | 统一由平台管理；业务插件不重复实现分身。复核复制后关闭开关、更新及冷恢复。 |
| P04 基类、配置与数据 | `app/plugins/__init__.py:239–326`：配置、同步/异步KV、数据目录、默认值合并与保存后初始化 | `plugin_original_base.go`、SDK `base.go`、配置与表单组件 | 复用；保存后服务、命令、API重注册和错误传播一并核对。 |
| P05 普通事件、命令、输入和消息 | `app/core/event.py`、`app/command.py:269`、`app/helper/interaction.py:402`、`app/plugins/__init__.py:328` | 事件/命令/输入桥、`plugin_original_notice.go`、系统消息资源 | 复用普通入口；通知接入既有发送器并保留模板/历史/队列/错误，不自动扩成迁移所有通知渠道。 |
| P06 定时与手动服务 | `app/plugins/__init__.py:128–139`、`app/scheduler.py:1251,1338–1390`：声明、job隔离、更新先移旧job、手动运行、进度、停止 | `plugin_original_services.go`、后台提交与进度资源 | 复用；普通定时任务不属于被排除的工作流。核对实际时钟、取消/关闭和异常。 |
| P07 自定义API与普通认证 | `app/api/endpoints/plugin.py:114–233`、`app/core/plugin.py:873–903,1092–1131`、`app/core/auth.py`：动态路由、v1/v2、apikey/bear/显式匿名、静态资源和登录provider | `plugin_custom_api.go`、普通认证/静态资源桥、`PluginAuthLogin.vue` | 复用；get_plugin_apis不按get_state过滤，卸载/更新要移除旧路由。官方站点认证UI排除，普通登录provider保留。 |
| P08 日志与运维交互 | `app/log.py:345`、`src/views/system/LoggingView.vue`、`PluginLogDialog.vue`：插件日志归属、文件/SSE/过滤/下载、进度与失败交互 | `plugin_logs.go`、投影/轮转、`PluginLogDialog.vue` | 复用；缺文件、不可读、断流、轮转、重连及不同实例隔离分别验证。 |
| P09 前端完整入口 | 固定前端 `PluginCardListView.vue`、Form/Page/DashboardRender、federationLoader、登录/侧栏/AppCenter/日志/通用Dialog：市场、卡片、分组排序、设置/数据页、Vue联邦、仪表盘和导航 | Plugins、配置/数据/联邦/仪表盘/导航组件与相关后端 | 复用现有技术栈和已确认UI边界。不能只移植名称含plugin的文件；31个共享前端依赖已补绑定。 |
| P10 宿主调用插件的覆盖 | `app/core/plugin.py:933`、`app/chain/__init__.py:225–517`：get_module、优先级、同步/异步、返回聚合和系统回落 | `plugin_original_modules.go`、module DTO/结果桥 | 复用并按源语义验证：None/全None元组回落，list聚合；False、0、空dict、空字符串仍可阻止核心回落。 |
| P11 插件主动调用宿主 | `_PluginBase:40–50` 注入chain/systemconfig/data/message/event；实际插件还可直接导入helper/DB/Chain | `plugin_original_resources.go`、准确Hash任务读取、只读inventory及分类资源；已有业务宿主 | **当前主要补齐项。** S01–S13依赖清单和实际调用点已有记录；不能用另一方向的get_module或旧业务包代替可调用入口。 |
| P12 启动与Go必要适配 | startup/lifecycle、插件初始化、普通命令/调度、资源/网络/代理、包安装及混合配置 | `cmd/server`、runtime、标准SDK/制品验证、现有Go包 | 原Python导入、类改写、pip/二进制依赖做必要Go适配；现有正确实现保持，明确源码未闭合的外部输入。 |
| P13 完整调用链验收 | 安装→保存→初始化→调用→停止/重载→更新→卸载，以及失败、重启和分身 | 既有真实worker/宿主/浏览器与源行为证据 | 后续需要真实启动光影、浏览器和真实依赖；旧证据须绑定当前实现和依赖后才可复用，本轮不认领新通过。 |

目标文件在上表省略的路径前缀：后端 `internal/app/`，SDK `internal/plugins/`，前端 `web/src/components/`。精确现有候选和固定源码符号保存在账本子合同与取证文件中；“候选”不代表本轮验收。

## 已确认需要补的宿主桥接

沿用唯一对照表 S01–S13，不建立另一份竞争状态表：

- S01–S03：已完成任务枚举；备份/fastresume/临时文件完整接入；添加、校验、启动和只删任务。
- S04–S07：站点资料/账户操作、订阅、下载/整理历史、限定全局设置读写。
- S08–S10：媒体服务器刷新、受管文件操作、跨插件管理和关联调度。
- S11–S13：宿主影视识别、站点浏览/RSS/种子获取，以及下载器标签/分类/暂停等附加控制。

这些接口应桥接现有Go业务能力；普通Go自身的文件、网络、解析或算法没有同名RPC，不因此被判为必须新建公共系统。详见 `docs/plugin-shared-host-full-audit-20261029.md` 的源码映射和限制。

## 排除与仍待核实

明确排除 `_PluginBase.get_actions/get_agent_tools`、插件actions/Agent工具聚合与缓存、工作流消费者、AI初始化/模型/工具调用和专用前端。`app/core/plugin.py`、`app/scheduler.py`、`app/chain/message.py`、配置/Schema等混合文件必须拆符号，不能整文件删除。HTTP的 `USER_AGENT` 也不是AI Agent。

官方SitesHelper等级/RSA及 `UserAuthDialog.vue` 的 `site/auth` 路线按用户既有决定排除；普通 `login.vue:208/249`、`get_auth_providers`、票据兑换与API资源鉴权保留。独立审查曾误把UserAuthDialog当普通provider依赖，本轮核源后已纠正，原误归类留在correction_history。

本轮尚不能闭合的事项：

1. 固定平台没有具体插件代码。实际插件的动态方法、属性、直接import和宿主依赖需继续按已绑定快照核对；当前13类缺口不构成绝对穷尽保证。
2. SitesHelper普通索引/账户资源来自外部CPython制品及站点数据；`docker/Dockerfile:107–111`、`app/helper/resource.py:46–57` 指向可变仓库来源。排除官方认证不等于删除普通站点能力，也不能把主仓commit当作这些制品已固定。
3. Go产物/身份/文件系统边界、Vue已构建产物与动态依赖需要逐项保留原行为和已批准适配；不引入Python运行依赖、空接口或兼容壳。
4. 本轮没有执行源程序、光影、浏览器差分或生产写操作。因此这是范围盘点，不是实现完成或等价验收。

## 独立复核、账本与交付

两个隔离上下文先从原始固定源独立发现，再与主索引对账。结果覆盖19组保留的平台职责、28个插件管理API，并补列31个通用前端文件和9个后端依赖。259处引用的机械位置核对不等于259项功能通过，也不能用整树1594个文件登记替代语义核对。

既有 `migration/ledger.json` 的顶层是另一份已安装插件快照合同，不能改写成MoviePilot固定提交或覆盖其历史状态。本轮在同一账本新增 `plugin_module_scope_plan_20261031` 子合同；原顶层字段保持。插件实现状态仍在既有唯一对照表。导出的检查视图只用于技能结构检查，非第二份权威账本。

本轮执行非final结构/完整性检查，检查源树节点、对象ID、排除授权、功能反向引用；不执行最终等价门禁。证据在 `migration/evidence/plugin-module-scope-20261031/`，恢复从账本子合同和本说明继续。

后续若开始开发，顺序为：先固定必要依赖和原行为案例，再补共享宿主桥，接入相关前后端与生命周期，最后验证失败/重启/分身完整链。本轮到范围清单为止，未启动该实施过程，未提交、推送或部署。

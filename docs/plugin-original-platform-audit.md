# MoviePilot 插件平台功能与迁移契约审计

审计日期：2026-10-16。此文依据用户“按原版 1:1 实现插件功能，并迁移已安装插件”的新要求，覆盖插件平台完整行为；此前“P1 仅预留”不再代表本次开发范围。

这是一份**源码审计与待实施验收契约**，不代表新项目已实现下列功能，也不代表原插件已迁移。此次没有访问 NAS、读取业务数据库或凭据、安装/运行插件、操作媒体或修改旧项目。用户实际安装清单、版本、配置、数据和文件应由另外的只读盘点取得；不能用源码目录或市场列表代替用户安装清单。

## 1. 取证基线与不能省略的区别

| 项目 | 本地只读源码 | 提交 |
| --- | --- | --- |
| MoviePilot 后端 | `/Users/accidia/projects/MoviePilot` | `8a83efeacd3e26b82be495e17d50146e31c96a60` |
| MoviePilot 前端 | `/Users/accidia/projects/MoviePilot-Frontend` | `2829e3b08214348f05ff2f5beae644882eccad58` |

审计对应这两个本地源码版本及读取时的工作区。前端原有未提交变更包含 `src/pages/resource.vue`、`vite.config.ts`；联邦构建配置按实际读取的 `vite.config.ts` 内容记录，不能把它冒称该提交的原样内容。它们不自动证明 NAS 当前容器与提交完全相同；实机平台版本、实际插件源文件及功能还需逐项核对。没有修改参考仓库原有工作区变更。

必须分别验证三个层面：

1. **平台交互等效**：我的插件、市场、仓库、文件夹、配置、数据页、日志、版本管理、分身、服务入口等能按原版操作。
2. **插件业务等效**：每个原已安装插件原来执行的动作、事件、查询与副作用在新项目实际可用。一个按钮、一份配置表或下载成功的 ZIP 不等于插件运行成功。
3. **状态迁移完整**：安装及启用状态、版本与来源、配置、KV 数据、独立文件、分身关系、顺序/文件夹均有对应结果。不能仅保存插件名称就报告迁移完成。

原版插件是继承 `_PluginBase` 的 Python 类，直接访问 `ChainBase`、系统配置、数据库、事件、消息、调度器及其他 Python 包。Go 后端不能执行这些类，也不能靠加载原 `package.v2.json` 获得业务实现。在继续遵守非 Python 运行依赖约束时，需要为实际插件提供等效的 Go 实现与宿主能力；如果另选原 Python 兼容运行器，必须明确它引入 Python 运行与资源依赖，不能仍称“无 Python”。本审计不替用户悄悄改变语言/运行时要求，也不把不兼容插件标为可运行。

## 2. 原版完整功能验收表

下表是新项目逐项验收清单，所有项目在新项目的完成状态均需另行提供证据。为缩短源码引用，`F` 为 `MoviePilot-Frontend/src/`，`B` 为 `MoviePilot/app/`；路径及行号按以上基线。

### 2.1 我的插件、市场与搜索

| ID | 原版行为 | 1:1 验收要点 | 主要证据 |
| --- | --- | --- | --- |
| PL-01 | 插件页有“我的插件／插件市场”两个页签，管理入口受管理员权限保护，页面保活 | 来回切换能刷新正确数据，不把市场项混成已安装项；移动端主操作入口与桌面浮动按钮均可用 | F `pages/plugin.vue:1`、`router/index.ts:162`、`views/plugin/PluginCardListView.vue:43` |
| PL-02 | 已安装列表与市场分开请求；市场按需缓存/强刷 | 普通进页不强制遍历所有仓库；强制刷新可观察，单仓失败不能被误报成“没有插件”；列表刷新不打断拖动排序 | F `PluginCardListView.vue:788`、`:867`；B `api/endpoints/plugin.py:343` |
| PL-03 | 卡片显示图标、名称、描述、版本、运行状态、作者/主页、下载次数及更新角标 | 已安装、已加载、启用、存在更新不能混为一个布尔值；加载失败仍须可定位原因 | F `components/cards/PluginCard.vue:543`；B `core/plugin.py:1375` |
| PL-04 | 已安装名称可选/输入/清空；有运行中、存在更新筛选 | 名称忽略大小写；原当前实现中运行中/更新条件优先于名称，并选时两者 AND，不能把源码描述成三条件始终同时 AND | F `PluginCardListView.vue:253`、`:981` |
| PL-05 | 市场按名称及描述搜索，作者/标签/仓库多选，已选 chips 可移除或清空 | 字段间组合筛选；标签多值任一命中；本地源显示本地路径或“本地”，不显示成 GitHub 仓库 | F `PluginCardListView.vue:681`、`:876`、`:962` |
| PL-06 | 市场按热度、名称、作者、仓库、添加时间排序；虚拟列表首 20 项，滚动追加 20 | 保留可见排序选择、空状态、加载状态；当前原实现只有统计非空时应用排序，客户端分页不等于后端翻页 | F `PluginCardListView.vue:134`、`:876`、`:1572`、`:1815` |
| PL-07 | 浮动搜索跨已装与市场查名称/描述 | 展示版本、安装标记、标签和图标；已安装点击进入数据页或配置，未安装点击安装；支持 URL `?id=ID` 定位插件 | F `components/dialog/PluginSearchDialog.vue:7`、`PluginCardListView.vue:756`、`:1012` |
| PL-08 | 卡片有数据页时优先打开数据，否则打开设置 | 不能所有卡片都只开通用表单；卡片菜单及 URL 定位同样遵循该分支 | F `PluginCard.vue:359`、`:430` |

### 2.2 仓库与来源

| ID | 原版行为 | 1:1 验收要点 | 主要证据 |
| --- | --- | --- | --- |
| PL-09 | 市场设置读取/保存逗号分隔的 `PLUGIN_MARKET` 字符串 | 列表/文本页签相互转换，增删、行内编辑、Enter 确认/Escape 撤销、拖动排序、无效/重复项提示均可用 | F `PluginMarketSettingDialog.vue:31`、`:79`、`:111`、`:191` |
| PL-10 | 文本支持换行、中英文逗号；过滤空项、trim、精确去重 | 保存拒绝无效项和空仓库清单；原前端 URL 格式检查仅为 HTTP(S)，不等于连接成功 | F `PluginMarketSettingDialog.vue:31`、`:171`、`:444` |
| PL-11 | “同步 Wiki”合并仓库、计数并立即持久化 | 这是有写入的同步动作，不是预览；关闭弹窗不会撤销已同步仓库。普通“保存”与同步的状态分别反馈 | F `PluginMarketSettingDialog.vue:142`；B `api/endpoints/system.py:812` |
| PL-12 | 多仓库合并、v2 清单与基础清单兼容回退、按插件 ID 选最高版本 | 同 ID 同版本按市场顺序保留来源，在线市场优先于同版本本地源；不能因并发完成顺序改变来源 | B `core/plugin.py:1526`、`:1558`、`:1725` |
| PL-13 | 支持 `PLUGIN_LOCAL_REPO_PATHS` 与 `local://` 安装来源 | 支持 `package.v2.json`/`package.json` 和 `plugins.v2/<id>`/`plugins/<id>`，来源路径/包代际明确，不将运行目录当本地源 | B `helper/plugin.py:82`、`:209`、`:725`；B `core/plugin.py:1462` |

### 2.3 安装、版本与生命周期

| ID | 原版行为 | 1:1 验收要点 | 主要证据 |
| --- | --- | --- | --- |
| PL-14 | 市场卡、详情页和搜索结果均能安装 | 安装显示进度、业务失败原因，成功后刷新已装/市场/统计/文件夹；不能仅看 HTTP 200 | F `PluginCardListView.vue:717`；B `api/endpoints/plugin.py:576` |
| PL-15 | 系统版本要求与认证等级约束 | 显示 `system_version`、兼容警告并在前后端阻止不兼容安装；需认证/特殊密钥的插件不能伪造授权等级 | B `helper/plugin.py:160`、`core/plugin.py:1852`；F `PluginMarketDetailDialog.vue:100`、`:185`、`PluginCard.vue:222` |
| PL-16 | 有更新角标、更新说明和“更新到最新” | 展示插件更新历史/兼容提示；使用指定来源更新，成功后新版本实际加载、旧服务/API 不重复；当前弹窗没有单独当前/目标版本标签 | F `PluginCard.vue:193`、`PluginVersionHistoryDialog.vue:42`；B `api/endpoints/plugin.py:410` |
| PL-17 | 版本历史及后端指定 Release 版本安装/回退 | 前端历史 Markdown、载入/失败/无历史状态；后端仅声明 release 且有匹配 tag/ZIP 的版本可安装，并返回当前/最新标记；不虚构前端旧版选择器 | B `api/endpoints/plugin.py:428`、`helper/plugin.py:418`、`:620` |
| PL-18 | 下载支持当前索引 Release 与源文件模式 | 当前 Release 资产暂不可用时可走源文件回退；用户选定历史 Release 不应悄悄安装别的版本。需要依赖处理及内容准备失败的明确结果 | B `helper/plugin.py:620`、`:1738`、`:1865`、`:2689` |
| PL-19 | 重新加载插件 | 先停旧实例/事件，再初始化配置，重建调度、命令、动态 API；不是单纯把 UI 状态改成运行中 | B `core/plugin.py:156`、`:641`；B `api/endpoints/plugin.py:223`、`:560` |
| PL-20 | 常规卸载与“重置”含义不同 | 常规卸载移除已装登记、API、调度、文件夹成员与实例，保留普通插件配置/KV；重装可复用。不能默认把重置当卸载 | B `api/endpoints/plugin.py:992` |
| PL-21 | 重置配置及数据 | 先发送 `PluginDataReset` 事件供插件补偿，再停止实例、删除配置及 KV、重载；未证实可直接删除独立数据目录 | B `api/endpoints/plugin.py:739`；B `schemas/event.py:67` |
| PL-22 | 创建插件分身 | 后缀、名称、描述、自定义版本、图标；生成独立 ID、配置前缀、Vue 联邦名称/资源与配置，进入原插件所在文件夹 | F `PluginCloneDialog.vue:40`；B `core/plugin.py:1905`、`:2004`、`:2139` |
| PL-23 | 分身不是第二张相同卡片 | 原版复制原配置并尝试将 `enable`/`enabled` 置 false；分身操作使用自己的存储命名空间、事件/服务/API。分身卸载还删除其配置、KV 和分身代码目录 | B `core/plugin.py:1905`；B `api/endpoints/plugin.py:1009` |

原版没有卡片上的平台级统一启停开关。卡片圆点是插件 `get_state()` 返回值，启用通常由插件自身配置控制。不能用固定的 `enabled` 字段取代所有插件的状态逻辑。卸载、重置、删除文件夹各有一次操作确认，取消不发送对应写请求（F `PluginCard.vue:114`、`:193`、`PluginFolderCard.vue:140`）。原版安装器的 `force=true` 也可能跳过临时内容备份，不能把源码描述成任何更新失败都会自动完整回滚；新项目可提供更可靠恢复，但必须区分实际验证。

### 2.4 配置、页面、日志与执行

| ID | 原版行为 | 1:1 验收要点 | 主要证据 |
| --- | --- | --- | --- |
| PL-24 | 配置读取动态表单及默认 model，合并已保存配置 | false、0、空字符串与未设置保持区别；未知字段不能被通用表单保存动作清掉 | B `api/endpoints/plugin.py:648`；F `PluginConfigDialog.vue:85` |
| PL-25 | Vuetify JSON 配置表单 | 递归组件、属性、子内容、命名插槽、双向绑定、条件显示、表达式与事件都需兼容，不能仅支持输入框/开关 | F `components/render/FormRender.vue:15` |
| PL-26 | Vue 联邦配置组件 | 动态加载 `./Config`，传入 `initial-config`、API 实例，接收 save/switch/close；超时、骨架、错误及销毁行为明确 | F `PluginConfigDialog.vue:46`、`:120`、`:187` |
| PL-27 | 配置保存会重建真实插件状态 | 保存成功后调用插件初始化，再刷新调度/命令/API；保存失败保留弹窗和字段，不发成功提示 | B `api/endpoints/plugin.py:975`；F `PluginConfigDialog.vue:85` |
| PL-28 | JSON 数据详情页与 Vue 联邦数据页 | 有内容、空页、加载、切换设置及 Vue 模块加载错误；JSON 页面动作执行后刷新数据，Vue Page 成功加载后 action 不重复探测渲染模式；原 JSON 请求错误仅 console，新项目不能据此伪报成功 | F `PluginDataDialog.vue:42`、`:95`、`:126`；B `api/endpoints/plugin.py:680` |
| PL-29 | 页面元素声明 API 动作与进度 | `events` 的 API/method/params 可触发，完成向上发送 action 并重新获取页面；不把展示按钮当业务完成 | F `components/render/PageRender.vue:35` |
| PL-30 | 独立插件日志弹窗、实时流、完整日志新窗和下载 | 文件名按插件小写 ID；级别/关键词过滤、暂停日志流、跟尾/上滚暂停跟尾、新日志计数、重连与后台断流、下载均可用 | F `PluginLogDialog.vue:38`、`LoggingView.vue:51`、`:398`、`:530`；B `system.py:953`、`:1067` |
| PL-31 | 注册一个或多个插件仪表板 | key/name 元数据、多列布局、标题/副标题/边框、刷新周期与 User-Agent 参数；支持 JSON/Vue 渲染 | B `core/plugin.py:1191`、`:1223`；B `plugin.py:704` |
| PL-32 | Vue 插件可声明主侧栏全页入口 | `plugin_id/nav_key/title/icon/section/permission/order`，过滤停用插件和非法 key，主侧栏、应用中心和全页路由正确定位组件 | B `core/plugin.py:1133`；F `pages/plugin-app.vue`、`utils/pluginSidebarNav.ts` |
| PL-33 | 插件注册定时服务、命令、事件和工作流动作 | 服务 ID、cron/interval/date、参数、手动执行、进度/失败和重载注销可用；工作流返回更新后的上下文，不是点击即完成 | B `scheduler.py:1197`、`:1251`、`:1338`、`command.py:269`、`workflow/actions/invoke_plugin.py` |
| PL-34 | 插件可替换核心模块实现 | 启用状态、插件顺序、返回列表合并或首个有效结果、同步/异步、异常反馈均是业务契约；Go 不能只展示这类插件却不提供被替换能力 | B `core/plugin.py:933`、`chain/__init__.py:298`、`:331` |
| PL-35 | 插件可提供认证入口及 Agent 工具 | 认证页面联邦资源、票据交换、工具注册/失效与启停一致；若迁移插件依赖这些能力，必须实做，不能把它们称作普通配置页面 | B `core/plugin.py:1004`、`:1092`、`api/endpoints/auth.py:50` |
| PL-36 | 插件自定义 HTTP API 与静态资源 | 命名空间、HTTP 方法、query/body、业务响应、API key/bearer/声明匿名模式可适配；普通静态资源需登录资源 Cookie，认证插件仅特定 remote 可匿名 | B `api/endpoints/plugin.py:114`、`:279`、`:764` |

日志流暂停与用户先前要求移除的**全局暂停按钮**不同，不应因为后者被移除而删除插件日志的暂停/跟尾控制。原版没有可适用于所有插件的 `POST /plugin/{id}/run`：单次执行还可能是 `init_plugin` 读取 `onlyonce` 等配置，或插件专有 API；必须核对每个插件真实入口。

### 2.5 文件夹、排序和界面状态

| ID | 原版行为 | 1:1 验收要点 | 主要证据 |
| --- | --- | --- | --- |
| PL-37 | 新建、打开、返回、重命名及删除文件夹 | 名称必填、拒重，失败回滚本地状态；删除分组不卸载插件，成员回到根列表 | F `PluginCardListView.vue:1196`、`PluginFolderCreateDialog.vue:41`、`PluginFolderRenameDialog.vue:34` |
| PL-38 | 文件夹外观 | 显示图标开关、10 图标、8 色、8 渐变、自定义背景图；图片优先，保存保留成员/顺序 | F `PluginFolderSettingsDialog.vue:5`、`:97`；`PluginFolderCard.vue:65` |
| PL-39 | 插件拖入文件夹、换文件夹及移出 | 禁止嵌套文件夹、无效 ID 和重复成员；迁移到新组须从旧组移除；筛选时目录数量及可见性正确 | F `PluginCardListView.vue:345`、`:1338`；`PluginMixedSortCard.vue:38` |
| PL-40 | 文件夹与根插件混排、文件夹内部排序 | 排序模式禁卡片默认点击/菜单，保存混排顺序及组内顺序；重载和另一会话恢复正确，不因自动刷新丢失拖动结果 | F `PluginCardListView.vue:519`、`:559`、`:1658` |
| PL-41 | QuickAccess 最近使用/全部插件快捷面板 | 只展示有数据页的已装插件，最近使用最多 3 项；点开数据页隐藏切换配置按钮并记录访问；底部上滑/背景/关闭均可退出 | F `layouts/components/QuickAccess.vue:158`、`:189`、`composables/useRecentPlugins.ts:41` |
| PL-42 | 插件仪表板参与个性化启用、排序、拖动及缩放 | 按 id:key 区分同插件多个部件；桌面/平板/手机分别保存布局；离开页面停自动刷新、激活后重新读取；自动装饰色不是用户颜色偏好 | F `pages/dashboard.vue:245`、`:558`、`:650`、`:738`、`:1145` |
| PL-43 | 登录页展示插件认证入口并交换票据 | 仅显示 enabled 的插件 remote；加载 AuthPage/声明组件，接收 authenticated ticket 后走标准登录，失败可见；不把安装一个认证插件等同于认证已打通 | F `pages/login.vue:95`、`:205`、`:826` |
| PL-44 | 工作流编辑器选择插件/动作并输入 JSON 参数 | 两级选择真实注册项；对象美化展示，非法 JSON 暂留原输入，不静默清空；执行需验证真实结果/上下文 | F `components/workflow/InvokePluginAction.vue:19`、`:51`、`:87` |
| PL-45 | 开发模式与本地源变更监测 | `DEV`/`PLUGIN_AUTO_RELOAD` 控制监测；已装本地源 Python 变更同步并重载，联邦产物等入口完整后同步；requirements 变更提示重装，不假装热装依赖；Go 等效实现需明确源码构建/包刷新边界 | B `core/plugin.py:289`、`:335`、`:582` |

前端完整检索没有发现插件“收藏”按钮或收藏持久化功能；侧栏入口与最近使用不是收藏。QuickAccess 注释写“已启用”，实际查询后只筛 `has_page`，并未再筛 `state`。侧栏导航 store 缓存成功结果（含空数组）、合并并发请求，失败延迟 500ms 重试一次但不缓存失败，登出清空，避免另一登录态沿用旧入口（F `stores/pluginSidebarNav.ts:8`、`stores/auth.ts:32`）。版本历史弹窗当前可读说明并“更新到最新”，没有发现调用 `plugin/releases`/`release_version` 的前端旧版选择器；指定 Release 属于已有**后端**能力，应与已有 UI 区分。`PluginMemoryInfo` 虽有模型定义，未找到对应平台统计入口，不能据此宣称已有插件内存统计页。

## 3. 原版 HTTP 契约

以下为相对 API 基址的路由，原后端通常暴露 `/api/v1/`；插件动态路由还注册 `/api/v2/` 别名。管理接口多数要求超级管理员，侧栏/仪表板等另有相应登录鉴权。**原接口混合直接对象/数组与 `{success,message,data}` 包装**，不能让新平台前端只判断 HTTP 状态，也不能把所有返回强改成同一种包装后仍称兼容。新 Go 服务如使用自己的规范路由，需要兼容适配层承接既有页面/联邦组件的调用。

| 方法与路由 | 输入 | 业务结果/语义 |
| --- | --- | --- |
| GET `plugin/?state=installed\|market\|all&force=bool` | 状态、是否强刷 | `Plugin[]`；market 包含未安装项及可更新候选，all 可能包含同 ID 的已装项和更新候选 |
| GET `plugin/installed` | 无 | 安装 ID 数组，来源是 `UserInstalledPlugins` |
| GET `plugin/history/{id}?force=bool` | 默认 force=true | 插件对象及远程更新说明；不存在 404；按来源查单插件，不强刷全部市场 |
| GET `plugin/releases/{id}?repo_url=...&force=bool` | 指定仓库 | `{release_supported,latest_version,current_version,items[]}` |
| GET `plugin/statistic` | 登录 | `{plugin_id:install_count}`，依赖 MoviePilot 服务端统计 |
| GET `plugin/rating?plugin_ids=a,b`、GET `plugin/rating/{id}` | 批量/单项 | `average_rating,rating_count,user_rating`；这是原版已有后端契约，不能据此虚构前端当前有评分控件 |
| POST `plugin/rating/{id}` | `{rating:0.1..5.0}`，步长 0.1 | 只允许已安装插件；远端失败 `success:false` |
| GET `plugin/install/{id}` | `repo_url,release_version?,force?` | 兼容检查、准备代码/依赖、登记、重载、注册；`success/message` |
| GET `plugin/reload/{id}` | ID | 停旧、重载、重建注册；`success` |
| DELETE `plugin/{id}` | ID | 常规卸载保留配置/KV，分身例外；成功更新文件夹 |
| GET `plugin/reset/{id}` | ID | 先重置补偿事件，再删配置/KV并重载；不是卸载 |
| GET `plugin/{id}` | ID | 配置对象；可能含秘密，只可用于授权迁移/配置会话，不写入审计报告 |
| PUT `plugin/{id}` | 完整配置 JSON | 保存并应用配置，更新事件/命令/服务/API；`success` |
| GET `plugin/form/{id}` | ID | `{render_mode,conf,model}`；model 为默认值合并持久配置 |
| GET `plugin/page/{id}` | ID | `{render_mode,page}`；不存在/未加载 404 |
| GET `plugin/remotes?token=moviepilot` | 约定标记 | `{id,url,name}[]`；URL 指向插件静态 `remoteEntry.js`，该标记不是管理员凭据 |
| GET `plugin/sidebar_nav` | 登录 | `PluginSidebarNavItem[]` |
| GET `plugin/dashboard/meta` | 管理员 | `{id,name,key}[]`；支持单插件多个板块 |
| GET `plugin/dashboard/{id}/{key}`、GET `plugin/dashboard/{id}` | User-Agent 可选 | `PluginDashboard` 或 null；旧版无 key 方法也兼容 |
| GET `plugin/file/{id}/{path}` | 资源 Cookie；认证 remote 有限定例外 | JS/MJS/CSS/其他静态内容，资源 MIME/路径检查必须可用 |
| GET `plugin/folders` | 无 | 文件夹完整字典，旧/新格式兼容 |
| POST `plugin/folders` | 整个文件夹字典 | 保存完整结构，不能只保存 name→members 丢掉外观/顺序 |
| POST `plugin/folders/{name}` | 路径名称 | 创建空旧格式分组，重复 `success:false` |
| DELETE `plugin/folders/{name}` | 路径名称 | 删除分组；不存在 `success:false` |
| PUT `plugin/folders/{name}/plugins` | `string[]` | 更新成员；完整新格式读写主要由前端 `POST folders` 完成 |
| POST `plugin/clone/{id}` | `{suffix,name,description,version,icon}` | 成功返回消息并登记/重载分身、加入原文件夹；失败保留原插件 |
| GET `system/setting/public/PLUGIN_MARKET` | 登录 | `{success,data:{value:逗号字符串}}` |
| POST `system/setting/PLUGIN_MARKET` | **JSON 字符串** | 立即更新市场设置；不是 repo 数组对象 |
| POST `system/setting/PLUGIN_MARKET/sync-wiki` | `{}` 或 `{wiki_url}` | 合并并持久化，返回 repos/value、added_count、total_count |
| GET/POST `user/config/PluginOrder` | 读/写当前用户混排数组 | 读结果 `data.value`；与 `PluginFolders` 独立 |
| GET/POST `user/config/Dashboard`、`DashboardOrder` | 部件开关字典、顺序 `[{id,key}]` | 插件部件与内置部件共存，不能覆盖不相关项 |
| GET/POST `user/config/DashboardGridLayout`、`DashboardGridLayoutTablet`、`DashboardGridLayoutMobile` | `{items:{部件键:{x?,y?,w?,h?}}}` | 按屏幕分别持久化；空 items 包装也须保留，避免旧本地值复活 |
| GET `system/logging?logfile=plugins/{lower_id}.log` | 默认 length=50 | SSE；length=-1 返回完整倒序纯文本 |
| GET `system/logging/download/{lower_id}` | 资源登录鉴权 | 当前及轮转日志 ZIP |
| GET `dashboard/schedule`、`dashboard/schedule/{job_id}/progress` | 管理员 | 插件服务与进度沿用系统服务契约 |
| GET `system/runscheduler?jobid=...` | 管理员 | 提交运行已注册服务；“接受执行”不等于业务完成 |
| GET `workflow/plugin/actions?plugin_id=...` | manage 权限 | `{plugin_id,plugin_name,actions}[]` |
| GET `auth/providers`、POST `auth/exchange` | provider 发现；`{ticket}` 交换 | 插件认证入口返回 remote/组件信息，票据兑换标准登录态 |
| 动态 `plugin/{id}/{api_path}` | 插件声明的 methods/query/body/auth | 保留该插件实际响应契约，不能统一替换为假成功 |

版本历史 `items` 的字段为 `version,tag_name,name,published_at,body,asset_name,is_latest,is_current`。可安装 Release 需 tag=`<PluginID>_v<version>`，ZIP 资产名为该 tag 转小写后加 `.zip`。当前索引的 `release` 能力位、真正存在的资产和主系统兼容状态是不同概念。历史 Markdown 版本不自动代表该版本存在可下载安装包。

## 4. 持久模型与迁移字段

### 4.1 平台记录

| 原记录 | 结构/语义 | 新项目需要保存 |
| --- | --- | --- |
| `UserInstalledPlugins` | 有序插件 ID 数组；不等同于市场返回列表或磁盘有目录 | 原 ID、安装状态；加载失败、停用与未安装分别记录 |
| `plugin.<ID>` | 任意完整配置 JSON | 完整原字段及配置版本、脱敏读取规则、秘密字段的密文；明确空值不能被默认值覆盖 |
| `PluginData` | 行字段 `id,plugin_id,key,value(JSON)`，按 plugin_id/key 查询更新 | 每插件独立 KV、原 key/value 类型、去重规则、迁移来源和收据；不通过普通日志输出 value |
| `CONFIG_PATH/plugins/<ID>` | 插件独立文件/数据库/缓存/历史等任意内容 | 文件清单、相对路径、内容哈希、格式/版本、引用关系；按插件语义判断哪些是运行所需而非一律丢缓存 |
| 运行代码目录 `app/plugins/<id.lower()>` | Python 源码、依赖、dist 联邦资源；可能是本地补丁或分身 | 只读来源证据/版本/哈希；Go 版对应实现与前端资源映射，不能把原代码目录拷到新项目便标可运行 |
| `CONFIG_PATH/plugins_backup/<id.lower()>` | Docker 安装后持久化代码副本，供容器重置后恢复；不是插件运行数据目录 | 可作实际版本/本地补丁的补充证据，不能混同 `CONFIG_PATH/plugins/<ID>`；相关宿主重建后的插件包恢复需可验证 |
| `PLUGIN_MARKET` | 有顺序的逗号分隔 URL | 来源清单及顺序，避免同 ID 在多源中错配 |
| `PLUGIN_LOCAL_REPO_PATHS` 与 `local://` | 本地来源位置和包代际 | 原来源描述及可访问的新来源/替代映射；绝对旧路径不能直接视为新容器路径 |
| `PluginFolders` | 见下节 | 新旧结构、名称/成员/外观/顺序完整保留 |
| 用户配置 `PluginOrder` | 根目录混排顺序，和文件夹定义分开 | 对应用户的完整有序项数组；不能只复制安装 ID 顺序 |
| 仪表板用户配置 | `Dashboard`、`DashboardOrder`、三类 `DashboardGridLayout*` | 插件部件 id:key、开关、顺序/布局与映射后的实例对应；颜色为内存生成的装饰，不作用户偏好迁移；不能覆盖新项目其他用户配置 |
| 最近访问/侧栏配置 | 前端持久状态和声明的导航 key | 最近使用 localStorage=`moviepilot_recent_plugins`，最多 3 项；声明侧栏不是收藏，失效入口不可伪造有页面 |

`Plugin` 公共展示对象包含：`id,plugin_name,plugin_desc,plugin_icon,plugin_version,plugin_label,plugin_author,author_url,plugin_config_prefix,plugin_order,auth_level,installed,state,has_page,has_update,system_version_compatible,system_version_message,system_version,release,is_local,repo_url,install_count,history,add_time,plugin_public_key`。这些是展示/能力元数据；配置和 KV 是另外的模型，不能只迁移这个对象。

### 4.2 文件夹与排序

原版同时读取：

```json
{
  "旧文件夹": ["ExamplePlugin"],
  "新文件夹": {
    "plugins": ["AnotherPlugin"],
    "order": 1,
    "icon": "mdi-folder",
    "color": "#2196F3",
    "gradient": "linear-gradient(...)",
    "background": "",
    "showIcon": true
  }
}
```

当前用户 `PluginOrder` 的新格式为：

```json
[
  { "id": "新文件夹", "type": "folder", "order": 0 },
  { "id": "ExamplePlugin", "type": "plugin", "order": 1 }
]
```

还兼容旧 `string[]`、`{id}[]`。不是用 `PluginFolders.__root__` 或 `__order__` 特殊键存根顺序。文件夹内部顺序保存于成员数组，前端也可能给全局成员顺序添加 `folderGlobalOrder + 0.1 + index * 0.01`；转换时不可只保留整数或按名称重新排序。分身 ID 与原插件 ID 必须同时映射到新实例，分组删除不等于插件卸载。

仪表板服务端配置优先，本地缓存缺省/失败时兜底：`MP_DASHBOARD`、`MP_DASHBOARD_ORDER`、`MP_DASHBOARD_GRID_LAYOUT`（桌面旧兼容）、`MP_DASHBOARD_GRID_LAYOUT_TABLET`、`MP_DASHBOARD_GRID_LAYOUT_MOBILE`。最近使用条目是 `{id,plugin_name,plugin_icon?,has_page,state,plugin_id,access_time}`，按访问时间排序、相同 ID 移到最前；源码虽提供删除/清空方法，当前 QuickAccess 模板没有对应可见按钮。读取浏览器本地状态与从数据库迁移是不同来源，后者不能自动证明前者已迁移。

## 5. Go 实现必须提供的运行和渲染契约

本节列的是必要契约，不是宣称已有 SDK 或实现。可以用单独 Go 插件进程/版本化 stdio 协议实现故障隔离，也可由同一 Go 程序的原生模块承接；不应把运行方式选择当成功能范围缩水的理由。

### 5.1 插件实例与生命周期

| 契约 | 必须表达的内容 |
| --- | --- |
| 包身份 | 原插件 ID、映射后的实现 ID、插件版本、来源仓库/包代际/Release tag、内容哈希、宿主接口版本、OS/架构；分身另有 clone_of/suffix/实例 ID |
| 描述与能力 | 公共元数据；配置、页面、仪表板、侧栏、API、事件、服务、命令、模块替换、工作流动作、认证/工具的真实能力清单 |
| Init/Reconfigure | 读取完整配置和插件自身命名空间数据，返回真实状态及注册项；单次操作参数由具体插件消费，不凭字段名推测通用执行逻辑 |
| State | installed、loaded、业务 enabled/state、loading/error 分离；原 `get_state()` 不总等于 `config.enabled` |
| Stop/Reload | 停调度/事件接收、完成或正确中止在途操作、关闭插件资源，再释放实例和重新注册；不能重载后双重订阅事件 |
| Uninstall | 普通插件保留配置/KV并移除实例/注册/成员；分身按其独立空间处理；卸载不等于清理媒体/种子 |
| Reset | 先通知插件进行状态补偿，再删除约定配置/KV并重建；不隐式扩大到未声明的独立文件或媒体 |
| Clone | 独立配置、KV、文件、API/调度/event namespace、前端资源 scope；复用逻辑实现可以，但实例隔离必须真实 |
| Version replacement | 安装/更新/选定旧版都要加载真正对应版本，配置/数据格式不兼容时给出失败结果与恢复路径，不能仅改卡片版本号 |

宿主提供的数据访问接口至少包含按实例 ID 的配置读写、KV Get/Put/Delete/List、独立文件路径、结构化日志和通知、受控 API/事件调用以及各插件需要的现有下载/站点/整理能力。原版插件能直接调用 Python 内部模块，移植时必须逐个把这类调用映射为明确的 Go 能力；未知方法不能忽略后返回成功。

### 5.2 JSON/Vuetify 动态 UI

表单元素不是单纯 JSON Schema。原 `RenderProps` 节点有 `component,props,content,slots,html,text` 等；表单还支持：

- `model`/`v-model` → `modelValue` 和更新事件；`modelvalue` → `value`；`model:<prop>`/`v-model:<prop>` → 指定属性的双向绑定。
- `show`/`v-show` 条件表达式，`{{ expression }}` 动态属性，以及字符串命中 model key 的间接取值。
- 事件 `on*` 的字符串函数代码，命名插槽和递归节点。原前端用 `new Function` 执行表达式；若新平台采用受限表达式实现，必须验证所有待迁移插件的真实表达式与交互，不能默默吞掉不支持项。
- HTML 与文本内容、实际 Vuetify 组件/自定义 Cron/路径等组件的可用性。当前新项目只有 Vue 原生控件，不能天然解析任意原 Vuetify JSON。

数据页使用另一套 `events` 声明：`eventName -> {api,method,params}`，触发后显示进度并向父级发送 action 以重取数据；当前原 `PageRender.commonAction` 对 GET 使用 query params，其余分支 POST，不应假称支持所有 HTTP 方法。`FormRender` 不读取顶层 events，`PageRender` 不实现表单 model 表达式和命名 slots。`DashboardRender` 又是第三种：支持原样 props、html/text、命名 slots 和递归 content，**不**解释 events/model。不能复用一个简化 renderer 后把三类都标兼容。

仪表板要保留布局 cols、全局 attrs、刷新和 JSON/Vue 两模式。部件唯一键为 key 为空时的 id，否则 `id:key`；`attrs.refresh` 单位为秒，刷新还要求该部件启用、refreshStatus 允许且页面激活，没有视口可见性判定。`attrs.border=false` 省略标准标题/副标题头部，仍有 VCard/VCardText 容器；正常标题采用 `attrs.title ?? name`，另有 subtitle。

### 5.3 Vue Module Federation

原前端构建共享依赖至少为 `vue` 和 `vuetify`。插件的 `remoteEntry.js` 位于插件声明 dist 路径，通过已登录的同源静态资源接口加载；配置 `./Config`、数据 `./Page` 等远程模块各自需要正确 props、事件和宿主 API 对象。

| 远程组件 | 原版输入与事件 |
| --- | --- |
| `./Config` | props `initial-config,api`；事件 `save(newConfig),switch,close`；组件自行提供保存按钮，配置容器处理业务保存与结果 |
| `./Page` | props `api,show_switch`；事件 `action,switch,close` |
| `./Dashboard` | props `config,allow-refresh,api`；节点卸载关闭刷新；失败可重试 |
| 全页组件 | `/plugin-app/:pluginId/:navKey?`；main 尝试 `AppPage→Page`；其他 navKey 转 Pascal 后尝试 `AppPage{Pascal}→AppPage→Page`；props `api,nav-key,plugin-id` |
| 认证组件 | provider 指定组件或 `AuthPage`；props `api,provider,plugin-id`；事件 `authenticated({ticket}),close,error` |

Federation loader 使用 `format:'esm',from:'vite'` 动态注入，失败会重新发现对应 remote 并重试一次；URL 包含当前 origin、pathname 和 API 基址，因此必须覆盖 `/light-media/` 子路径。原 `api` 是 Axios 实例，自动加 Bearer，响应拦截器返回 `response.data`，还暴露为 `window.MoviePilotAPI`；新项目当前的 `{data,error}` 封装不能直接冒充同一契约。

仅把 remoteEntry 文件复制到 `web/dist` 不能证明兼容：需要检查共享版本、导出模块名、动态 chunk/CSS 路径、静态文件 MIME、同源认证、子路径反代、克隆后的 federation scope/资源名，以及插件是否直接导入旧宿主 store/router/API。保留同样界面可以复用可兼容的前端资源；业务 API、身份与会话仍须在新项目闭环，不能依赖旧 MoviePilot 代执行后声称已迁移。

### 5.4 执行与集成

- **定时服务**：原声明含 `id,name,trigger,func,kwargs,func_kwargs`；服务公共 ID 为 `<PluginID>_<service_id>`，`|` 后缀与调度器实例 ID 有区分。支持 cron/interval/date，手动运行、重载清理和进度。
- **事件**：启用/停用实例影响事件 handler 状态；重载事件携带 plugin_id；重置补偿事件含 `plugin_id,reset_config,reset_data`。每个待迁移插件的订阅类型/载荷必须登记，不能只保留一个“onEvent”空方法。
- **命令**：`cmd,event,desc,category,data,show,pid`，按原命令分派产生插件事件；命令执行和通知渠道依赖也属于该插件功能，不由平台默认向外发消息。
- **工作流动作**：插件 ID、动作 ID、参数、结果成功布尔和更新后的上下文；注册服务并不替代工作流动作。
- **核心模块替换**：原返回空结果继续下一个提供者，列表结果可合并，标量已有有效结果会停止；异常进入日志和通知。不能照原内部 Python 函数引用直接序列化到 Go。
- **认证提供者/Agent 工具/侧栏**：这些依赖插件所需宿主能力；单管理员平台能保护管理接口，不等于已经具备原认证扩展或工具注册机制。实际已安装插件若依赖它们，迁移验收必须包含该能力。

## 6. 用户已安装插件的迁移收据与完成条件

本审计没有取得用户实机插件清单，下面是迁移必须产出的结构，**不是虚构的安装列表**。实际收据不得包含配置值、Cookie、Token、私钥或插件数据正文。

| 步骤 | 必须取得/核对 | 不能当作完成的替代证据 |
| --- | --- | --- |
| MI-01 完整清单 | 安装 ID、名称、当前版本、启用/加载状态、分身、来源、更新候选、每插件能力清单 | 市场清单、源码目录、只收集当前运行插件 |
| MI-02 配置 | 原配置结构/字段类型及秘密字段清单、目标版本转换、空值/默认值规则 | 只迁移 enabled 和路径两个字段 |
| MI-03 数据 | 按实例所有 PluginData key、数据版本、条目数/脱敏哈希/迁移结果 | 未复制 KV 却把卡片显示为已迁移 |
| MI-04 独立文件 | CONFIG/plugins/<ID> 文件清单、数据库/持久数据的用途与一致读取方式、路径转换 | 只复制主数据库或无差别丢弃所有插件文件 |
| MI-05 依赖 | 原模块/网络/下载器/站点/目录/消息服务/本地命令/第三方 API；映射为新宿主的可运行能力 | 导入原 Python 源文件、装饰性的配置页 |
| MI-06 组织状态 | 文件夹及其外观、根/组内顺序、分身对应、侧栏入口/偏好 | 按名称重建新列表、把所有克隆合并到原插件 |
| MI-07 行为验证 | 每插件打开配置/页面/日志，执行代表动作，核对业务结果；涉及写入的用隔离样本/用户已授权的具体动作 | 配置 PUT 200、安装 ZIP 成功、重载没有抛错 |
| MI-08 幂等与重启 | 重复迁移不重复创建实例/服务，重启后配置/数据/调度可用；核对 `onlyonce` 等单次标志与在途任务，避免导入配置触发重放；进程失败可恢复 | 一次进程内演示 |

建议每插件收据最少记录：`source_plugin_id,source_version,source_origin,target_instance_id,target_version,config_status,kv_status,files_status,capability_status,verification_cases[],result,reason`。源端停止/删除/卸载不属于“复制迁移”的默认步骤，不能为切换新实例而擅自破坏旧实例；对于会主动执行的插件，需要避免新旧实例同时产生重复动作，并把切换时点和验证结果写入收据。

最终交付需要同时满足：

1. 平台功能表中对应的 UI、API 和运行能力有实际验证；尚未实现项保持真实可见，不出现空成功页。
2. 实机完整安装清单的每个插件、分身都有独立迁移收据；失败或能力缺失不能从清单中消失。
3. 原配置、KV、独立文件、来源和组织状态可核对，敏感值不进入 Git/普通报告/日志。
4. 每个插件有至少一个能证明原业务目标的代表性验证，不能仅证明平台装载成功。
5. Go 新应用与迁移后需要运行的所有组件按实际进程树统计依赖和资源；如引入 Python 兼容层，必须明确披露，不能继续沿用“无 Python”结论。

## 7. 主要源码索引

| 证据 | 位置 |
| --- | --- |
| 全部插件平台管理 API | B `api/endpoints/plugin.py:343–1123` |
| 元数据、仪表板、导航、评分模型 | B `schemas/plugin.py` |
| 加载、状态、配置、服务、事件、分身、市场合并 | B `core/plugin.py` |
| 市场/本地源、Release、依赖、安装恢复 | B `helper/plugin.py` |
| 插件 Python 宿主基类 | B `plugins/__init__.py` |
| KV 数据模型与操作 | B `db/models/plugindata.py`、`db/plugindata_oper.py` |
| 启停和启动后同步 | B `startup/plugins_initializer.py` |
| 日志、仓库同步、执行服务 API | B `api/endpoints/system.py:812`、`:953`、`:1343` |
| 定时器/命令/工作流 | B `scheduler.py:1197`、`command.py:269`、`workflow/actions/invoke_plugin.py` |
| 管理主视图与所有筛选/排序/文件夹流程 | F `views/plugin/PluginCardListView.vue` |
| 已安装/市场/文件夹/排序卡片 | F `components/cards/PluginCard.vue`、`PluginAppCard.vue`、`PluginFolderCard.vue`、`PluginMixedSortCard.vue` |
| 全部对话框 | F `components/dialog/Plugin*Dialog.vue` |
| 动态渲染器 | F `components/render/FormRender.vue`、`PageRender.vue`、`DashboardRender.vue` |
| 全页路由与导航 | F `pages/plugin-app.vue`、`utils/pluginSidebarNav.ts`、`stores/pluginSidebarNav.ts`、`composables/useRecentPlugins.ts` |

本文件属于开发审计材料。不能单独用它宣布“原版插件全量 1:1 已实现”或“用户插件全部迁移完成”。

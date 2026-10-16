# 插件文件夹与排序（PL-37–40）

已接入当前Go应用与可操作前端，用户原文件夹/排序尚未迁移、正式服务尚未部署。分身创建/自动入原文件夹、远程市场、QuickAccess和仪表板仍分别待接线；文件夹界面不能代表全部45项平台功能完成。

## 原版依据与交互

依据本地 MoviePilot 前端提交 `2829e3b08214348f05ff2f5beae644882eccad58` 中 `PluginCardListView.vue`、`PluginMixedSortCard.vue`、`PluginFolderCard.vue`、`PluginFolderCreateDialog.vue`、`PluginFolderRenameDialog.vue`、`PluginFolderSettingsDialog.vue`，后端提交 `8a83efeacd3e26b82be495e17d50146e31c96a60` 中 `api/endpoints/plugin.py`。来源与旧仓库未修改，当前安装版平台未另外声明与这些提交逐字一致。

- 我的插件支持新建空文件夹、进入/返回、重命名、外观和一次确认删除。删除分组使成员回到主列表，不卸载插件。
- 新建/改名400px，外观600px且窄屏全屏；使用原版10个图标、8个颜色、8个渐变、显示图标开关及图片URL。有效图片优先于渐变，关闭/保存失败保留原提交状态，保存错误留在弹窗。
- 排序模式使用既有vuedraggable，停卡片默认动作/菜单及轮询覆盖；支持根插件/文件夹混排、插件拖入文件夹及文件夹内部排序。移出按钮沿用原版右上角小图标，移出后可再拖入其他文件夹；禁止文件夹嵌套和重复成员。
- 搜索已装插件使用原名称大小写不敏感匹配；分组显示匹配成员数，筛选下无匹配成员的组隐藏。运行中/更新筛选的其余完整控件不在本切片完成声明内。市场只在进入对应页签时请求，不再每五秒重复请求未打开的市场。

## 格式与存储

原PluginFolders的旧string[]、新{plugins,order,icon,color,gradient,background,showIcon,...}都保留；原PluginOrder的string[]、{id}[]、旧字符串/对象混排和typed对象均保留。新格式缺order按数组索引使用；旧格式按原前端分支投影。order原值用RawMessage保存，包括文件夹内部排序实际生成的0.1、0.11等小数，不能缩成整数。未知字段、false、0、空列表不因读取/卸载丢失。

原排序引用已删除/改名的文件夹或已卸载插件时可能仍留在PluginOrder，本实现作为无副作用偏好保留，显示时不生成虚构卡片；改名/删除文件夹与拖入动作均不擅自清此原字段。组内排序按原版写folderGlobalOrder+0.1+index*0.01。根排序仅在用户明确拖动后更新；有筛选的不可见项保留在后方，避免因隐藏而丢失其偏好，此为平台保留策略。

新表`plugin_organization`加密保存两个独立源值和revision。当前应用只有admin.id=1，原全局分组/当前用户顺序在一个owner1快照中提交；若未来增加多用户，必须拆分全局分组与各用户顺序，不能将owner1当通用多用户模型。文件夹字段与日志/普通Settings分离，不属于任何插件KV。整个JSON沿用1MiB插件值上限。

前端通过管理员/同源保护的`GET/PUT /api/plugins/organization`提交完整快照；CAS避免另一个页面的旧快照覆盖新成员，失败不报成功。原`/api/v1/plugin/folders`等raw管理路由尚未提供兼容别名，不能据此声称所有原外部API均已兼容。正常卸载与PluginUnInstall批量卸载在原事务内移除相应成员，保留外观、其他成员、PluginOrder以及原卸载配置/KV语义。持久化失败回滚整个卸载。

背景只在浏览器显示用户设置的无userinfo HTTP(S) URL，不由宿主请求；javascript/data/带凭据URL不渲染。图标/颜色/渐变显示限定原预设，未知字段仍保存。这些是明确渲染边界，不是原任意CSS/脚本运行能力。

## 验证

4个Go用例覆盖混合源格式、精度/presence/未知字段、加密重启、CAS、正常及真实批量卸载、SQLite失败回滚、坏成员/嵌套和真实HTTP鉴权/Origin。目标普通通过3.807秒、目标竞态通过44.941秒；本地Go全量App76.447秒，modernconly全量App75.768秒，vet和模块校验通过。前端新增6个实际SFC操作用例（不是重复抄写算法），含保存失败不改原状态、实际拖拽回调、轮询冻结、组内小数顺序与删除确认；总134项通过，类型/格式/构建通过。

真实浏览器连接独占Go App/SDK与SQLite，已操作新建、重命名、外观保存、原生拖入新/旧分组、根混排、组内排序、移出、删除组后插件保留及刷新保留。桌面1280×720与手机390×844检查无横向溢出；样式公共部分独立于配置弹窗加载，避免先开文件夹时关闭按钮/输入样式缺失。截图保存在本次Codex工作区outputs，未作为用户数据导入。

NAS两个CGO0 linux/amd64二进制在alpine:3.22、UID1000:GID1001、256MiB内存/GOMEMLIMIT64MiB、只读/无网络/无capability容器执行：App所选23顶层用例、rsscontext4顶层用例全部通过、零跳过，退出0且无OOM。后者完整保留110个实际原RSS源码参考，应用RSS消费者仍未接入。二进制本地/远端SHA一致；容器和owner标记临时目录已清理，正式三个服务启动时间/重启数不变，见[隔离证据](plugin-organization-nas-verification.json)。NAS此轮为后端/纯包证据，浏览器UI验证使用本机实际构建。

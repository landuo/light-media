# 插件主动调用宿主的遗漏复查

2026-10-29；目标 `main@97476582910dc4b696f7ccd5010f28d823812d40`。这是用户追问“是否还有其他遗漏”后的实际复查，接续下载器三项核对，不重新认领历史完成结论。本轮没有修改产品代码、提交、推送、部署、配置或生产种子任务。

结论：**除 S01–S03 外，又确认 S04–S13 十类共享宿主能力缺少现行标准插件调用入口。** 分类用于维护可验收逻辑单元，不表示只缺十个 RPC；每类具体方法与调用点保存在原始 AST 和下表。已有宿主页面/内部算法、宿主调用插件的覆盖方法，以及插件主动调用宿主，是不同的调用方向。当前白名单仅允许 25 个资源名称，不能用旧业务包或页面可操作证明外部标准 worker 可以调用对应宿主服务。

唯一状态仍在 `docs/plugin-module-parity-matrix.md`，本轮追加 S04–S13，当前已确认清单为 **45/59**；50 综合结论继续“已实现待验证”。动态/运行环境依赖仍有待核实部分，因此也不承诺遗漏仅限这十三类。历史 final JSON 和旧证据不改写。

## 取证范围与方法

1. 从 `migration/evidence/handoff/installed-plugins.json` 取得当前已安装 23 个实例（22 个基础插件）的已封存文件清单。
2. 通过 SSH，在旧 moviepilot 容器内**只读**解析其中全部 77 个 Python 文件的 AST；没有导入插件、执行插件业务或读取数据库/配置。77 个文件全部解析成功，SHA256 全部匹配交接清单。记录 9,976 处语法调用；这些包含插件自身和普通库调用，不能称为 9,976 个宿主接口。
3. 解析导入和构造器赋值，收集 1,361 处宿主对象候选调用、88 个不同的对象/方法组合。此别名匹配不是完整控制流分析，以下具体缺口又与固定平台方法和标准白名单人工核对。
4. 用 `git show 8a83efeacd3e26b82be495e17d50146e31c96a60:<path>` 读取 16 份相关平台源码并记录摘要和定义行。MediaServerHelper 的服务查询来自继承的 ServiceBaseHelper，不因本文件未定义而误判原版缺实现。
5. 从当前标准资源入口及其 Base/Input/Category 分支枚举白名单，得到 25 个资源名称。本轮是源码/契约取证，**不是新功能实机验收或十类新增测试通过**。上轮 9 项回归仅证明准确 Hash 读取、拒绝边界、目录和底层保留文件删除，不用于认领这些缺口已完成。

## 新确认的缺口

表中 B 为固定 MoviePilot 提交，I 为上面逐文件验哈希的已安装业务源码。目标所有行都以 `internal/app/plugin_original_resources.go:14–21,38–82` 和 `plugin_implementation.go:30–46` 为实际标准入口依据；不存在合法入口时不编造新接口名称或假调用成功。

| 项 | 原版源码位置与实际调用 | light-media 当前实现与差异 | 验收状态 |
|---|---|---|---|
| S04 站点资料、签到结果、历史账户数据与刷新 | B `app/db/site_oper.py:26,50,56,192,198,225,266`、`app/chain/site.py:66`；I `autosignin/__init__.py:1837,1839`、`sitestatistic/__init__.py:372,402,1108` | `originalBrushInventory` 仅投影站点基本资料和最新本地统计，不含这组查历史、写签到结果、触发刷新操作；现行资源入口没有相应方法。已有站点管理/采集宿主能力须按实际调用接入，不能把数据目录或配置 KV 当成站点数据库。 | 待实现 |
| S05 订阅查询与更新 | B `app/db/subscribe_oper.py:174,214,258,268`；I `subscribegroup/__init__.py:182,214,269`、`brushflowlanduo/__init__.py:3319` | 现行 Base 操作的是插件配置/KV，标准白名单没有宿主订阅查询/更新入口。宿主已有订阅业务和事件不等于业务插件可以主动查询/更新它。 | 待实现 |
| S06 下载/整理历史查询与删除 | B `app/db/downloadhistory_oper.py:19`、`app/db/transferhistory_oper.py:81,92,189`；I `subscribegroup/__init__.py:253`、`removelink/__init__.py:1190,1193,2095`、`scrapefileclean/__init__.py:992–996` | 插件自身 `get_data/save_data/del_data` 已有，但没有这组宿主历史资源入口；不能用插件本地副本代替原宿主记录的行为。 | 待实现 |
| S07 全局系统配置的限定读写 | B `app/db/systemconfig_oper.py:27,92`；I `remoteidentifiers/__init__.py:150,162`、`pluginuninstall/__init__.py:48,72`、`subscribegroup/__init__.py:334` | Base `get_config/update_config` 含义是插件配置，未覆盖源 `self.systemconfig.get/set` 的宿主全局设置。只应补源普通业务实际用到的键和行为，不能借此接入工作流、AI或多用户。 | 待实现 |
| S08 媒体服务器列表与刷新 | B `app/helper/mediaserver.py:8–18` 继承 `app/helper/service.py:95,142,162`；I `mediaserverrefresh/__init__.py:62,69,137,242,245` 查询服务并调用 `refresh_library_by_items/refresh_root_library` | 当前标准资源不提供媒体服务器选择、连接状态与刷新入口。需优先连接目标已有支持的媒体服务器宿主，不等于本轮应开发全部外部媒体系统。 | 待实现 |
| S09 受管存储文件的 exists/list/delete | B `app/chain/storage.py:46,88,100`；I `removelink/__init__.py:1674,1679,1757` | `StorageOperation` 描述符是插件向宿主提供存储实现的另一方向；实例数据目录也不等于主动调用宿主受管媒体文件操作。标准资源没有此组入口；已存在的路径/归属/历史检查应复用。 | 待实现 |
| S10 跨插件管理与关联调度更新 | B `app/core/plugin.py:121,634,815,825,836,1375`、`app/scheduler.py:1251,1338`；I `pluginuninstall/__init__.py:54–66,193`、`mergesiteswitch/__init__.py:788,790`、`seedrescue/__init__.py:681` | 当前 `Manager.Submit/RefreshServices` 只操作当前调用对象；Base 跨命名空间配置读写不能替代读取插件状态、实际重新初始化、卸载和关联定时任务管理。用户界面生命周期已实现，不等于标准业务插件具有调用入口。 | 待实现 |
| S11 使用宿主影视识别 | B `app/chain/__init__.py:637`；I `subscribegroup/__init__.py:167`、`brushflowlanduo/__init__.py:3330` | `MediaInfo` 等 DTO 和宿主调用插件的识别覆盖链已有，当前标准资源没有插件主动请求宿主 `recognize_media` 的入口。应复用宿主现有识别结果/配置，不强迫各业务插件重复实现身份解析。 | 待实现 |
| S12 通过宿主站点浏览/RSS/种子材料获取 | B `app/chain/torrents.py:402,434`、`app/helper/torrent.py:66`；I `brushflowlanduo/__init__.py:1803`、`seedrescue/__init__.py:898,1021,1402,1529`、`iyuuautoseed/__init__.py:1185` | 站点配置和已有宿主网络能力没有经标准资源暴露为这些普通业务操作；不能要求业务插件另行复制宿主凭据。普通 Go HTTP/解析本身不因此被判为框架缺失，也不预设增加任意 HTTP/RPC 通道。 | 待实现 |
| S13 下载器额外查询/修改控制 | I `cleaninvalidseed/__init__.py:660,667` 标签、`iyuuautoseed/__init__.py:1099` 分类、`brushflowlanduo/__init__.py:1183,1231,2210,2784,3126` 标签/暂停/重新通告；B `app/modules/qbittorrent/qbittorrent.py:304,336,383,490,686`、`app/modules/transmission/transmission.py:168,240,388` 定义普通标签/暂停/分类方法；重新通告的厂商客户端细节仍须在实现时进一步核对 | S01/S03 没有涵盖这些额外行为；现行准确 Hash DTO 的 tags/category 字段只读，不能推导出标签/分类修改、暂停或重新通告能力。底层原生协议代码仍需逐方法核对后复用，未声称每个底层操作已有。 | 待实现 |

## 已有与尚待核实的边界

- 普通 Base 配置/KV/数据目录、实例与分身、生命周期、普通事件、命令与调度声明、日志写入、通知发送、输入交互和系统消息已有入口。本轮不因名字出现在导入列表就将其重复判为缺失；系统消息资源虽在 CategoryEditor 接线文件内定义，处理函数没有限定只允许 CategoryEditor 调用。
- `RequestUtils`、普通 JSON/正则/RSS 解析、插件本身的渠道实现、纯算法和进程信息读取等，可用普通 Go 或已有包原生实现；不能仅凭没有同名 RPC 就判定为公共宿主能力遗漏。
- `getattr`、属性/动态工厂返回对象、配置属性读取、浏览器渲染/OCR、运行环境与备份相关依赖仍需按真实调用契约逐项裁定；这次静态索引不证明全部运行路径等价。非 Python 的页面制品不在本次 Python 主动调用审计覆盖内。未知范围由综合项50保持未验收，不能据25个已支持名称或88个候选方法宣称枚举了所有可能依赖。
- 工作流、AI、官方授权等级认证继续按用户要求排除，不计总数或阻塞。普通事件/命令/调度、普通站点索引器查询不得以此一并删除。旧服务只读，生产写操作未执行。

取证文件及哈希见 `testdata/plugin-resource-full-audit-20261029/manifest.json`，包含收集脚本、77文件 AST、候选调用索引、固定宿主方法定义及标准资源白名单。它们是本轮遗漏依据，不是新增能力已验收的证据。

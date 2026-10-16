# AutoSignIn 2.9.14：Go 主流程、站点适配与恢复

本批把已冻结的自动签到候选合并到正式源码 `internal/plugins/autosignin`，包括原版主流程、22 类站点适配及逐效果 journal。全 Go，不引入 Python 运行环境。**这不是已经可安装或已迁移的插件**：正式注册、实际 SQL owner、调度器、Browser/OCR、站点依赖及消息消费接线仍在推进，原用户配置和 24 个数据键均未导入。

## 来源与行为

以 NAS 实际安装的 AutoSignIn 2.9.14 为参照，而非远端仓库最新版本。主文件 SHA256 为 `04044049166caa06659249b167539f96f1b3505d57da3fbf379ba4472a84008d`。25 个原文件及其静态内容、参考场景和依赖版本保存在 `testdata/plugins/auto-signin-2.9.14`；没有用户配置、Cookie、Token 或真实签到结果。

保留原 10 字段表单、命令、域名 API、服务描述、一次性 date job 请求、普通 init 对实例字段的保留、reload/冷启动的新实例语义、站点删除处理、签到与登录的区别、重试/去重、14 天历史、详情节点树和各站原调用顺序。Manifest 描述、作者及图标对齐安装版；平台层的排序、权限、组件及路由仍须分别接入。

22 类适配包含固定 GET/POST、JSON API、时钟/token、问答、验证码、Tjupt 图片匹配及馒头专用签名。没有用 HTTP 200、登录成功或消失的签到按钮代替签到完成证据。保留原版不直观行为，例如 HDSky 部分响应不增加尝试计数、Tjupt 缓存读写不对称、原统计的平均值分母及同秒覆盖。

| 原版对照 | 数量 | 证据范围 |
| --- | ---: | --- |
| 适配器 → wrapper → Do 的实际安装 AST 调用 | 1,118 | 固定站点、JSON、动态参数、问答、OCR、Tjupt、馒头的完整结果及调用轨迹 |
| 上述 Do 场景经真实 Go 受控回环 HTTP | 327 | 实际 Cookie、请求体、请求顺序、响应、结果、统计与历史 |
| 实际安装 RequestUtils 与依赖的 HTTP/TLS 录制 | 26 | 本次自建端点；不是用户站点连接测试 |
| 原 requests URL prepare | 24 | 原始查询插值、percent、Unicode 与 fragment |
| 原效果失败前缀 | 23 | 配置、历史、调度、事件、通知前的失败及原异常吞掉位置 |
| 原 SiteOper 统计 | 9 | 成功/失败、秒数、日期和 note 的实际 AST 参考 |

AST oracle 的线程池是有序 recorder，不构成原线程池真实并发证据。OCR、浏览器、通知、数据库和事件 recorder 也不构成外部服务验证。真实 SDK 子进程测试验证既有可信绑定、算法、受控 HTTP 和页面；其中旧的末尾 checkpoint Machine 现仅存在测试文件，不能作为生产持久化入口。

## 单一引擎与逐效果恢复

原五个效果相关引擎文件合并进同一 autosignin 包，适配器、配置/页面和 durable Controller 共用类型；没有再维护一套 core/compat 引擎。生产 SDK 只保留 Manifest、固定入口和受控反向能力。旧 Machine 与加密独立 FileStore/OS lease 都移入 `_test.go`，构建产品不会携带这些测试宿主。

生产 `StateJournal` 接收可信 `StateBackend`。Backend 须在真实生命周期和运行记录授权下，短事务完成原效果及其精确收据；网络、OCR、浏览器、等待和消息发送不得放在 SQL 事务内。配置、每次历史保存/删除、统计、缓存、调度意图和通知/事件 outbox 分别按源调用顺序记录。调度计划被记录不表示物理计时器已安装；outbox 已排队不表示消息已送达。

网络顺序为：持久化 intent → 调用已授权能力 → 持久化观察到的响应 → 继续算法。run ID、实例、generation、admission revision、实现、epoch、站点操作及凭据/传输版本由宿主固定。收据核对 Kind、Guard 和 HMAC digest；响应和内部基线仅供可信宿主加密保存，不通过页面或日志返回。

没有收到可信回复，或回复无法持久化时，执行停止并保留 unknown。已提交的远端动作不能仅因工作器/主进程重启而重发。`NewState` 与 `RestoreOwnerState` 已在生产纯函数中实现；实际 owner 接管必须持排他的生命周期授权，在同事务把遗留 intent 转成 uncertain 并建立跨 run 的站点操作阻挡。测试 FileStore 也调用同一恢复函数，不再另藏一份仅测试有效的恢复算法。

本批额外修正了候选恢复边界：

- 已完成或中断的 run 使用原基线，不再依赖当前 Inventory 可用；已完成结果仍校验原 action/plan。
- 域名 API 首次公开站点查找结果、找不到和已知错误均持久化；恢复时不因站点消失而把原成功改为不存在。凭据仍由可信 Factory 解析并校验私有版本。
- 已退役 run 不能在后续 active run 下追加配置或请求；已有精确收据可只读回放。
- 网络/通知收尾写失败会尝试安全标记 uncertain，支持同 owner 核验；若提交实际成功但回复丢失，保留 receipt/delivered，不倒退或重发。
- 未决请求不能被标记 completed；损坏集合或 run 身份拒绝执行，避免 panic 后绕过恢复。

`Reconcile` 与 `ReconcileOutbox` 是可信宿主核验入口，不是任意工作器可填写成功布尔的公共 API。远端没有幂等/可靠回读能力时，结果可能需要人工核实；这里没有承诺远端 exactly-once。

## 明确差异与未完成项

传输保留安装环境的 gzip/deflate/br 能力，不协商安装版没有的 zstd。严格 TLS、同源及有限固定跨源授权、1 MiB 压缩/解压限额、64 KiB headers 与上下文取消属于新宿主边界，与原版宽松 TLS/重定向策略不同。响应 URL、HTML 图片链接或原宽泛 matcher 均不自动授予新域名凭据权限。

HTML 仅覆盖已核验 lxml 文档形状，尚非完整 libxml2；编码检测、CPython regex、异常 JSON、任意非字符串配置与病态 Cookie/header 仍有明确未覆盖范围，不能宣称全部原依赖语义相同。OCR 当前正常字符串流程已有原参考，生产受控 OCR 尚未接入。浏览器候选另有 native Go CDP 实现，但未提供实际生产网络隔离能力，不把 Fetch 拦截等同完整浏览器隔离，也未启动用户浏览器执行签到。

Tjupt 使用正式纯 Go imagehash/JPEG。已有原始像素/PNG/GIF/JPEG 对照见图片专文；16 bit PNG、JPEG arithmetic/lossless 和任意 Pillow 格式仍不承诺。受控公共图片 origin、答案缓存真实目录及自定义站点/SitesHelper 宿主仍须接线。

仍待完成：正式 SQL backend 与实际 owner/执行授权、配置 CAS 投影及 revision 转换、统计/KV 投影、源定时器安装/触发/停止收据、事件/命令/API 注册、消息路由、VCronField/页面 CSS 与组件、不可变私有站点快照、Browser/OCR，以及原配置/KV/文件/引用迁移和实机验收。不能因为测试 FileStore 可恢复，就宣称这些生产边界已经成立。

## 验证记录

`go build`、普通测试、race、vet 均通过。普通测试：主包 2.760s、durable 7.883s、imagehash 1.606s；race 分别为 20.688s、8.772s、9.572s。见 [本机记录](plugin-auto-signin-core-local-verification.json)。

NAS linux/amd64、CGO_ENABLED=0 的两个静态测试包共 **75 个顶层测试通过，0 跳过、0 失败**，exit 0、无 OOM。运行于 Alpine 3.22、256 MiB 内存上限、64 MiB Go 软限制、网络 none、只读根文件系统、无 capabilities。测试仅使用独占回环 HTTP 与崩溃 fixture；不是实际站点签到、浏览器/OCR 或外发通知的证据。容器和独占远端目录已清理，前后核对原 MoviePilot、下载器和新服务的生产容器启动时间/重启次数不变。见 [NAS 记录](plugin-auto-signin-core-nas-verification.json)。没有部署或启用迁入数据。

`promotion-manifest.json` 固定候选输入，`formal-manifest.json` 固定重构后的正式 Go/资源和参考文件。已有 imagehash 独立维护其来源与验证清单，不重复算作新注册插件。

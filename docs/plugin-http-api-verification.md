# 插件自定义 HTTP API：实现与验证

日期：2026-10-16。此文记录一批已实现行为及其证据，不代表 23 个插件或整个平台已完成迁移。原版服务只读取指定源码；新实现只在合成账户、独立数据库和隔离容器运行，没有修改原 MoviePilot、真实站点、下载任务或媒体文件。

## 来源与范围

原版路由来自 `app/api/endpoints/plugin.py` 的动态注册，鉴权来自 `app/core/security.py`；SiteStatistic 以实际安装的 1.9.7 为准。源码摘要：

| 来源 | SHA-256 |
| --- | --- |
| security.py | `7c2f1920295d13a40979acc2f74554bdbb94010663909a281c0904d6e3db5040` |
| schemas/response.py | `0afd53be12ee3a6f513e74e10c98344b0d9f66e01c250e66a74ccee604bc1970` |
| schemas/site.py 中 SiteUserData 所在文件 | `aa759e3f1348863b109acdecb765c87a14822a14fbf63bab3fc93584d51bfaa6` |
| SiteStatistic/__init__.py | `763a88be85185a94082a2f131ed1c80d8306abe24b10ebe9ae95447cfe5da17d` |

已安装源码中的 AutoBackup、AutoSignIn、BrushFlowLanduo、CloudflareSpeedTest、LarkMessager、SeedRescue、SiteStatistic 共声明 24 项自定义 API。此批只实现 SiteStatistic 的实际业务；其他声明不是已经注册可用的接口。匿名、原始请求体及纯文本响应的传输契约供后续插件接入，不能据此声称 Lark 回调或其他业务已完成。

## 路由与鉴权

- 原路径别名：`/api/v1/plugin/{instance}/...`、`/api/v2/plugin/{instance}/...`；新平台入口：`/api/plugins/{instance}/api/...`，均支持应用的 `/light-media/` 前缀。
- 仅注册当前可执行 Go 目录中明确声明的方法、路由、参数及鉴权模式。安装记录、版本、generation 和配置 revision 均需有效；暂存导入不注册，卸载立即失效。原插件业务 `enabled=false` 本身不移除自定义 API，这与原 PluginManager 保持一致。
- 默认 API key：非空 `X-API-KEY` 优先于查询 `apikey`；重复查询参数按最后一项处理。无效或缺失返回原版 401 JSON。
- `auth="bear"` 保留 bearer 优先、再 API key、再查询 token 的分支；无效 bearer 不回退到正确 key。Go 登录会话可用作 bearer，API_TOKEN 本身不能冒充 bearer。只在新平台同源入口允许现有会话 Cookie；原路径别名仍要求显式凭据。尚不接受原系统签发的 JWT。
- 匿名仅能由可信编译声明授予；请求及自报 manifest 不能将需鉴权接口降为匿名。全局 API key 不授权设置等普通管理 API。
- 方法不匹配返回 405/Allow；缺少必填查询参数返回原 FastAPI 的 422 结构。自定义业务响应不套普通管理 API 的 `data/request_id` 外壳，关联号保留在 `X-Request-ID`、执行记录和执行日志。

全局 API 令牌在“设置 → 系统”使用原控件含义的 API令牌字段，可生成 32 位随机值、保存和复制。普通设置只返回配置状态；只有已登录用户的显式同源 POST 复制请求可取得原值。密钥与插件配置、输入、结果均密文持久化；实际 key 不跨插件 IPC，工作器仅接收宿主核验结果。令牌不进入日志、普通设置响应或公开执行列表。

## SiteStatistic.refresh_by_domain

`GET /api/v1/plugin/SiteStatistic/refresh_by_domain?domain=站点域名&apikey=令牌`

原端点在平台鉴权后仍单独要求并比较查询参数 `apikey`。因此只带正确请求头而缺少查询 key 返回 422；正确请求头加错误查询 key 返回 HTTP 200、`success=false`；错误请求头加正确查询 key 仍返回 401。这些特殊行为按原代码保留。

目标只能从已配置的规范站点域名查找，接口不把外部传入的 URL 当抓取地址。站点可以停用；显式刷新仍使用该站自身凭据。重复规范域名报歧义错误。找不到站点、采集失败和成功分别返回原版 `success/message/message_i18n/data` 结构。实际账户采集使用原生站点适配器，不把连接成功、排队或 HTTP 200 当成统计业务成功。

网络采集不占用设置/插件全局锁。提交时再次核验站点连接身份、当前 API 凭据、实例代际和配置修订；站点快照、日历史、单站更新事实和本次 API 响应收据同事务提交。失败保留上次成功快照。若在收据提交后中断，真实 App 重启从收据恢复结果，不再请求站点；若晚期事务失败，快照和事件一并回滚。单站刷新不冒充全站事件，不触发汇总通知。

原 SiteUserData 的可空字段保持可空；当前解析器未采集的下载中体积、做种明细、未读消息等返回 null，不虚构零值。原 SitesHelper 是编译模块，额外域名别名的匹配行为尚未取证；当前只证明规范域名契约，不能称全部来源字段和别名均已等效。

## 验证证据

`testdata/plugins/site-statistic-http-original-oracle.json` 保存 24 组原版 FastAPI/鉴权/端点的 ASGI 结果。生成时只抽取指定原函数和 schema，使用内存假站点及合成凭据，没有导入原应用、打开业务数据库或执行原插件初始化。Python 只用于读取参考源码和生成参考结果，不是产品依赖。数据文件摘要：`af0c82600f3640ed77a8f7ee091dc79ac1cb71e3f1966951e8474cb59e165e87`。

Go 测试在根入口和 `/light-media/` 分别通过全部 24 组，比较状态码、Allow、业务正文及实际采集是否发生；仅动态日期/时间规范化。HEAD 使用真实 HTTP 客户端，避免内存 Recorder 的响应体行为影响结论。另验证：

- 真实 Go SDK 子进程、HTTP 账户采集、密文 SQLite/WAL 和请求关联号。
- bearer/key 优先级、Cookie 来源边界、密钥轮换和不扩张普通管理权限。
- 伪造目标/匿名授权被拒绝，暂存及卸载不保留路由。
- 授权行写入故障时队列一并回滚；收据故障时采集发布一并回滚。
- 正在采集时撤销密钥不发布旧结果；实际关闭并重新创建 App 后不重复采集。
- 清除一次性标志会同步推进已排队 API 的配置修订，不无故废掉合法请求；旧存储 manifest 不隐藏当前同源码版本 Go 工作器的编译能力。

NAS 测试使用 alpine:3.22、UID 1000:GID 1001、只读根、network=none、cap-drop=ALL、no-new-privileges、256 MiB 内存及 128 MiB 独占 tmpfs。仅挂载测试二进制和合成参考数据，运行全部 `TestPluginHTTP*`，实际通过；没有挂载旧配置、下载目录或媒体库。

| 验证构建 | SHA-256 |
| --- | --- |
| Linux app 测试程序 | `b5f84337b218221e367f30387582a4b94d337a9adafdacacd6885dae3414d7b4` |
| Linux 待交付服务构建，尚未切换正式服务 | `8a1f38efc308563b14894a73276abd4d4002b77f64980e083c3e81d4fba14995` |

独立浏览器实例以合成管理员核对生成、保存、重载、复制及 390×844 布局，无横向溢出。保存后令牌长度和字符集由只输出长度/布尔值的本机校验确认，未输出令牌正文。应用控制台没有错误；浏览器密码管理扩展有一条独立的 autofill overlay 错误，未计作应用错误。截图在本轮 outputs 的 `light-media-plugin-api-token-go.jpg` 与 `light-media-plugin-api-token-mobile-go.jpg`。

本批 `go test ./...` 与 `CGO_ENABLED=0 go test -tags modernconly ./...` 全量通过；涉及 API、站点统计、站点管理和插件生命周期的竞态检查通过（90.283 秒），`go vet ./...` 与模块校验通过；前端 116 项测试、格式检查和生产构建通过。测试容器、独占临时目录、浏览器页及本机测试服务均已清理，视口已恢复；正式服务没有切换。

## 仍未完成

PL-36 还包含受鉴权的插件静态资源、联邦资源、其他插件真实 HTTP 业务、相应页面动作、CORS 等完整契约；此批没有将其标完成。匿名 webhook 的真实签名/挑战/事件处理需随 Lark 业务验证。二进制下载、流式 API 尚未开放。重置后按原版立即重新初始化/注册的完整生命周期亦待补齐，不能把暂存保护冒称重置等效。

原 API_TOKEN、插件配置、KV、独立文件、站点/下载器/媒体库引用和历史均未正式迁移。完整通知通道、仪表板布局、剩余插件业务和最终切换继续属于交付范围。

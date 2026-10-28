# 插件认证提供方与一次性登录票据

2026-10-25，G18 的 Go 后端链路已实现。固定参照为 MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60` 的 `app/core/plugin.py`、`app/core/auth.py` 和 `app/api/endpoints/auth.py`。原项目只读；应用和插件运行不依赖 Python。

## 当前行为

`GET /api/auth/providers` 按运行实例的插入顺序读取真实插件。先执行 `get_state`，再检查可选 `get_auth_providers`；仅后者的调用错误按插件隔离。`get_state` 和 `get_render_mode` 错误继续向上传播，与原版 try 的边界一致。falsey 返回值当作空集合；非字典、空字典条目跳过；字典键和字符串字符迭代后也全部跳过；不可迭代的 truthy 数值报错。

提供方逐项复制，覆盖 `type`、`plugin_id`，仅为缺失的 `id`、`name`、`enabled` 设置默认值。显式 `null`、false、空字符串、重复 id、顺序及超过浮点精度的整数均保留。API 最后按 Python truthiness 过滤 `enabled`；原始注册表不做此过滤，供匿名认证组件资源检查使用。Vue 模式设置缺失的 `component=AuthPage` 并覆盖 `remote`。remote 的 URL 适配为实际安装包摘要绑定的同源资产路径，不接收插件任意指定的远程脚本地址。

标准 Resources SDK 增加 `auth.ticket.create` 和 `plugins.CreateAuthTicket`。插件须先自行验证外部身份，再断言本地用户。宿主验证当前真实执行身份、仍存活的调用作用域、当前插件启用状态、该对象真实声明且启用的 provider，以及当前本地管理员存在。此同步调用需要协商并发 SDK，以在业务回调等待期间读取 `get_state/get_auth_providers`，不能从声明读取、初始化或停止阶段重入。旧 serial worker 返回明确的不支持错误；不缓存旧 provider 声明作为授权。

票据为 32 字节密码学随机数的 URL-safe 编码，仅存内存。锁保证单次消费；使用墙上时钟，年龄严格大于 120 秒才过期，恰好 120 秒有效。保留原版的清理顺序：create 在插入前清理，所以可暂存 1025 条；consume 先移除候选票据再清理。相同创建时间按插入顺序淘汰。

`POST /api/auth/exchange` 要求非 null 字符串 `ticket`，额外字段忽略，无效请求为 422；不存在、过期或重放为 401。先消费，再查当前本地用户；用户不存在为 403，并且票据不能再用。兑换不重新查询 provider 或插件状态：停用后已有合法票据仍可兑换，保留原版行为。成功建立实际的七天 Cookie 会话，使用现有 HttpOnly、SameSite、HTTPS 挂载和令牌摘要存储规则，并返回 `authenticated:true`。现有同源写入检查仍适用。

## 与原版核心的明确差异

light-media 当前只有 `admin(id=1)` 和 Cookie sessions，没有原版多用户、用户停用字段、权限、passkey、avatar、JWT 或向导登录响应。此实现没有伪造这些字段或通行密钥提供方。HTTP 响应仍采用项目既有 `data/request_id` 包装；会话是真实 Cookie 登录，不是原版 bearer JWT 响应。这是已批准的现有核心适配，不能据此宣称 G18 或用户系统完整 1:1。

原版受信任 Python 插件可以直接调用 `create_plugin_auth_ticket`，mint 时不验证用户或 provider。Go 的跨进程宿主边界在签发前额外验证作用域、当前声明及管理员；这属于明确的权限适配。兑换阶段仍保留源代码顺序，不新增 provider 撤销语义。原版异常字符串/traceback 没有穿过当前安全 SDK 错误码协议，日志不伪造 Python 堆栈。

## 验证口径

`testdata/standard-auth/source_oracle.py` 从固定提交读取并校验 Git blob，用原版 AST 方法验证默认值、falsey、异常边界、Vue remote、TTL、容量、失效用户先消费、无 provider 再验证。它只用于开发源代码对照。

`cmd/owned-auth-fixture` 是独立编译、通过真实仓库 HTTP 和摘要校验安装的 Go 插件进程。测试先验证固定测试密钥的 HMAC proof，才调用真实宿主票据能力；未连接任何外部登录服务，未使用真实凭据。普通和 race 测试覆盖以下七个入口：

- `TestOriginalAuthTicketSourceLifetimeAndCapacity`、`TestOriginalAuthTicketConcurrentConsume`、`TestOriginalAuthExchangeRequestValidation`。
- `TestOriginalAuthProvidersActualSDKSourceValues`：真实 worker 的声明、空值、大整数、getter/state/render 异常。
- `TestOriginalAuthActualSDKProofExchangeAndAuthority`：无效 proof、外来 provider、无效用户、已结束回调、停用 provider/插件、真实 Cookie 和受保护管理 API、票据重放。
- `TestOriginalAuthActualSDKCloneAndConcurrentExchange`：独立分身 provider 身份及 12 个并发 HTTP 兑换恰好一个成功。
- `TestOriginalAuthActualSDKExpiryMissingUserAndOrigin`：过期、同源检查、删除本地用户后先消费再拒绝。

race 验证同时使用 `go test -race` 与 `go build -race` 构建的实际 worker。原始日志保留于 `work/plugin-module-20261025/auth/`，归档摘要见 `docs/validation/plugin-auth-provider-parity.json`。首轮失败只是测试误把项目 `data` 包装当顶层列表；后续一次宽正则包含了正在并行开发、未提供对应二进制的前端测试。最终后端门禁使用明确的七项范围，不能把这些中间失败删除后声称首次通过。

此报告只记录后端、真实本地插件进程和 HTTP 证据。登录界面、实际 Vue 模块执行、匿名资产图、浏览器交互由同任务的前端门禁另行记录；不把本文的 HMAC 合成验证当作真实第三方认证服务或已迁移认证插件的业务验收。

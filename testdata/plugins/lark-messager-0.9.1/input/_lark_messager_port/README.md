# LarkMessager 0.9.1：独立纯 Go 候选

本目录尚未注册、迁移配置、部署或向真实 Lark 发信。运行时代码全 Go，Python 仅用于离线原执行参考。这里提供可迁入的核心、固定 REST 适配器和 SDK 往返，并不宣称完整应用、全部 Python 输入或真实消息交付已经完成。正式接入条件见 MIGRATION.md。

## 固定来源

reference/source 是重新只读获取的实际安装原始源码，不是 work/plugin-source 的脱敏副本。原安装 __init__.py SHA256 为 d9f6a1078706b03dc97247450a8084a8c67f07dbfccb675235fc0e1b91b18372；实际 client.py 为 28ac4a3dae4879a86bba956f493b60121948fb2ca5e631d81c4868c604468654。六份原文件、旧脱敏副本各自的 SHA 和 get_form 原片段/AST/literal 区别见 reference/installed-source-provenance.json。旧 cached-source-manifest.json 中 redactions 数字只描述旧副本。

reference/oracle.py 每次先校验实际安装原 SHA，再编译真实类 AST。只将 app 导入、HTTP、存储、目录与时钟替换为受控依赖；真实 Pydantic 2.13.4/pydantic-core 2.46.4 与 AES 均在原 Python 3.12.13 环境离线执行，不导入旧应用、不读配置/凭据/运行对象。接触外部副作用的路径全部使用合成输入和假的 transport。remote_image 参考的临时文件只存在于其 owned TemporaryDirectory。

原 get_form 在 NAS 真执行，与完整 form.json 逐 JSON 树比较；form.json SHA 为 b027d9566f56aae8b2601e8158e7a5bfbd4f6370f58c81c6b86d8e9a0fffef1d。原 get_form 原片段 SHA 为 a55877576b9fba09454e7e2a9331aa51bfd9cdf5153cfe78f85b9604d6e7d361；旧脱敏副本的该方法恰好逐字相同，不据此把整份脱敏文件称原源码。

实际依赖还包括 requests 2.34.2、FastAPI 0.136.3、pycryptodome 3.23.0。客户端本来就是 requests REST，没有 lark-oapi 依赖。pydantic-core 官方固定 sdist、其 Cargo.lock 对应 jiter 0.14.0 和许可已保留；Go 只移植实际调用的整数验证闭包，没有引入这些库的运行时。

## 已实现并对照的范围

- 原完整配置表单/defaults、状态/Init/Stop、13 个 MessageChain 入口，配置优先级、通知类型过滤、目标记忆/回退、管理员目录解析/失败缓存/一次 5 秒重试与 Notifications 变更判断。
- 普通卡片、文本/引用回复、图文列表、文件/语音上传及附文、图片/文件/消息资源下载与回退、旧版卡片/回复接口、表情标记开始/结束。
- Agent CardKit create→send→metadata→PUT content→PATCH settings；原顺序号在编辑请求前递增。完整真实 loopback 还验证 PUT 断连不降级普通编辑、收据提交失败不继续发送、重放不重复外部效果。
- 消息 text/image/audio/media/file/post、卡片回调/命令/附件模型、加密与签名分支、四个原 API、动态页面和 last_test_result/last_chats 两个 KV 形状。
- Python ordered JSON/string/repr、Unicode15 可打印与大小写/空白/数字闭包、Pydantic 整数与 Union[str,int] 消费语义。必须用 DecodeSource 保留需要 repr 的原字典顺序，不能用无序 Go map 猜回调字符串。
- 固定可信 Binding 的 REST 地址/路由/凭据/接收者/文件/资源授权；实际 SDK Runner→Serve→不透明 ID host 调用→Go 核心→owned HTTP，worker 不能选择账号、收件人、路径或 API 地址。

当前 source-oracle.jsonl.gz 共 1,376 条，测试流式读取且逐类全部消费，不跳过未实现类别：

| 原执行参考 | 数量 | 比较内容 |
| --- | ---: | --- |
| 卡片/旧版/流式文本辅助/回调/CardKit 解析 | 357 | 原输出、条件分支与失败事实 |
| target/接收/消息模型与动态值 | 150 | 字段 presence、顺序、数值、原模型通过或异常 |
| 配置/表单/page/Notifications | 42 | 完整组件树、默认值、原配置变更 |
| 管理员 worker/cache/权限 | 39 | 调用顺序、一次重试、负缓存、权限提示 |
| 签名/AES 与 webhook | 107 | 原 primitive、无 PKCS7 验证事实、signed/encrypted 分支 |
| MIME/remote image | 529 | 实际152项 strict registry、大小写/编码后缀/URL 路径；原 remote 算法49条 |
| REST 请求与多步算法/API | 119 | 请求方法/地址/参数/超时、故障前缀、KV |
| 全13模块入口启停、返回模型 | 32 | 实际原 Client 方法与受控 transport，不以仅录参替代原算法 |
| 固定测试卡片 | 1 | 原 AST 字面量与时间占位 |

上表按 reference/verify.py 自动统计的 kind 分组核对；最细数量记录见 VERIFICATION.md。HTTP 对照比较方法、地址、头字段、JSON 树、multipart 文件内容/MIME 和前缀，并非 requests 与 Go 的任意原始 JSON 字节完全相同。远程图片的随机临时 basename 在 Go 用固定 owned 名表示；后缀/MIME/字节有原执行对照，不伪称随机文件名/HTTP multipart boundary 逐字相同。

## 明确保留的边界

这些不是删减需求，正式完整插件仍需关闭：

1. App durable owner/invocation envelope、加密 SQL Journal 与两 KV、配置/Notifications 同事务 CAS、事件/通知/13模块的真实生产者、MessageChain 与用户绑定查询、受信 MediaInfo/Context 消费快照、接收回调认证与防重、配置 UI 和旧配置/数据迁移均未接。Host 接口本身不是这些业务已落地的证据。
2. Go Decoder 明确拒绝 Python JSON 可接受的孤立 UTF-16 surrogate、NaN/Infinity 和指数溢出为 infinity 等完整非有限 JSON 语义。CardKit 检测裸非有限常量返回 unsupported，已发请求收据为 unknown，不能因 Go 解析失败落入“空 2xx 成功”。完整任意 Python 对象/循环 repr、所有非表单配置动态类型与全部 Pydantic 未消费模型不是已实现的 Python 运行环境。错误参考只验证非成功事实；并不声称 Python 异常类/traceback 与 Go error 类型完全相同。
3. 媒体视图是原方法实际消费的明确字段/派生调用结果，不冒充完整 MediaInfo/Context。原 schema.py 中未被该插件业务调用的模型/属性仅保留来源，不宣称通用外部 Python 模型 API 等效。
4. 实际152项 MIME registry 来自 fresh installed 进程与文件，不读取原运行应用对象；旧进程若被其他模块动态 add_type 的全局状态并未复制。
5. 原 form 的三个动态 AppSecret props 仅保留 literal；其密码显隐应由正式 UI 对精确可信表达式原生实现，不能执行任意 JS。是否在正式 UI 已完成须由父任务独立记录，不计入本包验证。

## 必须说明的安全差异

源 challenge 先于验签/token 返回；encrypted 和 card.action.trigger 分支跳过签名。DecodeSourceWebhook 的 Accepted 只表示“原源解析/检查通过”，绝不是可信 ingress 授权。生产宿主必须另给 owner/generation/revision 和事件认证、防重、接收者/message/resource grant，不能让 anonymous callback 越权发消息或执行命令。

默认只连固定 https://open.larksuite.com/open-apis，使用系统/受信 CA 校验证书，不关 TLS 验证、不跨地址带凭据重定向；测试地址只允许 literal loopback HTTP。真实远程图片获取须由受信宿主按冻结 URL 先获得字节和 Content-Type，Binding.Images 只提供这些 owned 快照，候选不会让 worker 任意 HTTP GET。

每个写效果先 Journal.Begin，再外部 IO，再 Journal.Finish；unknown 或 finish 失败在本 invocation 内阻断后续 API。已有 confirmed 收据可重放结果，不重发。MemoryJournal 仅供测试，不是崩溃持久化；跨新 invocation 的 unknown 必须由宿主持久 owner 阻断。成功收据才派生新 message/card/file/image/reaction 身份；管理员查询不能自动扩为接收者授权，目录返回身份必须匹配本次请求和可信 RecipientIdentifiers。

原 _send_agent_streaming_images 在 None 结果后仍记 sent；Go 仅记 confirmed 成功消息。原 HTTP 异常/接口 msg 可能回显私密内容；Go 保留已知 contact:user.id:readonly 权限事实，其他失败为固定脱敏文字；FetchChats 不把原始错误 msg 存进 KV。原错误吞掉并返回 False/None 的路径会保留失败/unknown error 信息给可信宿主，不伪装已送达。上述差异不是与原源逐字相同的行为。

## 许可与复现

原插件作者 ui-beam-9，原文件/作者信息完整保留。实际安装这6文件没有附独立仓库 LICENSE；其分发许可来源仍待确认，不能因为 MoviePilot 主仓库是 GPL-3.0 就推断第三方插件也相同。reference/MOVIEPILOT-LICENSE.txt 明确仅是本地主仓库许可证参考。Python 来源保留 PSF 完整许可与 binascii 固定源码；Pydantic/jiter 保留各自许可。源完整校验表见 reference/source-checksums.json。

离线生成参考要求显式 PLUGIN_REFERENCE_SSH_TARGET 环境变量，脚本只读固定公共源码、执行合成依赖，不读旧用户数据。普通使用与所有 Go 测试均不依赖 Python、SSH 或真实 Lark。验证命令和结果见 VERIFICATION.md。

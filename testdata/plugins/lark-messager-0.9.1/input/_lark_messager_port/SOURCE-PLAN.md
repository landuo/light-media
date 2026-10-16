# LarkMessager 0.9.1 独立 Go 候选：来源与实施契约

此目录尚未接入正式应用，也不代表插件已经迁移或可向真实 Lark 发送。全范围保留；下面的分段是实施顺序，未完成项不能返回成功。

## 固定来源

`reference/source/` 是只读读取实际安装版后核 SHA 的 6 个文件，SHA 与已有 `work/plugin-source/source-manifest.json` 全匹配；不使用脱敏后代码执行。实际 Python 3.12.13 / requests 2.34.2 / FastAPI 0.136.3 / Pydantic 2.13.4 / pycryptodome 3.23.0。客户端是 requests HTTP REST，不依赖 lark-oapi。精确 SHA 见 `reference/cached-source-manifest.json` 和 `reference/installed-metadata.json`。

`reference/dependencies/` 保存实际只读 MoviePilot 公共源码（消息模型、枚举、插件基类、MessageChain），不读取配置、实例、凭证或旧运行时对象；SHA 见 `reference/dependency-checksums.json`。每个源方法和调用闭包见 `reference/method-contracts.json`。参考生成只执行固定 AST 和合成受控 transport，不 import app，也不启动插件原服务。

## 完整范围与分段

1. 完整原表单/defaults、Init/Stop/state/module registration，保持旧字段 FEISHU_OPEN_ID/user_id 与 FEISHU_ADMINS/admin_users/admin_emails/admin_mobiles 优先级、去重、missing 自动禁用。Init 的后台管理员解析/5 秒重试/通知配置变更守卫需独立实现，不将“已排队”计为同步成功。Stop 停 client/crypto；源 `_stop_event` 未参与取消，Go owner 必须真正退休能力。
2. 客户端 target 记忆/解析；token 60 秒缓冲/force；文本/reply/卡片、图文列表、file/voice 上传与附文、图片下载与 data URL、联系人查询；每种端点错误/返回值保留。文件读取只能经宿主冻结文件能力，不能任意路径读取；远程图片只能经冻结输入 URL 的受信获取能力，不放 worker 任意网络。
3. schema2.0 card/body 双重 escape、8×3 按钮截断/20 Unicode 字符、legacy builders；Agent CardKit create→send→metadata、create 失败降普通卡片，edit sequence 在请求前递增，PUT content/PATCH settings、NUL/空/非 JSON 的源 2xx 判定。源图片辅助忽略 API None 而记录 sent 的不安全行为须以 source-attempt 与 confirmed receipt 分开；不能假称已发送。
4. webhook 原始/包裹 encrypt、challenge 提前返回、签名/token 分支、event schema；message text/image/audio/media/file/post；card action 顶层/event fallback、test_ok、映射与转发；message_parser 的用户名绑定/管理员命令/callback 与附件真实 schema。
5. 13 个 get_module 方法：message_parser/post_message/post_medias_message/post_torrents_message/edit_message/send_direct_message/finalize_message/processing started/finished/image/file downloads/reactions。MessageChannel=Feishu，source=Lark。入站消息使用受信 MessageChain callback，不由 worker 构造 loopback API_TOKEN URL。媒体/资源列表最多10项，宿主提供固定 Context/MediaInfo 真实所需派生契约，不能任意 map 猜属性。
6. /test、/status、/fetch_chats、完整动态 page 与两个 KV；管理员目录解析、缓存失败值、Notifications 同名条目更新/清空/守卫。源无定时 get_service/get_command 覆写；不要虚构独立 scheduler。
7. SDK worker 仅传 event/notice/receipt 等绑定 ID；host 校验 owner/config revision/generation、来源/接收者/文件/资源 ID 和动作能力。Worker stop 不是 owner retire。外部副作用先持久 intent，写入成功/拒绝/unknown receipt；断连或提交收据失败不能自动重试造成重复发信。完整 App durable owner/SQL 接线不在该独立候选，必须明确未完成。

## 精确持久数据

- `last_test_result`: `{ok: bool, msg: string, time: local "YYYY-MM-DD HH:MM:SS", displayed: false}`；每次 /test 所有终态更新。page 仅当非空且 displayed 显式 false 时显示，随后写 true。
- `last_chats`: 上述通用字段加 `chats: [{chat_id,name,description}]`；/fetch_chats 只请求 page_size=100 第一页，不偷偷扩为分页语义。page 显示与确认同上。
- Notifications 是宿主系统配置，不是上述 KV：找第一项 name=Lark；新项 type=larkmessager、enabled=false、switchs=[]；管理员 sorted(set) 逗号串；默认用户非空才覆盖，清空分支有精确 presence 检查。未知项/字段保留。必须由可信配置变更事务承接，不让 worker 任改系统配置。

## 源事实与必要安全差异

原 challenge 在验签/verification_token 之前返回；已解密或 card.action.trigger 跳签；无 encrypt_key/token 时 anonymous callback 可直接转入消息链。源仅同名 header event_type 用于实际分发，顶层 event_type 只用于跳签判断。不能把源这些事实改写为已验证授权。候选纯解析可对照，但生产 ingress 必须由宿主可信事件授予 recipient/message/resource；未认证事件不能因“解析通过”获得发信、命令、文件或用户绑定权限。具体默认 ingress 政策待父级统一接线；不偷偷添加全局弱 TLS 或关闭验证。

Crypto 源 decrypt 只按最后一个 byte 切片，不验证 PKCS7；Go 保留此算法供原执行参考，但密码学成功本身不代表可信事件。所有动态类型、Python ordered-dict repr、Pydantic coercion 或原库未移植路径须显式列为依赖/unsupported，禁止猜测回退。

## 当前状态

候选已实现上述原业务核心、固定 REST/SDK 和实际原执行对照；最新 1,376 条参考全部被测试消费。代码和夹具保持候选冻结，最终验证见 VERIFICATION.md，正式接入步骤、未实现宿主和动态输入边界见 MIGRATION.md / README.md。不得把独立 Go 目标通过称配置/数据迁移、正式 UI 或真实发信完成。没有修改正式源、主 go.mod、App 或旧服务。

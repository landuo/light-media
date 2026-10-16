# 正式接入清单

这是独立候选的实施交接，不是完成迁移的清单。完整需求仍保留；本轮只允许迁入纯叶包、参考和说明，注册/宿主/真实配置迁移须另外完成和验证。

## 可复用文件与 API

Go 源都在包 larkmessager 中，当前模块路径 light-media/work/larkmessager。建议正式叶包 internal/plugins/larkmessager；仅更改包导入/fixture 路径，不复制第二套 legacyanime/SDK。模块只依赖标准库及已有 internal/plugins、internal/releasemeta/legacyanime。

- `New(PluginHost) *Plugin`；`Init(Object) (epoch uint64,error)`；`Stop()`；`State() bool`；`Modules() []string`。Init 不启动脱离 owner 的 goroutine。宿主需为该 epoch 调一次 SyncAdmins；仍在 pending/重试不表示已同步。Stop 使旧 epoch 失效并清 client/crypto，State 保留原源 enabled 事实；真正 owner retire 由宿主执行。
- 13 个业务入口见 `source-contract.json`/`reference/method-contracts.json` 与 README。Go 对应 ParseMessageBody/Post/PostMedias/PostTorrents/Edit/SendDirect/Finalize/ProcessingStarted/ProcessingFinished/DownloadImage/DownloadFile/AddReaction/DeleteReaction。输入 Notification 是真实源实际消费属性投影，JSON Unmarshal 已保留源所需 Pydantic 语义，不能把其未使用字段算成完整 Notification 模型移植。
- `Form()`、`Page(ctx)`、`Test(ctx)`、`Status(ctx)`、`FetchChats(ctx)`；完整 literal 表单/8份状态模板/测试卡片均为 embed，source-contract.json 保留原 API summary/description、metadata、config_prefix/order/auth_level。SDK Manifest 只承载当前协议有的字段，目录 UI 需从可信定义填入剩余原 metadata。
- `DecodeSourceWebhook(raw,headers,queryToken,encryptKey,verificationToken)` 返回原检查事实；`DispatchTrusted(ctx,body)` 必须在宿主完成可信 ingress 授权之后调用。原始 webhook bytes/headers 不可先重编码再验签。source 的 encrypted/card/challenge 豁免仍以字段记录，Accepted 不是权限。
- `NewAdapter(Binding,Journal,Options)`；受信配置 Binding 必含 owner/generation/revision/invocation 与真实 AppID/AppSecret。生产 Options.Guard 必填；TestAPIOrigin 只允许 literal loopback HTTP，不能作为生产配置。API 是闭集路由，不提供任意请求 URL/RPC。
- `Journal.Begin(ctx,Intent)` / `Finish(ctx,Receipt)`；MemoryJournal 只用于测试。KV.Get/Put/Delete 只允许该 instance/generation 私有 last_test_result/last_chats。ConfigTrusted 包含真实秘密，仅供可信宿主，严禁普通 HTTP/UI/日志回显。
- `DecodeSource`/`DecodeSourceObject` 保留需 repr 的原 JSON 顺序；原 raw bytes 须持久保留。普通 map 无法恢复插入顺序，涉及多键 repr 返回 unsupported。SafeCardKit 返回 `(Object,error)`；显式 unsupported 不能当作原空 2xx 接受。

## 必须完成的真实宿主接线

1. 目录定义先保留原作者/icon/版本/order50/config_prefix larkmessager_/auth_level1。配置表单的 AppSecret 精确密码显隐需原生 UI 支持，不 eval 任意脚本。配置不向 worker 传明文；目录未注册前不得宣称已可使用。
2. 每个可信通知、模块调用、event、HTTP ingress 先写加密 frozen envelope：原始输入 bytes、原 Config 完整未知字段、配置 revision/owner generation、Notification/MediaView/TorrentView 的真实消费快照、入站认证/防重事实、接收者/contact/resource/file grants、调用 ID、初始 streaming metadata 和 epoch。不能根据 worker 输入临时补数据。字节/文件来源必须是宿主已授权的 owned snapshot，不能由 worker 给路径。
3. 原通知/消息链生产者分别绑定固定 call_id；SDK 仅转发该 ID，宿主按插件/实例/owner/run 再取 frozen envelope。13 个业务方法都要有正式 dispatcher，不是仅挂一个通用 callback。对媒体输入用真实 MediaInfo.title_year/title/get_message_image()与 message_image/poster_path/poster；Torrent 用真实 Context.torrent_info.title/site_name/media_info。当前 view 不是替代完整 MediaInfo 的身份识别。
4. 管理员 worker 启动/取消、5秒一次重试、用户绑定查名与 MessageChain Forward 应接现有 owner/runtime；禁止客户端持有 API_TOKEN 后从 worker 任选 loopback URL。UpdateNotifications 必须将 epoch/config revision/owner generation 的 durable CAS 与更新同 SQL 事务，callback 只作内存变换。当前 p.mu 前后检查不能替代提交时 CAS，也不能持 SQL tx 跨 HTTP/5秒等待。宿主避免在不同路径反向拿 p.mu/owner锁。
5. 以(instance,generation,revision,invocation,ordinal) 实现加密 Journal，Begin 与 intent 原子落盘后才授予 IO；Finish 的 confirmed/rejected/unknown 及必要业务 metadata/state 要落盘。保存 request digest、冻结原输入和实际 response receipt，使用 UseNumber 保留整数；敏感响应只能密文。别把重启后 ordinal/UUID 漂移当新发送。
6. 同 invocation 内候选序列化 API；unknown/收据提交失败阻断后续发送。正式宿主还须跨进程/新 invocation 阻断旧 owner 未解的 unknown，退休或重装 generation 不恢复旧发送。confirmed replay 可重建派生 grants；不得自动重发 pending/unknown。租约在外部调用过程中有效，配置改变/stop 必须等待或退休旧能力，不能只在开头看一次 Enabled。
7. 明确构造接收者 grant 与管理员查询 grant：ContactIdentifiers 仅允许查询，只有本次请求标识同时在 RecipientIdentifiers 才能把返回 Open ID 派生为发送目标；发送/上传成功收据才能引入 message/card/image/file/reaction ID。incoming event 的 message-resource pair/audio/file/image fallback 各需正确 grant；不因知道某 key 就允许别的消息资源。资源 URL 段只接受源正常 Lark ASCII ID，拒绝路径注入。
8. 远程图片 producer 只按已冻结可信 URL 获取，保留实际 bytes 和 Content-Type，再填 Binding.Images；不能给 worker 任意 GET。候选已移植原按响应头/URL path 选 suffix，再按实际 strict mimetypes registry 决定上传 MIME，不能直接把 HTTP Content-Type 当上传类型。
9. 正式实现并迁移两个 KV：last_test_result 和 last_chats 的 ok/msg/time/displayed/chats；page displayed=false→true、status 删除两项需同 owner 的持久事务及并发测试。源 time 是本地时间，时区由宿主明确提供。完整配置未知字段与两个 KV 原数字/空值保留，不重放任何发送行为；用户启用/迁移 owner 前不能让两服务同时发消息。
10. 完成 App 宿主目标/full/race、无外网普通 UID NAS 沙箱验证、原 UI 真实浏览器验证，再独立进行用户授权的受控实际 Lark 消息测试。该候选没有做这些步骤，不以只读 source 或 loopback 测试计真实 Lark 连接/投递成功。

## 尚未关闭的源输入/依赖

README 列明非有限 JSON、surrogate、任意 Python 动态类型/未消费模型、fresh MIME globals、异常类型/原始 HTTP 字节差异。无实现分支应显式失败，不可用空成功替代。原接口报错文字/图片 sent-attempt/签名豁免/线程退休/unknown 收据的安全差异需在正式文档保留。第三方插件原仓库 LICENSE 的来源尚未确认，不能根据主 MoviePilot 许可推断。

## 可逐项验证的验收顺序

先纯叶包 source checksums + Go normal/race/vet → SDK opaque-ID 往返 → 真实 owner/kv/journal restart 与 unknown fence →13 个真实生产者/消费快照 →受控完整 HTTP/card/资源/管理员场景 → 原配置 UI/迁移 dry-run → NAS 无外网沙箱 → 用户授权的真正 Lark 投递。每步分别记录，任何 contract-only 或未部署项保留未完成。

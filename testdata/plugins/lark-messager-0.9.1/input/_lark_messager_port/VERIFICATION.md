# 候选验证记录

2026-10-17，本机独立 module light-media/work/larkmessager；代码、测试、夹具和说明现冻结。没有运行根 App/full，没有连接真实 Lark，没有改旧应用/配置，没有迁移或重放旧通知。

## 最新终态

| 命令 | 结果 |
| --- | --- |
| go test . -count=1 | PASS 1.410s |
| go test -race . -count=1 | PASS 5.912s |
| go vet . | exit 0 |
| CGO_ENABLED=0 go test . -count=1 | PASS 1.331s |
| python3 reference/verify.py | 31 Go 文件、89 参考文件，完整 checksum 通过；1,376 原源参考 |

首次 race 暴露两处测试 HTTP hijack 后读取 capture slice 未同步；补互斥 snapshot 后上述最终 race 通过。vet 的一处 self-assignment 已修正。所有目标均终态后才编辑 Go，修正后重新运行最终集合；没有把之前失败当作最终通过。

## 原执行参考全部消费

| kind | 数量 |
| --- | ---: |
| admin_work | 36 |
| attachment_model | 29 |
| callback | 19 |
| card | 251 |
| card_action | 3 |
| cardkit | 67 |
| client_calls | 15 |
| crypto | 12 |
| crypto_decrypt | 20 |
| crypto_signature | 27 |
| endpoint_calls | 48 |
| form | 1 |
| init | 5 |
| legacy_button | 3 |
| legacy_card | 4 |
| legacy_reply | 16 |
| message_parser | 20 |
| mime_guess | 480 |
| module_calls | 26 |
| module_response_ids | 6 |
| multi_calls | 40 |
| page | 6 |
| page_template | 8 |
| python_value | 29 |
| receive | 42 |
| remote_image | 49 |
| resolve_permission | 3 |
| stream_helpers | 10 |
| sync_config | 22 |
| target | 30 |
| test_card | 1 |
| webhook | 48 |

TestOriginalSourcePureOracle 负责纯分支；四个专用 suite 分别消费 client_calls/multi_calls/endpoint_calls/module_calls。没有未实现 kind 被跳过或只比较 success=true。Python异常分支检查 Go 非成功事实，不声称异常类/traceback 完全同型。API 私密报错文字以文档列明的固定脱敏文字归一化，原值仍在离线 source reference，不称逐字一致。

源 module_calls 执行原 13 个入口与原 Client 真实 REST 算法，启停各一轮；媒体/Context 注入的是明确消费属性视图，没有冒充完整 MetaInfo/MediaInfo。multi_calls 的40条是实际源多步方法调用，不是由 Go 自产 expected。client_calls 的15条还在真实 owned loopbackHTTP 对照请求与故障前缀。

## 额外真实 loopback / SDK / 状态验证

- 实际 Runner→子进程 Serve→opaque-ID host→Go Notice/MessageChain→owned HTTP；worker 的秘密、接收者、路径和输入覆盖被拒绝。
- multipart 文件/语音及附文、修改显示名仍只读冻结文件 bytes、MIME 与派生 file/image grants。
- CardKit create→send→edit→finalize 从零 card/message grant 开始；成功收据派生 ID，PUT/PATCH 路由和 sequence1/2 精确验证。
- PUT 断连不普通 edit fallback；card_create unknown/收据提交失败阻止第二条消息；重放 confirmed 不请求网络，pending/unknown 不重试。
- 未绑定目标/资源/路径/contact 在任何 HTTP 前拒绝；联系人返回只为本次已授权接收者派生 grant；Guard 在 token 获取之后再次复核。
- token60秒缓存/force、跨 origin redirect 拒绝、私密服务器错误不入公开错误/KV、非有限 CardKit JSON unsupported 后收据保持 unknown。
- TestInstalledScalarPredicateTables 对所有 U+0000–10FFFF 标量对照实际 Python15 printable 及实际 pydantic-core2.46.4整数 trim 表；不是推断 NAS Rust 编译器的 Unicode 版本。

## 未验证

无正式 App/SQL owner/KV/Journal、真实用户绑定/MediaInfo生产者、真实接收回调、旧配置数据迁移、NAS运行、正式浏览器或 Lark 投递证据。当前可迁入独立叶包，不代表完整插件迁移完成。README 的输入和安全差异、MIGRATION 的正式接线条件继续有效。

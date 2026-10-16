# AutoSignIn：实际 SQLite journal 与 owner

本批在 `internal/app/plugin_autosignin_journal.go` 接入现有 App SQLite 和 PluginStore 加密，替代独立 FileStore 测试宿主。使用真实 `plugin_installations`、配置 revision 和 `plugin_runs` 核验。**尚未注册或部署 AutoSignIn**；Manager、外部配置切换、调度和消费者仍须接入，不是原用户配置/KV 已迁移的证明。

## 已实现

- 实例 owner 使用受控数据目录的排他文件锁和 SQL 中加密的 epoch 证明。每个短事务重新核对安装 ID、固定版本、generation、配置及真实 running run 的 action、admission revision 和私有输入。持有 Binding 数值不能创建有效 owner。
- 单实例执行和 outbox 维护互斥。等待网络时不持 SQL、pluginMu 或 lifecycle 锁，视图仍可读取；Close 在执行未释放时返回 Busy，不偷换活跃实例。
- 原状态拆为字段、run、baseline、lane/entry、cache、stats 和 outbox cells，每个大值按 128 KiB 分块加密。行与完整清单经过 HMAC/AEAD 验证，删除、调换、身份和大小不符均拒绝。RawMessage 和 byte slice 独立保存，保留 nil/空字节区别；不改用另一套数据库。
- 每次只重加密和写入变化的 cell/分块。大缓存超过 16 MiB 的判别证明它不是一个固定 16 MiB 总 blob；小配置/收据更新不会重加密既有大响应。
- 每个未决网络请求在提交 intent 前预留 24 MiB 记录空间，最大观察响应 16 MiB；实例准入预算 256 MiB，计入行包装、密文开销、内部配置收据和未决请求预留。空间不够时先拒绝，不能先发送再假报可记录。
- 配置效果与公开 `plugin_values` 配置在同事务投影，公开 revision 每次单调增加。私有配置收据链绑定原 admission、run、lane/index、效果 digest、前项与配置 HMAC。执行仍保留原 Scope/PluginRun；后续 Guard 只接受链证明的内部更新，管理员的无关 revision 会吊销旧能力。
- 首次接管及同 owner 核验空窗调用同一个生产 `RestoreOwnerState`，保留已知响应，把遗留 intent 转为 uncertain。请求不因重启而盲重发。
- 维护 backend 只允许已有 outbox 的合法阶段转换；不能修改 ID/Kind/run/lane/index/payload、添加/删除通知、跳过 intent、自造阶段或修改 active run/runtime epoch。工作 run 也不能选择另一个 active run。

这些授权是可信原生宿主接口，不是 SDK 可指定 SQL、Binding、凭据或任意结果的 RPC。事件/通知 ack 必须由实际消费者证明；本批 recorder 的 ack 只证明隔离协议和 SQL 顺序。

## 验证

13 个顶层目标测试普通 **2.110s**、race **15.933s**，`go vet ./internal/app` exit 0。实际 Controller 流程在自建回环 HTTP 收到响应后注入历史写失败，再从 SQL 运行记录恢复，HTTP 总调用为 1；还核对实际关闭重开 Store、公开 revision 1→2→3 与原 admission 不变、配置投影失败回滚、容量不足在请求前拒绝、当前/下一 owner 的 uncertain、行篡改、生命周期吊销和通知 FIFO。见 [本机记录](plugin-auto-signin-sql-local-verification.json)。

NAS linux/amd64、CGO_ENABLED=0、modernconly 同 13 项 **全部通过，0 跳过/失败，exit 0，无 OOM**。容器 256 MiB、GOMEMLIMIT 64 MiB、网络 none、只读根文件系统、drop ALL capabilities；缓存判别约 18.6 MiB。二进制 SHA256 `05c8b35c33e333e1c8c46ed14b2cb151ab0f35b01a2e6699154fab1540e790bb` 与远端一致。独占容器和临时目录已清理，light-media/moviepilot/qb 的启动时间、重启次数等前后不变。见 [NAS 记录](plugin-auto-signin-sql-nas-verification.json)。

本报告只包含两个冻结的 journal/测试文件，不包含随后开发的配置 transition 源码。没有对真实站点签到、浏览器/OCR、用户通知或用户缓存执行操作。

## 仍须完成

StateBackend 当前会重组实例当前状态。增量写不等于常量内存或常量读取成本；256 MiB 是准入上限，隔离测试通过也不是原用户全部负载的 RSS/CPU 达标证明。按需加载历史响应、完成 run 的保留与容量回收、真实长期负载仍需实现及验证，不能把未决 intent/收据直接删除以腾空间。

管理员保存新配置时，旧 owner 返回 Stale。随后独立文件已补原生 namespace transition：核对真实 drain、完整归档旧 admission、原子转交文件锁并保留普通 init 窗口，见 [配置切换记录](plugin-auto-signin-transition.md)。Manager 仍须实际停止/等待旧触发器并替换 owner，历史通知交接也未完成；不能直接改旧 Run.Scope 或清空审计冒充切换完成。

原 History/Cache/Stats 已持久化在该 journal，原插件 KV 和核心 SiteOper 的实际投影仍未完成；只有配置投影已接原核心表。Manager owner map/关闭、实际 SDK 终态反向调用、定时器和事件 barrier、不可变站点能力、Browser/OCR、通知消费者、注册、原配置/KV/文件/引用迁移和正式部署仍在范围内。

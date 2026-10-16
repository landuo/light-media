# AutoSignIn：配置 admission 切换

`internal/app/plugin_autosignin_transition.go` 补充实际 SQLite owner 的原生配置切换。**尚未接 Manager、实际调度 barrier、catalog 或生产 SDK，未注册、部署或迁移用户数据。**

切换必须由当前真实 running 的 init/reload 发起，核对安装版本、generation、公开配置 revision、原运行输入字节和配置快照；工作插件不能提交任意 Scope 取得新 owner。先取得本实例独占执行权，再在短 lifecycle/pluginMu/SQL 窗口内验证实际 schedule、event subscription、occurrence 及其他运行均已 drain。仅写一个“已取消”标志不能代替此证明；当前实现负责检查，Manager 仍须实际执行停止和等待。

旧状态有 active run、Blocks、intent 或 uncertain 时拒绝切换。可切换时，同一事务完整归档旧 admission 的原行、Run.Scope、配置效果链与未交付 outbox，保存绑定旧/新 Scope、真实 init/reload、输入/配置 HMAC、旧 meta digest、行数/字节及未交付数量的加密收据。普通 init 保留运行窗口、历史、cache 和 stats；旧 timer 描述不投到新 admission。新 owner 接过原 OS 文件锁，旧 owner 被吊销。提交回复丢失仅通过精确持久收据读回确认，不重写旧运行。

归档与新命名空间共享保守预算。准入统计 current/archive 的 **7 张表**，包含 meta、config 密文及行开销；空间不足在任何复制/发布前拒绝。此预算不证明常量内存或长期资源达标。未投递通知留在加密归档，**不代表已经发送，也不获得重发授权**；历史 handoff 仍须接入。

6 个配置切换顶层测试验证原 Scope/未交付 outbox 保留、提交回复丢失、SQL 回滚重试、实际 core drain/原运行校验、归档行和配置链篡改、归档 meta/config 计入容量。统一测试还包含既有 13 个 AutoSignIn SQL、4 个 CleanInvalidSeed journal 与 1 个私有大小边界：本机共 **24 项**普通 2.722s、race 25.958s，vet exit 0。

NAS 同 24 项全部通过，0 跳过、0 失败、exit 0、无 OOM。linux/amd64、CGO_ENABLED=0、modernconly，二进制 SHA256 `2ed260e66a8e0d1a26b9999b8cdc6844d5f7e59fb3bd187e9e0ad3f80e1f73e1`；256 MiB 容器/GOMEMLIMIT 64 MiB，网络 none、只读根、独占容器及目录已清理，原服务状态不变。见 [NAS 记录](plugin-auto-transition-clean-journal-nas-verification.json)。这份证据只覆盖冻结 journal/transition 与边界，不包含随后新增的 Clean owner/Core/barrier。

冻结 transition 源码 SHA256：`2f8c3c7243daba482c3fba7acbd63ce689e51f1205641034d7d96ce93a62ca03`；测试：`1cc8919e2e7c7271f78a130baf4f6089c11990e2320965f57fe3441d19dc1f7a`。当前仍需接 owner map、SDK/catalog、实际 barrier、历史 outbox 消费者和资源按需读取/回收；不直接改旧 Run.Scope、不清空未知效果。

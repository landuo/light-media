# CleanInvalidSeed 2.1：App 宿主接线

本文件记录原装 CleanInvalidSeed 2.1 的原生宿主边界。算法、原表单及 SDK 位于 `internal/plugins/cleaninvalidseed`，逐效果宿主位于其 `host` 子包；已接正式目录和主程序执行入口。可信远程命令入口、外部消息送达及用户配置/数据迁移仍未完成。原源码及离线 oracle 的校验见同目录既有迁移文档。

## 执行与真实权限

`plugin_clean_invalid_seed_owner.go` 只接受固定 2.1 的实际安装身份、实际持久 Run/Input，以及通用 deferred 层签发的 started 执行上下文。SDK 子进程退出并释放进程槽后，才等实例 gate；worker 不提供下载器地址、文件路径、配置补丁或写入凭据。

每次真实 action 经 `BeforeRun` 取得以下锁序：实例 gate → host gate → lifecycle → 短 pluginMu 快照并释放 → download → import → 短 SQL 复核。网络开始前释放 lifecycle，download/import 租约保留到 action 的最终 journal checkpoint。Core 后续 Guard、配置保存、通知、收据和索引投影均不反向获取 lifecycle/pluginMu。Settings 发布的实际锁及下载器、目录快照因此在 action 内稳定。

私有租约使用真实 call 指针、nonce、存活期与绑定的 original Invocation；每个子效果再签发精确 grant。释放路径涵盖 admission、非法后端、取消、source 失败和最终持久化失败。源目录只来自受信任配置中实际 `download_dirs` 的左侧路径；允许父目录不直接变成删除权限。媒体目录复用真实 Root.ID，但删除目标还必须是实际配置目录的一层子项。文件宿主的实体快照、根、父目录与树 CAS 仍独立执行。

## 逐效果保存及失败前缀

`plugin_clean_invalid_seed_core.go` 在原 source 的调用位置保存配置和通知，不能等整次 Clean 成功后统一补记。配置、私有 config receipt、源状态及实际排队任务修订在同一 SQLite 事务内更新；正在执行的 original Run/Input/InputSHA 不变。只有本 owner 私有链且尚未开始的精确 pending admissions 可以迁移，已 started/deferred 的其它执行不会被改写。

对于已应用对象，内部配置的精确 From→To 收据同时迁移 AppliedBinding，保留原 AppliedRun。此路径要求真实 config effect、对应密文 Core receipt、当前源 Config 与相同原 Invocation；外部尚未 applied 的 transition 不能借此变为已完成。这样 command 内部保存后突然退出，不会因 revision 增加而错误重新 init、取消尚未到期的原 once 计划；未知效果仍会阻止自动启动。

Native 写入先记录 intent，实际 ACK 后才提交 Core receipt；已发写入后回复或 SQL 结果未知，保留 started/unknown，禁止盲重放。`DeleteTorrent` 沿原路径固定 keep-data，源文件删除是另一独立分支。文件步进以 durable fs_confirmed 加精确 Core receipt 收尾；只有实际被删媒体路径、相同 managed_file/Asset 身份且确认 ENOENT，才把既有资产置为 missing_pending。保留管理身份、slots、排除、整理计划与 jobs 审计，不触发自动补下或取消订阅。删除源硬链接不把仍存在的媒体目标标丢失。

通知事务只接受到私有 outbox 和本地通知历史，状态为 history_saved；没有外发成功含义。只读 `cleanInvalidSeedDeferredOutcome` 必须证明 original scope、全部效果及实际 Core receipts 才能给 confirmed 或两个固定 known failure。任一 started、unknown、fs_confirmed 或缺收据都无法改成成功。失败前的已提交配置/通知保持原事实。

## 生命周期与现有调度器

`plugin_clean_invalid_seed_transition.go` 暂停旧实例 admission，随后 HTTP 在短 lifecycle/pluginMu 及同一 SQL 事务内保存新 config/run/transition。预检失败不发布配置或新任务；确证 rollback 后恢复旧计划。commit 返回错误不能被当成 rollback，必须读同事务 marker；已 published 保持待核对。reset/uninstall 推进 generation，旧 owner 收尾后退休；reset 后 revision 0 的首次保存无需重装。

ordinary init 沿原 warm 对象语义，显式 reload 重建源对象。clean Close 执行源 StopService 并标 Closed，下一次启动需要 fresh init；未 clean stop 的崩溃恢复保留已持久 once 时间和 cron anchor，不把它们提前执行。初次 disabled 安装不要求先配置任何目录。

源命令内部保存配置后，使用精确原 config Effect 与已加密 Core/config receipt 在同事务推进已应用对象的 Binding，保留原 AppliedRun。只迁移已应用的内部链，不能擅自应用用户发布中的 transition。否则冷启动会把正常的内部 revision 变化误判为新初始化并取消旧单次任务；实际 SQLite 关闭/重开回归验证了原 date/next_due 保留并恰执行一次。

源计划投影到现有 plugin_schedules，实际执行仍只有 plugin_runs 队列。已有 enqueueDue 与 manual service 事务调用专用 producer，源事件、真实 Run、occurrence 与 next_due 一起提交。自动事件保留一秒 grace；手动服务直接调用同一 Clean，无 date misfire，不消耗原 date anchor。onlyonce 采用持久 date 计划与标志清理同事务的可靠性适配；原 Python 是先建内存 date 再保存标志，并非原子。

用户重新配置或显式 reload 时，私有 queued trigger 和精确对应的实际 pending Run/queued occurrence 同事务取消，防同 revision reload 把旧事件留成无意义的核对任务。只比较真实 QueuedRunID、immutable InputSHA 和 Binding，已 running 的历史执行不当作尚未开始处理。混合 RemoveLink/CleanInvalidSeed 批量卸载按全局 InstanceID 排序预留全部 gate，再获取生命周期锁提交，释放生命周期锁后逐项 resolve/release。

## 本片仍保留的边界

* 固定五种 source command 的私有 producer 已实现；可信 PluginAction 消息入口尚未连接，不能声称远程命令可用。
* Native Client.Control 在真正写入前的本地歧义/不存在等拒绝，目前无法通过可信类型区分 write phase，保守记录 unknown。后续应在 download 客户端增加不可伪造的写前拒绝分类；不能仅根据 ErrNotFound 猜测没有发写入。
* 通知只有本地历史/outbox，外部 MessageQueue/渠道投递另行接线。
* 独立文件恢复维护入口还需要实际 owner 授权租约；无法证明的文件或远端效果保留核对状态，不自动重试。
* 私有 journal 有 32 MiB 有界存储，单 Core receipt 有界；容量失败不是无限历史支持。先记录写入 intent，写后无法收尾的事实保持未知，不假成功。

## 统一验证与交付边界

正式 catalog/runtime 全链验证从真实 Handler 安装与配置保存进入主程序 executePluginRun，经编译目录中的 SDK 子进程、deferred 握手、原生 owner 与实际加密 SQL 存储完成，未手动替它提交终态收据。验证源 once 标志、不可变 admission、known failure 和已发下载器写操作 unknown 的区别；unknown 重开或新任务均不能造成第二次删除。

本片 **24 项 CleanInvalidSeed 直接测试**包含在148项联合应用回归中；其余覆盖 AutoSignIn SQL/切换/typed读取、RemoveLink、共享调度、卸载、组织及原有 CategoryAPI。普通测试与 NAS 的148项均通过；并发与静态检查的最终退出状态、时间和源 SHA 见 [App 验证记录](plugin-clean-invalid-seed-core-verification.json)，NAS隔离参数与清理记录见 [联合 NAS 记录](plugin-clean-auto-owner-nas-verification.json)。另有独立 host29项 NAS 报告，不能合并成插件已完成或迁移数量。

所有文件和下载器效果只发生在自建临时文件、合成库、独占SQLite及loopback qB服务。没有安装或启用生产插件、迁移原配置/KV/文件、操作既有种子/媒体、发送真实通知或部署新版本。正式可安装条目已有部分真实业务，不代表原版全部交互、恢复维护和消息功能已经1:1完成。

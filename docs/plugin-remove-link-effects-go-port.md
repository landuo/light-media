# RemoveLink 2.17：核心副作用适配器

本切片提供 `internal/app/plugin_remove_link_effects.go`，供后续 RemoveLink 原生 owner 接线使用。没有注册 catalog、worker RPC、生命周期 owner 或后台消费者，因此这不是“插件已安装可用”的交付。已有纯算法、连续 watcher、transition 和 SQL journal 的来源与验证，分别见 `plugin-remove-link-go-port.md`、`plugin-remove-link-host-go-port.md`；本文不覆盖其旧 SHA 或 NAS 证据。

## 原行为与核心数据模型

原装 2.17 的 `delete_history` / `delete_history_by_dest` 分别按源和目标查找并删除整理历史；Go 纯算法只在 `delete_history` 开启时调用 `History`。下载删除事件与通知也由原开关决定。适配器遵守这三个开关，不因为删除文件额外引入订阅取消、自动补下或删种子策略。

本项目的整理历史是按 `ResourceSource` 聚合的展示记录。稳定的 `ID` 与本次执行的 `FileID` 不同，`ExecutionKey` 为持久执行顺序。`transfer_history.go` 的实际已提交 job CTE 负责选择最新记录；本适配器在同一 SQL 事务读取其最新可见执行，写入既有 `transfer_history_hidden`，记录实际 `file_id` / `execution_key`。`ImportPlan`、jobs、import metadata 和文件归属审计都保留，历史操作不再次删除任何文件。

源文件直配必须与当前实际 PlanFile 的 dev/inode/size/mtime 完全一致。失败但已提交的整理记录可能没有 Asset，此时仍可按精确源身份隐藏记录。最新源身份不符时立即拒绝，不回退挑选一条旧记录。

重整别名不能由 `Asset.SourcePath` 或 `ResourceSource` 字符串自行证明。适配器从最新执行与当前 managed video / Asset 归属出发，沿每层冻结的 `import_meta.reorganize.Asset.ID` 查找旧的已提交 PlanFile，核对旧目标、RootID、AssetID、Video.Snapshot、Video.SourceSnapshot、MediaKey；执行键必须严格递减，资源分组必须相同。旧 Asset 已被 superseded 并不导致冻结祖先失效；整 job 后续失败也不否定此前已提交的单文件执行。

重复整理产生的 `already_imported` 执行会合法复用 AssetID，不能在筛选冻结身份之前用“多候选”一概拒绝。适配器先按冻结 owner、源快照、Root / Target、媒体键、成功单文件状态、严格早于当前执行的 fence 及同资源分组筛选；剩余分支必须有相同物理 SourcePath / SourceSnapshot，并逐分支证明同一个请求来源及指纹，任何分支断链、冲突或失败都拒绝，不能只选一条成功旧记录。分支栈防环、全调用共享 128 节点预算，成功证明可 memo；超过预算明确失败，不指数扩张。多个可见历史组仍指向同一物理 Source 时，外层历史变更保持歧义拒绝，即使某个祖先证明单独成立。

## 私有发件箱与事务

构造器仅以加法创建 `plugin_cleanup_core_receipts`、`plugin_cleanup_private_outbox`。行键为绑定实例与代次的 HMAC token；收据和完整 outbox 载荷以现有私有 `cleanup_journal` 加密通道保存，AAD 额外绑定 `core-effects:v1:<generation>:<kind>:<token>`。明文列不包含文件路径、通知正文或标题。

每个 effect 的原始 payload 字节、完整 Binding / Scope / ExpectedPin 均进入身份校验。历史隐藏、必要资产索引更新、outbox 插入和对应 effect 收据在同一 SQLite 事务提交；收据写入失败会回滚之前变更。已存在且身份完全相同的收据返回原布尔结果，包括“没有对应历史”的 false 结果，避免以后重试误删新历史。host 在回复丢失后把计划标成 uncertain 时，也只有真实 core 收据允许恢复成功；没有收据的 uncertain 行不会盲重放。

单 effect 的完整身份编码限额为现有 1 MiB，超额明确拒绝，不截断路径、通知或 payload。外层密文 envelope 使用既有 16 MiB 私有 journal 解封额度，不能把普通插件配置或公开 API 限额随之放宽。

`download_file_deleted` 与 `notification` 只落入私有 pending outbox，**没有外部消费者，也没有发送、投递完成或下载器已删种子的承诺**。没有扩展公开 `DomainEvent` 的引用契约，没有通过普通 notification record 伪造送达。SQL commit 结果不确定时返回 `ErrUncertain`；后续恢复应核查收据，不能把错误当作安全重试信号。

## 授权与最小接线契约

私有构造器：

```go
newPluginRemoveLinkEffects(ctx, app, journal) (*pluginRemoveLinkEffects, error)
effects.authorize(ctx, binding, action, actualRunID, roots) (release func(), err error)
effects.Commit(ctx, effect) (bool, error)
effects.ReconcileConfirmedFiles(ctx) error
effects.afterAction(ctx, binding, action) error
```

真实 owner 的 Guard 必须先按 **lifecycle → download → import** 顺序获得核心写租约，再调用 `authorize`；适配器不获取这些锁，不声称能从 Go mutex 检测调用者持锁。release 先撤销适配器租约，再由 owner 释放写锁。不能在持有 import 锁时反向获取 pluginMu / lifecycle。普通保存、显式 reload、reset/uninstall 必须使用已实现的 transition API 隔开旧自动执行与配置事务，不能靠这个租约绕过生命周期 barrier。

实际 App owner 必须构造非 nil 的 `cleanuphost.Options.AfterAction = effects.afterAction`。宿主在 event / apply / timer / configure / stop 的完成 checkpoint 之前、同一 Guard 租约仍有效时调用它；drain / reload 沿 stop 使用相同边界。嵌套处理被暂停期间捕获的事件仍保留外层 Guard action，不创建另一份租约。回调失败会保存 Active、Blocked、原文件效果和原事件错误证据，关闭 watcher 并停止自动 timer；不把该事件写成 confirmed，也不写 drained / lifecycle_applied 成功标记。重启发现 Active / pending transition 后明确拒绝自动重放。`block` 的后续错误处理也不再用 nil Active 覆盖这些证据。

不能把 `ReconcileConfirmedFiles` 放到无返回值的 Guard release 中：动作可能已经写入成功，release 无法回传事务错误。nil AfterAction 仅保留独立纯宿主、不关联核心索引的兼容行为，不能作为 App 构造方式。

租约绑定同一个 App / SQL journal / instance / generation / config revision、原始可比较 context、nonce、授权 root 快照、action 和实际 init/reload RunID。包装 context、取消、释放或变更核心配置后均不再有效。每个 Commit / reconcile 事务重新读取实际安装身份 RemoveLink 2.17、installed / enabled 状态、配置修订及 init/reload 的真实 running run。source root 必须仍在核心 AllowedRoots 内，media root 必须与当前核心 RootID / Path 一致，目录与每一级路径都须通过禁止符号链接的目录打开检查。

Commit 还必须读到完整 MAC 校验后的 journal，匹配原计划的所有字段、Active scope、事件的真实 planned receipt 或 init transition 的精确 From / To / RunID；仅插入一条 planned effect 不构成权限。普通事件使用有效配置；falsey `{}` 保留对象原有效配置。普通 init 在新配置下处理旧事件，只允许与真实已发布 init transition 匹配的旧 Binding。显式 reload 的旧 Stop 使用旧对象 / 旧 flags，仍按宿主现有 transition 实现区分，不能伪装普通 init。

SDK 的 `cleanup.apply` 使用 Guard action `apply`，必须绑定实际 sealed、running 的 event invocation，且 input.event_id 与当前 Active / planned event scope 一致；无 run、错误 action、错误 event ID 或终态 run 均拒绝。原生 watcher 的 `event` 不虚构 SDK run。取消未提交 transition 时处理的旧事件仅使用真实 `transition_abort` 租约与同代次事件证据。

停止时要在旧实例仍有效、且退休 publication 尚未增加 generation 前完成 Drain → Reconcile → core publication，不能在 uninstall 后再借旧租约做变更。

## 真实文件确认与资产索引

宿主在原算法选择后完成其文件 CAS / quarantine / unlink，再写 confirmed 文件效果。本适配器只协调 confirmed 的 regular `unlink` / `observed_delete`，不因 planned / staged / uncertain 行判断删除成功，也不执行任何新的文件删除。

只删除源文件不会把仍存在的媒体目标标为缺失。对 media 角色的确认删除事实，适配器重新核对授权 root、当前根目录实体、canonical parent、ExpectedPin，以及当前 managed video 的 Path / AssetID / RootID / MediaKey / Snapshot 和 Asset 的 Path / Root / 状态。文件必须确为 ENOENT；替换文件、符号链接、权限错误、根目录换挂载都不能当作缺失。若同次动作清理了空父目录，仅同一租约中新 confirmed 的 `rmdir` 身份链能代替已不存在的 canonical parent，普通缺失父目录不能充当证据。

核对通过后设置 `missing_pending`、`needs_attention` 和 MissingSeenAt，保留 asset_slots、exclusions、managed_file、plans/jobs。这里直接进入 pending，是因为已有成功删除的持久事实；不是把普通扫描的第一次缺失观察缩短为一次确认，也没有调用会释放占用、添加排除或允许重下的 `resolveMissingAsset`。

`ReconcileConfirmedFiles` 通过上面的强制 AfterAction 在每次 owner 动作完成前、**仍持有该写租约**时调用，覆盖 `delete_history` / `delete_torrents` / `notify` 全 false 而从不触发 Commit 的路径。它不能放到释放 import 锁之后或退休 generation 提交之后。

旧 FS receipt 没有发生时间和冻结 Core AssetID。为防止把崩溃前旧删除事实关联到后来同路径的新资源，租约会记住开始前已 confirmed 的 FS IDs；旧事实已有 core 索引收据则幂等恢复，旧事实缺少该收据则返回 `ErrUncertain`，等待明确核对，不猜新 owner。当前未实现跨进程自动修复此缺口，也未添加第二套任务模型。

## 本切片验证

目标测试正则为 `^TestPluginRemoveLinkEffects`，所有路径、SQLite 和文件均由测试拥有，不访问真实媒体、下载器、NAS 服务或外部通知。

测试覆盖真实提交历史、失败无 Asset 的整理执行、实际硬链接宿主删除、三个可选开关全 false 的索引收尾、原开关对应的 pending outbox、多次 copy 重整祖先链、16 个并发收据重试、Close/New 后密文收据恢复、末尾收据 SQL 故障的全事务回滚、不同 context / nonce 释放 / 修订 / 代次 / 版本 / scope / Pin / 配置 / 授权 root 拒绝、已确认父目录清理与无证据父目录缺失的区别。

本机结果：`go test ./internal/app -run '^TestPluginRemoveLinkEffects' -count=1` 通过（2.673s）；相同正则 `-race` 通过（11.352s）；`go vet ./internal/app` 退出 0。日志分别为 `work/remove-link-effects-targeted.log`、`work/remove-link-effects-race.log`、`work/remove-link-effects-vet.log`。这是 9 个顶层目标及其子场景，未执行全 App 回归。旧 host / transition NAS SHA 不代表这个新增 App 适配器已经过 NAS 或已接线。

后续 AfterAction 修正新增 8 条实际 SQL 故障路径（event、注入原生观察器循环的异步 event、apply、timer、普通 configure、stop、退休 drain、显式 reload），验证已真实删除文件、索引及收据同事务回滚、动作返回失败、Active / confirmed FS 证据保留、无 lifecycle 成功标记及重启拒绝重放。异步用例还经过 loop 的二次 block，确认不会丢失最初 Active。另有真实持久 event run 的成功 Apply 和四种实际 run 身份拒绝。宿主纯包对应 7 条 Guard 持锁与生命周期失败测试；来源 manifest 保留修正前 SHA 和旧 NAS 证据，新增 AfterAction 当前 SHA。

这批正式宿主全包 normal 通过（17.795s）、全包 race 通过（19.540s）；App 的 11 个顶层 Effects 目标 normal 通过（3.594s）。`go vet ./internal/plugins/removelink/host ./internal/app` 退出 0。日志为 `work/remove-link-after-action-host-normal.log`、`work/remove-link-after-action-host-race.log`、`work/remove-link-after-action-app-targeted.log`、`work/remove-link-after-action-vet.log`；App 同一 11 项目标 race 通过（15.880s），日志 `work/remove-link-after-action-app-race.log`。本切片未执行新的 NAS 验证。

重复执行祖先链的后续修正增加了真实 copy import → 相同源/目标再次 preview+execute（断言 already_imported 复用原 AssetID）→ 从原媒体目标 copy 重整 → 隐藏最新实际执行的完整回归；错误冻结快照、不同 Source、共享断链、异组/不同目标过滤、严格未来执行 fence、分支环保护均有独立场景。128 节点边界使用 127 次真实重复已整理执行后再重整，明确拒绝总计 129 节点的证明。该批 App 12 个顶层目标 normal 通过（6.658s），日志 `work/remove-link-repeat-ancestry-targeted.log`；同目标 race 通过（23.496s）、`go vet ./internal/app` 退出 0，日志分别为 `work/remove-link-repeat-ancestry-race.log`、`work/remove-link-repeat-ancestry-vet.log`；仍不冒充新的 NAS 验证。

## 本轮正式 NAS 验证

App 的 12 个 Effects 顶层目标在独占 NAS 容器全部通过，0 跳过/失败、退出0、未 OOM，本地及 NAS 二进制 SHA `628330c1bb6062c3308da0bac0ac6dc2bbd45f85cb19f2956a19a4dcb61d31ce`；见 [Effects 报告](plugin-remove-link-effects-nas-verification.json)。连续宿主包含新增 AfterAction 的当前 Linux 42 个顶层测试也全部通过，同样无跳过/失败及 OOM，SHA `74a3563c86ec525541489afe87c936945792dd8766a6ebd0b7bffb0b01d3fa41`，见 [宿主报告](plugin-remove-link-after-action-host-nas-verification.json)。

宿主首轮验证因载荷缺少来源测试读取的 `.go` 文件而失败（41通过、1失败）；同一二进制补齐载荷后42项通过，报告保留首轮结果与清理证据。两次均使用无网络、只读、UID1000:1001、256 MiB 内存等限制，测试只使用自建文件/SQLite/SDK夹具；所有容器及远端独占目录已清理，light-media / moviepilot / qb 的状态、启动时间和重启次数未变。这些验证仍不代表 App owner、平台注册或用户数据迁移已完成。

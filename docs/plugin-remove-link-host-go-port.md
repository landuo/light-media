# RemoveLink 2.17 连续本地宿主包

状态：已将独立验证候选及生命周期 transition 移入 `internal/plugins/removelink/host`（Go 包名 `cleanuphost`）。本包只提供可供 App 接入的连续本地服务，未注册插件、未连接用户目录、未迁移实际配置/历史。App 私有 SQL Journal 已有独立切片，当前正在补 Transition 编码与事务验证；实际 registry/hosthandler、核心业务 Effects、Guard 和配置切换接线仍未完成。现有纯算法/SDK包的状态、原表单、114 条 AST oracle 和原行为审计见 [plugin-remove-link-go-port.md](plugin-remove-link-go-port.md)。不能据此宣称 RemoveLink 已可安装使用。

## 来源与搬迁范围

- 原实际安装源 `/app/app/plugins/removelink/__init__.py`：2.17，89,455 bytes，SHA256 `1f9e719e16f5f72340aa50e942d6f7062390a4bddd82f5f3f47d58ef757dbda4`。42 个可执行函数/方法的 AST 与脱敏参考一致。来源证据沿用 `testdata/plugins/remove-link-2.17/installed-reference.json`，没有读取或输出原配置值。
- 此宿主新增代码来自已冻结的 `work/_remove_link_host`；初次搬迁逐字保留 10 份 Go 运行/测试文件，仅将 `journal.go` 改名为 `fixture_journal_test.go`，不再向生产包暴露 FileJournal；fake Effects 同样只在测试文件。随后仅修复 Recover 未恢复 Machine.Watches 的真实 bug，并新增 common/Linux 回归。`testdata/plugins/remove-link-host/source-manifest.json` 同时保留候选原 SHA256 与三份修后文件 formal_sha256，包测试核对精确差异，未改删除选择算法。
- Transition 来自冻结的 `work/_remove_link_transition`：精确修改 `host.go`、`types.go`，新增 `transition.go`、`transition_test.go`、`transition_linux_test.go`，五份均与候选逐字一致；另外只更新来源校验测试和本文。`transition-migration.json` 保留基线/新文件 SHA，原候选、clean-recover 实机 SHA 和日志均保留，不以新证据覆盖旧版本。
- 纯算法依赖为已验证的 `internal/plugins/removelink`（引入提交 `a00c150`）；使用已有 `golang.org/x/sys v0.49.0`，不增加 fsnotify、Python 运行环境或主模块依赖。
- **许可证据限制**：实际安装源和脱敏参考没有许可头，本切片未取得可独立核验的 RemoveLink 分发许可。来源校验不是许可授权，不推断 MIT/AGPL 等许可；对外分发前仍需核验该插件上游适用许可。Linux inotify 是现有 x/sys 依赖，继续适用其自身许可。本宿主为项目新增 Go 实现，未复制 watchdog Python 实现。

## 保留的执行语义

`Open(Options)` 校验授权根身份并恢复私有状态，只创建串行 owner；不会自动启用监听或过期队列。调用方必须明确选择首次 `Configure` 或干净重启 `Recover`。

`Configure(ctx, Binding, persistedConfig)` 对应**复用同对象的 init**。先应用**新配置**，再排空已捕获事件、关闭并排空旧 watcher，再调用原 `Machine.Init` 按新开关立即排空旧延迟任务。新 watcher 在初扫之前建立，初扫期间的变化保留在有界队列中，成功 checkpoint 后才运行 timer。不能在普通保存中清空旧任务或按旧配置执行。

平台显式 reload 是另一条生命周期：原 Manager 先 stop 旧对象，再建立新对象并 init 保存配置。新增 ApplyTransition 按持久 Kind 区分：普通 init 复用对象、应用新 flags；reload 先按旧 flags Stop/收据，再 New Machine/新 defaults 后 init。保存 `{}` 等 falsey 配置时，普通 init 保留宿主有效配置并持久化，fresh-object reload 从默认配置开始。首次 cold-start 从默认 Machine 初始化；干净 generation 恢复走 Recover，不能用 Init 代替并提前排空延期任务。具体 App 调用尚未接入，不能把这一包级实现当作界面保存/reload 已生效。

`Recover(ctx, Binding)` 只接受同实例/代际/修订和同根身份的干净 checkpoint。先建立 watcher，再完整补扫：保留仍存实体的原插入顺序与 Added 时间，离线新文件赋予新的 Added 时间，保留队列截止时间与选中删除目标的 Frozen 快照。不会把离线消失猜成删除事件，也不会用补扫的新身份覆盖旧删除快照。Active、未应用的持久捕获事件、先前 Blocked 等不确定状态明确拒绝自动恢复，等待对账。

`RecordEvent(ctx, Event)` 是可信宿主 producer；`Apply(ctx, Binding, eventID)` 只能消费已持久捕获并匹配绑定的事件。pending 事件按 Sequence 顺序执行；已确认事件重试返回原收据。`Observe` 是同一 owner 路径的宿主直接调用入口。实际 native watcher 已直接走该 owner；未来 App 不得再将同一 native 事件独立发布一遍，造成两个不协调 producer。

`Stop(ctx, reason)` 关闭 watcher、排空已捕获回调，再**立即**处理所有剩余队列，不等 delay 到期；保留原 `stop_service` 语义。`Close` 先业务 stop 再结束 owner，必须在移除存储/资产引用或关闭数据库之前完成。短 SDK 的 `WorkerStop` 则无业务副作用，不能让每次 worker 退出导致队列提前执行。`Suspend` 只用于模拟突发丢失，不伪造成功业务停机。

## 本地文件与监听边界

- 删除选择、顺序、排除、STRM 映射、通知原文仍由现有纯算法决定；宿主不通过清洗 `..` 改变原选择结果。只有可信 registry 的非重叠 source/media 授权根可操作，未知或云盘 Storage 引用明确失败。
- 每个目录分量通过 `openat` / `O_NOFOLLOW` 打开，复核根和 canonical parent 的 dev/inode/mode；选定文件快照含 dev/inode/size/mtime/mode。根自身不可删，符号链接、特殊文件、物理别名根、路径替换和权限撤销均不可静默成功。
- 单项实际删除：durable planned → 同父目录 NOREPLACE 私有 quarantine → 复核快照/parent → fsync → durable staged → 再复核/取消检查 → unlink/rmdir → fsync → durable confirmed。失败恢复仅 NOREPLACE，发生冲突保留 quarantine 并阻止重放。递归刮削冻结完整树后逐项删，不用 RemoveAll 吞掉后来出现的条目。
- observed_delete 记录原文件外部消失的事实，与宿主实际 unlink/rmdir、source/media 角色分别收据。已确认事件不能删除同路径替换文件；不确定外部 Effects 不盲目重试。
- POSIX 没有按 inode/mtime 条件执行的 unlink。quarantine 和重复复核需要 App 共用写租约；无法声称隔离恶意同 UID 外部进程在最后复核后改 inode/parent 的行为。这不是内核原子 CAS。
- Linux 使用递归 inotify。rename cookie 先配对，再交原算法过滤，保留 `.trickplay` 双向移动；重叠 hardlink/STRM 同路径监听保持两种独立处理。baseline 与目录 move 子项采用原 os.walk 同层文件优先顺序。
- 实际安装 watchdog **6.0.0**，`InotifyBuffer.delay` 为 **0.5 秒**；只读核验禁写 bytecode、未读配置。未配对移动等待相同时间并保持捕获顺序，证据 `testdata/plugins/remove-link-host/installed-watchdog-metadata.json`。这里是观察到的调度参数，不代表移植 Python 库。
- capture 与 rename buffer 各 4096 项；kernel overflow、buffer overflow、授权 watch 根移走均 fail closed，需要对账。默认后台动作超时 30 秒，适配器必须响应取消。未验证大型媒体库吞吐。
- 非 Linux 的 NativeWatch 明确 ErrUnsupported。Darwin 仅验证注入事件加真实 POSIX 文件；不提供等价轮询。STRM 内容不作为 URL 请求，没有网络云盘实现。

## App 最小接线契约

### 身份和持久调用

`Binding` 字段是 `InstanceID`、`Generation`、`Revision`（均来自真实 installed/run）。首次构造 `Options` 还需 `Roots`、`Journal`、`Effects`、`Guard`；`Roots` 的每项是稳定 `ID`、canonical `Path`、`Role=source|media`，必须来自现行授权 root registry，不能从 worker JSON 授权路径。`MediaExtensions` 从可信现行设置冻结；`Now`、`Watch` 默认使用系统时间和 Linux native observer，fault hook 仅私有测试。

实际私有 SDK RPC 仅允许 `cleanup.configure {}`（宿主按真实 action 区分 init/reload）与 `cleanup.apply {event_id}`（真实 event）；宿主从持久 invocation 取 action/input/config，核对 plugin/version、实例/代际/修订、运行状态、能力、DesiredEnabled 和停机上下文。worker 不可提供 Event、patch、路径、snapshot、存储凭据、history ID 或下载器 ID。RPC 必须在全局 pluginMu 之前分派。

包的事件/effect 收据不代替插件运行收据。App 另需 `(run_id, instance_id, generation, config_revision, action, durable_event_id)` 与 init/reload/applied 结果的绑定，准确处理 Configure 已提交但 worker reply/FinishRun 丢失；不能再次 Init 将已恢复延迟任务提前排空。核心配置修订提交与新宿主接入应有明确阶段、恢复和失败结果，不能仅保存配置就称 watcher 已生效。

### SQL Journal：字段、行与事务

生产只依赖 `Journal.Read(ctx) (Durable,error)` / `Update(ctx, func(*Durable) error) error`。Read 返回独立快照；Update 在独立状态上执行回调，同事务持久提交后才成功，失败不得污染已提交值。回调可能只更新 effect/event，不能把其余字段置空或丢失。不要直接拿持久对象内部 slice/map 给异步读写，也不要将完整 64MiB fixture JSON 塞入 16MiB plugin KV。

推荐每实例 sealed SQL header 加有顺序的行/delta；需无损保存以下所有字段：

| 分组 | 必需字段 |
| --- | --- |
| header | Binding 全部字段、原始 Config JSON、Roots[]、Serial、EventSerial、Active 可空、Blocked |
| root identity | Root 的 ID/Path/Role；Stamp 的 Dev/Inode/Size/ModNS/Mode |
| Machine.State（顺序有义） | Path、Identity.Dev/Inode/Added；持久 position |
| Machine.Queue（顺序有义） | Path、Identity.Dev/Inode/Added、At、Processed；持久 position |
| Machine timer/watches | TimerDue 可空；Watch.Path/Mode 及顺序 |
| Pins / Frozen（各自独立） | path key、Pin.RootID/ParentPath、Parent Stamp、File Stamp、Epoch |
| Events | id key、完整 Event(ID/Kind/Path/Dest/Mode/Directory/At)、Binding、Sequence、State、Error |
| Effects | id key、Scope/Kind/Path/Role、Binding、Expected Pin、Stage/State/Error、Payload 原 JSON、Result bool |
| Transition（可空） | ID/Kind/Phase/RunID、From/To Binding、WasRunning；仅 lifecycle marker，不是第二个 job/队列 |

私有路径、原配置、Payload、错误可能敏感，存储与诊断需按现有私有数据规范密文/脱敏，不直接作为公开 DTO。整数不能经 float64 损失 dev/inode/serial 精度，时间需保留纳秒与 Added 比较语义。事件及确认收据的 GC 只有在对应重放窗口关闭后进行，不能按随意容量截断。

### Lifecycle transition API

- `BeginTransition(ctx, From, opID, kind)` 在无 App 锁时等待 owner，校验旧状态后持久 prepared；暂停旧 timer/业务执行，保留 watcher 仅按序 capture。现行进程可信捕获与恢复遗留 pending/Active/Blocked 分开处理，后者不会被 Begin/Abort 祝福或清除。
- `PublishTransition(*Durable, ticket, To, runID)` 仅是可信事务回调中的内存变更。必须通过 App SQLJournal.updateTx 与配置 CAS、现有 PluginRun 同一 SQL 事务提交；不能配置提交后另补 marker。RunID 必须是对应真实执行，且不能复用已应用 RunID。
- `ApplyTransition(ctx, ticket, actualRunID, persistedConfig)` 读取已发布 marker，按 init/reload 区分执行。参数只来自实际持久调用；SDK JSON 仍是 `{}`，worker 不能给 ticket/config/path。paused native 回调不取旧修订 Guard，也不删文件；真正应用前核对捕获的 From/顺序/原监控范围，文件 CAS 保留。
- `DrainTransition` 为 reload/reset/uninstall/shutdown 按旧 flags 收尾，普通 init 不允许先 Stop。`AbortTransition` 只在确认未发布且 Guard 核对核心仍为 From 时恢复旧工作；已提交或读取不明保持暂停，不猜回滚。reset/uninstall drain 后 CAS 失败不会撤销已删文件，只会保留收据并重建旧监控，不重放旧队列。
- Phase 是 prepared、published、drained、published_drained、applied。From/To 复用现有 Binding，WasRunning 防止 Abort 启动原先未运行的服务；不另存一套 deadline、队列或原配置。
- 最终 checkpoint 与内部 `lifecycle_applied` Effect 同事务保存，后续保存不覆盖旧 run 收据。该收据不经过外部 Effects.Commit；App 回复丢失/启动恢复需优先查可信收据，再决定是否修复既有 PluginRun。退休收据 Binding 保持 From，To 新 generation 仅在 Transition/Payload；旧 Journal 不跨代改写，也不放宽普通 Effect 校验。

SQL metadata 必须显式保存新指针，采用 `omitempty` 保持旧无字段 metadata 的规范化 MAC；FileJournal 的宽松 JSON 读回不代表 SQL codec/MAC 兼容已验证。App 实际同事务发布、SDK 权限、生命周期锁和私有 registry/hosthandler 仍是后续接线范围。

### Guard 和锁序

Guard 签名 `func(context.Context, Binding, action string) (release func(), err error)`；成功必须返回 release。除 configure/recover/record/apply/event/timer 和 `stop:<reason>`，transition 还使用 `transition_begin:<kind>`、transition_abort、`transition_retire:<kind>`、transition_receipt。每阶段必须按真正行为授权；retire 核对旧 journal From 加真实核心 To，不能要求旧代际仍 installed 后才允许纯收尾。stop/reload 在正确 stopping 上下文可执行，不能因 DesiredEnabled 已关而无法按源语义收尾。

**串行 owner 内部才调用 Guard**。结合当前 App `saveSettings` 的 pluginMu→downloadMu→importMu 顺序，建议：等待 owner（不持 App 锁）→ owner 中取得 lifecycle → 短 pluginMu 授权快照并释放 → downloadMu→importMu 的共用写租约（确需订阅行再 subMu）→ 直接短 SQL/CAS。持写租约后不能再取 pluginMu 或重入已持锁；Journal/Effects 需用 Tx 内部适配，不能调用会再次拿这些锁的公共 mutator。所有进程 drain 等待不在 SQL/pluginMu 中。持 worker process 令牌的 RPC 可以等待 owner，但后台 Guard 不能再取得同一个 process 令牌。凡 Configure/Stop/Apply 调用者先持目标 lifecycle/file-write/pluginMu/SQL 事务再等待 owner，都会与等待这些锁的后台事件形成环，禁止这样接线。

私有 context lease token 可避免同请求重复取得已持租约，但不能解决“后台 owner 等 lifecycle、RPC 持 lifecycle 等 owner”的跨请求反序。App lifecycle/drain 必须按上述顺序设计，或在接线前明确替代的无环 owner 模型；不能只加重入 token 就称安全。Guard/Effects 不能回调同一 owner；Journal 事务不能跨一次 host 调用，host 会多次短读写 Journal。不要在持有 Registry/pluginMu 时阻塞 Close/Stop。

新增包级屏障解决其自身的旧 event/timer 发布窗口，但 App 仍须使用完整顺序：锁外 owner pause/capture-only → 配置/run/transition 同事务 CAS → SDK 按实际 action 初始化 → applied 收据/恢复。直接调用旧 Configure 不会替界面补齐此事务边界。reset/uninstall/close 及批量卸载同样必须先在锁外成功暂停目标 owner，再取得生命周期锁；失败须查真实提交 marker，不能猜回滚。当前应用入口尚未接入，不能宣传保存窗口已经端到端修复。

搬迁后发现并修复 clean Close 回归：Machine.Stop 会清空 Watches，原候选 Recover 启动 observer 后未重设 Machine.Watches，随后 allowedEvent 拒绝事件。新增 common 回归先实测失败（ErrUnsafe，exit 1），再仅于安全补扫完成后、checkpoint/activation 前恢复 validated specs 副本。保留队列截止时间、实体合并和 Frozen 快照不变；真实 Linux clean Close→Open→Recover→外部 unlink 用例通过，不通过 Observe/RecordEvent 注入代替 native 事件。

### 核心 Effects 和引用迁移

`Effects.Commit(ctx, Effect) (bool,error)` 使用 Effect.ID 幂等，并将自己的核心变更/持久 outbox 与该 ID 收据放在同事务；计划已存在而未确认时也要能返回已提交结果。不能先发真实通知再写去重，不能将“提交了种子事件”报告成“已删除种子”。

| Kind | App 实际责任 |
| --- | --- |
| history_src / history_dest | 按 Path/Role 和 Expected Pin 核对原实体及归属后变更对应整理历史；同事务处理资产、managed-file、plan/source 引用。原记录不存在可合法返回 false；不能删除同路径的新记录。 |
| download_file_deleted | 精确 `{src}` 事件持久 outbox 和幂等收据，消费者按当前下载器归属/做种保护决定动作；false 必须错误。 |
| notification | 按可信宿主收件范围写 `{title,text}` 通知 outbox 和幂等收据；false 必须错误，未知结果返回 ErrUncertain。 |

unlink/rmdir/observed_delete 的文件收据由宿主 Journal 自行管理，不能误映射成核心 Effect 直接重删。源文件与媒体文件分别收据；文件系统和 SQL 没有共同原子事务。外部动作完成但确认未落盘、Active/pending 捕获恢复、root/parent身份变化等情形需要专门对账，不能清空标记重新运行。本切片不提供此对账 UI/API。

## 验证和证据

冻结候选：本机 normal 5.615s、race 7.354s、vet exit 0，CGO0 linux/amd64 构建 exit 0。随后 NAS 独占 alpine:3.22 容器运行 **25 个顶层用例，0 skip/0 fail，exit 0、无 OOM**；UID1000:GID1001、network none、只读根、cap-drop ALL、no-new-privileges、256MiB RAM/swap、128MiB tmpfs、GOMEMLIMIT64MiB。包括原生真实10秒timer、0.5秒move顺序、trickplay双向move、重叠hardlink/STRM两种实际执行、恢复补扫/冻结CAS、大小mtime/parent/root替换、unlink提交间隙和并发去重。

候选二进制本机、远端及执行后 SHA256 都为 `3838c7533a0d122764a975be1651798808c649af7046286c711890b5cf56d713`。退出后才删除该新容器，核对独占 marker 后才删除该新目录；未挂媒体库、未操作旧服务。脱敏结果和完整测试输出分别保存于 `testdata/plugins/remove-link-host/candidate-nas-result.json` / `candidate-nas-tests.log`；去除了个人工作区/主机临时路径、容器标识和 Docker 其他主机字段。仓库不含测试二进制、配置凭据或 cleanup marker。**上述是候选路径的 NAS 结果，不冒充搬迁后正式二进制已跑 NAS。**

初次搬迁正式包验证（2026-10-17，全部终态，以下二进制尚未包含后续 clean Close 修复）：

- `go test ./internal/plugins/removelink/host -count=1`：exit 0，4.813s；`work/remove-link-host-formal-normal.log`。
- `go test -race ./internal/plugins/removelink/host -count=1`：exit 0，6.511s；`work/remove-link-host-formal-race.log`。
- `go vet ./internal/plugins/removelink/host`：exit 0；`work/remove-link-host-formal-vet.log`。
- `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o work/remove-link-host-formal-linux.test ./internal/plugins/removelink/host`：exit 0；`work/remove-link-host-formal-linux-build.log`，正式二进制 SHA256 `dd50a165edb528adfec56b109a86dc39538ae9473a9f08dc8460eaacd7fc55bc`，只保存在 ignored work，没有提交二进制。本次未运行正式路径 NAS 测试；前述候选 NAS 结果明确另计。

修复后正式包验证（2026-10-17，全部终态）：

- 回归修复前：`go test ./internal/plugins/removelink/host -run '^TestCleanCloseRecoverRestoresMonitoring$' -count=1`，exit 1，准确报“recovered monitor rejected its authorized event”/ErrUnsafe，`work/remove-link-host-clean-recover-before.log`。
- normal：exit 0，5.233s；`work/remove-link-host-clean-recover-normal.log`。
- race：exit 0，6.793s；`work/remove-link-host-clean-recover-race.log`。
- vet 和 CGO0 linux/amd64 构建：均 exit 0；`work/remove-link-host-clean-recover-vet.log` / `work/remove-link-host-clean-recover-linux-build.log`。
- 修后正式二进制 SHA256 `11b2c12063cee7fd905327b303f4d1f78aacde14951089026549149579c93536`，本机/远端/运行后相同。相同隔离参数的新独占 NAS 容器运行正式包 **28 个顶层用例，0 skip/0 fail，exit 0、无 OOM**。新 native clean-close 用例真实完成 source/follower 删除收据；容器先确认退出再删除，owner marker 核对后删除本次新目录。未挂媒体、未修改旧服务。
- 脱敏结果 `testdata/plugins/remove-link-host/formal-clean-recover-nas-result.json`、完整输出 `formal-clean-recover-nas-tests.log`；原始本机输出 `work/remove-link-host-clean-recover-nas/tests.log`。本次正式包 NAS 结果与前述候选结果分别保存，没有用旧 SHA 的证据代替修后版本。

该 clean-recover 阶段 Darwin 21 个顶层用例（20 个文件/生命周期用例加来源校验），Linux 另有 7 个原生 watcher 用例。生产 GoFiles 中没有 FileJournal 或 fake Effects。该阶段源/证据校验清单见 `testdata/plugins/remove-link-host/formal-verification.json`；其 SHA 不代表后续 transition 版本。

### Transition 候选与正式迁入（分别计证）

隔离 transition 候选 normal 10.153s、race 11.903s、vet/CGO0 linux/amd64 构建均 exit 0；候选二进制 SHA256 `147ed28588e1d2c257c7b3b4804a1e7dde3ed66c7be20650b3b4851777526ac5`。独占 NAS 容器 **40 个顶层通过、0 skip/0 fail、exit 0、无 OOM**，包括真实 publication gap 跨旧 10 秒 deadline，零旧 revision Guard 调用，随后用新 flags 实际删除两组链接。只有本轮新建 fixture，无媒体库挂载；容器先退出再删除，marker 校验后删除独占目录。

候选结果和输出保存在 `transition-candidate-nas-result.json` / `transition-candidate-nas-tests.log`，明确标注 isolated candidate，不能当作正式二进制证据。`transition-migration.json` 绑定其两项修改、三项新增与基线 SHA；`source-manifest.json` 保留原候选、clean-recover、transition 的各阶段校验值。

正式 transition 验证（2026-10-17，全部终态，使用正式源码与正式 provenance 测试）：

- normal PASS，13.511s；`work/remove-link-host-transition-formal-normal.log`。
- race PASS，16.052s；`work/remove-link-host-transition-formal-race.log`。
- vet 与 CGO0 linux/amd64 构建 exit 0；`work/remove-link-host-transition-formal-vet.log` / `work/remove-link-host-transition-formal-linux-build.log`。
- 正式二进制 SHA256 **`17780a4ee7e9b23d68ae14429d463328b9da1647c46d168f9caa4c273f567f02`**，本机/远端/运行后相同。独占 NAS 正式包 **41 个顶层通过、0 skip/0 fail、exit 0、无 OOM**，包括正式来源校验，以及真实 publication gap 跨旧 timer 的 native 用例（10.58s）。与前轮相同隔离参数，无生产媒体挂载；容器先退出再删除，owner marker 核对后清理独占目录。
- 本轮正式 NAS 结果与输出为 `formal-transition-nas-result.json` / `formal-transition-nas-tests.log`；完整检查与当前源码哈希为 `formal-transition-verification.json`。前轮候选 SHA147ed... 和 clean-recover SHA11b2... 的文件保持原样，均不冒充本轮正式二进制。

上述证明纯宿主包行为；App registry/hosthandler、真实 Guard/Effects 与界面保存/卸载接线继续未完成。App 私有 SQL codec/事务测试由其独立切片记录，不能用本包 FileJournal fixture 代替。

`testdata/plugins/remove-link-host/build_linux.py OUTPUT` 只构建正式包的 CGO0 Linux test binary；`run_linux.py USER@HOST BINARY NEW_EVIDENCE_DIR` 仅供已授权环境重复隔离验证，明确传入目标，以独占目录提供只读测试源码/fixture，最终记录 SHA 和精确清理结果，不拉取镜像、不运行 App、不操作既有容器。

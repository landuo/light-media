# RemoveLink 私有 SQL journal

本文件记录 RemoveLink 宿主的状态存储适配器，不代表 watcher、删除动作或插件 App 接线已经完成。实现位于 `internal/app/plugin_remove_link_journal.go`，满足 `internal/plugins/removelink/host.Journal`。不增加 worker capability，也不把持有 journal 当作授权。

## 接口与事务边界

```go
newPluginRemoveLinkJournal(ctx context.Context, p *PluginStore, instanceID string, generation int64) (*pluginRemoveLinkJournal, error)
Read(context.Context) (cleanuphost.Durable, error)
Update(context.Context, func(*cleanuphost.Durable) error) error
updateTx(context.Context, *sql.Tx, func(*cleanuphost.Durable) error) error
```

构造函数以加法方式创建私有表，不修改 `NewPluginStore`。实例 ID 使用既有 `pluginStorageID` 约束，代次必须为正。每次读写都精确限定实例和代次。重新安装的新代次从空集合开始，不能读取旧代次的队列或效果收据。

`Read` 使用单个只读事务并从密文重新构造完整对象；`Update` 使用一个短写事务完成读取、可信内存 mutation、验证、变更行写入和提交。callback 失败、context 取消或任一 SQL 失败，整个事务回滚。callback 不能访问外部文件、网络或其他不可回滚副作用，也不能并发修改传入对象。

`updateTx` 供同包宿主把配置、运行记录 CAS 与 journal 更新放入同一已有事务。它不创建、提交、回滚事务，也不获取 `pluginMu`；调用方必须在任何错误后回滚整个事务，不得提交它之前已写的部分行。它自身不执行安装状态、revision 或权限 CAS；这些属于调用方的 live-generation guard 与同事务条件更新。单独的 `Update` 会管理自己的事务。

## 行布局与完整性

`plugin_cleanup_journal_meta` 的主键为 `(instance_id, generation)`，保存版本、密文、keyed 摘要和更新时间。密文仅包含标量/可空字段、原始 Config、计时器、Active、Blocked 与八个集合的数量及聚合摘要。

`plugin_cleanup_journal_rows` 的主键为 `(instance_id, generation, collection, row_key)`。八个集合为：

| 集合 | Durable 字段 | 顺序 |
| --- | --- | --- |
| roots | Roots | 连续 ordinal |
| state | Machine.State | 连续 ordinal |
| queue | Machine.Queue | 连续 ordinal，保留 nil task |
| watches | Machine.Watches | 连续 ordinal |
| pins | Pins | map key 在密文内 |
| frozen | Frozen | map key 在密文内 |
| events | Events | map key 在密文内 |
| effects | Effects | map key 在密文内 |

公开 `row_key` 为 namespace、集合及原 key/ordinal 的 HMAC token，不能从普通路径哈希进行字典枚举。实例 ID、代次、集合、ordinal、行数、摘要、时间戳和密文长度属于可见结构；原 map key、媒体路径、Config、事件/效果内容均位于密文内。

所有行复用 `PluginStore.seal/unseal` 的 AES-GCM，kind 为 `cleanup_journal`；AAD 绑定实例、版本、代次、集合和 token。摘要使用 Store key 的 HMAC-SHA256，并将实例、代次和用途分域。规范 JSON 对对象 key 排序并用 `UseNumber` 避免 float64 转换。集合聚合摘要绑定所有行的 token、ordinal、摘要与数量，可发现孤立删行、改 ordinal 和集合替换。

元信息与 collection 行在同一事务提交。未变化的行不重新加密；单个元素变化只更新该元素及必要的元信息摘要。状态量不会被塞入一个不断膨胀的 Durable KV。单个加密明文继续遵守既有 16 MiB plugin payload 上限；列表与 map 拆行不等于取消各项大小上限。

## 数据契约

完整保存当前 `cleanuphost.Durable` 的 Binding、Roots、Config、Machine 的 State/Queue/TimerDue/Watches、Pins、Frozen、Serial、EventSerial、Events、Effects、Active、Blocked 和 Transition。

Transition 保存 ID、Kind、Phase、RunID、From/To 和 WasRunning，使用可空指针及 `omitempty`。旧版本元信息没有该字段；nil 时不改变规范 JSON，因此原 MAC 无需重签即可读取。From 必须属于当前 journal；普通 init/reload/shutdown 的 To 保持同代次，reset/uninstall 可引用下一代次，但旧 header 与 Effect.Binding 仍属于原代次，不能让退休收据迁入新实例。

Config 与 Effect.Payload 使用密文内的 opaque byte 槽保存，保留原始 JSON 字节、空白、大整数/指数数字、未知配置属性，以及 nil、空 byte slice、JSON `null` 的区别；它们不经 float64 解码。其余宿主字段按精确 Go 整数类型反序列化。所有空集合初始化为非 nil 空集合。每次读取均新建 maps、slices、任务指针、时间指针和原始 byte slice；修改读到的对象或提交后保留的 callback 对象，不能修改已经持久化的状态。

非零 Binding 必须属于构造时的实例和代次，revision 不得为负；该约束也检查每个事件和效果。未知存储版本、未知集合/宿主字段、重复 map 原 key、非连续 ordinal、身份不符、无元信息的孤立行、格式错误和 MAC/AEAD 验证失败都会显式返回错误。Config 和 Payload 中非空字节必须为有效 JSON，业务上未知的 JSON key 保留。宿主路径、ID、map key 等 typed string 的非法 UTF-8 明确拒绝，避免 `encoding/json` 静默替换为另一身份；opaque 字节槽不参与这种字符串重编码。

本适配器不提供数据库外的防回滚锚点：若整个数据库被替换为较早的有效快照，或者一个 namespace 的所有记录同时被外部删除，单靠该数据库内的收据不能识别这种外部回滚。它也不负责删文件、运行队列、监控生命周期或授权判断。

## 验证范围

测试只使用临时真实 SQLite Store 与合成内容，覆盖所有 Durable 字段和列表顺序、真实关闭重启、原始 JSON/精确数字、可变引用隔离、跨代次隔离、数据库/WAL 明文检查、不变行密文不变、callback/context/SQL 整批回滚、错 scope/MAC/未知字段/删行篡改、超过普通 KV 大小的 Config，非法 UTF-8 回滚、同调用方 CAS 事务提交/回滚，以及并发更新不丢收据。未启动真实 watcher，未访问用户媒体、下载器、站点或通知服务。

新增 host Durable 字段时，必须同时扩展本文件中的编码/解码和全字段测试；不能把当前显式字段映射当作对未来字段的自动兼容。

本次窄目标验证：`go test ./internal/app -run '^TestPluginRemoveLinkJournal' -count=1` 通过（0.940s）；相同目标 `-race` 通过（4.587s）；`go vet ./internal/app` 退出 0。未运行全仓测试，也未进行 NAS 或真实媒体操作。

后续与文件夹原管理接口增量联合验证：全仓普通测试、modernconly全量App、相关竞态、vet/模块校验全部通过。正式CGO0 Linux App测试二进制在NAS独占隔离容器执行34个顶层目标，其中本journal为8个；零跳过、零失败、无OOM，校验值与精确清理结果见[联合NAS收据](plugin-organization-journal-nas-verification.json)。只使用合成SQLite/SDK及自建临时文件，没有启动生产清理监听或导入用户配置。

## 配置切换的 SQL 适配验证

连续宿主的 BeginTransition、暂停捕获、PublishTransition、绑定运行的 ApplyTransition、Abort、显式 reload 和退休语义见[宿主专文](plugin-remove-link-host-go-port.md)。发布函数只修改已有 Durable，不另建业务任务；调用方必须把它与实际配置、既有 PluginRun 放在同一个 SQLite 事务中。

新增三个顶层测试覆盖旧字段集合及同一 MAC 兼容、完整切换字段/指针隔离、退休下一代次及跨实例拒绝；使用真实 `saveValueTx` 和 `enqueueRunTx`，验证 journal 后段 SQL 失败回滚配置与运行记录，成功则共同提交，旧 revision CAS 不改变切换标记。

真实连续宿主另接本 SQL journal，在自建目录与真实硬链接上保留旧延期任务，先提交新配置/运行/切换标记，再由匹配运行应用新 flags 并实际删除目标，最终生命周期与文件收据保留；停止、重新打开宿主并从 SQL 检查点恢复监控成功。该测试使用注入 watcher 及仅核对 SQL 修订的测试 Guard，核心 Effects 明确禁用；不是实际 App HTTP/SDK/写租约接线证明。SQL 本身的关闭/重启由完整字段测试覆盖；Linux 原生事件由正式 host 包单独验证。

此版本目标 normal 1.799s、journal/store竞态7.197s、完整App normal88.493s和modernconly88.466s全部通过，vet及模块校验通过。新正式CGO0 Linux App二进制SHA-256为`33f63505ed6797df93a393f148678b3469448f8ade936113c4950e4e967ac8ec`，本地/NAS一致。独占NAS容器37个顶层目标全部通过、零跳过、零失败、无OOM；容器及owner目录已清理，三项原服务启动状态不变，详见[切换SQL隔离收据](plugin-remove-link-transition-sql-nas-verification.json)。App管理入口、实际Guard/核心Effects及工作器回复丢失恢复的最终桥接仍待接入，不能因此将RemoveLink登记为可安装。

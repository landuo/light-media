# PluginUnInstall 2.2 Go 行为切片

2026-10-16，本切片实现原已安装“插件彻底卸载”的独立 Go 算法、原配置表单、Go SDK handler 和真实 App 宿主接线。本机普通/纯 Go SQLite 全集、相关竞态、合成网页及本轮 NAS 隔离验证已通过。**未导入原配置或部署，不能称已在正式服务迁移完成。**

## 实际来源

实际安装 `/app/app/plugins/pluginuninstall/__init__.py` SHA-256：`2210e9dea47eeccdc46208d515e45ce1222dcadf50dbfc63cbeb41f94e470487`。`/app/app/core/plugin.py` SHA-256：`b0d26b661624ca4dd643af20553dd23c70ab3e38be4573844e053dffa3c3e4b7`。只读提取，未导入原应用或读取用户配置。

工作副本的图标 URL 经过脱敏，其正文 SHA 不是安装文件 SHA。参考快照完全去除插件类元数据，只保留八个方法、三个初始私有属性和所需管理器方法；全部八个方法与安装版用同一本地 AST 解析器核对一致。原私有图标 URL 没有进入报告或 Git。Go 声明只使用 `uninstall.png` 文件名。

参考见 `testdata/plugins/uninstall-original-oracle.json`、`testdata/plugins/uninstall-reference/`。31 组执行案例直接运行原 init 方法及实际管理器 stop/remove/delete AST；5 组表单案例直接运行原 get_form/get_local_plugins。服务、数据库、模块缓存为内存替身；插件包目录仅为参考脚本自己创建的临时目录。没有执行任何用户 one-shot、原插件卸载或既有文件删除。

## 原版行为

1. 非空配置先取 plugin_ids、clear_config、clear_data。未选择插件立即返回，不读取安装表，不清空配置。
2. 按已安装列表的原顺序判断成员；选择顺序不改变执行顺序。选择列表重复项不会重复执行，安装列表自身的重复项会重复执行。未安装的 ID 不会触发删除，但非空选择仍在末尾被清空。
3. 每个选中已安装插件依次移除定时服务、移除内存插件、检查/删除插件代码目录、请求清配置、请求清运行数据。包删除使用 ignore_errors，失败后继续执行。
4. 循环结束保存剩余安装列表，再完整替换自身配置为 plugin_ids=[] 和两个原开关值。缺失开关在这一替换中保存为 null，不擅自补 false。未知配置字段不进入重置后的对象。
5. get_state 始终 false；没有命令、API、详情页或周期服务。原表单为两个独立清理开关、已安装插件多选和原说明。选项只来自本地已安装元数据，按 plugin_order 稳定排序；不查询市场或比较版本，也不排除自己。

Go 对原 JSON 形态还保留空配置、null、非空但未命中的选择、字符串子串/字典键成员判断及 Python 开关真值。普通界面仍使用原多选数组；非正常标量选择按源错误边界失败，没有提供任意目录或不在安装快照中的对象清理能力。

## 已核实的原管理器清理问题

安装版 `remove_plugin(id)` 调用 `stop(id)`，后者先从 `_plugins` 删除该类。之后 `delete_plugin_config/data(id)` 默认 force=false；类已不存在时直接返回 false。因此这一实际安装组合会发出两个原清理请求，但不会清除其配置或 KV。原插件没有传 force=true。

本切片保留该副作用范围，不借迁移悄悄多删除用户数据。`Result` 分开报告 config/data clear requested 与实际 cleared；Go Operations 适配器必须保留上述 guard，不能把请求成功等同数据已删。若后续修复原 bug，应作为明确功能变更处理。即使只看原插件的包清理路径，它也只处理 ROOT_PATH/app/plugins 下的代码包，不处理 `/config/plugins/<ID>` 数据目录。

## 实现与接线边界

- `internal/plugins/uninstall.Run` 实现原顺序和错误中断；Operations 是宿主内部适配接口，不是 RPC。它不自行读取真实文件、数据库、调度器或运行配置。
- `Handle` 可由现有 `plugins.Serve` 执行。配置表单只调用空参 `plugins.uninstall.options`；init/reload 的非空选择只调用空参 `plugins.uninstall.apply`。没有 worker 自报目标 ID、目录或强制清理参数。
- 最终宿主必须从当前有效初始化执行、实例、版本、代际和配置 revision 读取真正配置，再与自身安装快照求交。不能直接信任 Invocation.Config 或增加通用删除 RPC。
- 当前普通 Uninstall 保留配置/KV；Reset 同时删除两者。此插件另外执行原有顺序、停止任务/事件、保存执行收据与原重置配置；不能用一个普通 Uninstall 调用代替全部流程。
- 自卸载是原版允许的选择。宿主需要处理当前 worker 与自身安装代际失效的顺序；不能在收据提交前直接杀掉正在等待 host_call 的自己，也不能因自身已标卸载而阻止原 update_config 的最后重置。
- Go 内置插件共享主程序，不能为卸载一个插件删除共享 light-media 可执行文件。包清理只作用于宿主确认归属的独立代码包；配置、KV、迁移文件和审计记录不得被当成代码包误删。

`internal/app/plugin_uninstall.go` 从绑定的真实 running init/reload、加密配置和已安装快照读取目标，拒绝 worker 提供目标、路径或 force。实例使用数据库插入顺序执行；原版单实例插件的值仍为插件 ID，新程序的多实例使用具体实例 ID，避免卸载同类其他实例。

首次安装及卸载后重装读取默认表单时，允许宿主按编译目录构造的 Generation=0、实例 ID 等于插件 ID、无运行记录、清单字节及其他初始元数据完全匹配的只读预览。该 ID 必须尚不存在或已卸载；不能用预览替代已有安装的精确代际检查，更不能据此调用卸载。正常表单在卸载、重装或代际变化后仍失效。

宿主先无副作用规划，再在无 SQL 事务区间完成已选媒体刷新收尾；停止任务、移除安装及事件权限、取消待执行调用、保留配置/KV、完整重置卸载插件自身配置与加密执行收据在同一事务提交。内置 Go handler 没有独立程序目录，因此没有代码包文件可删除，不删除共享可执行文件。两个清理开关仍记录 requested=true、cleared=[]，符合已安装原管理器的非强制 guard。

外层卸载 worker 按 lifecycle→process 顺序持锁，精确绑定 run ID/实例的私有上下文允许其 host 回调复用生命周期锁。只有当前选中的已安装 MediaServerRefresh 实例可在宿主收尾阶段重入执行，外层仍保留唯一进程令牌；其他实例和表单继续互斥。网络不占用数据库事务。消费者在收尾前的代际/revision 写入独立小型日志，最终事务删除；错误或重启只恢复尚未卸载且配置未变的消费者，避免进程在收尾与提交之间退出而使已安装插件永久失活。

自卸载保留当前调用，私有 CAS 允许在同一事务完成原版的最后一次自身配置重置。完成记录优先于 worker 传输错误；重启按收据完成旧调用，不重放到后来重新安装的对象。没有收据的事务未提交，可在当前身份/配置仍有效时重新排队。

## 验证范围

新包普通测试通过，覆盖 31 个原执行及 5 个完整表单参考、真实 Go SDK handshake→空参 host_call→绑定配置执行→结果→stop。SDK 测试故意令 worker 配置与宿主绑定配置不同，最终只处理宿主绑定的对象；所有物理删除仅为测试自建包目录。原管理器清理 no-op、自卸载、重复 ID、未知 ID、缺失包、包删除失败及保存中断均有判别案例。

新增 `TestPluginUninstall*` 集成测试已在本机目标测试、全仓普通及 modernconly 全集通过，覆盖真实 SQL 副作用与回滚、自卸载收据、重新安装后的恢复隔离、空参授权、只读选项不泄露配置、原表单/真实子进程以及嵌套媒体刷新收尾/取消互斥。专门的 SDK 子进程在真实 host_call 已完成后立即 os.Exit(3)，不发送 invoke 结果，验证可信收据可跨实际 EOF 完成自卸载。另有真实 HTTP 首次安装→普通卸载→重装用例，不能以直接写入安装表替代用户入口。

独立合成实例的网页验证经过原表单多选、保存、实际工作器执行、列表移除目标和重开表单清空选择；没有操作原 MoviePilot、已有媒体或用户 one-shot。

最终本机 `go test ./... -count=1` 和 modernconly 全集通过，App 分别 75.343/75.697 秒。`go test -race ./internal/app -run '^(TestPlugin|TestSubscribeGroup)' -count=1` 通过 449.580 秒；builtin/uninstall 两包竞态也通过。前端 124 项测试、构建、格式检查，以及 Go vet/mod verify 均通过。

最终 App 测试程序在 NAS Alpine 3.22、UID 1000/GID 1001、无外网、只读挂载、cap-drop=ALL/no-new-privileges、256 MiB 内存、128 MiB tmpfs、GOMEMLIMIT=64MiB 下通过相关正则的 156 个顶层测试，无跳过；不是 NAS 全 App 测试。正则为 `^Test(Plugin|SubscribeGroup|Subscription|Recognition|DownloadAdded|NativeDownload|ResourceMetadata|DownloadContext|IntentRecognition)`，包括本轮卸载、结果通道、历史和相关生命周期/API。程序本机/NAS SHA-256 均为 `6078e252486435be37ff66db0e650fed76848d18363fc50a941a5ffc3cedb022`；容器退出码 0、OOMKilled=false。独占容器及目录已精确清理，原服务保持运行；没有执行正式迁移。

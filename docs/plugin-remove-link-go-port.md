# RemoveLink 2.17 纯 Go 包与正式宿主接线边界

状态：已将冻结独立原型移入 `internal/plugins/removelink` 纯包；尚未正式安装、注册或接入用户目录。本切片未修改 App、builtin/catalog 和 go.mod。本切片不提供 watcher goroutine、生产文件适配器或云盘网络适配器；包存在不等于插件已可用。

源：实际 NAS `/app/app/plugins/removelink/__init__.py`，SHA256 `1f9e719e16f5f72340aa50e942d6f7062390a4bddd82f5f3f47d58ef757dbda4`，89,455 bytes。已读取源文件、未读取配置值。42 个函数/方法的可执行 AST（排除 docstring、跨 Python 版本重新解析）与脱敏副本一致；唯一本地脱敏发生在说明文字。证据 `testdata/plugins/remove-link-2.17/installed-reference.json`；只读验证器 `verify_installed.py`。实际 NAS watchdog/watchfiles 均存在，Linux 首选 InotifyObserver。

## 原接口与生命周期

- 原名称“清理媒体文件”、版本2.17、order0、auth_level1，状态直接返回 `_enabled` 原值。
- `get_form` 完整 Vuetify schema/defaults原样提取到 `internal/plugins/removelink/form.json`，其原始提取副本保存在 `testdata/plugins/remove-link-2.17/form.json`。13个字段：enabled、notify、delete_scrap_infos、delete_torrents、delete_history、delayed_deletion、delay_seconds、monitor_dirs、exclude_dirs、exclude_keywords、custom_scrap_extensions、monitor_strm_deletion、strm_path_mappings。
- 无 API、command、page；无事件装饰器/cron/service注册。监听文件created/moved/deleted，动态threading.Timer处理延迟队列；DownloadFileDeleted是发送事件，不是接收订阅。
- enabled/notify等使用 Python truthiness；非空config缺字段会覆盖部分属性为None，空config沿用旧配置。delay默认30，int后clamp10..86400，TypeError/ValueError回退30。
- init先应用新config，再stop_service，再清空队列，再注册监控，然后扫描初始状态；旧队列因此按新配置收尾。没有一次性clear flag或配置回写。
- 原实现没有自己的持久 KV：file state、延迟队列及watch均在内存。正式宿主新增的崩溃恢复/收据应标明是可靠性设计，不冒称原版已有保证。重叠目录可以分别启用硬链接/STRM监控，不能仅按路径去重而丢掉一种模式。
- stop_service先停并join全部observer，再cancel timer，再取未processed任务清空队列，在锁外立即执行全部任务，**不等待剩余延迟**。
- **短worker的协议stop必须无业务副作用**。每次invoke都调用的协议stop不能当成源stop_service；真实watch/state/queue归宿主，显式reload/uninstall/shutdown触发生命周期收尾。

## 必须保持且有辨别力的源细节

- 本地实体是(dev,inode)二元组，不能只比inode；新增加入时间用于重整保护。初扫所有目录使用同一初始化时间。
- 所有路径分量只要大小写不敏感地以`.trickplay`结尾，就忽略整棵子树；旧状态/已排队任务也忽略。
- created排除.!qB/.part/.mp/.tmp/.temp（大小写敏感）及非空逐行子串keyword；初扫**不应用这两类筛选**；deleted仅排除.!qB/.part/.mp，后再keyword。目录deleted在keyword之前就发DownloadFileDeleted（若开关启用）。
- moved先移除src状态，尝试跟踪dest；只有dest未跟踪或dev/inode不一致时才把src放回并按删除处理；trickplay两端任一命中直接移除src。
- excluded目录采用expanduser+normpath+normcase+commonpath边界，只防被动unlink与向上目录删除，不阻止原删除路径的history/事件/刮削处理，也不是监控排除项。
- 延迟任务执行：源路径重现则整项跳过；有同(dev,inode)且add_time严格晚于被删项、并>=删除时间-max(5,delay)的新路径时，只清旧名刮削，不删链接/history/发种子事件。
- unlink路径不存在：移除过期跟踪项，返回false，不阻断其余；其它OSError保留跟踪项；不stat二次验证实体。
- 原被删项先刮削→种子事件（非刮削文件）→源history；然后按file_state插入顺序unlink同实体链接并逐个处理。仅实际成功unlink的项计入通知。
- 刮削自定义后缀支持逗号/中文逗号/换行，strip+lower，非`.`/`-`前缀补点，顺序去重；匹配整个文件名endswith。
- 本地同名刮削：case-sensitive startswith视频stem；`.trickplay`目录还须stem严格相同。随后清空仅含刮削/`.trickplay`的目录再向上删除；目录通知与批次通知独立。
- **源错误保留为审计事实**：`parent_path in self.monitor_dirs.split("\n")`用Path比str，实际无法在监控根停止。宿主正式权限必须限制已授权根；不可把此源码行为无声当成安全目录策略。
- **源竞态**：unlink不验证当前路径实体，可删到同路径新文件。正式宿主须明确CAS/identity保护与原行为差异，不能让worker给任意path。
- STRM映射split(':',2)，两段默认local，三段显式存储，字典重复键更新值但保留第一次插入位置；顺序优先的**原始字符串startswith**，不做最长前缀或目录边界。只去小写`.strm`后缀，尽管监控事件大小写不敏感地接受STRM。
- STRM找父目录第一个“name startswith base_name且extension.lower在当前RMT_MEDIAEXT”的file，不读取strm内容。删除失败不做后续动作。
- STRM成功后按开关清本地刮削→远端刮削→远端空目录→dest history→通知；不发DownloadFileDeleted、不使用硬链延迟队列、不清除本地file_state残留。
- 远端刮削额外删除poster.jpg/backdrop.jpg/fanart.jpg/banner.jpg/logo.png（lower后精确匹配）。空目录清理可删除只含刮削的目录；遇任何子目录停止；逐层通过父list拿真实fileid。
- Alist/OpenList空目录删除将条目复制为type=file走精确remove，保留真实fileid/未知属性，不能误用父目录扫描接口。
- 种子操作仅发送DownloadFileDeleted{src}；实际安装核心只读取hash，对该src载荷直接返回，不会删任务或数据。磁盘可选MediaSyncDel有另一套路径消费者，但不在已确认的23个安装ID中，不能默认启用。通知原文可能声明已联动，须区分事件发布与业务确认；新转交边界见plugin-remove-link-outbox-go-port.md。

## 正式宿主后续要求

1. 私有cleanup configure/apply/lifecycle API，绑定真实持久run、instance/version/generation/config revision、已授权目录/存储引用、持久watch事件；worker不得提供patch、目标路径、存储凭据、下载器ID、history ID。
2. host持久file state、delayed queue、事件ID去重、实体与路径恢复、timer due；完整串行锁序、watch建立/初扫竞态，配置切换时源语义新配置收尾。SDK进程重启不能等于插件stop。
3. 本地删除是外部文件副作用，SQL收据不能使unlink与DB天然原子；持久planned/submitting/confirmed、目录FD/无符号链接路径检查、实体CAS、ENOENT对账与用户替换保护；通知/种子outbox去重，history删除与本项目整理历史/asset引用/源记录迁移有事务策略。
4. Storage引用必须来自可信host registry；原支持local/alipan/u115/rclone/alist等，本项目当前无这些云盘适配器。原型完整模拟其Storage契约，不等于正式支持或可接真实云盘；缺失引用明确失败，不映射到任意本地路径、不假成功。
5. 无原注册API/page/cron便不新增按钮/通用运行入口；原表单保留。所有真实测试目录均由本轮新建，所有storage/history/event/notification为fixture或httptest，不触及现有媒体/种子/消息。

## 已完成的隔离验证

离线 AST 抽取器仅执行选中的源函数和类；系统/数据库/存储/通知/observer/timer由隔离fake替换，文件只在TemporaryDirectory。72 组源流程 oracle 比较状态、队列、剩余文件、history/event/notification/Storage trace；42 组源纯 helper oracle 比较路径、后缀、配置与映射。额外验证真实本地硬链接、unlink 缺失/权限、timer不重置、stop提前排空、32并发重复事件及真实SDK子进程协议stop不排空队列。上述先前普通/race/vet及CGO0 linux/amd64交叉编译均仅针对隔离原型的独立go.mod；正式包的当前结果另列在下方。

实际NAS源核验运行于Python3.12.13/Unicode15.0；离线oracle生成器运行于本机Python3.11，避免3.14改变Pathlib尾点后缀语义。隔离原型曾复制已验证的目标Python15兼容helper；正式包直接复用现有兼容实现。普通oracle只证明列出的辨别用例，不声称所有运行时字符串边界穷尽。正式宿主不得直接把fixture adapter当生产路径安全实现。


## 正式纯包结构与验证

- `internal/plugins/removelink/{types,machine,storage}.go` 保留已冻结的原状态机/删除选择/存储 facade 行为，所有外部副作用仅通过显式 `Ops` 契约。`sdk.go` 复用正式 `plugins.Manifest` / `HostCaller` / `Invocation` / `Serve` / `Runner`，只发绑定的 configure/apply 请求。
- Python 整数和 Unicode 15 小写兼容逻辑复用已有 `internal/releasemeta/legacyanime`，不复制协议或 Unicode 表，不引入 Python 运行依赖。
- `testdata/plugins/remove-link-2.17/` 保存脱敏源 `source.py`、离线 AST 生成器 `oracle.py`、已安装来源/AST证据与 72 流程 + 42 helper 期望。Python 文件只用于开发离线 oracle，不由产品导入或执行；正常 Go 测试直接使用已经固化的 JSON fixture。
- 测试保留真实 SDK 子进程、真实新建临时硬链接、unlink 缺失/拒绝、延迟队列、协议 stop 与业务 stop 的差别、并发重复事件和 trickplay 保护。真实文件适配器仅在 `_test.go` 中，不能用于生产。
- 原副作用/删除根越界风险属于尚待审查的 host 接线问题。此切片没有注册到 builtin/catalog，也没有任何生产监听或外部云盘实现。

正式纯包验证（2026-10-16）：

- `go test ./internal/plugins/removelink -count=1`：exit 0，1.315s；证据 `work/remove-link-formal-normal.log`。
- `go test -race ./internal/plugins/removelink -count=1`：exit 0，5.513s；证据 `work/remove-link-formal-race.log`。
- `go vet ./internal/plugins/removelink`：exit 0；证据 `work/remove-link-formal-vet.log`。

共保留 72 条真实原 AST 流程与 42 条 helper oracle，额外通过真实 SDK 子进程/并发/文件失败测试；来源校验测试绑定 7 份冻结源/生成器/数据文件的 SHA256，并逐字比较原表单提取副本。未运行 App 或全套应用测试。独立原型的先前验证见 `work/_remove_link_port/README.md`，不以它代替正式包验证。

后续正式App全量及modernconly全量通过；正式纯包在NAS独占alpine:3.22/CGO0 linux/amd64容器中10个顶层用例全部通过、零跳过，退出0且无OOM。本地与远端二进制SHA一致；隔离参数与清理证据见 [plugin-status-nas-verification.json](plugin-status-nas-verification.json)。该测试仍使用包内临时文件/受控Ops，不代表原生watcher或生产删除宿主已正式接入。

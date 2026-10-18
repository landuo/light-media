# 源文件联动清理 1.0.7 的 Go 移植

状态：正式纯算法、SDK 与 App 原生监听宿主已接入，可安装入口已注册。原配置/KV/文件尚未正式迁移，未部署或启用用户目录监听；完整业务与平台验收仍在进行。当前范围和证据见[原生宿主集成进展](plugin-brush-scrape-native-progress.txt)。与 `RemoveLink` 不是同一个算法。

## 来源与范围

实际安装 `/app/app/plugins/scrapefileclean/__init__.py` 为 1.0.7，57,535 字节，SHA-256 为 `c14c36e17112605e6c856614e88ea425530be2462123126a85e1adfea009a3af`。完整模块可执行 AST（排除 docstring；包括 watchdog 导入回退及 except 内 watchfiles 类）与只读副本一致，规范化 AST SHA-256 为 `02f66833b2f3acb2347a6ee9361db5805629ae9d6f79fd50884edcdcf1bfe595`。

原源码、作者说明、方法清单、参考生成脚本和固定结果保留在 `testdata/plugins/scrape-file-clean-1.0.7`。原插件未带独立 LICENSE，保留作者及出处，不推定其许可。Unicode 数据许可随包保留。Python 脚本只生成离线参考，Go 测试、产品和 SDK 不调用 Python。

`New(Ops)`、`Init`、`Created/Deleted/Moved`、`Tick`、`Stop`、`GetState`、`Scrap` 已实现。状态保留原字典插入顺序、dev/inode/add_time，延期任务保留删除时间、known_paths 的 nil/空列表区别及 processed。原程序的内存状态本身不持久；这个纯包也没有宣称具备持久恢复。

配置、扫描与事件过滤、硬链接保护、覆盖移动、延期、整理记录查询顺序、刮削清理及通知文本均按实际源码处理。`Home` 和可选 `UserHome` 提供 expanduser 环境；命名用户没有 resolver 时明确失败，不猜测其家目录。

## 与清理媒体文件插件的实际差异

- Stop 丢弃 pending；Init 先按旧对象 Stop，再应用新配置。RemoveLink 的 Stop 会排空执行，不能复用。
- 延期依赖删除时的 known_paths，保护后来新增的硬链接，不按 add_time 时间窗推断。
- Moved 先取出源和旧目标；异实体覆盖先清理旧目标，已被新实体占用的路径跳过原路径副作用。
- 同处监控范围内的过滤重命名不作为删除；实际源码允许跟踪仍真实存在的移出目标，不能按注释擅改。
- 全量与增量扫描都按不区分大小写的全部临时后缀过滤；初扫不应用 keywords，也不排除整个 trickplay 子树。
- 目录删除先过滤 keywords；整理历史先查目标，再查源。
- 刮削名需要非字母数字边界；相同 stem 和更长其他媒体的共享刮削文件受保护。监控根采用规范化路径边界。
- 延期实际调用的删除函数仍生成“⚡ 立即删除完成”；保留原文本，不把通知文字当作下载器删除收据。

`FallbackBatch` 只翻译原 watchfiles 的变化批次：初始 dev/inode 表、added 优先、同实体 delete/add 配对 moved、跨批次 Known 表。原 Python set 没有规范排序，批次顺序由宿主提供；这个类型不启动 OS watcher。

## SDK 与宿主边界

配置表单完整保留原 11 字段和 defaults。原 get_command/get_api 为空、get_page 为 None；没有添加页面、API、cron 或事件订阅。

唯一 `ScrapeFileClean@1.0.7` 使用正式 Runner/Serve 协议。能力是 `scrape_clean.configure` 与 `scrape_clean.apply`：初始化只提交空对象，配置由宿主的冻结执行读取；文件事件只接受单一 `event_id`。工作器不能传入路径、另一实例或任意删除参数。短工作器的协议 stop 不调用长期 Machine.Stop。

`Ops` 接口提供有限文件操作、整理记录、事件和通知。现已接入 App 原生长期实例、真实监听和 timer、stop/join、文件身份核对及操作收据、SQL 历史与通知/事件 outbox。源码的 `DownloadFileDeleted` 仅有 src，原 core 缺 hash 时直接返回；不得擅自从路径推导 hash。发出该事件不等于种子已删除，下载器消费者的实际接线与确认结果按[集成进展](plugin-brush-scrape-native-progress.txt)另列。

单个状态机方法有互斥锁；宿主仍须串行派发原事件批次，不允许外部并发修改公开状态字段。不能把算法测试扩大为生产文件删除能力。

## 验证证据

- 67 个完整生命周期、真实临时文件/硬链接、延期、移动覆盖、刮削和异常场景，由 NAS 实际 Python 3.12 执行原方法 AST 得到参考。原系统没有 import、初始化或写入。history、event、notice、observer、timer 是受控适配；文件仅位于自建 UUID 临时目录。完整比较剩余文件、状态、任务、timer、逐操作 trace、异常及错误数。
- 8 个实际 watchfiles 方法 AST 场景，使用真实自建文件与录制 handler，只证明批次转换。
- 224 条原 helper 参考，包括 169 个路径包含关系、5 个扩展名、45 个名称边界和 5 个其他媒体保护判断。
- 原未捕获 OverflowError 参考保留新字段已赋值、旧 delay 未替换的顺序；Python 3.12 的 Unicode 15 isalnum 表逐项对应。
- 真 Go 子进程 Runner/Serve/HostCall 核对配置、延期任务跨工作器保留、绑定事件执行及恶意额外字段拒绝。并发取消不会提前删除文件。

正式包 normal 通过（0.499s）、race 通过（4.893s），vet 退出 0。CGO=0 的正式 Linux/amd64 测试二进制 SHA-256 为 `5a774e11ff25fc0840dba3b1e538e79326d97ba4caa7171b83065675b3ba7797`；本地与 NAS 校验一致。在 NAS 的新建 Alpine 3.22 容器中，10 个顶层测试全部通过、零跳过、退出 0、无 OOM。容器采用 UID 1000/GID 1001、无网络、只读根、无 capability、no-new-privileges、256MiB 内存和 128MiB 独占 tmpfs，GOMEMLIMIT=64MiB。文件/硬链接仅由测试生成，未挂载原系统配置、下载或媒体目录。临时容器及带 owner 标记的独占目录已清理，light-media、moviepilot、qb 的启动状态及重启次数未变。机器可读收据见 [plugin-scrape-file-clean-nas-verification.json](plugin-scrape-file-clean-nas-verification.json)。

原 `delete_empty_folders` 在无有效监控根且文件系统根不能删除等输入下可能不终止；Go 检测 `parent == path` 返回 `source_non_terminating_root_cleanup`。协议接受标准单一 JSON 值，任意 Python 对象和非标准 NaN/Infinity 不在输入范围。路径和原生监听面向实际 NAS 的 POSIX 环境，不宣称 Windows watcher 已完成。

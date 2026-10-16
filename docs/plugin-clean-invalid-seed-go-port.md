# CleanInvalidSeed 2.1 的 Go 移植

状态：算法、原生对象语义、原表单/命令及 Go SDK 已迁入 `internal/plugins/cleaninvalidseed`；没有注册 App/目录或导入用户配置，没有启动真实下载器、文件删除、调度或通知消费者。这不是“插件已可用”或正式迁移完成。

## 固定来源

原安装插件作者 DzAvril、版本 2.1，文件 85,202 字节；SHA-256 为 `fd398230ee132f6002dbaf892e28fb10b9b95567fda9ce513e1543329a8b7fda`，完整可执行 AST 为 `0e8b6823865834f207ba1480bf2f73b91c46a2b7f51ac41c934f3cb7333af40a`，逐方法记录 27 项。参考原机 Python 3.12.13、Unicode 15.0.0、qbittorrent-api 2026.6.0、transmission-rpc 7.0.11。

来源、逐方法校验、实际原生对象/包装器、StringUtils 和安全属性方法的参考，保存在 [测试资料](../testdata/plugins/clean-invalid-seed-2.1/source-checksums.json) 及其 reference 目录。离线脚本只提取安装版 AST 并注入受控依赖，不导入原应用、不读取账户配置、不连接下载器。保留来源和作者；源插件副本未见独立 LICENSE，未对它声明新的许可。CPython/Unicode 算法相应许可保存在 Go 包目录。

新程序运行时全 Go；参考 Python 文件只属于离线验证材料。可复用的 Unicode/整数和 APScheduler 语义继续调用现有 Go 包，没有复制第二套引擎。

## 已移植行为

核心入口为 `New(Host)`、`Init`、`Clean`、`DetectFiles`、`HandleCommand`、`ServiceTrigger`、`StopService`。Machine 是单所有者的可变状态机；后续宿主必须串行化并冻结配置修订/代次，不能并发改 Config 或 Host。

- 原完整配置表单动态填入下载器名称，保留五个原命令，不自创原插件没有的 page 或 HTTP API。
- enabled/cron 服务使用已有 `schedule.Spec`；onlyonce 的 date(+3 秒)、onlyonce=false 回写和 stop 状态保留为调度契约，纯包没有自己启动后台计时器。正式迁移不能重放旧 onlyonce。
- 按源规则第一轮筛无效 tracker，第二轮要求同 netloc 另有非明确错误的 tracker 证据，才允许处理。不能把“第一次请求失败”改成删除依据。
- 保留分类、标签、大小写、重复匹配、label_only 优先以及删除始终保留数据。无 tracker 或全部 tier=-1 不虚构同域可用证明。
- 文件检测、原目录映射的全量 replace/substring 语义、一级子项、mtime 对应的最小天数、递归大小累计和多服务尾部重复全量文件检查均按源执行。
- 保留原返回 None、部分结果后异常、命令异常时临时 flags 不恢复等分支；不通过“空成功”抹去源异常。

源异常也分别计证：未选下载器会遇到 None.values；notify_all 配合空排除列表的部分通知分支会出现未赋值变量；StringUtils.str_filesize 的 re.I 实际传到了 count 参数，导致 count=2 而不是忽略大小写。URL netloc 保留原大小写前缀、IPv6/IPvFuture 和 Unicode15 NFKC 行为。这些都没有按表单提示或常识重新解释。

可选 `LogHost` 提供核心分支进度、数量、结果和错误，notify=false 也能看到结果。源 verbose/debug 的全部逐字文本尚未复制；实际 App 日志宿主还必须使用真实插件作用域并脱敏，不能把远端错误体或私有 tracker 凭据写入日志。

## 下载器对象与效果边界

`NativeDownloader{Service, Call}` 接受宿主已授权、已绑定下载器的 `NativeCall`，没有 endpoint 或凭据参数、默认网络客户端或全局下载器搜索。其操作为 qB 列表/lazy trackers/加标签/保留数据删除，以及 TR torrent-get/标签/保留数据删除。正式网络协议、认证、名称与稳定实例 ID 绑定仍待接入。

原 `download.Task` 不含 category/content_path、原生 paused、tracker 完整属性，因此不能拿该投影代替原生对象。JSON 数字保留 UseNumber；qB tracker getter 按源 hasattr/truth/赋值分别读取，保留每轮三次 lazy 请求。TR 严格区分 snake_case 属性、camelCase 底层字典和 Tracker 对象，缺失属性/KeyError 不会被自动补成“可用数据”。

原包装层吞掉部分 RPC 错误，返回 false/None；原插件不检查这些返回，仍计入已处理数量。本 Go 算法保留这份来源计数，**后续宿主必须另外记录真实效果/未知效果收据，不能把源消息当作已删除的证据，也不能盲重试**。标签集合按原 set 语义去重，Python 随机哈希决定的线序不作逐字承诺；原对象 repr 的内存地址也不是跨语言稳定字段。

Host 物理文件接口尚无生产实现。合成文件系统不是授权、路径快照、TOCTOU、符号链接或 rglob 的实机证明；必须冻结允许目录及实际文件身份，在每次副作用前重新核对。目录、名称、hash 不能由 SDK worker 自行提供来取得权限。

## SDK 与剩余接线

真实 `plugins.Runner` / `plugins.Serve` 已使用本包的 Manifest 和 Handle 执行配置、动态表单、清理及受绑定命令；不是以返回 null 代替效果验证。反向调用只提供 `clean_seed.configure`、`clean_seed.form` 和 `clean_seed.execute`。事件/服务/once/命令只提供持久 event_id；配置、下载器、hash 和路径须由宿主可信上下文解析。工作器 stop 不取消宿主 date 计划；禁用/reload/uninstall 应由生命周期宿主明确停止。

仍需实施：当前安装身份/代次/配置/触发绑定、原名称到下载器实例映射、真实认证与原生 RPC、物理文件 Host 和逐动作恢复、持久 +3 秒/cron 与 onlyonce 回写、通知 outbox 与日志、命令入口、App/前端注册、正式配置/引用迁移、受控真实实例验收。参考 [接入清单](../testdata/plugins/clean-invalid-seed-2.1/host-integration-plan.md)。在这些完成前不出现在可安装目录，也不增加已迁移插件数量。

## 验证

- 85 组安装版 AST 完整业务轨迹，包含 8 组实际原生对象与更多日志分支；逐项比较返回、源错误、通知/调用、状态、计划和合成文件结果。
- 52 组实际 StringUtils、32 组实际下载器 wrapper、14 组实际安全 getter 参考。
- 真实 Go 子进程 SDK、越权参数拒绝、阻塞 getter 超时/自建 goroutine 回收、无溢出大小和 notify=false 日志结果。

独立候选 normal 0.445 秒、race 5.861 秒、vet 通过。迁入正式包后，仅改变注释、参考位置和文件输入方式；算法与原来源参考未更换，原表单/命令/许可校验仍完整。正式包 normal 0.476 秒、race 5.876 秒、vet 与 CGO 禁用的 Linux/amd64 编译通过。未执行完整 App 回归，未连接真实下载器或用户文件。

正式包的 Linux/amd64 测试二进制 SHA-256 为 `fc16bef3eb07b98d4dd1aa4b4e1b4f37614da26481fb07ab9c7590e2728e3b94`，本地与 NAS 上传后完全相同。在无网络、只读、普通 UID1000:GID1001、无 capability、256MiB 内存及 swap/GOMEMLIMIT64MiB 的隔离容器中，13 个顶层测试与全部参考子场景通过，0 跳过、0 失败、退出码 0、未 OOM。临时容器与 owner 标记目录已清理；新应用、原版 MoviePilot 和 qB 的状态/启动时间/重启次数不变，见 [NAS 报告](plugin-clean-invalid-seed-nas-verification.json)。

报告另保留首次隔离包遗漏来源校验所需 form.json 的失败：12 通过、1 失败。补齐原表单/命令/许可上传材料后，用完全相同的测试二进制重测通过，没有删除或跳过来源校验测试。该包装问题及修复不构成真实下载器或文件后端验证。

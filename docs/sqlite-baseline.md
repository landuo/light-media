# M0 SQLite 驱动与合成负载验证

这是新项目的独立数据库实验，不连接旧项目数据库、下载器或媒体目录。下面的数据只代表合成数据库负载；它不是整个应用的空闲 RSS、容器内存或 72 小时稳定性结果。

## 固定版本与选择边界

2026-10-14 对照官方模块与发布信息固定：

| 方案 | 固定驱动 | 内置 SQLite | 构建方式 |
| --- | --- | --- | --- |
| 纯 Go | `modernc.org/sqlite v1.60.1` | 3.53.4，运行时再次核对 | `CGO_ENABLED=0`，可直接交叉编译 Linux |
| cgo 对照 | `github.com/mattn/go-sqlite3 v1.14.52` | 3.53.4，运行时再次核对 | 本机 C 工具链，独立对照二进制 |

来源：[modernc 官方包文档](https://pkg.go.dev/modernc.org/sqlite@v1.60.1)、[mattn 官方发布](https://github.com/mattn/go-sqlite3/releases/tag/v1.14.52)。modernc 的 libc 使用其模块固定的 `v1.77.1`，不单独升级它。

SQLite 官方说明 WAL-reset 修复从 3.51.3 起进入新版本，并有 3.50.7、3.44.6 的回移；实验拒绝已知未修复版本。两个驱动都通过 `SELECT sqlite_version(), sqlite_source_id()` 记录实际引擎，不能以系统 SQLite 版本代替。[SQLite WAL 文档](https://www.sqlite.org/wal.html)

## 可重复命令

在项目根目录执行；原始 JSON/`time` 输出放 `work/`，不提交合成数据库。

```sh
go test ./internal/sqlitebaseline ./cmd/sqlitebench
CGO_ENABLED=0 go test -tags modernconly ./internal/sqlitebaseline
go build -tags modernconly -o work/sqlitebench-modernc ./cmd/sqlitebench
go build -tags mattnonly -o work/sqlitebench-mattn ./cmd/sqlitebench
/usr/bin/time -l work/sqlitebench-modernc --driver modernc --temp-parent work
/usr/bin/time -l work/sqlitebench-mattn --driver mattn --temp-parent work
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags modernconly -o work/sqlitebench-linux-amd64 ./cmd/sqlitebench
```

Linux 的 `/usr/bin/time` 参数与单位另行核对；不要把 macOS `time -l` 的字节 RSS 当作 KiB。CLI JSON 只报告 Go 堆、Go runtime 的 `Sys` 和有限阶段采样；原生 SQLite 分配、共享映射、Go 堆以外的驻留内存必须由操作系统测量。独立二进制避免同时链接两个驱动对比较造成额外干扰。

CLI 每次在 `--temp-parent` 下创建一个全新临时目录，标记为合成实验，默认结束自动删除；`--keep` 可以保留该新目录。它没有接受现有数据库作为负载目标的选项。负载数量、读 worker、请求数量和总时限均有限制，默认总时限 90 秒。

## 场景及已实现验证

- 200 个订阅、5000 条元数据、50000 条文件记录。元数据简介为固定的合成文本；视频文件本身没有创建。
- WAL、`synchronous=FULL`、foreign keys、5 秒 busy timeout、每连接 2 MiB 页面缓存。写池 1 个连接，读池最多 4 个连接；外部 I/O 不进入事务。
- 分批 500 行初始化；8 个有界写 worker 执行 400 次占位，竞争 100 个唯一媒体/季/集/目的键。4 个读 worker 共执行 1000 次分页查询，每次最多 50 行。
- 唯一占位只产生一份下载声明和一份对应 job；在声明与 job 之间故意失败，确认两者一起回滚。
- 所有新建读连接均核对 foreign keys、FULL、busy timeout、缓存与只读保护，避免只设置第一个连接。
- 单独子进程先提交一份声明与 job，再开启未提交事务，并以 `os.Exit(86)` 异常结束。重开后，已提交内容保留、未提交内容消失；到期的 running job 租约转回 queued。
- `VACUUM INTO` 生成快照；检查行数、外键和完整性。快照完成后改变源库，确认快照不随之改变。

异常退出验证模拟的是进程终止，不是物理断电；它不能证明 NAS 磁盘控制器的 fsync 或断电可靠性。此实验不刻意重现 WAL-reset 的稀有内部竞争；版本门禁与来源核对负责确认补丁，而不是把一次普通并发跑通称作漏洞证明。

## 实测结果

2026-10-14 本机实测：macOS 27.0.1 / Apple M1 Pro / arm64 / 32 GiB RAM / Go 1.26.2。数据库位于项目 `work/` 下的本地文件系统，每个驱动顺序执行三次，每次新建数据库；未限制到 1 vCPU/512 MiB，测试期间机器也承担其他开发任务，因此属于工程基线而非受控性能排名。

| 指标 | modernc 纯 Go | mattn cgo |
| --- | --- | --- |
| 运行时 SQLite | 3.53.4 | 3.53.4 |
| 三次正确性检查 | 全部通过 | 全部通过 |
| 整体负载内部计时，中位数 | 658.897 ms | 479.690 ms |
| 初始化，中位数 | 372.082 ms | 332.968 ms |
| 并发读写，中位数 | 29.321 ms | 19.995 ms |
| 每次分页读 p95，三次范围 | 0.183–0.217 ms | 0.096–0.104 ms |
| 快照及一致性验证，中位数 | 139.784 ms | 65.186 ms |
| `/usr/bin/time -l` 最大 RSS，三次范围 | 32.7–34.3 MiB | 23.0–23.9 MiB |
| 负载完成时 Go HeapAlloc，三次范围 | 3.25–3.43 MiB | 0.66–0.84 MiB |
| 快照前数据库文件 | 8,953,856 bytes | 8,953,856 bytes |

这组数据中 cgo 对照的耗时与 RSS 更低；cgo 的 Go 堆数字不包含 SQLite 的 C 分配，因此不能仅用 HeapAlloc 比较总内存。RSS 为 `time` 对整个 CLI 调用报告的最大值，包含测试执行口径，不是常驻服务空闲值，也不是同时存活的整个进程树内存总和。`total` 是 CLI 内部计时，与外部 time wall clock 存在启动等口径差异。

所有运行产生 100 个竞争成功的声明/job，受控异常退出再增加 1 个已提交声明/job；报告 `claims/jobs=101` 对应快照前状态。随后为了验证快照隔离，源库再增加一对记录，快照仍保持 101 对，默认结束均清理。

当前初选 **modernc**：其合成负载正确性检查通过，且可以不用 C 交叉工具链生成 Linux 静态二进制，便于 NAS 上继续验证。这个选择优先考虑部署与工程复杂度，不能据此声称纯 Go 更快或内存更小。M0 保留 mattn 作为对照；若 NAS 实测或真实流程的 CPU/内存超预算，再明确切换，生产实现只保留一个驱动。

原始本机测量位于 `work/sqlite-{modernc,mattn}-run{1,2,3}.{json,time}`；这些仅含合成数据，无真实路径索引或秘密。`work/sqlitebench-linux-amd64` 已交叉编译，并用 `file` 确认静态 x86-64 ELF。

### NAS 首轮实机执行：未限额临时目录

2026-10-14 已在实际 NAS 执行纯 Go Linux 二进制：Debian 12 / x86_64 / 4 个逻辑 CPU / MemTotal 8,053,360 KiB。数据库在 `/tmp` 下的新建临时测试目录，没有连接已有服务数据库。沿用 200/5000/50000 默认场景、相同 SQLite 版本与 FULL/WAL 配置，完成后清理。未施加 1 vCPU/512 MiB 容器配额；本次只执行一轮。

| NAS 指标 | modernc 纯 Go |
| --- | --- |
| 驱动 / Go / SQLite | v1.60.1 / go1.26.2 / 3.53.4 |
| 正确性检查 | 全部通过，退出状态 0 |
| 内部总耗时 | 2118.810 ms |
| 外部 wall clock | 2.12 s |
| 初始化 | 1427.481 ms |
| 并发读写 | 182.344 ms |
| 分页读 p95 / 最大值 | 0.642 / 3.071 ms |
| 快照及一致性验证 | 276.277 ms |
| GNU time 最大 RSS | 28,548 KiB = 27.879 MiB |
| 负载完成时 Go HeapAlloc | 1,080,736 bytes |
| 有限阶段采样的最大 Go Sys | 13,228,296 bytes |

NAS 上唯一占位、领域/job 回滚、外键、受控异常退出恢复、租约重开、源库完整性、快照一致性及隔离检查均通过。保存拆分后的原始 JSON 为 `work/sqlite-nas-modernc.json`，GNU time 输出为 `work/sqlite-nas-modernc.time`，单位换算与主机口径为 `work/sqlite-nas-modernc-measurement.json`。

上述首轮结果只覆盖未限额的 `/tmp` 合成数据库。下述第二轮补充了受限容器与 ext4 持久化测试卷；正式 `/config` 的生产部署、Linux cgo 对照、完整应用的常驻内存及断电恢复仍需另外验收。实际媒体目录的 ext4 跨目录硬链接验证属于独立路径探针。

### 资源配额与持久化卷探针

另有只使用 Go 标准库的 `cmd/nasbench`，用来在一次性受限容器中运行同一个合成 SQLite CLI，并同时记录实际看到的 cgroup 配额。静态 Linux 产物为 `work/nasbench-linux-amd64`：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o work/nasbench-linux-amd64 ./cmd/nasbench
nasbench-linux-amd64 -bench /path/to/sqlitebench-linux-amd64
```

未指定 `-bench` 时只在 wrapper 同目录寻找 `sqlitebench` 或 `sqlitebench-linux-amd64`。子进程参数固定为 `--driver modernc --temp-parent /data --timeout 90s`，因此 `/data` 必须是新建的独立可写测试卷，不能绑定业务数据库目录。wrapper 总时限 95 秒；只执行一次合成负载，不提供服务器或持续 worker。

输出区分三个口径：

- `benchmark`：原 CLI 的实际 SQLite 版本、检查、阶段耗时与 Go 内存。
- `child_usage.maximum_rss_bytes`：Linux wait4 的子进程最大 RSS，可能包含其已等待后代的最大值；不是整个同时存活进程树的内存总和。
- `cgroup_before/cgroup_after`：实际 `/sys/fs/cgroup/memory.max`、`memory.peak`、`memory.current`、`cpu.max`。memory.peak 覆盖当前 cgroup 所有成员，可反映这个临时容器的峰值。`wrapper_self_status` 单独标记 `/proc/self/status` 只属于 wrapper，不能当作 SQLite 子进程 RSS。

JSON 捕获最多 1 MiB，stderr 最多 8 KiB；资源文件每个最多 16 KiB。子进程非零、超时、输出超限、JSON 非法/尾随数据、状态不是通过均返回非零；缺失 cgroup/proc 文件显示 unavailable，不编造资源数值。测试覆盖固定参数、有限捕获、非法 JSON、子进程失败及超时。

2026-10-14 已完成第二轮 NAS 一次性容器实测：实际 `/vol2` 上新建独立 ext4 测试目录绑定为 `/data`，容器 `--network none --read-only --cpus 1 --memory 512m`，`GOMEMLIMIT=256MiB`。二进制只读挂载，合成数据库写入这一轮的 `/data` 测试卷，结束清理。本轮数据库位置为 ext4 持久化卷；只有前述首轮使用 `/tmp`。

| 受限容器指标 | 实际结果 |
| --- | --- |
| 驱动 / SQLite | modernc v1.60.1 / 3.53.4 |
| cgroup `cpu.max` | `100000 100000`，1 CPU 配额 |
| cgroup `memory.max` | 536,870,912 bytes，512 MiB |
| Go `GOMEMLIMIT` | 256MiB，Go runtime 软限制 |
| 合成负载检查 | 7 项全部 passed，wrapper 与 benchmark 均 passed |
| SQLite CLI 内部总耗时 | 31,057.481 ms |
| wrapper 总耗时 | 31,090.697 ms |
| 初始化 / 并发读写 | 17,680.008 / 10,545.659 ms |
| 分页读 p95 / 最大值 | 0.839 / 3.201 ms |
| 快照及一致性验证 | 1747.720 ms |
| 子进程 wait4 最大 RSS | 26,935,296 bytes，25.688 MiB |
| 临时容器 cgroup memory.peak | 52,736,000 bytes，50.293 MiB |
| wrapper 自身 VmRSS / VmHWM | 6668 KiB，明确只属于 wrapper |

原始完整报告为 `work/nas-quota-baseline.json`，包含真实 cgroup 配额、峰值、子进程 RSS 与 SQLite 的七项检查：唯一占位、领域/job 回滚、外键、异常退出恢复、到期租约恢复、源库完整性、快照一致性及隔离。配额值来自运行环境观测，未以启动参数代替实际值。

这轮通过说明同一合成场景可以在 1 CPU / 512 MiB 和实际 ext4 测试卷中完成。两轮 NAS 的 CPU 配额与数据库存储位置均不同，耗时不可用于语言优劣排名，也不代表正常应用性能。该次 memory.peak 包含 wrapper、SQLite CLI、受控恢复子进程及 cgroup 记账范围内的缓存等；它不是生产服务空闲内存。一次成功执行也不能代替完整应用资源验收、72 小时稳定性或物理断电可靠性检查。

测试结果：双驱动 `go test ./internal/sqlitebaseline ./cmd/sqlitebench` 通过；`CGO_ENABLED=0 go test -tags modernconly ./internal/sqlitebaseline` 通过；`go test -race ./internal/sqlitebaseline` 通过。测试覆盖不同连接上的保护配置、异常进程结束、已提交/未提交边界、领域/job 原子性、快照隔离和无效负载/未标记数据库拒绝。

## 后续验收

NAS 已完成未限额 `/tmp` 和受限 ext4 测试卷各一轮执行，合成资源配额与持久化卷基线得到实际结果支持。M1 接入生产数据层时固定一个驱动，复用唯一约束和同事务约定，但需要真正的 schema migration、分页查询、可恢复状态机和备份恢复流程；本包仅是 M0 负载探针。

仍需验证：完整应用在 NAS 上 1 vCPU/512 MiB 配额下的真实混合流程及外部调用、72 小时持续负载、图片/日志缓存增长、磁盘满与挂载掉线、配置/密钥随备份恢复。不能由本次合成结果直接宣称应用资源目标已达标。

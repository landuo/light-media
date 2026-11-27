# NAS 内存增长调查（2026-11-23）

此次实验的目标是常驻约 100 MB、峰值约 150 MB，同时减少 GC，不以容器 OOM、重启或调低 GOGC 作为达标手段。下表的 RSS 使用 MiB（1,048,576 B）；若按十进制验收，100 MB = 95.37 MiB，150 MB = 143.05 MiB。

## 实机基线（只读）

基线采样时 NAS 运行 0.58.0，build `plugin-job-probe-d55469e3`，已加载 18 个插件、启用 13 个；本地修复起点 `37db6931`。对正在运行的实例每 15 秒读取一次 `/healthz`，检查 `data.status`，只留数字与版本，21 个样本跨度 305.1 秒。未重启、改配置、调用强制 GC 或写入媒体。

| 主进程量 | 最小 | 中位 | 采样最大 |
|---|---:|---:|---:|
| RSS | 140.88 | 151.82 | 162.61 MiB |
| Go HeapAlloc（含等待回收对象） | 24.07 | 41.71 | 63.30 MiB |
| Go 管理内存估计 `Sys - HeapReleased` | 88.62 | 99.55 | 112.09 MiB |
| 空闲未归还 `HeapIdle - HeapReleased` | 13.84 | 28.41 | 48.32 MiB |

GC 计数 1622 → 1678，约 **11.0 次/分钟**。RSS 中包含文件映射（RSSFile 中位 29.66 MiB），还受到 Go 以外内存影响。原版健康探针本身遍历 4 条规则、解码约 840 个观察项，采样并非零开销。这段记录支持“频繁分配和高水位保留”，不足以断定泄漏，也未覆盖用户看到的 200 MB 以上峰值。容器统计和各进程 RSS 之和不是主进程 RSS。

## 修复及成本验证

先减少每项工作产生的垃圾，再给收集器留出余量。下列均为本机 Go 1.26.4、darwin/arm64 的相同合成工作量，B/op 是每次操作的累计分配量，不是常驻。

| 工作 | 根因与修改 | 基线 → 修复（B/op） |
|---|---|---:|
| 探索图片，8 MiB 未命中 | 整张图读入可增长缓冲区再写缓存 → 限长流式写入未发布文件，验证实际 MIME / 长度后原子发布 | 17,076,553 → 44,760（−99.7%） |
| 4 规则、844 文件的一轮监听 | 分配剖面中，反复核对祖先目录身份主导；在原 no-follow 描述符上直接 `Fstat`，不创建通用 FileInfo，不缓存身份、不跳过历史检查 | 38,607,822 → 28,066,064（−27.3%） |
| BrushFlow，512 个 qB 任务 | 合并提取 hash、筛选和投影，省掉全量解码及筛选时的整批重编码，保留精确选集与顺序 | 列表 4,790,006 → 3,807,082；下载中 6,156,850 → 4,461,749 |
| 500 个未到期订阅、每个含 24 集数据 | 每 15 秒完整加载/解码窗口 → 逐行判定时间，只对到期候选完整解码，关闭读取游标后入队 | 4,397,517 → 1,221,184（−72.2%） |
| 完整健康检查 | 目录按原最新 500 项窗口，用已有索引统计，仅取最近 5 条待查详情；订阅摘要不再完整解码两遍 | 见进程实验 |

旧版本把 GOGC 强制设成 200、Go 内存软限制强制设成 256 MiB，并在两处定时调用 `FreeOSMemory`，其中调度器第一次 tick 以及繁忙时也强制 GC。修复后主程序无显式配置时使用 **GOGC=200 / GOMEMLIMIT=96MiB**。这个预算是给非 Go 内存留余量的起点，不是进程或容器硬上限。只有一个补充归还循环，默认 5 分钟：分配增量 <48 MiB 且空闲未归还 ≥16 MiB 才强制执行；否则交给运行时。`LIGHT_MEDIA_MEMORY_RECLAIM_INTERVAL=off` 关闭这一路强制回收。

旧 Compose 中显式的 `GOGC=50`、`GOMEMLIMIT=128MiB` 过去被主程序忽略，现在会生效。部署新版宜删除这两项旧覆盖以使用主程序默认值；若已有自定义参数请保留意图、核对实际值。未下调容器硬限额，也未停用插件或更改任务周期。主程序内置默认值不向外部插件进程传递。

## 本机复现与验证边界

[进程实验说明](../experiments/nas-memory/README.md)：真实 handler + 独立 SQLite 数据，4 条启用目录、840 条合成观察，逐响应核对全部计数和待查列表；safe 模式隔离外部系统，故没有生产插件负载。短程两版各 400 次，20 秒空闲收尾；长程跨越多次自然回收。RSS 使用系统采样，不代表内核瞬时最高值。

| 实验 | 基线 | 候选 |
|---|---:|---:|
| 400 次业务响应校验 | 400/400 | 400/400 |
| 400 次负载期 RSS 采样峰值 | 94.09 MiB | 79.86 MiB |
| 20 秒空闲 RSS 中位 | 94.09 MiB | 79.92 MiB |
| 400 次负载期 GC 增量 | 23 | 0 |

另对开发候选运行 4,000 次：全部业务断言通过，每 500 次约 1 次自然 GC，共 8 次。后四轮 RSS 中位为 85.53、85.58、85.67、85.73 MiB；随后 125 秒空闲中位 86.48 MiB，未退回刚启动的 64.95 MiB。它说明跨过多次回收后仍能维持较低的占用，不证明长期无泄漏。短程用最终检查构建（SHA-256 前缀 `6d1faa46bff1`），长程用加入轻量指标和目录 Fstat 前的候选（`387269dbf9c7`），基线 `f6ed96b77fff`。

Linux/amd64、CGO=0、modernconly 交叉构建通过；`cmd/server` 普通测试及关联 app 定向回归实际执行通过（非全库通过证明）。另在相同宿主和图片路径上做了竞态测试。`go vet ./internal/app` 仍报基线 `directory_priority.go:56: unreachable code`；已有订阅首检测试在旧代码上也出现下载器夹具失败，均未作为本轮成功项。

[目录轮询基准](../internal/app/directory_poll_alloc_test.go)、[订阅基准](../internal/app/subscription_poll_alloc_test.go)、[图片基准](../internal/app/catalog_explore_image_mime_test.go)、[下载器回放](../internal/app/plugin_original_downloader_enumeration_test.go) 可用 `go test ./internal/app -run '^$' -bench 'Benchmark(DirectoryPollWarm844|SubscriptionIdlePoll|ExploreImageColdMiss|BrushHostTaskEnumeration)$' -benchmem` 复现。此仓库的 app TestMain 需要已构建的 transfer-events fixture，设置 `OWNED_TRANSFER_EVENTS_BINARY` 指向其本机绝对路径，检查实际执行的用例而非只看退出码。

## 当时拟定的上线后测量方法

1. 按相同插件、下载器、目录配置覆盖至少一次完整刷流/订阅周期和一次图片/目录突发，并留足空闲时段，建议观察 24 小时。分开记录主进程和整容器，不能把短程合成测试当作生产验收。
2. 该版本的 `GET /healthz?view=memory` 检查数据库可用性后，只读取进程/Go 指标，不查询目录、订阅或插件。每 30–60 秒读 `data.process_memory.VmRSS`（当前）及 `VmHWM`（进程生命周期真实 RSS 峰值）、`data.memory.heap_live`（上次标记的活堆）、`total_alloc`、`num_gc`、`num_forced_gc`、`go_memory_limit` 和 `gogc`。计数仅在同一进程内做差，`Δnum_gc / 分钟` 与 `Δtotal_alloc / 秒` 要按同负载比较。若 `heap_live` 跨多次 GC 仍持续抬升，再比对不强制 GC 的堆剖面找引用持有方，而非继续收紧软限。
3. 对照 NAS 面板口径：若目标是整个容器，同时记录 `docker stats --no-stream light-media`（工作集近似），以及 cgroup v2 对应组的 `memory.current`、`memory.peak` 与 `memory.stat` 的 `anon/file`（含页缓存等，和 Docker 工作集口径不同）。Chromium/插件子进程和页缓存不受主进程 Go 预算控制。若高峰只在容器出现，先看这些项；不要把容器硬限直接设为 150 MB。

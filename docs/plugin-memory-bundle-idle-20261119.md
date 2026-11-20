# 插件内存优化：多调用 bundle 与空闲退出（2026-11-19）

## 目标

把 NAS 容器里插件宿主进程的 158–168 MB 压到 70–100 MB，同时不改变任何插件的调用语义、更新体验或调度行为。

## 两项机制

### 1. 多调用 bundle（`internal/app/plugin_bundle.go`）

插件仓库是单一 module（`github.com/landuo/light-media-plugins`，22 个插件共享一份 `go.mod`），设备上 20 个进程各自链接了同一份共享 SDK（jinja、regexp2、notice、sourcejson）。bundle 把它们编进一个可执行文件：

- 插件侧入口：插件仓库 `cmd/light-media-bundle/main.go`，`argv[1]` = 插件类名分派；`--manifest <Class>`、`--manifest-all`、`--identities` 供诊断与收据生成。
- 宿主侧：`resolvePluginImplementation` 在**逐类校验通过后**改写执行路径为 `Command{Path: bundle, Args: [类名], Dir: <逐类包目录>}`。
- 信任链（收据 schema 2）：`<packageRoot>/light-media-bundle.json`，逐类记录 `{version, identity}`。`identity` 是**握手语义身份摘要**——PluginID、Version、排序后的 Methods、排序后的 Capabilities 的 SHA256。宿主在选路前用逐类收据 manifest 计算同一摘要，完全一致才启用 bundle。
- **identity 的必要性（实测踩坑）**：runner 侧握手（`protocol.go:764`）除 PluginID/Version 外还比对 `sameSet(Methods)` 与 `sameSet(Capabilities)`。设备上 MergeMessageNotify 的收据 manifest 方法集与 bundle 构建源不同源，仅比对版本时表现为 `handshake_mismatch`、插件加载失败。语义身份不匹配现在**静默回退单件**，任何 manifest 漂移都不再破坏加载。
- 文件校验：收据 sha256/size 与 bundle 文件一致、可执行位存在，按 `(size, mtime)` 缓存一次全文件摘要校验。任何不匹配**静默回退单件二进制**。
- 回滚：删除 `light-media-bundle` 与 `light-media-bundle.json` 两个文件即完全回到原状；`LIGHT_MEDIA_PLUGIN_BUNDLE=off` 也可关闭。

### 2. 空闲退出 + 懒唤醒（`internal/app/plugin_idle_reaper.go`）

- 收割循环挂在 `App.Start`，每分钟检查一次；TTL 默认 30 分钟。
- 前置守卫（全部满足才退出会话）：`inflight==0`、无 submitted 租约、未 sealed、无任何 closing 状态、会话存活、非 stopping。
- 退出走正式 stop 车道（`StopWithHost` + stop 宿主回调，15 秒上限，失败即 kill），**不 seal 提交状态**——已加载对象、注册、事件、调度全部保留。
- 唤醒：下一次任何调用（UI/调度/事件）→ `ensureOriginalSessionReady` → `wakeMu` 串行 → 重建会话 → **重放 `initSerialized`（重新读取 manager 配置）** → 再执行业务调用。唤醒失败保留 `sessionNeedsInit`，下次调用重试而不是反复拉进程；唤醒成功后市场状态缓存失效，回到活体读取。
- 白名单 = init 幂等审计通过且**纯被动**的 7 类：MergeSiteSwitch、CustomHosts、PluginUnInstall、RemoteIdentifiers、AutoBackup、CloudflareSpeedTest、CategoryEditor。审计结论：2 个 SAFE（重放纯内存/同值幂等），5 个 CONDITIONAL（自带自愈 latch：`onlyonce=false`/`plugin_ids=[]`/`enabled=false` 在 init 成功路径写回 manager，重读配置即 no-op）。**排除项**：RemoveLink（init 启动常驻 inotify + `private_watch` 循环）；**MediaServerRefresh（2026-11-19 运营决策移出）——它是 `transfer.complete` 事件待命角色，技术上退出后事件也能唤醒它（事件链走 `callSession` 唤醒口，[plugin_original_event_call.go:144](internal/app/plugin_original_event_call.go#L144)），但待命角色被反复唤醒不划算，6.8 MB 常驻换零延迟响应**；ScrapeFileClean、SeedRescue、CleanInvalidSeed 等照常驻留。
- 配置：`LIGHT_MEDIA_PLUGIN_IDLE_TTL` > `<data>/plugin-idle-ttl` 文件 > 默认 30m；`off`/`0` 关闭。`LIGHT_MEDIA_PLUGIN_IDLE_EXTRA` 可增减白名单（`RemoveLink` 添加、`-MergeSiteSwitch` 移除）。
- **市场状态探测不打扰空闲记账**（实测发现的 TTL 饥饿）：市场刷新 30 分钟 ticker（`readOriginalMarketPluginStates`）对每个运行中插件调 `getState`——恰好把 lastUsed 每 30 分钟重置一次，30 分钟 TTL 永远到不了期，且会唤醒已退出的插件。修复：探测调用带 `originalMarketProbeKey` 上下文标记，保留 inflight 守卫但不刷 `lastUsed`；已退出的对象用 `rememberMarketState` 缓存的上次状态应答（唤醒后缓存失效），市场卡片显示不受影响。

## 设计要点

- **唤醒必须重读 manager 配置**（`initSerialized` 与启动 init 完全同路径），这是 CONDITIONAL 插件 latch 成立的前提；宿主不做快照重放。
- **retire 不持 admission 锁走 stop**：预约只能来自进行中的调用（inflight 门）或已存在的租约（leases 门），两个守卫已闭合竞态；持 admission 锁走 stop 会与"stop 处理器里再提交收尾任务"的插件（如 PluginUnInstall 的续跑链）自锁死。
- 显式 stop 仍然是终态：`sessionStarted && !sessionRetired` 拒绝重建；只有收割路径设置的 `sessionRetired` 允许唤醒。
- 唤醒串行用 `wakeMu`（叶锁，不与 sessionMu 反向嵌套）；`inflight` 在 `callSessionBound` 入口增减，同时刷新 `lastUsed`。

## 验证

- 单元：TTL 解析、文件回退、增减白名单、资格规则、`submittedRetireBlocked` 纯函数、身份摘要稳定性（方法/能力集无序等价、漂移必变）、收据构造/选路/回退。
- 进程级（真实 fixture worker）：`TestPluginIdleRetireRebuildsAndReplaysInit`（retire → 重建进程 nonce 变化 → init 重放 init_count==1 → 会话状态复位）；`TestPluginIdleRetireGuards`（inflight 拒绝、租约状态拒绝、二次 retire 观察、stopping 终态）；`TestPluginBundleResolverPrefersBundleForCoveredClass`（逐类校验后命令改写、在线会话不受影响）。全部通过 `-race`。
- 跨仓库摘要一致性：bundle `--identities` 与宿主 `pluginBundleIdentity` 对同一 manifest 产出相同摘要（用 7a847c1 构建实测）。
- 插件仓库：`cmd/light-media-bundle/main_test.go` 校验 22 类分派表完整、manifest 唯一、版本非空。
- 全量：`PLUGINS_ROOT=<固定 revision 的插件仓库工作树> node scripts/go-regression.mjs run`。63 个分片全绿；4 个 internal/app 分片共 30 个失败项**与本次改动无关**——同一失败集合在干净 HEAD 工作树上按相同子集复现（顺序依赖型存量失败，另测单跑时两棵树同样失败）。新增测试不引入任何新失败。

## 设备实测（FN EVO4-R，2026-11-19 凌晨）

| 阶段 | 插件进程 | 容器 cgroup | 主进程 |
|---|---|---|---|
| 0.5.9 基线（单件，无空闲退出） | 20 个，158–168 MB（RSS） | 349–368 MB | 165 MB |
| 0.5.11（主进程空闲回收上线） | 20 个，158–168 MB（RSS） | 186–263 MB（安静时段） | ~102 MB |
| **0.5.17（bundle + 空闲退出，最终）** | **12 个**（8 个白名单空闲退出） | **280–293 MB（BrushFlow 高负载时段）** | **95.9 MB** |

- 18 类经 bundle 运行（同一 32 MB 二进制，代码页 cgroup 只计一份）；IYUUAutoSeed（1.0.4 版本不匹配）与 MergeMessageNotify（收据 manifest 方法集与 bundle 构建源不同源，语义身份不匹配）按设计回退单件。
- 收割实测：默认 30 分钟 TTL，8 个白名单类在 idle 跨过 1800s 后被收割，进程 20→12；市场刷新周期（30 分钟）过后**保持退出**（探测走缓存，不再唤醒、不再重置空闲钟）。
- 排障过程实证：2 分钟 TTL 实验 8 类 4 分钟内全部退出（收割机正常）→ `/healthz` 探针曲线定位到市场 30 分钟 ticker 的 `getState` 全量触碰 → 修复后曲线连续增长不再被重置。
- 观测口径：`monitor proc-list` 的逐进程 RSS **把共享 bundle 代码页在每个进程重复计入**（12 进程标称 ~324 MB，其中每个 bundle 进程 RSS 里有同一份 ~25-30 MB 共享代码页）；容器真实计费以 cgroup 为准。
- 诊断通道（保留）：`/healthz` 的 `plugin_load_failure`（最近一次插件加载失败文本，≤600 字节）与 `plugin_idle`（收割机 ticks、TTL、白名单逐类 idle 秒数/retired/inflight）。
- 回滚：删 `<packageRoot>/light-media-bundle` 与 `.json` 两文件重启 → 全部回单件；删 `data/bin/light-media-server-0.5.1[5-7]` 重启 → 回退宿主版本；`LIGHT_MEDIA_PLUGIN_IDLE_TTL=off` 或写 `data/plugin-idle-ttl` 为 `off` → 关闭空闲退出。

## 运营决策（2026-11-19 上午）：停用 bundle，保留空闲退出

设备上 bundle 实测功能完整（18 类运行、身份/版本回退、收割不唤醒），但**进程监视器的显示口径**让每个插件进程都背上同一份 ~25–30 MB 共享代码页（`/proc` 的逐进程 RSS 重复计入共享页）：标称合计从 ~160 MB 涨到 ~510 MB，而真实 cgroup 只省 ~50–90 MB。日常盯进程列表的体验劣化三倍，收益撑不起显示成本——按运营者决策**删除两个 bundle 文件停用**，回到单件布局：

- 生效状态：20 个单件插件（4–16 MB 显示），空闲退出保持开启（TTL 30 分钟 → 12 个常驻，监视器稳态 ~105 MB 标称，**低于优化前基线**）；主进程空闲回收照常（89 MB）。
- 代码保留：宿主 bundle 选路与插件仓库 `cmd/light-media-bundle` 均保留在树中，未提交；bundle 文件不存在即自动禁用，无额外开关。
- 恢复路径：两个部署产物的本地副本保留在构建机（`light-media-bundle` + 收据 v2），重新上传到 `<packageRoot>/` 并重启即恢复方案 A。

## 预期账（以 cgroup 为准）

| 阶段 | 插件侧 | 容器稳态 |
|---|---|---|
| 基线（主进程回收已上线） | 158–168 MB | 186–263 MB |
| + bundle（20 份代码页 → 1 份） | ~95–125 MB | ~160–220 MB |
| + 空闲退出（8 个白名单类） | ~70–100 MB | ~150–190 MB |

唤醒代价：bundle 已在 page cache，无冷页调入；一次握手 + 一次 init 重放（白名单类的 init 重放为 no-op 级别）。

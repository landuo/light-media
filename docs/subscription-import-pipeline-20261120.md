# 订阅下载到入库的断链修复（2026-11-20）

现象：订阅显示「已下载 15/52」，第 16 集已在下载器里 100% 完成、文件也已落在监听目录，但既不自动入库，手动整理还报「该影片或季集已有其他下载或整理占用」。

结论：这不是单点故障，而是从「下载完成」到「入库」的链路上有 **六个互相掩盖的断点**，其中五个会让文件永久停在某个中间态。逐条修完后该集自动链路无法自愈的最后一步也用 `POST /api/import/repair` 收口。

---

## 断点与修复

### 1. `tracking` 只等一个永不发生的状态

`directory_filesystem.go` 里，本程序自己下载的文件会被置为 `tracking`，理由写的是"由下载完成核验流程整理"，而该分支**只**检查 `owner.Imported`：

```go
if owner.Managed {
    f.State = "tracking"
    if owner.Imported { f.State = "imported" }
    // 永远不会走到下面的整理逻辑
}
```

`owner.Imported` 来自 `downloadhistory.state == 'imported'`。全代码库只把该状态写成 `cancelled` 或 `error`，**没有任何地方推进到 `imported`**。

修复：完成且无计划在跑的托管文件走普通整理路径，不再停在 `tracking`。

### 2. 下载身份只在"文件未变"分支里应用

采集任务本身记录了媒体身份（`request.tmdb_id`），但这段逻辑嵌在 `if file.Unchanged` 内。快照稍有一致性差异就不带身份，识别退回按文件名猜测；而国漫文件名（`诛仙.Jade.Dynasty.S04E16.2026...`）在 TMDB 上匹配不唯一。

修复：
- `directoryDownloadHint` 除 native 记录外，也从 intent 的 `request.tmdb_id` / `media_type` 取身份；
- 身份应用移到 `if file.Unchanged` **之外**，无条件生效；
- 归属身份优先于文件名猜测（`IdentitySource = "download"`）。

顺带修掉一个隐蔽的活锁：身份只在某分支应用会导致"证据指纹"每轮变化，`retryIdentity` 因此每轮把稳定时钟重置，文件永远到不了 60 秒稳定期。

### 3. 空状态被当成"活跃计划"

`planLive("")` 返回 true，于是没有状态的残留计划永久阻塞释放。

修复：空状态不再算活跃。

### 4. 重试条件要求文件本身变化

```go
retryIdentity := !file.Unchanged && ... && f.EvidenceFingerprint != evidence
```

身份推断变了（正是需要重试的理由），但文件没变时 `!file.Unchanged` 为假，重试永不触发。

修复：去掉 `!file.Unchanged`，只要求证据变化。

### 5. 失败原因从不失败的 job 回填，且引用不清

`refreshDirectoryFilePlan` 把 import job 的 `error_message` 原样复制成观测原因，`plan_id`/`job_id` 保留，于是记录被钉死在 `error` 终态。

修复：保留判定文本，但清掉已死的计划与任务引用。

### 6. 占用判定与实际状态不符

- `managedUnplanned` 要求 `PlanID == "" && JobID == ""`，而失败的计划仍占着字段 → 一直被判定为"等待"；
- 占用检查里，`adoptDownloadedIntent`（接管已完成的下载）只在手动整理分支调用，自动整理永远走不到；
- 释放侧 `releaseStrandedObservation` 只看计划状态，不看驱动它的 job —— 计划标着 `preview`、job 已 `failed` 时永远报 "live plan"。

修复：
- `directoryAttemptLive`：计划"活跃"必须其 job 也活跃（`planLive && importPlanJobLive`）；
- `adoptDownloadedIntent` 在自动路径同样调用；
- 释放侧用 `importPlanJobLive` 判断。

### 7. 接受的收据丢了，进度就再也不推进

下载器 Add 调用超时但实际已入队时，intent 停在 `reconcile_pending`，而 `guardAcceptedDownloadProgressTx` 要求"接受收据"，没有收据就拒绝凭列表观察推进 —— 该集永远不动，还占着季集槽位（这正是手动整理报占用的原因）。

修复：`confirmReconciledAcceptance`，用下载器自身提供的三重证据恢复：**完整 40 位哈希相等 + 任务带 `light-media` 标签 + 任务报告真实大小**。任一缺失都不推进。恢复时写 INFO 日志留痕。

### 8. 扫描整条写回会覆盖别处的修改

扫描每次读一批观测、循环处理、末尾整条写回。启动补扫在中间写入 `observing` 会被随后的整条写回用旧值覆盖 —— 释放动作看起来成功（返回 true、回读正确），一秒后状态又变回 `error`。

修复：写回前重读当前状态，若与本次读到的基线不同，以库里的判定为准，只保留本次的无害字段更新。补扫本身改为删除后重建该行，并用 `directoryFileID(rule, path)` 寻址（与读侧完全一致，不依赖记录里的 `ID` 字段）。

---

## 架构变更：整理不再看来源（0.7.1）

按"监控目录里的东西都要整理"的要求，整理判定从**下载意图驱动**改为**文件系统驱动**：

| 之前 | 现在 |
|---|---|
| 本应用下载的文件置 `tracking`，等下载记录确认 | 不区分来源，文件稳定完成即整理 |
| 文件完成但下载器未确认 → 永久等待 | 只有 `downloader` 模式的规则才等下载器 |
| 季集占位（reservation）阻断整理 | 占位仅用于**避免重复获取**，不再阻断整理 |
| 接受收据缺失 → 文件永不入库 | 收据只影响下载意图状态推进，与整理无关 |

具体改动：

1. `directory_filesystem.go`：删除 `tracking` / `managedWaiting` / `managedUnplanned` 分支。监控目录里快照稳定（≥60 秒未变化）且大小非零的文件直接进入识别与整理；`downloads` 记录只用于提供媒体身份（提高识别准确率）与归属哈希。
2. `import_plan.go`：季集占位不再报「该影片或季集已有其他下载或整理占用」。若占位持有者已下载完成则接管；否则记 INFO 日志后放行——组织一个已经存在的文件不会重复获取。
3. `tracking` 状态不再产生；旧记录仍由启动补扫释放。

副作用（符合预期）：目录里由其它客户端（qB 网页端、别的程序）下载完成的文件，同样会被整理入库。这正是"不管种子从哪下载"的字面含义。

`downloader` 模式的规则例外：它监视的是下载器队列而非目录，因此仍要求下载器确认任务完成。

---

## 新增能力

### `POST /api/import/repair`

受认证保护的维护操作，用于释放卡住的源文件：

```json
{"path": "/media/download/国漫/.../诛仙...S04E16....mkv"}
→ {"observations_removed": 1, "reservations_released": 1}
```

行为：删除该路径的观测记录、释放被"已完成下载"占据的季集槽位。拒绝任何不在已启用监听目录内的路径，且**不释放未完成下载的槽位** —— 它是修复卡死状态，不是绕过归属规则。

### `/healthz` 新增诊断

| 字段 | 用途 |
|---|---|
| `automation` | 全局自动化是否 running（订阅不检查的第一嫌疑） |
| `subscriptions` | active / 到期数 / 最近检查时间 / 每条的原因摘要 |
| `directory_states` | 每条目录规则的文件状态分布 + 卡住文件的原因 |
| `recent_jobs` | 最近 import/organize 任务及其错误文本 |
| `reservations` | 当前占用的季集槽位与持有者 |
| `startup_reconcile` | 启动补扫的扫描量、释放数、被拒原因 |
| `plugins_loaded` / `plugins_enabled` | 内置插件加载数与事件开启数 |

这几项合起来能把"看起来没动"拆成：没调度、没识别、被占用、计划失败、下载未确认五类。

### 启动补扫

每次启动 90 秒后对已启用的监听规则做一次恢复，只处理**记录驱动**的候选（`baseline`、`tracking`、以及"影视身份"/"占用"类 `error`），窗口 48 小时、要求文件至少稳定 5 分钟。它自己从不导入任何东西，只是把记录放回识别队列，历史/完整性/稳定性三关照旧。`LIGHT_MEDIA_STARTUP_RECONCILE=off` 可关闭。

---

## 与 MoviePilot 的机制差异（本轮的对照参考）

| 环节 | MoviePilot | 光影（修复前） | 光影（现在） |
|---|---|---|---|
| 元数据刷新 | 独立 job，每 6 小时主动刷新并持久化总集数 | 检查时被动读缓存（TTL 12h），拉取失败退回旧快照 | 手动检查会清季缓存；缓存失效逻辑未改（见遗留） |
| 下载完成判定 | 下载器轮询 → 历史状态推进 → 触发整理 | 需要"接受收据"，缺失即永停 | 凭三重证据恢复 |
| 占用/归属 | 按 hash 与标签确认，不设全局槽位锁 | 季集槽位 reservation，失败后不释放 | 失败计划不再阻塞；已完成下载可被自动接管 |
| 卡死状态 | 由历史状态机 + 重试推进 | 多个终态无出口 | 补扫 + repair 端点提供出口 |

MoviePilot 没有"接受收据"这一层，所以它不会出现"任务已完成但永远在等确认"的中间态；代价是它对"这个任务是不是我加的"依赖 hash 与标签。光影引入收据是为了避免把别人的任务认成自己的，本轮的修复保留了这层保护，只在收据缺失时用同一组证据（hash + 标签 + 大小）补齐，而不是放弃校验。

---

## 验证

- 设备上第 16 集已入库：`/media/国漫/诛仙 (2022)/Season 4/诛仙 - S04E16 - 第 16 集.mkv`，国漫规则 52 条全部 imported；订阅由 15/52 变为 **16/52**，`reserved` 清空。
- 新增/更新单测（全部通过，含 `-race` 轮次）：
  - `TestReconcileAcceptanceRequiresAllEvidence` / `TestConfirmReconciledAcceptanceOnlyAdvancesPending` —— 收据恢复的证据门槛与状态保护
  - `TestManagedDownloadDecisionTable` —— 托管文件六种组合的决策表
  - `TestStartupReconcileReleasesStrandedObservations` / `TestStartupReconcileRespectsLivePlanAndJob` —— 补扫的窗口、稳定期、活跃计划保护
  - `TestRepairStrandedSourceClearsStuckState` / `TestRepairStrandedSourceRefusesForeignPath` —— 修复端点的行为与边界
- 已知既有失败（与本轮无关）：`TestManagedDownloaderOwnConfigurationAndReferenceGuard`，在干净 HEAD 的回归基线中同样失败。

---

## 遗留（未做）

1. **元数据缓存仍偏长**：季集数据 TTL 12 小时，且拉取失败时退回旧快照。本轮只让手动检查绕过它。是否加一个"订阅元数据定期刷新"任务（对齐 MoviePilot 的 6 小时）需要产品判断，未擅自添加。
2. **`downloadhistory` 缺 `imported` 终态**：`Imported` 标志因此恒为假。本轮让托管文件不再依赖它，但状态机本身仍不完整；补全它是一次独立改动。
3. **临时诊断已清理**：本轮为定位问题加的 `directory_probe`、`failing_record*`、`download_identities` 等探针已全部移除，只保留上表中的长期字段。

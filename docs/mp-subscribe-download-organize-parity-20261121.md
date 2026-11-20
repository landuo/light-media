# MoviePilot 订阅 → 下载 → 整理 → 入库 全链路梳理

目标：光影与 MP 1:1 对齐。本文先记录 MP 的实现事实（源码位置为准），再逐段对照光影现状与差异，
最后给出改造清单。**先理解，再改**。

---

## 一、调度：什么时候跑

MP 的调度器是 `internal/service/system_jobs.go`，每个任务显式声明周期或 cron。

| 任务 | ID | 周期 | 源码 | 作用 |
|---|---|---|---|---|
新增订阅搜索 | `new_subscribe_search` | **5 分钟** | system_jobs.go:207-210 | 只搜 `state="N"`（新建）的订阅；创建不足 60 秒的跳过 |
订阅元数据更新 | `subscribe_tmdb` | **6 小时** | system_jobs.go:239-243 | `RefreshAll()` 刷新所有订阅的媒体元数据 |
订阅刷新（spider，默认） | `subscribe_refresh` | **随机 32 次/天，仅在 7:00–23:00**，间隔 20–40 分钟 | system_jobs.go:287-293 + `randomSchedulerCrons(32, 7, 23, 20, 40)` (890-907) | `Refresh()` 遍历所有订阅做匹配下载 |
订阅刷新（rss） | `subscribe_refresh` | `SUBSCRIBE_RSS_INTERVAL`，默认 **30 分钟**，最小 5 | system_jobs.go:296-303 | 同上 |
下载文件整理 | `transfer` | **5 分钟** | system_jobs.go:315-320 | `Transfer.CheckCompleted()`：扫描已完成的下载并整理入库 |

关键点：

- **spider 模式是随机 cron**，不是固定间隔。`randomSchedulerCrons` 生成 32 个时间点，
  从当天 `beginHour`(7) 开始，每个点间隔 20–40 分钟随机，超过 23 点就停。
- **整理是独立任务**，5 分钟一次，与订阅检查完全解耦。
- **新增订阅单独一条快线**（5 分钟），不等下一次全量刷新。

### 光影现状

- 订阅检查：`scheduler`（jobs.go:615-640）每 15 秒 tick，内部 `time.Since(lastPoll) >= 30s`
  触发 `pollDownloads`；订阅检查自己有一套 30 分钟节奏。
- 整理：目录监听 + 启动补扫（`runDirectoryStartupReconcile`），不是独立 5 分钟任务。
- 没有"新增订阅快线"，也没有随机化。

### 差异

1. MP 的 spider 随机化光影没有 → 站点侧请求时间高度规律，容易被限流/风控。
2. MP 的整理是 5 分钟独立任务；光影是文件系统监听驱动（更快，但语义不同）。
3. MP 有"新建订阅 5 分钟内先搜一次"，光影要等下一轮。

---

## 二、搜索：用什么词去搜

`subscribe_service.go:1870-1912`

```go
func (s *SubscribeService) searchKeywordsWithMedia(sub, info) []string {
    if kw := strings.TrimSpace(sub.Keyword); kw != "" { return []string{kw} }
    names := subscribeNamesWithMedia(sub, info)   // [title, original_title, en_title, hk_title, tw_title, sg_title] + mediainfo.names
    if limit := s.maxSearchNameLimit(); limit != 0 { names = names[:limit] }   // MAX_SEARCH_NAME_LIMIT
    return names
}
```

搜索阶段（`searchSubscribedSitesWithMedia`, 2214-2258）：

```go
for _, kw := range s.searchKeywordsWithMedia(sub, info) {
    rs, _, err := s.searchService.SearchByTitleForSubscribe(kw, siteIDs, mediaType)
    // 按 DownloadURL/PageURL/Title 去重
    if len(results) > 0 && !multiple { break }     // SEARCH_MULTIPLE_NAME 关闭时，第一个有结果的名字即停
}
```

关键点：

- 名字顺序固定：**title → original_title → en_title → hk_title → tw_title → sg_title → aliases**。
- `en_title` 来自 TMDB 的 **translations**，不是 zh-CN 详情。
- `SEARCH_MULTIPLE_NAME`（默认关）决定是否继续搜后面的名字。
- `SearchIMDBID` 开启时用 IMDb ID 代替名字。

### 光影现状

`subscription_runtime.go` 的多名字搜索（本轮已加）：

```go
for _, query := range subscriptionSearchQueries(s) {   // title, original_title, aliases
    found, err := a.originalSubscriptionCandidates(ctx, request)
    // 合并去重后统一按优先级重排
}
```

### 差异

1. 光影此前只搜中文 title；现已改为多名字 + 合并重排。
2. **别名来源**：光影原先只读 zh-CN 的 `alternative_titles`，缺 `en_title`。
   已补：从 `translations` 取 `iso_639_1=en` 的 name，置于别名首位（`catalog/identities.go`）。
3. 光影没有 `SEARCH_MULTIPLE_NAME` 开关语义——目前**总是全搜**（比 MP 默认更宽）。
4. 光影没有 `MAX_SEARCH_NAME_LIMIT` 限制。
5. 光影没有 `SearchIMDBID` 的名字替换分支（有 `SourceSearchIMDb`，语义不同，需核对）。

---

## 三、匹配：这个种子是不是这个订阅

`subscribe_service.go:2298 downloadCandidates` → 每个候选：

```go
allowed, admitted := admitSubscribeCandidate(sub, meta.SeasonList, candidateEpisodes, result.PriOrder)
```

配套：

- `fullSeasonResource(seasonList, episodes, sub)`（subscribe_best_version.go:529-557）：季列表必须只有一个且等于 `sub.Season`；否则 false。
- `subscribeLefts`（2955-3000）：缺集计算**完全基于订阅自己的范围**：

```go
start := sub.StartEpisode; if start <= 0 { start = 1 }
if sub.TotalEpisode <= 0 || sub.TotalEpisode < start { 返回"目标范围未知" }
for episode := start; episode <= sub.TotalEpisode; episode++ {
    if 已下载 || 已记录 { continue }
    // 在途下载也算覆盖
    missing = append(missing, episode)
}
```

**MP 不读 TMDB 的季集列表来算缺集**，只用 `start_episode..total_episode` 减已下载/在途。

### 光影现状

- 缺集：`subscriptionCoverageLoadedAt`，`known` 来自 `a.season()`（现改为只用原生 TMDB），
  再按 `subscriptionTargetEpisodes`（start..total）过滤。
- 季号：`AliasSeasonNumbering` + 本轮新增"单季剧允许季号偏移、年份匹配优先"。
- 年份：`titleIdentity` 本轮修复为"前缀匹配后仍提取标题之后的年份"。

### 差异

1. 光影用季元数据参与缺集计算，MP 只用范围 → 光影更细（可区分"未播"），但一旦季数据异常就会算错。
   **建议**：保留 AiredEpisodes 的区分（这是光影优势），但缺集主体改按 MP 的 `start..total` 递减，
   季数据只用于"是否已播"和集号匹配。
2. MP 的 `fullSeasonResource` 要求季列表长度为 1 且等于订阅季；光影的整季包判定更复杂（多道闸）。
3. 光影的候选排序有 `priority_rules`（MP 也有），但配置为空 → 本轮已改为站点顺序兜底。

---

## 四、下载：怎么把种子交出去，以及下哪些文件

`subscribe_service.go:2764 addSubscribeTorrent` → `downloaderSvc.AddTorrentForLayout(AddTorrentOptions{...})`
→ qbittorrent.go:225-240：

```go
keep, drop := selectEpisodeFileIDs(files, opts.Episodes)
if len(keep) > 0 && len(drop) > 0 {
    q.SetFilePriority(hash, drop, qbFilePrioritySkipped)   // 不需要的文件设为跳过
}
q.startOrForceStart(hash)
```

关键点：

- **Add 时是 paused**（`paused: len(episodes) > 0`），先设文件优先级再启动。
- `selectEpisodeFileIDs` 按文件名解析集号，只保留目标集；**解析不出的文件不参与 keep/drop 计算**。
- 下载完不做二次校验，交给整理阶段。

### 光影现状

- `download.SourceSelectEpisodeDownload` + `source_post_add.go` 已经实现了同样的 paused→选文件→启动。
- 但只在 `SelectedEpisodes` 非空时触发；本轮已让 verified season pack 也设置 SelectedEpisodes。
- qB 的 `temp_path_enabled=false`，未完成文件直接写在目标目录并带 `.!qB` 后缀。

### 差异

1. 选文件触发条件已对齐（目标集非空即选）。
2. **`.!qB` 残留**：qB 删除任务时若 `deleteFiles=false` 或进程中断，未完成文件留在原目录，
   扩展名带 `.!qB`。光影的目录监听会把它当成普通文件（`isVideo` 判定看扩展名，`.!qB` 不是视频
   扩展名，所以被忽略）——但文件会永久占据空间且用户可见。
   **需要**：整理前清理 `*.!qB`，或识别为未完成并跳过 + 定期回收。
3. 光影没有"下载完成后立刻整理"的 5 分钟节拍，依赖监听。

---

## 五、整理与入库

MP：`transfer` 任务每 5 分钟调 `Transfer.CheckCompleted()`：

1. 列出下载器中已完成的种子（progress == 1）。
2. 对每个种子取文件列表，识别（`Recognize`）媒体身份与季集。
3. 按订阅/目录规则决定目标路径。
4. 硬链接（默认）/复制/移动，重命名为 `标题 (年份) - SxxEyy - 集名.ext`。
5. 写入历史记录，通知媒体服务器刷新。

### 光影现状

- 目录监听：`pollDirectoryFiles` + 启动补扫，状态机 `baseline/observing/downloading/tracking/queued/imported/needs_review/error`。
- 整理：`runDirectoryOrganizeFile` → `executeImport`，落库为 `asset_slots` + 文件重命名。
- 本轮修复：失败/成功都回写观测记录；孤儿 `queued` 记录会被释放。

### 差异

1. MP 以"下载器任务完成"为触发；光影以"文件出现在监控目录"为触发。后者更通用（不依赖下载器），
   但会看到 `.!qB` 这类中间态。
2. MP 的识别用 `releaemeta`；光影用 `media.Parse`（本轮已补 `S01 E43` 空格格式）。
3. 入库后刷新媒体服务器：MP 有显式刷新；光影需核对是否通知 Emby/Jellyfin。

---

## 六、改造清单（按影响排序）

1. **[高] `.!qB` 残留治理**：整理前跳过并回收未完成文件；删除任务时默认 `deleteFiles=true`。
2. **[高] 订阅调度对齐**：spider 随机 cron（32 次/7–23 点/20–40 分钟）+ 新建订阅 5 分钟快线。
3. **[高] 整理节拍**：增加独立 5 分钟 `CheckCompleted` 等价任务（下载器完成 → 触发整理），
   与目录监听并存。
4. **[中] 缺集计算**：主体改为 `start..total` 递减已下载/在途，季数据仅用于"已播"判定。
5. **[中] 搜索名序**：`en_title` 已补；补 `MAX_SEARCH_NAME_LIMIT` 与 `SEARCH_MULTIPLE_NAME` 语义。
6. **[中] `SearchIMDBID`**：核对光影 `SourceSearchIMDb` 是否等价。
7. **[低] 整理识别**：补齐 `releaemeta` 常用格式（中文"第N集"、纯数字、`S01 E43` 已支持）。

---

## 七、本轮已完成的对照修复（供核对）

| 项 | 之前 | 现在 |
|---|---|---|
搜索名字 | 仅中文 title | title + original_title + aliases（含 translations 的 en 名） |
别名来源 | 只读 alternative_titles | + translations(en) |
`S01 E43` | 解析失败 | 宽松回退解析 |
年份 | 前缀匹配后丢弃 | 保留为身份证据（标题内年份不算） |
季号偏移 | 一律拒绝 | 年份匹配时接受；年份差 ≥2 拒绝 |
整季包文件校验 | 多层拒绝 | 只拒"能解析出集号但不含目标集" |
候选排序 | 无规则组时不排序 | 站点顺序兜底 + 合并后统一重排 |
意图生命周期 | 失败后仍占位 | 失败即作废并释放预留 |

---

（待补：MP 的 `Transfer.CheckCompleted` 细节、媒体服务器刷新、订阅完成判定）

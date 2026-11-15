# 剧集组整理：第七阶段实现与实际验证

## 已贯通的链路

- [共用弹窗](<../web/src/components/ReorganizeDialog.vue>)对电视剧提供真实剧集组查询、默认空项、季/集数量、加载和错误状态。切换媒体编号或类型清空旧组，400ms 防抖及过期请求隔离仅作用于组查询。单条与批量仍在原页面使用同一弹窗。
- [认证查询与映射](<../internal/app/organize_episode_groups.go>)验证组属于当前 TMDB 电视剧；[手动入口](<../internal/app/organize_manual.go>)、[计划构建](<../internal/app/import_plan.go>)保存 `episode_group`，用原版完整 MediaInfo 和同步 `tmdb_episodes` 链识别。
- 组内集号按提供方数组位置从 1 开始，不按可选 `order` 字段重排。`PlanFile.Season/Episodes` 保持标准库存身份，`GroupNumbering` 单独保存展示编号。命名、原版事件、字幕及历史使用展示编号；资产、预留与订阅完成判断仍用标准编号。紧凑媒体 `Seasons` 保留标准季列表。
- [历史继承](<../internal/app/organize_history_batch.go>)在 `from_history=true` 时继承已知组；未知组不覆盖本次表单。当前历史继续同源稳定 ID、最新实际提交覆盖，预览只生成预览计划。
- [原版刮削消费](<../internal/app/plugin_original_metadata_scrape.go>)的 TV 文件重新识别现在保留组；原生 NFO 直接读取组季，保留真实 episode TMDB ID，写入请求的展示季集号。集缩略图使用组数据；季海报仍按 MP 查询普通季，没有擅自改用其他图。

## 实际发现并修复的问题

最初“计划/硬链接/历史”测试通过后，新增实际 NFO 测试发现两处遗漏：无插件的 TV 文件重新识别丢失组，原生 NFO 仍查询普通季。真实 SDK 识别替代曾掩盖第一处问题。因此同时保留无插件与实际 SDK 两条端到端测试，不以结构体字段正确替代真实旁车输出。

[原版 TMDB 组季实现](</Users/accidia/projects/MoviePilot/app/modules/themoviedb/tmdbapi.py#L1393-L1427>)只改写组内 `episode_number`，保留行上的标准 `season_number`；[原版刮削器](</Users/accidia/projects/MoviePilot/app/modules/themoviedb/scraper.py#L35-L82>)使用传入的展示季集号写 NFO。实现分别保留这两层含义。

## Go 与实际 SDK 验证

- [原生执行/历史测试](<../internal/app/organize_episode_groups_test.go>)：真实认证查询、两文件预览、硬链接、标准资产槽、展示历史、反序批量历史复用及再次执行后的稳定 ID；另测同一标准季的多集文件和缺失/外来组。
- [NFO/SDK 测试](<../internal/app/organize_episode_groups_sdk_test.go>)：真实主文件、字幕、NFO；命名、拦截、完成、字幕和刮削事件收到组编号；NFO 为 S01E01、标题 `Canonical sixteen`、TMDB episode ID 4016，内部资产为 S04E16。
- 剧集组专项最终通过，4.216s（包含真实 episode ID 的 NFO 断言）；历史、手动、规则/自动推荐、原版元数据及实际 SDK 合并回归通过，47.435s。宽测试首次运行缺少额外 SDK fixture 环境变量，补建并提供真实 input/downloader-control workers 后重跑通过；未绕过失败测试。
- 全仓 `go test ./... -run '^$'` 编译通过。这是编译检查，不是全 Go 业务测试全部通过。
- 前端由本轮 UI 实现验证：SFC 24/24、完整 635/635、类型检查和构建通过，详见[UI 报告](<history-mp-episode-group-fallback-ui-20261115.md>)。

## 独占真实浏览器与文件系统

使用[可复现 Go fixture](<../internal/app/organize_episode_groups_browser_test.go>)启动实际 App.Handler、真实导入/普通任务 worker 和构建后的前端。只绑定 loopback；不启动定时任务、监控、下载器或旧系统。合成提供方请求使用内存响应，不访问真实 TMDB。

操作记录见[浏览器 JSON](<../testdata/history-mp-alignment-20261115/episode-group-browser-evidence.json>)，落地验证见[文件系统 JSON](<../testdata/history-mp-alignment-20261115/episode-group-filesystem-evidence.json>)：

1. 勾选两条预置失败历史，在批量弹窗选择 TV/123 和“合并篇章”。组查询真实返回 2 季、4 集。
2. 非空但全部停用的规则表触发真实自动推荐，生成 `Sample.S01E{ep}.mkv`，多数派 2/2、原生验证 2、低置信度、放宽体积门槛；其他表单值保留。
3. fixture 最初没有启用目录规则，空目的路径的第一次预览得到明确目标错误，表单和历史保留。随后实际输入独占目标目录并勾选刮削；第二次预览两条均 ready，历史完整响应仍不变。fixture 保留此配置，便于复现错误留稿及路径独立行为。
4. 在 390×844 移动端点击“立即整理”，真实执行两个硬链接并生成两个 NFO；成功关闭弹窗，两个原历史 ID 保持不变，状态更新为 success。
5. 切回桌面，从单条历史菜单打开同一弹窗；表单类型/编号仍为自动/空，仅勾选复用历史识别信息。实际预览继承已保存组并得到相同 S01E01 目标；历史仍不变。

| 来源 | 实际目标/NFO | 内部库存 | 物理结果 |
|---|---|---|---|
| 第 1 个合成文件 | S01E01，Canonical sixteen，episode ID 4016 | S04E16 | 同 inode 硬链接，源保留，scraped |
| 第 2 个合成文件 | S01E02，Canonical special，episode ID 2 | S00E02 | 同 inode 硬链接，源保留，scraped |

当前历史 2 条，内部执行尝试 4 条（2 条预置失败 + 2 次真实成功），导入成功任务 2 个、刮削成功任务 2 个。页面捕获的 `window.error` 为 0，移动端文档宽度 390，无页面横向溢出；历史表本身保留横向滚动。

截图：[桌面组选择/推荐](<../testdata/history-mp-alignment-20261115/episode-group-auto-desktop.png>)、[移动端表单](<../testdata/history-mp-alignment-20261115/episode-group-auto-mobile.png>)、[成功历史](<../testdata/history-mp-alignment-20261115/episode-group-history-desktop.png>)、[单条历史继承预览](<../testdata/history-mp-alignment-20261115/episode-group-inherit-desktop.png>)。

浏览器页签、控制器、独占 Chrome 和 fixture 服务均已停止；服务正常退出且无 fixture 请求越界失败。临时数据库、合成媒体/NFO、Chrome profile 与浏览器服务二进制已清理，18081/9224 无监听。实际 SDK workers 暂留供持续目标的后续回归复用，最终交付时统一回收。

## 第七阶段结束时的剩余范围

跨季单文件和蓝光目录已在后续[第八阶段](<history-mp-cross-season-20261115.md>)实现并验证；下面保留本阶段结束时的边界。

- 一个物理多集文件映射到不同标准季时仍返回 `EPISODE_GROUP_CROSS_SEASON`；当前单季资产模型尚未覆盖此情况。不同物理文件各自映射到不同标准季已实际验证，不应混淆两者。
- 蓝光/目录材料的剧集组编号与库存投影还需专项核对，未计为已验证。
- 原生 move/softlink、媒体来源选择和 provider-native 历史继承仍在[剩余清单](<history-mp-remaining-options-20261115.md>)。
- 本阶段是合成提供方 + 真实浏览器、API、队列、文件系统和 SDK 验证；不是生产 NAS/真实影视库验收，不宣布全部 MP 选项 1:1 完成。

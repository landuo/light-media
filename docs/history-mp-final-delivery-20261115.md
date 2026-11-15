# 整理历史对齐 MoviePilot：最终交付说明（2026-11-15）

## 1. 范围与基线

- 目标：按 MoviePilot 的实现逻辑与交互重构 light-media 的整理历史，包括自动整理、手动整理、单条/多条历史重整、列表加载与整理记录保存。
- 远程基线：`origin/main` = `8307f90e3d8556588d2426dc15cf1159c85cea69`（其中 BrushFlow qBittorrent 5 下载列表登录修复已直接应用到工作树）；此前阶段基于 `4a222f0fa0c06848e7924e6614654a38e999fb19`。
- 开发分支：`codex/history-mp-alignment-20261115`，复用原主工作树；MoviePilot 只读。**未提交、未推送、未部署**。
- 所有验证使用独占临时目录、合成媒体、真实 Go 服务/SDK worker/文件系统；未操作生产媒体、既有种子或 NAS 配置。

## 2. 已交付能力（按 MoviePilot 语义）

### 整理入口与执行

| 能力 | 实现要点 |
| --- | --- |
| 下载完成自动整理 | 下载完成监听 → 原 `organize_file` → `import` 队列；目录规则方式、目标、刮削与历史继承一致；未完成/混合任务不排队，停用与 none 不产生任务。 |
| 目录监控自动整理 | 监控目录扫描、盘根（BluRay）单候选逐叶发布、重复巡检由成功历史门控；普通视频/字幕各自链接与历史，盘根一条历史。 |
| 手动整理（文件/目录） | 共用弹窗：目的存储、方式、目的路径、类型/TMDB 或来源编号、季集/定位/偏移、Part、最小大小、类型分类、刮削、可选预览；立即执行显示进度，成功关闭并刷新，失败保留表单。 |
| 单条历史重整 | 按原历史恢复身份/季集/来源，允许改编目标/方式；最新实际提交覆盖同源历史的展示字段，稳定历史 ID 不变。 |
| 多条历史重整 | 明确选中 `history_ids` 逐条复用单条逻辑；缺失/不可用条目独立失败，其余继续；后台队列与立即执行同源。 |
| 整理队列 | 3 秒刷新、按媒体合组、逐文件结果、精确取消执行 ID、轮询不覆盖失败选中。 |
| 历史列表 | 搜索（显式 `* ?` 与普通分词）、状态筛选、排序、分页、标题分组；默认 50 条与 25/50/100/500/1000；直接读取写时保存的精简快照。 |
| 历史保存 | 同事务写时投影；同源稳定 ID、旧尝试保留、最新实际提交替换展示；预览不写执行历史；迟到旧结果不覆盖新提交。 |
| 删除 | 记录/源/媒体库/两端四种范围，一次确认；真实删除结果计数，失败保留选中；归属、快照、做种检查在服务端。 |
| 整理方式 | copy/hardlink/move/softlink/auto；同盘快速移动、跨盘复制回退；复制采用源 mtime/atime 与目标默认创建策略。 |
| 识别来源 | 可空持久 `recognition_source`（未显式保存继承启动值），TheMovieDb/豆瓣/Bangumi/AniList；手动与自动目录、订阅刷新统一；非 TMDB 以来源身份参与计划，不冒用 TMDB。 |
| 剧集组/集数定位 | TMDB 剧集组选择、跨标准季投影、蓝光目录；独立集数规则与自动推导；季集、Part、偏移与字幕/NFO 对齐。 |
| 软链接生命周期 | 文件/蓝光目录/外部 transfer/source actor；entry/referent 双身份、断链与新 inode 保护、库旁 NFO、删除/重整/库存/订阅完成/V3 覆盖。 |
| 既有目录 | 非受管普通/共享根四种 native 方式；同受管目录追加复用资产；同资产与跨资产（快照一致）成员替换并作废旧资产；外部整树软链向已有目标只认领新增链接。 |

### 交互与数据边界

- 共用弹窗保留原版字段顺序与独立开关；预览位于内容底部，操作栏固定；不新增必选步骤或额外确认矩阵。
- 全局默认整理方式固定硬链接；目录规则可显式选择 copy/move/softlink/auto。
- 文件管理仅对精确 owned 链接显示软链接/断链标识与读取/识别/继续整理；破坏性操作仍走历史归属校验。

## 3. 最终验证（当前代码）

| 项目 | 结果 |
| --- | --- |
| 扩大 Go 回归（历史/手动/自动/目录/共享/合并/接管/软链/来源/插件/删除/订阅/文件管理） | [357.709s PASS](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round27-final-integration2.log>) |
| 全仓编译 `go test ./... -run '^$'` | PASS（[日志](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round27-compile.log>)） |
| 裸 `go test ./...` | PASS（[日志](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round27-bare-go-test.log>)）；未提供 `OWNED_*_BINARY` 时 `internal/app` 整包跳过并提示夹具命令，不把缺失夹具报成业务失败 |
| Linux amd64 交叉编译 | PASS（产物已删除，未执行 Linux 运行） |
| `git diff --check` | PASS |
| 公共设置脱敏 | `GET /api/settings` 不再返回 FNOS 密码或 TMDB 凭据，`secret_state` 仍报告已配置；`TestFNOSSettings*` 通过 |
| 前端测试 | **641/641 pass, 0 fail**（[日志](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round27-web-tests.log>)） |
| 前端构建 | 成功（[日志](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round27-web-build.log>)） |
| 真实 Chrome 最终联调 | 历史列表加载 2 条；单条字幕历史预览→立即整理→成功关闭并刷新为最新目标；多选出现「批量删除（2）」「批量重新整理（2）」；[界面](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round27-history-final.png>) |

真实 Chrome 使用独占实例（实际 App/SQLite/SDK/文件系统、合成登录），结束后关闭页面与监听；用户其余 Chrome 页面未操作。

## 4. 未验证边界（明确不宣称）

- 未在生产下载器、真实站点 RSS、NAS 或云盘执行写入验收；网络协议与存储为合成/回放样本。
- Linux 仅交叉编译，未在 Linux 主机运行测试。
- MoviePilot 参考仓库只读；未把本项目的内部归属索引、队列 ID、收据结构描述为与原版数据结构相同；对齐的是行为与交互语义。
- 未提交、未推送、未部署；工作树保留全部改动与证据。

## 5. 复核入口

- 总进度与阶段索引：[history-mp-alignment-20261115.md](<history-mp-alignment-20261115.md>)
- 分阶段实现报告：`docs/history-mp-*.md`
- 结构化证据：`testdata/history-mp-alignment-20261115/*.json`
- 测试命令与环境变量见各阶段报告；worker 复用 `work/history-mp-alignment-20261115/workers/`，未累积版本副本。

## 6. 测试环境约定

`internal/app` 的集成用例需要独立 SDK 夹具进程。未设置 `OWNED_*_BINARY` 时包级 `TestMain` 会跳过整个包并给出提示；权威业务回归命令需显式提供 workers，例如：

```bash
TMPDIR="$PWD/work/history-mp-alignment-20261115/tmp" OWNED_TRANSFER_EVENTS_BINARY="$PWD/work/history-mp-alignment-20261115/workers/transfer-events" OWNED_TRANSFER_EVENT_OVERRIDE_BINARY="$PWD/work/history-mp-alignment-20261115/workers/transfer-event-override" OWNED_MODULE_OVERRIDE_BINARY="$PWD/work/history-mp-alignment-20261115/workers/module-override" OWNED_HOST_SERVICES_FIXTURE_BINARY="$PWD/work/history-mp-alignment-20261115/workers/host-services" go test ./internal/app -run '^(TestTransferHistory|TestHistory|TestManualOrganize|TestDirectory|TestSharedDirectory|TestImportDirectoryMerge|TestBrowser|TestFileManager|TestOriginalTransfer|TestImport)' -count=1
```

`internal/plugins/artifacts` 的 `TestMain` 在包目录下分配临时根，避免 macOS `TMPDIR` 位于 `/var` 符号链接导致的安全检查误报。

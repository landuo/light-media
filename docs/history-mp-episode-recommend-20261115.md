# 历史 MP 对齐第六阶段：集数定位规则推荐

## 本轮交付与入口

- 设置 → 规则 → **集数定位规则**：新增、启用/停用、名称、正则、最小文件大小、拖动排序自动保存、删除、整体保存。新建规则默认最小大小 500 MB；服务端规则字段默认值沿用 MP（enabled=true、order=0、min_file_size_mb=0）。
- 文件管理/整理历史共用手动整理弹窗 → 类型「电视剧」→ 集数定位输入末端的 Vuetify 推荐按钮及 tooltip。
- 推荐成功立即覆盖 `episode_format`，只显示规则、样本和命中数量，不新增确认，不改目的存储/方式/路径、类型、编号、季、集范围、偏移、Part、最小文件大小、分类或刮削。
- 失败保留已有输入，字段提示给出实际原因；规则未配置或全停用时禁用按钮并给出设置提示。用户在请求期间编辑定位输入、切换来源或类型时，过期结果不覆盖新值。

## 原版证据与字段不兼容事实

只读核查源码，未执行 MoviePilot 服务：

| 项目 | 源码证据 |
| --- | --- |
| 前端单文件/目录、多文件同目录条件、覆盖原定位、响应字段 | MoviePilot-Frontend `src/components/dialog/ReorganizeDialog.vue:740-910` |
| 末端 IconBtn/VTooltip | 同文件 `1452-1482` |
| 规则设置字段/默认值、保存、排序 | MoviePilot-Frontend `src/views/system/WordsView.vue:14-59,174-225,354-453` |
| POST 契约 | MoviePilot `app/api/endpoints/transfer.py:584-610` |
| 规则读取及样本选择 | MoviePilot `app/chain/transfer.py:2085-2236` |
| 配置规则匹配、模板和消费校验 | MoviePilot `app/helper/format.py:276-325,1291-1484` |
| 附加样本兼容验证 | 同文件 `543-905,1234-1288` |

原版规则来自独立 `EpisodeFormatRuleTable`，不是 `CustomIdentifiers`。LM 原有识别词是加密的文本替换/删除/偏移语义，没有 name/enabled/order/pattern/min_file_size_mb 表字段，不能直接当定位规则使用。本轮新增独立加密记录 `organize_episode_rules/main`，版本核对失败不覆盖当前配置；不修改现有识别词。

原始文件名直接参与规则匹配，命名捕获组的 Unicode 字符位置转换成 `{组名}`，字面花括号转义。复用识别词已有的 `internal/pyregex` 编译器，支持当前编译器实际支持的 Python/PCRE 风格命名捕获、lookaround；使用既有 `organizeEpisodeTemplate`、`organizeEpisodeRange` 校验最终手动整理的消费结果。不宣称支持所有 Python/PCRE 扩展。

附加样本验证复用 LM 当前全局识别词、元信息配置、文件扩展名配置及已移植的 `MetaInfoParser.ParsePath` / `legacyanime.Parse`，并按原版显式 token 定位、原生冲突处理、特典/00/非纯整集过滤。没有把“全部文件都必须命中正则”作为自创门槛：未识别出有效集数的字幕可以跳过；已识别出有效集数的字幕/音频必须通过兼容验证；视频兼容检测的 100 MB 门槛与规则自己的最小大小是不同输入。

源码快照：

```text
MoviePilot HEAD: 8a83efeacd3e26b82be495e17d50146e31c96a60
MoviePilot-Frontend HEAD: 2829e3b08214348f05ff2f5beae644882eccad58
SHA256 app/helper/format.py:
 d1fec01f9d22aca4f035fe2f8c66cb7a8f1faf331aac503672bcff31b07bca6d
SHA256 app/chain/transfer.py:
 1e42c4e69cb6294afe36e3c4aa4bb40cb9356aa017f2e8e9c91b9c928b140b37
SHA256 ReorganizeDialog.vue:
 7168cc64bdfb3b5c01be5b571d67e95002ab5ece8242e26115fe73810a9534fd
SHA256 WordsView.vue:
 235a98f5f37832d8b49f545982d0e1449a6217b7e90b4982f25190f0b38c8d71
```

## 支持条件与采样

| 输入 | 实际范围 |
| --- | --- |
| 单个普通文件 | 核验所选文件后，读取其直接父目录，不递归 |
| 单个目录 | 读取本目录，不递归 |
| 显式多文件 | 全部必须为同目录普通文件；去重、保留请求顺序，只使用所选项 |
| 单条历史 | 复用当前历史 source resolver；成功 move 读 target，其他状态/方式读 source，然后采用单文件/目录语义 |
| 多条历史 | 解析当前各条来源后要求同目录普通文件；保留请求顺序，只使用选中项 |

复用文件授权根、`browserStat` / `importDir` 的无链接检查和现有视频/字幕/音频扩展名分类器。跳过隐藏/回收目录、非媒体扩展名及目录里的符号链接；显式选择符号链接、失效文件、跨目录、混选目录有明确错误。目录扫描为确定性文件名顺序；显式选择保留首个命中样本的选择顺序。只读文件元信息，不创建整理任务，不更新历史，不访问下载器。

启用规则按 order 稳定排序；逐条匹配，按首个匹配样本生成模板，校验该规则匹配的样本和原版认可的附加兼容样本，通过即返回该规则。`rule_index` 是排序后的启用规则零基索引。

## 实际接口契约

沿用 LM 已认证 HTTP 分发，非 MP 的 `success` 包装；成功外层为 `data` 和 `request_id`，错误外层为 `error` 和 `request_id`，业务错误返回非 2xx。

```http
GET /api/files/episode-format/rules
PUT /api/files/episode-format/rules
POST /api/files/episode-format/recommend
```

规则保存请求/读取 data：

```json
{
  "revision": 0,
  "rules": [{
    "name": "显式集号",
    "enabled": true,
    "order": 1,
    "pattern": "(?P<title>.+)\\.E(?P<ep>\\d+)\\.(?P<ext>.+)",
    "min_file_size_mb": 0
  }]
}
```

保存成功返回递增的 revision 和实际 rules。推荐请求二选一，不混传：

```json
{"paths":["/allowed/Show.E01.mkv","/allowed/Show.E02.mkv"]}
```

```json
{"history_ids":["selected-history-file-id"]}
```

推荐成功 data 示例：

```json
{
  "rule_name":"显式集号",
  "rule_index":0,
  "pattern":"(?P<title>.+)\\.E(?P<ep>\\d+)\\.(?P<ext>.+)",
  "episode_format":"{title}.E{ep}.{ext}",
  "sample_file":"Show.E01.mkv",
  "min_file_size_mb":0,
  "sample_count":2,
  "matched_count":2,
  "message":"已根据配置规则生成并验证集数定位模板"
}
```

主要错误码：`EPISODE_RULES_REQUIRED`、`INVALID_EPISODE_RULE`、`EPISODE_RULE_REVISION_CONFLICT`、`INVALID_EPISODE_SELECTION`、`INVALID_SOURCE`、`EPISODE_SOURCE_UNAVAILABLE`、`EPISODE_HISTORY_UNAVAILABLE`、`EPISODE_NO_SAMPLES`、`EPISODE_SAMPLE_LIMIT`、`EPISODE_RULE_LIMIT`、`EPISODE_RULE_NO_MATCH`。

## 验证与尚未完成项

已验证（独占临时目录、合成文件，不是 NAS/真实下载器验收）：

- Go 实际文件行为：规则加密持久化与版本冲突、原始 Unicode/花括号模板、PCRE/Python命名捕获、范围消费、单文件/目录/显式多文件、隐藏项/非递归、授权及链接、大小过滤与字幕音频、禁用/顺序、无匹配不猜、历史成功移动与失败/复制来源、同目录历史批量及选中样本顺序。
- 经真实 `App.Handler()` 的认证 HTTP 测试，验证未登录 401、缺规则 400、规则保存及成功 data 的模板/样本字段，不以 HTTP 200 单独计成功。
- 源码导出样本：[episode-format-rule-oracle.json](../testdata/history-mp-alignment-20261115/episode-format-rule-oracle.json)。其中区间案例来自原版已存在测试；另有命名组、字面转义、重叠组和无匹配样本。
- SFC 编译后实际渲染事件测试：配置新增/编辑/排序/删除/失败草稿/版本，推荐回填仅改变 episode_format、历史只发 ID、多选条件、未配置提示、请求失败与过期响应。
- `npm run check` 通过；`npm test` **631/631** 通过；`npm run build` 通过。构建保留 Vite 既有 JSON import/future configLoader 与 chunk-size 提示，不作为失败。
- 最终 Go 推荐及相邻回归通过（2.087s），`go build ./cmd/server` 通过，临时构建二进制已删除，`git diff --check` 通过；全部后台 job 已收集，无本任务遗留服务进程。
- 工作日志：[Go 测试](../work/episode-recommend-go-tests.log)、[前端全测试](../work/episode-recommend-web-tests.log)、[前端构建](../work/episode-recommend-web-build.log)（work 为 Git 忽略目录）。

最终 Go 命令：

```sh
go test ./internal/app -run '^(TestEpisodeRecommend|TestOriginalOrganizeNumberingOptions|TestFileBrowserNavigationContainmentSortAndScan|TestFileManagerTVBatchNumberingPreviewPreservesExplicitEpisodesWithOptionalMetadata|TestHistoryMPMovedFileRecognizeCapability)' -count=1
go build -o work/episode-recommend-server ./cmd/server
rm work/episode-recommend-server
```

明确未完成：

1. 原版 `format.py:327-538,907-1232` 在无规则命中后的自动多数派/差异模板推导尚未移植。当前返回明确错误，设置提示也标明“自动推导尚未实现”，不使用自创数字猜测替代。此轮交付为配置规则端到端功能，不声明原版完整智能分析 1:1。
2. 子任务阶段仅含SFC与本地合成HTTP/文件测试；父任务随后完成独占GUI验证，单独记录如下。没有NAS、真实下载器或旧服务写入。
3. 没有提交或推送。父任务并行修改的执行失败事实、分类投影及 transfer context 不在本功能改动范围。

## 父任务真实GUI补充验证

使用原工作树最新构建资源、真实App.Handler、独占回环服务及独占Chrome profile；3条预置合成历史，不是实际生产整理记录。

- 经[设置页控件](<../testdata/history-mp-alignment-20261115/episode-rule-settings.png>)保存规则，读取API确认revision=1、enabled=true、min_file_size_mb=0。
- [历史批量推荐](<../testdata/history-mp-alignment-20261115/episode-history-recommend.png>)选中E03、E02，返回首样本E03和命中2/2，生成`Sample.S01E{ep}.mkv`。复制方式、目的路径、分类/刮削开关不变。
- [移动端](<../testdata/history-mp-alignment-20261115/episode-history-recommend-mobile.png>)390×844，dialog宽390、页面scrollWidth390，固定底栏可见。
- 暂移走选中的自建源，实际推荐返回HTTP400并显示源失效原因；手改`User-{ep}.mkv`及复制方式、目的路径保留。源已复原。[详细证据](<../testdata/history-mp-alignment-20261115/episode-recommend-browser-evidence.json>)记录表单和计划/任务/历史均仍为3。
- 真实浏览器发现移动端长tooltip的auto宽度与connected定位相互影响：11次ResizeObserver错误，宽度按12px增加。只为新增tooltip设置专用content-class，并用`.v-tooltip > .v-overlay__content.episode-recommend-tooltip`覆盖默认`width:auto`为`max-content`；首次仅类选择器被Vuetify更高特异性覆盖，实测失败后已修正。[前后证据](<../testdata/history-mp-alignment-20261115/episode-recommend-resize-after.json>)确认一次稳定测量、0错误；恢复源后的[成功复测](<../testdata/history-mp-alignment-20261115/episode-recommend-success-after-fix.json>)也保留2/2命中和0错误。未修改全局错误上报。
- 修复后再次通过前端check、631/631测试、生产构建；刷新相同测试URL验证新资源。独占服务、Chrome、控制器已停止，本轮临时数据/二进制清理，截图与fixture源码保留。

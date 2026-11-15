# 第七轮：原版多数派/diff 自动集数定位推导

本轮补齐[上一轮规则推荐报告](history-mp-episode-recommend-20261115.md)明确留缺的默认自动 fallback。范围为后端和独立测试证据；前端空规则表行为由父任务单独核对，不以此后端交付代替 GUI 验收。

## 只读依据

MoviePilot HEAD：`8a83efeacd3e26b82be495e17d50146e31c96a60`。

- [format.py](../../MoviePilot/app/helper/format.py)：SHA-256 `d1fec01f9d22aca4f035fe2f8c66cb7a8f1faf331aac503672bcff31b07bca6d`。
- [test_episode_format_helper.py](../../MoviePilot/tests/test_episode_format_helper.py)：SHA-256 `36504c076cbdad484360f370920837abadbe3924999f76b4f66c62212e7f70d4`。

源码分段核对：

| 原版位置 | 本轮实现 |
|---|---|
| 276–325 recommend | 规则优先；空/全禁用配置、全部规则未匹配或模板不兼容时进入自动推导 |
| 327–500 _auto_recommend | 候选筛选、失败分类、原生冲突、正片覆盖率、多数派、基础模板与diff、置信度及计数 |
| 502–540 reasons/message | 保持原版原因顺序与文案优先级 |
| 543–647 filter/build_detected | 视频100MiB门槛；字幕/音频不受大小门槛影响；按媒体/字幕/音频、名称、路径排序 |
| 649–905 locate/extract | 复用上一轮反向定位，提取逻辑现在由规则兼容验证与自动分支共享；复用 Go legacyanime 和 MetaInfoParser.ParsePath |
| 907–969 select_base/ep_only | 精确 before_ep 分组，最大组票数严格大于次大组；不是强制超过50%；基准按类型/名称/span排序 |
| 971–1082 diff/insert | 最多依次使用a/b/c三个变量，仅泛化ep之后部分；已有变量保留 |
| 1084–1232 spans/probe/render | 解析已有变量区间；逐字面间隙探测；共同前后缀；可空变量；Unicode字符偏移与花括号转义 |
| 1234–1288 validate | 全部多数派样本经已有真实整理消费器校验集数，并与原生识别结果一致 |

新实现：[自动推导](../internal/app/organize_episode_recommend_auto.go)、[共享提取/兼容校验](../internal/app/organize_episode_recommend_compat.go)、[API路径](../internal/app/organize_episode_recommend.go)。未改手动整理/导入核心、前端、路由、旧项目或运行服务；没有 Python 运行依赖。

## 关键行为与有意保留的原版失败分支

1. 先按100MiB过滤视频。**只有候选集合完全为空**时才放宽体积；不是检测失败后放宽。因此“小视频 + Credits.srt”不会忽略字幕去重试小视频。
2. SP、NCOP、NCED、PV、CM、Trailer等特典不参与；00、非整数及范围集数不参与自动正片模板。范围仍可由配置规则处理。
3. 自动识别与原生识别冲突时，仅当原生数字位于自动集数之前才降级为标题续作号；其他冲突阻止推荐。当前配置的识别词仅影响原生识别，不重写物理模板。
4. 非特典正片数大于1且有效正片覆盖率低于0.6时失败；恰好0.6通过。字幕/音频不冲淡此覆盖率的分母。
5. 前缀分组并列第一时失败。2/1/1的分组有效：只验证2个多数派样本，返回有效样本总数4、majority_count=2，不自创“必须兼容所有文件”的门槛。
6. 自动模板先只替换ep；消费验证失败才diff。diff可包含空变量，例如结尾 ` Fin ` 有/无两种样本。消费器仍会识别错误的懒匹配集数并使整个推荐失败，不通过改正则猜测绕过原版失败。
7. 所有路径/历史解析、授权、文件类型过滤和选择语义沿用上一轮：单文件取父目录；目录不递归；显式多选同目录且不扩大选择；历史解析当前真实source/target。

## API实际契约

`POST /api/files/episode-format/recommend`，请求仍为 `paths` 或 `history_ids`，不新增请求字段。

无配置表是有效输入。成功依旧返回 LM 的 `{data, request_id}`，不伪造 MP `{success,data}`。自动结果示例：

```json
{
  "rule_name": "智能分析",
  "rule_index": -1,
  "pattern": "",
  "episode_format": "Show.S01E{ep}.mkv",
  "sample_file": "Show.S01E01.mkv",
  "min_file_size_mb": 0,
  "sample_count": 2,
  "matched_count": 2,
  "majority_count": 2,
  "confidence": "high",
  "size_filter_relaxed": false,
  "native_verified_count": 2,
  "native_fallback_count": 0,
  "native_conflict_count": 0,
  "reason": "native_meta_verified",
  "reasons": ["native_meta_verified"],
  "message": "无匹配自定义定位规则，已智能生成（仅供参考）"
}
```

`sample_count` 为检测有效样本数，`majority_count`/`matched_count` 为最终多数派数。`confidence=low` 仅对应单多数派样本或放宽体积，和原版一致。保留现有字段类型，自动 `pattern` 使用空字符串而非MP的null、`rule_index=-1` 表示没有配置规则索引。配置规则成功响应仍带其名称/索引/正则/最小大小，不受auto置信度影响。

以下失败均是 HTTP 400 的 `{error, request_id}`，不是成功空模板：

| 代码 | 原版错误语义 |
|---|---|
| EPISODE_AUTO_NATIVE_CONFLICT | 样本命名与原生识别结果冲突 |
| EPISODE_AUTO_NOT_DETECTED | 样本未识别到有效集数 |
| EPISODE_AUTO_LOW_COVERAGE | 有效正片样本覆盖率不足 |
| EPISODE_AUTO_NO_MAJORITY | 样本命名差异过大、没有明确多数派 |
| EPISODE_AUTO_FAILED | 特殊集/无候选、或最终模板校验失败 |

配置损坏、读取错误、正则超时和输入路径错误保持各自错误，不伪装成自动成功。

## 证据与验证

- [源码oracle](../testdata/history-mp-alignment-20261115/episode-format-auto-oracle.json)：15组源码推导/原版测试断言，记录对应原版测试名称；这不是Python执行回放。
- [自动测试](../internal/app/organize_episode_recommend_auto_test.go)：真实legacyanime及原生解析器运行全部oracle，倒序稳定性；同名样本物理落盘为稀疏合成文件后再走App推荐路径；规则优先/未命中/禁用；历史多选；100MiB放宽条件；60%边界；原生fallback/冲突/标题续作号；可空变量、既有变量、Unicode与花括号。
- [原规则与HTTP回归](../internal/app/organize_episode_recommend_test.go)：认证401、空配置真实自动200及模板/置信度/原生验证计数、真实自动失败400、配置规则继续优先；原有加密持久化/CAS/授权/历史/消费器测试保留。
- 原生fallback/冲突的独立注入测试对应原版monkeypatch测试；正常App路径没有注入开关。合成稀疏文件只用于文件名/类型/stat验证，不包含真实媒体内容；未访问NAS、下载器或已有媒体库。

最终验证通过：

```sh
go test ./internal/app -run '^(TestEpisodeRecommend|TestOriginalOrganizeNumberingOptions|TestFileBrowserNavigationContainmentSortAndScan|TestFileManagerTVBatchNumberingPreviewPreservesExplicitEpisodesWithOptionalMetadata|TestHistoryMPMovedFileRecognizeCapability)' -count=1
go test ./internal/releasemeta/... ./internal/pyregex ./internal/transferextensions -count=1
go build -o work/episode-auto-server ./cmd/server
rm work/episode-auto-server
git diff --check
```

App推荐与相邻回归3.753s；releasemeta主包7.052s、legacyanime 2.833s、pinyin 0.990s、rustanime 1.327s、transferextensions 0.437s均通过；pyregex成功编译（本包无独立测试）。服务构建与差异检查exit0，临时二进制删除后再次glob确认不存在。本轮后台job已全部收集，无遗留服务进程。

工作日志：[推荐及相邻回归](../work/episode-auto-tests.log)、[识别/扩展名回归](../work/episode-auto-parser-regression.log)（work为Git忽略目录）。本轮未运行/声明前端测试或GUI验收。

## 边界说明

上述自动推荐算法分支均已移植；没有另写通用数字猜测器。消费器的既有512字节模板限制继续有效，不修改父任务独占的整理核心；Go的既有ASCII占位符/集数字符契约也未在本轮扩大。

前端原版存在明确不一致：后端允许空规则直接auto，而MP前端handle在空表时toast返回；非空但全部disabled会继续请求。此子任务只实现后端真实算法，前端按父任务另行记录的原版交互处理。

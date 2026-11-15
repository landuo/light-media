# 第八阶段：跨标准季单文件与蓝光剧集组

## 基线与范围

本轮重新 fetch 后，当前分支 HEAD 与 origin/main 仍为 `4a222f0fa0c06848e7924e6614654a38e999fb19`。继续原工作树，不修改 MoviePilot 参考树，不提交、推送或部署。

第七阶段的单文件跨标准季限制已解除。本轮新增的是实际执行能力，不是允许保存一个尚未消费的字段；前端复用已贯通的共用整理弹窗，没有增加步骤或新界面。

## 单物理文件的两套编号

[组转换](<../internal/app/organize_episode_groups.go>)仍按提供方数组位置解释展示集号。跨标准季时，计划和资产通过可选 `episode_scopes` 保存完整标准范围：

```json
{
  "season": 4,
  "episodes": [16],
  "episode_scopes": [
    {"season": 4, "episodes": [16]},
    {"season": 0, "episodes": [2]}
  ]
}
```

这对应一个展示为 S01E01-E02 的物理文件。仍只有一个资产、一条视频归属和一个物理目标；两个标准槽指向同一资产。旧的单季记录不写新字段，继续使用原 `Season/Episodes`，没有全库回填或额外索引体系。

[统一库存助手](<../internal/app/import_episode_scopes.go>)由预览、提交、运行、失败释放、排除、删除、重整、质量替换及普通订阅完成校验消费。展示投影继续供命名、原版事件、字幕、NFO 和历史使用；标准库存不随展示编号改变。

覆盖同一物理目标时，新增文件须覆盖旧文件全部标准季集；只覆盖 S04E16 的文件不会删掉同时承载 S00E02 的旧目标。不同路径的不同版本仍按原有 MP 覆盖策略处理，不把库存槽改成全局禁止多版本的限制。相关[覆盖助手](<../internal/app/import_inventory_overwrite.go>)及[聚焦测试](<../internal/app/import_episode_scopes_quality_test.go>)覆盖完整/部分范围和恢复读取；空 scopes 保持旧计划哈希兼容。

## 蓝光目录

[实际目录测试](<../internal/app/organize_episode_groups_scopes_test.go>)从共用手动入口提交三个真实合成目录：

| 源目录 | 展示 | 标准库存 | 刮削 |
|---|---|---|---|
| `Disc.S01E01-E02` | 第 1 组季、1–2 集 | S04E16 + S00E02，同一目录资产 | 第 1 组季的 NFO |
| `Disc.S01` | 第 1 组季，无明确集范围 | 原有未知内容槽 0:0，不推断整季已齐 | 第 1 组季的 NFO |
| `Disc`，不填季号 | 原版默认展示第 1 季 | 原有未知内容槽 0:0 | 剧级 NFO |

真实 `BDMV/STREAM/00001.m2ts` 和索引文件均逐成员硬链接，来源保留。空 `CERTIFICATE` 目录没有单独复制，符合 [MP 按文件递归](</Users/accidia/projects/MoviePilot/app/modules/filemanager/transhandler.py#L948-L1003>)，没有改造成整目录软链接或新的搬运流程。

目录沿用已有任务结束后的批次刮削。测试通过实际 `finishJob` 钩子冲刷批次，不能用直接把 SQL 任务状态改成成功代替这个过程。可选预览依旧仅计算结果，执行从原表单重新提交。

本轮同时发现并修正原生季 NFO 原先复用单集 `<season>` 标签的问题：[当前生成器](<../internal/app/plugin_original_metadata_scrape.go>)复用原版 XML 构建工具，输出 plot/outline、标题及缺省季标题、premiered/releasedate/year、`seasonnumber`，与 [MP 季 NFO](</Users/accidia/projects/MoviePilot/app/modules/themoviedb/scraper.py#L360-L389>) 一致。

## 验证结果

- [跨季执行测试](<../internal/app/organize_episode_groups_scopes_test.go>)：实际预览、发布、两个标准槽、同一资产；两个标准季的普通订阅完成依据均读取这个真实受管文件；次标准季上的其他任务预约使该项预览明确阻塞；历史重整保留稳定 ID；删除目标清除两个槽并保留源。
- [原生与实际 SDK 测试](<../internal/app/organize_episode_groups_sdk_test.go>)：两集文件和字幕实际发布，NFO 按 MP 写首集的展示 S01E01 / episode ID 4016；完整内部库存仍含 S00E02。拦截、完成、字幕和刮削事件均保留展示结束集 2，不变成标准季号。
- [质量替换测试](<../internal/app/import_episode_scopes_quality_test.go>)：旧哈希兼容、次季更改影响身份哈希、整文件覆盖以及恢复上下文，3 项通过。
- 合并的历史、手动、目录、导入、资产、质量、普通订阅、元数据及实际 SDK 回归通过，**92.259s**；日志：[本轮合并回归](<../work/history-mp-alignment-20261115/round8-integration.log>)。
- 全仓 `go test ./... -run '^$'` 及 Linux/amd64 的应用测试交叉编译通过。前者是编译检查；Linux 产物没有执行，编译二进制已清理。
- 本轮没有修改前端，也没有重做真实浏览器验证；此前浏览器证据仍属于[第七阶段](<history-mp-episode-groups-20261115.md>)。本轮新增证据是合成提供方、真实 API/任务调用、物理文件、SQLite 和实际 SDK，不是生产媒体库验收。

## 后续

原生 move/softlink 不是“已有模块支持但缺少选项”，仍需完整实现发布、恢复及源生命周期。[本轮源码审计](<history-mp-native-modes-audit-20261115.md>)已经核清可复用部分和缺失部分。网页媒体来源及 provider-native 历史继承也仍待补齐。整体目标继续，不宣布全部功能 1:1 完成。

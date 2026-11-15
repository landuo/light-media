# 订阅刷新来源联调（第二十五阶段）

## 背景

第二十一阶段把全局 `recognition_source` 接入识别、刮削、订阅刷新判断与手动整理；第二十三阶段让自动目录识别跟随该来源。MoviePilot 的订阅刷新同样通过全局识别链重识别资源标题，因此本轮验证订阅刷新识别是否真的使用所选来源，并补齐自动目录电视剧季集路径的验证。

## 验证

### 订阅刷新识别

- 新增 `TestSubscriptionRefreshRecognitionUsesGlobalProviderSource`：保存 `recognition_source=douban`，使用独立 SDK worker 的 `recognize_media` 返回豆瓣 DTO，调用真实的 `originalChatLatestRecognize`（订阅刷新使用的同一函数）。
- 结果：`row.Media` 为 `source=douban`、`douban_id=705`，不含 TMDB 编号，`rsscontext.ResolveIdentity` 得到非空来源身份。说明刷新识别没有回落到 TMDB，也没有把豆瓣编号当 TMDB。
- 该路径无需改动生产代码：现有 `effectiveRecognitionSource()` 已在刷新识别中生效，本轮以真实模块回调确认，而不是只靠静态阅读。

### 自动目录电视剧来源与季集

- 新增 `TestAutomaticDirectoryRecognitionProviderTVNumbering`：全局来源 Bangumi、目录规则为电视剧，文件名 `Directory.Show.S01E01.mkv`。
- 结果：`directoryFileIdentity` 得到 `bangumi:706`、`tmdb_id=0`，季 1、集 [1] 来自文件名；`previewImport` 计划媒体为 Bangumi 身份且文件 `ready`。此前第二十三阶段只覆盖电影路径，本轮补齐电视剧编号分支。

## 回归

- [订阅刷新与来源](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round25-refresh-source.log>)：`TestSubscriptionRefreshRecognition|TestAutomaticDirectoryRecognition|TestMediaRecognitionSource|TestHistoryProviderIdentity|TestManualOrganize` PASS **16.721s**。
- 整体回归 **351.766s** PASS；全仓编译、Linux amd64 交叉编译与 diff 检查通过（本轮未改生产代码路径）。

## 边界

- 外部插件整树 softlink 对既有受管根的增量认领仍未实现（当前只允许目标不存在时的整树认领，或走原生逐叶追加）。
- 订阅刷新只验证识别来源与身份，不重放真实站点抓取；未宣称生产订阅源验收。
- 未提交、未推送、未部署。

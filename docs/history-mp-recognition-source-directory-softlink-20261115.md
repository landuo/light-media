# 识别来源设置、目录规则软链接与自动整理（第二十一阶段）

## 背景与口径

MoviePilot 的识别来源是全局 `RECOGNIZE_SOURCE`（TheMovieDb／豆瓣／Bangumi／AniList），手动整理接口另有 `media_source`／`media_id`，历史重整与多选都走同一个 `manual_transfer`。本地存储 `transtype` 为 copy／move／link／softlink。本阶段把这三处按 MP 的实际行为补齐，不引入全局整理／刮削设置，全局默认整理方式仍是硬链接。

## 识别来源

1. 新增可空的 `recognition_source` 设置项：缺省（旧客户端或其它分区保存）继续继承启动时的 `RECOGNIZE_SOURCE`，只有显式保存才落库；`effective_recognition_source` 只读返回当前生效值。宿主的插件设置读取（`RECOGNIZE_SOURCE`）同样按生效值返回，来源识别、元数据刮削、订阅刷新统一走 `effectiveRecognitionSource()`。
2. 手动整理支持 MP 同名的 `media_source`／`media_id`：TMDB 走原 `tmdb_id`，其它来源经宿主 `recognize_media` → `obtain_images` → provider DTO 投影后以私有身份参与计划，`tmdb_id` 与 TMDB 剧集组对非 TMDB 来源为空；历史选中优先，表单来源不覆盖历史身份。
3. 前端「媒体识别」分区新增识别数据源选择（TheMovieDb／豆瓣；若启动值是 Bangumi／AniList 也保留当前项）。整理弹窗按来源显示编号标签、来源搜索与回填 `source_id`，非 TMDB 时隐藏 TMDB 剧集组并提交 `media_source`／`media_id`。旧的整段保存不会把继承值写成用户显式选择。

## 目录规则与自动整理

1. 目录规则校验接受 `softlink`；`/api/directories/transfer-types` 的本地方式列表按 MP 增补「软链接」，前端双查询交集后出现该选项，默认仍为硬链接。
2. 目录监控／下载完成两条原有自动入口复用原队列与执行链：普通视频＋字幕产生各自真实软链接与历史；BluRay 盘根作为单个候选、逐叶发布，只产生一条历史。源目录保持完整。
3. 盘根聚合结果不写入下载单文件缓存，逐叶清单每轮从真实下载文件重建；未完成或混合任务不排队。停用与 `monitor_type=none` 不产生任务。

## 验证

- 目录自动主样本 [最终日志](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round21-directory-auto-final.log>)：PASS **5.079s**（真实 API 保存→下载完成／目录监控→独立 Source SDK→真实链接／资产／历史／源不变，重复巡检不重排）。
- 目录回归 [日志](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round21-directory-regression-final.log>)：PASS **20.315s**；前端目录相邻 **16 tests PASS**。
- 来源与手动整理 [日志](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round21-provider-http.log>)：`TestMediaRecognitionSourceSettingsPersistenceAndHostRead`、`TestManualOrganizeExplicitProviderIDsOrdinaryHTTP`（豆瓣／Bangumi 预览＋立即整理＋历史＋实际发布字节）、`TestManualOrganizeGlobalSourceAutomaticRecognitionHTTP`、`TestHistoryProviderIdentityOrdinaryHTTP` 全部 PASS。方式列表与目录软链接相关 [复核](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round21-transfer-types.log>) PASS **7.020s**。
- 整体 Go [回归](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round21-integration-final2.log>)：PASS **355.922s**，全仓编译与 Linux amd64 交叉编译通过，`git diff --check` 干净。
- 前端 `npm test` **641/641 pass、0 fail**，`npm run build` 成功。
- 真实 Chrome（独占实例，实际 App／SQLite／SDK／文件系统）：
  1. 系统设置真实保存识别来源＝豆瓣，`/api/settings` 返回 `recognition_source=douban`、`effective_recognition_source=douban`、`auto_import_mode=hardlink`；
  2. 整理历史视频行→重新整理，弹窗显示「豆瓣编号」，填入 `701` 预热后预览出「来源测试电影 (2024)」，预览不写执行历史；立即整理成功后该源文件历史 ID `file_3U-UmkBOXYehPBft8GnsF96H` **保持不变**，投影更新为最新目标／hardlink／douban 701，字幕历史 `file_bdd…` 未受影响（[前后对比](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round21-browser-after.json>)）；
  3. 目录设置展开规则：自动整理＝目录监控、整理方式出现「软链接」并可选中，保存后 GET 返回 `mode=softlink`、`monitor_type=monitor`；随后改回不整理再保存，`mode` 仍为 `softlink`（[界面](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round21-directory-softlink-ui.png>)）。

## 边界与未完成

- 本阶段只验证本地存储与合成媒体；未宣称 NAS／云盘或生产媒体验收。
- managed 既有根同根追加／跨资产成员转移、外部 shared-root 所有权认领、全局识别来源对自动订阅来源链的进一步联调与最终整体联调仍需继续。
- 未提交、未推送、未部署；测试实例与页面均为独占合成数据，结束后关闭。

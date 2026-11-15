# 整理历史对齐 MoviePilot：实现分析与进度

## 基线与边界

- light-media 基线：远程 `origin/main` 于第二十二阶段更新为 `8307f90e3d8556588d2426dc15cf1159c85cea69`，对应 BrushFlow qBittorrent 5 下载列表登录修复已直接应用到工作树；此前阶段基于 `4a222f0fa0c06848e7924e6614654a38e999fb19`。
- 开发分支：`codex/history-mp-alignment-20261115`，复用原主工作树；未提交、未推送、未部署。
- MoviePilot 参考提交：`8a83efeac`；MoviePilot-Frontend：`2829e3b0`。参考整理相关文件没有本地修改；旧项目只读。
- 本报告是分阶段分析与开发记录，不代表全目标完成、实机验收或原版全量功能已复制。
- 遵循 [AGENTS.md](<../AGENTS.md>) 中稳定历史 ID、源文件归属、独立目的路径与开关、底部可选预览、四种删除范围和失败留表单要求；这些明确要求优先于原版相反行为。

## 原版功能与实现逻辑

| 功能 | MoviePilot 实现 | light-media 当前实现与结论 |
| --- | --- | --- |
| 自动下载完成整理 | [TransferChain.process](</Users/accidia/projects/MoviePilot/app/chain/transfer.py#L2238-L2391>) 查询待整理种子，排除队列已有 hash，匹配下载器监控目录；读取下载历史识别信息，调用 do_transfer 后台逐文件整理 | [下载完成处理](<../internal/app/search_download.go#L2075-L2144>) 冻结计划后调用 [enqueueCompletedImportFiles](<../internal/app/import_file_submission.go#L20-L67>)，每文件独立任务；已有提交（包括失败）不随轮询重试。继续核对识别、范围检查与失败持久化差异。仅管理本应用任务是既有产品约定。 |
| 目录自动整理 | 原版统一进入 do_transfer、目录规则和 task 队列 | [目录队列](<../internal/app/organize_directory_queue.go>) → [directoryFileIdentity](<../internal/app/directory_filesystem.go#L820-L882>) → previewImport → executeImport；识别路径与手动整理仍有不同分支，需继续核对。 |
| 普通手动整理 | [manual API](</Users/accidia/projects/MoviePilot/app/api/endpoints/transfer.py#L277-L581>)：文件/目录、历史单条、显式文件数组；自动类型标准化；构造 EpisodeFormat；background 控制后台与立即；显式文件集合不展开其他目录成员 | [fileManagerAPI](<../internal/app/file_manager.go#L183-L194>)：background 且非 preview 才提交准备任务，其余走 [manualOrganize](<../internal/app/organize_manual.go>)。即刻执行会返回实际任务，弹窗轮询完成状态；不是后台准备即成功。 |
| 单条历史重整 | 成功 move 从 dest_fileitem 取源，其他从 src_fileitem；force 强制；from_history 复用识别、下载上下文；非预览可清旧目标 | [manualOrganize](<../internal/app/organize_manual.go#L124-L203>) 解析原历史、计划、归属，再按当前表单构造计划；保留稳定资源 ID。第一阶段统一了立即、后台与能力查询的源路径决策。 |
| 多条历史重整 | [历史页](</Users/accidia/projects/MoviePilot-Frontend/src/views/reorganize/TransferHistoryView.vue>) 直接传选中 logids 打开共用弹窗；弹窗逐 logid 调接口，不按影视身份扩大选择 | light-media 仅使用明确选中的 history_ids；[后台准备](<../internal/app/organize_history_queue.go>) 逐源去重、缺失项失败、其他继续。第二阶段新增 [立即/预览批量入口](<../internal/app/organize_history_batch.go>)，逐条复用单条逻辑；缺失或关联执行信息不可用的条目明确失败，后续正常记录继续，按原源去重且保留输入顺序。 |
| 历史保存 | [TransferHistory 模型](</Users/accidia/projects/MoviePilot/app/db/models/transferhistory.py#L21-L82>) 独立持久化源/目标、存储/FileItem、模式、媒体身份、分类、季集、下载器/hash、状态、错误、日期和文件清单；[add_force/add_success/add_fail](</Users/accidia/projects/MoviePilot/app/db/transferhistory_oper.py#L207-L319>) 按源查旧记录、删除后创建最新结果 | light-media 保留 import_plan/jobs/回调作为执行事实；第二阶段通过 [同事务写入](<../internal/app/transfer_history_write.go>) 在提交、状态、回调、取消时保存 current 的独立展示快照，页面直接分页，无读时重建。attempts 保留精确执行/旧ID别名；按原源路径保留稳定ID，最新实际提交覆盖显示，预览不进历史。为了兼容现有收据，写侧仍按受影响计划生成索引，不复制 MP 的 delete+insert 换ID。 |
| 执行回调 | [默认回调](</Users/accidia/projects/MoviePilot/app/chain/transfer.py#L1087-L1304>) 在真实成功/失败时写对应历史，再发送事件/通知 | [completeOriginalTransferCallback](<../internal/app/plugin_original_transfer_callback.go>) 保存物理任务回调，再查询当前历史发送事件；附加文件有独立历史。重构不能丢掉这些文件或事件中的稳定 ID。 |
| 失败与去重 | [do_transfer](</Users/accidia/projects/MoviePilot/app/chain/transfer.py#L3413-L3454>) 自动整理按所有历史去重，失败不会自动无限重试；手动失败可重整，成功重整按明确用户操作处理 | [recordManualOrganizeFailure](<../internal/app/organize_manual.go>) 保存失败计划和失败 import job；第三阶段补充已完成计划但提交失败时的模式、媒体、目标、季集与Part保存；未识别的失败保持未知，不伪造媒体身份。 |
| 加载、搜索、分页 | [history API](</Users/accidia/projects/MoviePilot/app/api/endpoints/history.py#L161-L218>) 接受成功/失败快捷词；显式 * ? 转 LIKE，普通文字 jieba 分词；title/src/dest 分别匹配；日期倒序分页与独立总数查询。UI 默认50、25/50/100/500/1000可选 | [列表 API](<../internal/app/transfer_history.go>) 直接按索引分页读取已保存的精简快照，前端 deferred 避免列表遍历 NAS 路径。第一阶段修正 glob 锚定与逐字段匹配；第五阶段普通文字已按原版jieba-next固定词典HMM=False分词，保留顺序与独立字段匹配。 |
| 排序与分组 | [原历史页](</Users/accidia/projects/MoviePilot-Frontend/src/views/reorganize/TransferHistoryView.vue>) 只对当前分页结果排序/按标题分组，不是全库排序 | [前端 feature](<../web/src/features/transfer-history.ts>) 也是本页排序与分组；选择存稳定 ID，分页不扩选，同 TMDB 不合并源文件。 |
| 整理队列 | [TransferQueueDialog](</Users/accidia/projects/MoviePilot-Frontend/src/components/dialog/TransferQueueDialog.vue>) 每3秒更新队列，按媒体标题/年份合组，运行文件用进度 SSE，离开清理 | [TransferHistory](<../web/src/components/TransferHistory.vue>) 已有队列和准备任务接口，但第一轮发现 pollPreparations 未调用、队列无定时刷新。现已补3秒刷新、准备任务与 pending 历史监听、关闭后停队列请求、卸载取消在途请求；第四阶段已在独占服务+真实浏览器验证队列、精确取消及移动端全屏。 |
| 删除 | [原删除接口](</Users/accidia/projects/MoviePilot/app/api/endpoints/history.py#L221-L257>) 与 [原删除弹窗](</Users/accidia/projects/MoviePilot-Frontend/src/components/dialog/TransferHistoryDeleteDialog.vue>) 提供记录/源/目标/两端组合；按记录逐项处理 | [history_delete](<../internal/app/history_delete.go>) + [前端删除](<../web/src/features/history-delete.ts>) 保留四按钮和一次确认；真正 deleted 才算成功；失败项保留选中、显示数量和原因。文件归属/快照/做种检查是明确保留约定。 |

## 第一阶段已落地修改

1. 搜索显式通配符遵循原版边界：`*Feature.mkv` 不再匹配 `Feature.mkv.backup`；普通字符串仍子串搜索；字面 `%`、`_` 继续转义。
2. 标题、源路径、目标路径分别匹配，消除标题尾部拼到路径开头造成的虚假匹配。
3. 提取历史 `organizeSource()`，供立即整理、后台准备与 capability 查询共同使用；成功移动使用目的文件，复制/硬链接仍使用原源文件。媒体库内合法移动目的文件可重识别，目录范围外文件保持不可用。
4. 共用弹窗的多条历史集范围不再误用“多个文件/目录必须有集数定位”限制；文件管理多文件与目录入口的定位规则不变。
5. 队列3秒刷新、后台准备轮询、pending 历史刷新恢复，慢请求不重叠、卸载清理 timer/listener/request；删除错误和失败项选择不被轮询清除。
6. 批量重新整理直接打开明确选中记录的表单，移除打开表单前的额外确认；最终执行仍由“立即整理/加入整理队列”触发。

## 后续重构清单（尚未完成）

- [x] 历史 current 展示快照在执行写入事务保存，列表直接分页；启动前一次兼容迁移旧数据，保留内部收据与稳定ID。写侧仍保留现有执行投影用于兼容，不引入第三套历史库。第三、四阶段已运行独立SDK worker回调测试。
- [ ] 全链路核对自动、立即、后台、单条和多条的识别/计划/执行失败落库信息与错误隔离。
- [x] from_history 按原版用已有身份覆盖表单对应字段，未知字段保留输入；取消继承时使用当前修正值。
- [x] 分类展示保存识别DTO或完整已知本地元数据的二级分类，与目录开关解耦，不在历史读取时重新分类。
- [x] 普通文字分词、自动目录源历史去重、取消观察状态、下载确定性失败落库及冻结分类继承，详见[自动链路审计](<history-mp-auto-audit-20261115.md>)和[分词来源](<history-jieba-source-20261115.md>)。
- [x] 历史表格、搜索与每页条数使用原 Vuetify 同系列控件，保留跨页选择、IME、排序与批量数量；已构建并完成核心浏览器交互验证。
- [x] 队列使用原系列Dialog/Tabs/List，四范围删除使用BottomSheet/Card/Button；保留底部预览、可见批量数量和失败选中。
- [x] 补齐规则式集数定位推荐、SDK冻结分类投影与规划前失败的已确认事实。
- [x] 贯通剧集组主链路与无规则命中时的原版定位自动推导；完成真实 GUI、硬链接、NFO、历史和 SDK 验证。
- [x] 补齐跨标准季单文件与蓝光目录的剧集组投影、实际执行及库存消费。
- [x] 接通原生本地移动、文件/目录清源恢复、历史单/多选与删除，并用真实GUI核验。
- [x] 接通同盘原inode快速移动与跨盘复制回退，并实际验证不同文件系统；支持普通文件外部transfer真实move收养。
- [x] 接通普通文件存储对象move、目录transfer真实移动收养，并对齐原生复制/跨盘移动时间戳和目标权限创建策略。
- [x] 接通目录成员storage.Source.move、完整清单预冻结与已移动成员冷恢复。
- [x] 接通原生普通视频/字幕softlink的entry归属、资产恢复、删除、重整及库旁NFO。
- [x] 接通软链接普通订阅完成、共享库存及当前V3覆盖/entry清理，保持事务读者和归属值语义。
- [x] 接通原生蓝光目录逐成员软链接、冷恢复、SDK、历史删除/重整及目录NFO/metadata恢复。
- [x] 接通普通本地视频/字幕的外部softlink真实认领与source storage actor，验证冷恢复和反例不fallback。
- [x] 接通目录外部softlink认领与source actor逐成员发布、完整名称映射及真实暂存冷恢复。
- [x] 接通owned软链接delete_file提供方及真实效果/冷恢复，普通Cookie网页历史provider-native身份继承、单选和后台多选执行。
- [x] 接通非受管既有普通目录的native copy/hardlink成员级合并、shared子集消费者及copy/link source actor真实返回收据。
- [x] 接通非受管既有根softlink/move、文件管理精确owned-link读/整理及共享手动softlink选项，完成实际Chrome交互。
- [x] 接通识别来源持久设置、手动非 TMDB 来源编号、目录规则软链接与两条自动整理入口，并完成真实 Chrome 保存/重整/目录方式验证。
- [x] 开放同一受管媒体目录的追加发布并复用原目录资产，保留旧成员与独立历史；不同影视身份与跨资产同名成员仍明确失败。
- [x] 自动目录识别跟随全局识别来源，且同一受管资产的同名成员可按 MP 语义替换。
- [x] 目录整理可以接管其它资产的同名叶：快照一致时替换并作废旧资产归属，漂移则拒绝。
- [x] 订阅刷新识别按全局来源重识别资源标题；自动目录电视剧来源与季集路径已验证。
- [x] 外部整树目录软链接可向已有目标增量发布，只认领新增链接并保留既有条目。
- [x] 完成最终整体回归、前端全量测试/构建与真实 Chrome 联调，并整理[交付说明](<history-mp-final-delivery-20261115.md>)。
- [x] 当前核心历史流程已用独占Go服务、生产前端构建和真实Chrome验证；最终回归 357.709s、裸全仓测试通过、前端 641/641、构建与交叉编译通过。未执行生产下载器/NAS写入验证。

## 第一阶段验证

- 新增 [Go 行为测试](<../internal/app/history_mp_alignment_test.go>)。修改前重现3个 glob 误匹配及移动后重识别不可用；修改后通过。
- 相关 Go 集合 `TestHistoryMP|TestHistoryParityV031|TestHistoryDeferred|TestHistoryUpsert|TestTransferHistory|TestManualOrganize|TestMPOrganize` 运行完成，排除下述已复现的基线失败后通过；无新增竞态测试。
- [弹窗测试](<../web/tests/reorganize-dialog.test.mjs>) 13/13 通过，包括新增“多条历史集范围”和“文件/目录仍需定位”测试。
- [新增历史组件测试](<../web/tests/transfer-history-component.test.mjs>) 6/6 通过；与历史 feature、删除、手动弹窗组合29/29通过。覆盖3秒刷新、慢队列请求去重、准备成功/失败、卸载清理、删除结果留存与批量直接打开表单。
- `npm run check` 通过；本轮未构建最终嵌入产物，待后续重构收敛后统一 build 与浏览器联调。
- 基线失败单列：`TestHistoryUpsertDownloadedSourceRetryKeepsIntent` 在“活动任务时预览必须失败”断言失败。用仅覆盖本次三个实现文件的 `go test -overlay` 读回原始 main 源码后同样失败；未将它计为本轮新增回归，也未把整个测试集描述为全绿。需后续核对原版预览语义与实际提交保护，不能为让测试通过增加多余防御。
- 本轮上述为本地合成文件/持久化集成测试及实际 Vue SFC 行为测试，不是 NAS、下载器实机或浏览器验收。

## 第二阶段：写时保存与逐条批量整理

### 实现

- [写侧持久化](<../internal/app/transfer_history_write.go>)：在 SQLite 同一执行事务内维护历史，覆盖 import job 新增、`organize_file → import` 提升、计划/元数据更新、真实物理回调、任务状态、单文件取消与隐藏记录。UPDATE 的 OLD/NEW 计划 ID 先去重，不对同一计划更新两遍；没有实际提交的预览计划不创建历史。
- [当前历史表](<../internal/app/transfer_history_index.go>) 增加精简 plan/file JSON 快照。列表、详情、选择解析和插件查询直接读保存值，不再按分页回查展开原计划 JSON；精确队列执行查询仍使用 attempts 与原执行记录。
- [启动迁移](<../internal/app/store.go>) 在接受请求与恢复 running job 前完成一次 v3 回填和触发器更新。迁移版本与数据同事务提交；正常重启不回填已完成历史。
- 移除 GET 的 dirty 检查/写连接切换/循环刷新/5秒预算降级、分批临时表、启动后20秒预热 goroutine及插件查询显式刷新。列表 COUNT 与分页仍使用同一只读快照，不会为等待重建先返回半份历史。
- 保留同源稳定ID、旧执行ID别名、真实提交排序、迟到旧结果不覆盖新提交、单文件取消、附加字幕/音轨历史、四种删除范围与内部恢复收据。写侧兼容投影仍在，不把移植称作与 MP 数据结构完全相同。
- [批量整理](<../internal/app/organize_history_batch.go>) 对多条历史逐条调用单条准备路径，缺失/不可用记录不阻断其他项；预览也逐条报告，重复源只处理一次。

### 验证与尚未验证

- [批量行为测试](<../internal/app/organize_history_batch_test.go>) 通过：包含缺失记录、关联资产缺失、正常记录和重复选择；验证预览无历史新增、未选记录不变、正常项实际硬链接完成、原历史ID稳定。
- [索引迁移/同步测试](<../internal/app/transfer_history_index_test.go>) 与 [当前历史测试](<../internal/app/transfer_history_current_test.go>) 8项通过。断言基于已提交原始SQL，不通过刷新 helper 掩盖漏写；覆盖同事务可见/回滚、只读GET、旧v2无dirty升级、启动恢复、二次重启零回填、变更源清理与索引排序。
- 最终针对性集合 `^(TestHistory|TestTransferHistory|TestManualOrganize|TestMPOrganize)` 通过，仍仅排除第一阶段复现的 `TestHistoryUpsertDownloadedSourceRetryKeepsIntent` 基线失败。`go test ./... -run '^$'` 全包编译检查通过；这不是全量业务测试通过。
- 十万行合成SQLite基准（本机单次）：COUNT+前50条保存快照约3.45ms；旧全量排名/关联读取约4.50s；单源隐藏标记同步更新约30.63ms。该数据只说明此合成场景，不代表NAS吞吐。
- [计划写入基准](<../internal/app/transfer_history_write_test.go>) 每组10次：1文件约2.86ms/次，24文件约6.93ms/次，100文件约44.38ms/次。写侧当前仍重算受影响计划，整季包存在写放大；未用异步队列或读时兜底掩盖成本。
- 扩展匹配原版插件 Transfer/History 测试曾运行但未通过：多项要求单独编译的 `OWNED_*_BINARY` SDK worker，当时没有提供；另有目录样本用例失败。后续需构建对应测试worker后核对，不计作已验证回调/插件验收，不计作全部已有基线问题。
- 第二阶段未重新构建前端、未部署，也没有浏览器/真实下载器/NAS写入验收。下一步继续 from_history、失败字段保存、自动整理差异与Vuetify界面，随后统一联调。

## 第三阶段：身份继承、失败上下文与原版控件

### 实现

1. [历史身份继承](<../internal/app/organize_history_batch.go>) 在读取选中记录后、文件收集之前恢复已知媒体类型/ID、季与集范围。匹配原版 from_history 的字段优先级，不再让表单旧值覆盖所选记录的真实身份；历史未知值不清空用户输入，特别篇第0季保留。取消继承仍按当前表单修正。
2. [失败记录保存](<../internal/app/organize_manual.go>) 接受已解析的计划上下文。计划被阻断或实际提交失败时，保存已有标题/媒体ID、模式、目标、季集和Part，同时创建独立失败执行，不冒充资产或成功收据；失败响应也携带已知媒体与目标。
3. 第一阶段单列的基线失败已查明并修复，未简单删除断言或持续跳过：旧 [规划逻辑](<../internal/app/import_plan.go>) 把任意同inode目标直接视为 `already_imported`，即使没有资产登记；因此跳过下载归属接续、实际导入及完成登记。现只对确切原源、已登记的资产判定已导入；未登记硬链接走正常提交/复用与登记。不同源路径即使共inode也走自身的覆盖策略，不吞掉新资源记录。
4. [原测试](<../internal/app/history_upsert_test.go>) 保留“活动下载不被接管”的原断言，新增实际提交拒绝/预览不换归属/不换历史以及新计划绑定原下载的检查；原测试后半段真正完成硬链接、完成下载状态、保留目录快照和旧收据，现完整通过。
5. [历史界面](<../web/src/components/TransferHistory.vue>) 搜索采用 outlined/compact `VCombobox`，每页数量明确 outlined `VSelect`，移除原生搜索双边框；表格采用 `VDataTableVirtual`、固定表头、真实排序、分组与字段slots、`VCheckboxBtn`。保留无重复页标题、跨页稳定ID选择、批量数量、删除错误结果、IME/URL/轮询行为。队列和删除弹层尚未整体重构。

### 验证

- [新增身份及失败行为测试](<../internal/app/organize_history_identity_test.go>) 通过：历史身份优先、取消继承使用修正身份、未知字段保留、第0季、失败记录保留已识别信息、预览不更新原执行、冲突目标不被改写。
- `^(TestHistory|TestTransferHistory|TestManualOrganize|TestMPOrganize|TestImport|TestDirectoryOverwrite|TestDownloadedReservation)` 全部通过，**不再排除上述下载重试测试**。目录 same-inode 的 always/size 覆盖规则也实际通过。
- 构建独立 Go SDK worker 后，`^TestOriginal(TransferCallback|TransferHistoryCommit|HostBusinessHistory|MainHistory|TransferAuxiliaryActualSDK|TransferBlurayHistory|TransferCompleted)` 全部通过。覆盖历史保存先于事件、失败保存异常不发成功事件、主视频历史重整、单独字幕/音轨、蓝光历史、下载完成与取消恢复、插件历史查询删除。
- SDK首跑受 macOS 默认临时目录 `/var` 符号链接影响，插件根目录检查失败；改用工作树内独占 `TMPDIR` 后通过，没有修改检查逻辑或旧服务。这里是**真实独立SDK进程 + 合成媒体测试**，不是生产下载器/NAS验收，也不是原MoviePilot服务实机重放。
- 前端相关测试33/33、完整 `npm test` 616/616、`npm run check`、`npm run build` 均通过；[组件测试](<../web/tests/transfer-history-component.test.mjs>) 包含真实SFC渲染/slots、分组选择、排序及IME。
- 未提交、未部署；浏览器视觉与整页交互仍待独立实例联调，下一阶段继续二级分类、自动链路剩余差异和队列/删除界面。

## 第四阶段：分类、弹层与真实浏览器联调

### 实现

- `ImportPlan.Category` 保存本次识别DTO已知分类；缺失时可用当前实际分类或完整已知本地元数据求值。目录分类开关只控制目录层，不清除已知分类；关闭开关不会为展示新增元数据HTTP请求、不会因字段未知强行补全或阻断。历史JSON快照和失败快照保留分类，表格同时显示媒体类型和真正二级分类。旧记录缺值不猜测回填。
- 原版删除弹层实际使用BottomSheet，现采用同系列 `VBottomSheet/VCard/VBtn` 四范围一次确认；队列采用 `VDialog/VTabs/VWindow/VList/VChip`，小屏全屏，真实文件进度/状态，精确执行ID取消。取消失败信息跨轮询保留；没有增加暂停入口。
- **浏览器发现并修复一：** 受管目标原位重整成功会提交新资产而不另发视频收据；[任务结果投影](<../internal/app/plugin_original_transfer_job_result.go>) 以前把这种已完成任务显示为“文件整理未完成”。现在采用已提交的imported资产事实，并仍保留具体回调失败优先级。[原位重整测试](<../internal/app/import_reorganize_test.go>) 新增实际结果断言，修改前失败、修改后通过；真实浏览器重试后成功关闭共用弹窗。
- **浏览器发现并修复二：** 选中记录后浮动按钮容器的透明空白挡住第2页点击，组件mock测试未覆盖命中区域。现容器空白不接收pointer事件，真实按钮保留交互。真实浏览器确认命中对象从div恢复为分页button，跨页选两条后在原页打开“共2项”弹窗。

### 验证证据

- 新 [分类契约测试](<../internal/app/history_category_test.go>) 覆盖已有DTO分类优先、完整本地分类、元数据未知、自定义字段缺失、无效重载保留有效策略；所有关闭目录分类场景无额外元数据请求，目标不添加分类目录，实际提交后历史保存同值。自定义分类冻结及失败分类保存也通过。
- 前端完整 `npm test` **624/624**；`npm run check`、生产构建、差异格式检查通过。原有历史/手动/逐文件导入/覆盖策略/下载归属/分类回归通过；新增原位重整回归与独立SDK进程历史回调集合通过。没有将其描述为全Go业务测试集通过。
- 独占服务 `127.0.0.1:18081`、真实生产前端构建、独立headless Chrome；不是运行中的旧MP或NAS服务。种入60条**合成历史**仅用于分页/分组，不把它们算作60次真实整理。
- 实际点击验证：四个删除按钮和数量；仅记录删除两条后列表60→58且队列保留；仅取消58号执行时59号仍queued，两源字节不变；单条原页弹窗；识别错误保留表单；反复可选预览不新增import任务；复用历史身份+直接输入目的路径预览成功；三次实际手动执行（首次硬链接、两次原位重整）完成，源与目标inode相同，稳定历史ID一直为 `fixture-file-58`；修复后成功关闭弹窗；跨页选择两条直接打开共用表单；390×844移动端队列恰好全屏。
- 第一次UI fixture使用仅管理模式，提交任务待处理；切换到同一独占数据库的真实worker运行时后任务完成。切换中一次网络失败是测试服务重启，不计为产品回归。之后在持续运行的worker实例完成成功关闭和原位重整修复复验。
- [结构化浏览器证据](<../testdata/history-mp-alignment-20261115/browser-evidence.json>)；截图：[桌面历史](<../testdata/history-mp-alignment-20261115/history-desktop.png>)、[四范围删除](<../testdata/history-mp-alignment-20261115/history-delete-sheet.png>)、[桌面队列](<../testdata/history-mp-alignment-20261115/history-queue.png>)、[底部可选预览](<../testdata/history-mp-alignment-20261115/history-manual-preview.png>)、[跨页批量表单](<../testdata/history-mp-alignment-20261115/history-batch-dialog.png>)、[移动队列](<../testdata/history-mp-alignment-20261115/history-queue-mobile.png>)。
- 已停止本轮测试服务、独立Chrome及控制器，确认监听端口释放；清理本轮浏览器profile、含临时凭据的数据库、合成媒体及3个浏览器fixture二进制。保留证据和可复现脚本。前阶段SDK测试二进制暂留供后续回归，不接触其他任务文件。

### 剩余工作

[自动链路审计](<history-mp-auto-audit-20261115.md>) 明确列出目录已有历史去重/删除后再入队、识别前取消观察状态、目录失败已知上下文、下载计划形成前确定性失败、普通TMDB下载冻结分类继承。另需处理普通中文分词及确认剩余手动选项的真实支持范围。目标继续进行，未提交、未推送、未部署。

## 第五阶段：自动链路与精确普通分词

- 再次 `git fetch origin main`：远程main与本地HEAD仍是 `4a222f0fa0c06848e7924e6614654a38e999fb19`，未更换工作树。
- [自动目录](<../internal/app/directory_history.go>) 在观察和实际入队事务读取可见源历史，成功/失败均跳过；修改NFO或规则不会绕过旧失败。删除记录后重新经过原有稳定时间、完整性检查再提交；已有手动成功历史同样生效。
- [目录准备](<../internal/app/organize_directory_queue.go>) 保留解析字段、已确认身份、完整计划及分类；提交失败继续使用现有失败保存helper。原HEAD两个队列源码的overlay运行新增断言实际失败（标题/ID/方式/目标/分类变空），当前源码同一用例通过。未解析媒体不伪造ID。
- 识别前取消同步观察为cancelled。新增测试只做普通连续两轮poll：不再卡queued，不新增任务/历史，源字节不变；没有添加竞态框架。
- [下载失败](<../internal/app/download_preparation_failure.go>) 在取得完整源集合后，对明确语义错误冻结一次failed计划，保留Intent及已核验文件事实，沿用逐文件blocked admission；下一轮复用，不持续新增失败执行。连接/元数据服务503、不完整、大小不符、未出现文件等暂态不写失败历史。quality未选文件不完整时仍允许已选文件按旧路径核验和整理。
- [下载分类](<../internal/app/import_provider.go>) 只从相匹配的加密下载上下文继承category，不让旧DTO的标题/type/id替换当前身份；开/关分类目录均保存事实，开启时使用冻结分类目录，验证无新增元数据请求。
- [普通分词](<../internal/historywords/cut.go>) 核实原版实际使用jieba-next 1.0.0rc1，而非直接假定传统jieba。纯Go实现同词典的DAG概率路径、12候选上限、同分择长、ASCII合并、标点空白及CRLF规则；仅增加带MIT许可证的固定压缩词典，无Python/CGo应用依赖或Go模块依赖变更。真实原版Rust oracle的**328组样本逐项一致**；SQLite列表实测“流浪地球”匹配同一title/source/target字段中的“流浪 地球”，不跨字段拼接、不反序，显式glob和字面SQL特殊字符契约保持。
- 本机单次分词成本：词典第一次加载约131ms，GC后新增堆约18.93MiB，内嵌压缩数据约1.90MB；热词典样本约9.8µs/op。这是本机微基准，不是NAS性能结论。来源、SHA及实测方法见[分词记录](<history-jieba-source-20261115.md>)。

### 第五阶段验证

目录/历史/手动/单文件导入/重整/下载/覆盖/新分词的合并回归通过；全仓 `go test ./... -run '^$'` 编译通过，未声称全Go业务测试集通过。[目录测试](<../internal/app/directory_history_test.go>)、[下载测试](<../internal/app/download_preparation_failure_test.go>)、[实际搜索测试](<../internal/app/history_search_words_test.go>) 和[原版golden](<../internal/historywords/testdata/jieba-next-1.0.0rc1.json>) 已保存。所有测试使用临时合成文件/回环或内存协议；本轮无生产下载器/NAS操作、无新部署或运行中测试服务。

自动链路第四阶段发现的五项已处理；继续实现[剩余手动选项与投影差异](<history-mp-remaining-options-20261115.md>)，因此整体目标保持进行中。

## 第六阶段：规则推荐、冻结分类SDK投影与准备失败事实

- [独立规则配置及推荐](<history-mp-episode-recommend-20261115.md>) 已贯通实际设置页、认证API、原文件名匹配、附加样本兼容校验和共用弹窗。历史成功move取目标，其余取源；多选保持输入顺序且不扩展到未选文件。只回填定位格式，不改变目的存储/路径/方式等独立选项。原版无规则命中后的多数派/diff自动推导仍未实现。
- [冻结分类投影](<../internal/app/plugin_original_transfer_context.go>) 让主文件和字幕的命名、执行与回调使用计划Category；晚安装SDK处理器同样生效，备用源intercept复用完整冻结MediaInfo。无分类的旧计划保持旧DTO，不新增分类查询。[实际SDK测试](<../internal/app/history_category_sdk_test.go>)包含真实主文件/字幕硬链接出版、命名字典、intercept及完成回调，而非仅比较内部struct。
- [准备失败事实](<../internal/app/history_preparation_failure_test.go>) 区分实际确认身份与用户输入编号。preview错误返回已确认的部分计划；手动、目录与下载入口保留该事实，后台保留完整源集合，不补造目标，不追加HTTP。分类503与非法目的路径均经过真实失败路径；纯预览仍不写执行历史。
- 独占真实GUI验证：从设置保存规则，选择两条合成历史，推荐命中2/2且首样本按选择顺序；路径和复制方式保留。暂移走选中的合成源后，实际HTTP400错误保留手工模板及其他输入。桌面与390×844移动端实际截图见[浏览器证据](<../testdata/history-mp-alignment-20261115/episode-recommend-browser-evidence.json>)。本次是3条预置合成历史，不是3次真实整理；推荐前后计划、任务、当前历史均为3。
- 浏览器额外发现长tooltip在移动端触发11帧ResizeObserver错误，浮层宽度每帧增加12px。仅给新tooltip设置专用content-class及更高特异性的`width:max-content`，保留Vuetify视口max-width；没有屏蔽全局错误，也没有删掉错误提示。[修复前](<../testdata/history-mp-alignment-20261115/episode-recommend-resize-before.json>)与[修复后](<../testdata/history-mp-alignment-20261115/episode-recommend-resize-after.json>)实测：内容一次稳定到334px、错误数0；恢复源后的[成功推荐复测](<../testdata/history-mp-alignment-20261115/episode-recommend-success-after-fix.json>)也为0错误。

### 第六阶段验证与剩余

- 新SDK/失败事实测试通过（3.519s）；历史、手动、导入、目录、下载、分类及真实SDK的合并回归通过（72.206s）；全仓编译`go test ./... -run '^$'`通过，不等同全Go业务测试集通过。
- 最终前端`npm run check`、`npm test` **631/631**、`npm run build`通过。真实浏览器使用重新构建后的同一独占URL并刷新验证，不是仅SFC测试。Chrome/控制器/测试服务关闭，临时数据库、合成文件、浏览器profile和本轮浏览器服务二进制清理；保留可复现fixture源码和脱敏证据。
- 本轮未部署、未提交/推送、未操作生产NAS/下载器。剧集组、原版自动模板推导、原生移动/软链接以及媒体来源继承仍在[剩余清单](<history-mp-remaining-options-20261115.md>)，整体目标继续。

## 第七阶段：自动模板推导与剧集组真实执行

- [自动推导](<history-mp-episode-auto-recommend-20261115.md>)实现多数派、至多3轮差异推导、原生验证、冲突与覆盖门槛、样本体积放宽及原版消息优先级；15组源推导oracle和真实文件通过。规则表与CustomIdentifiers继续独立。原版空规则表的前端点击提示和API自动推导能力分别保留。
- [剧集组](<history-mp-episode-groups-20261115.md>)贯通共用表单、认证查询、原版识别、命名、字幕、刮削与历史复用。内部资产采用标准季集，展示编号独立保存，紧凑媒体标准季列表不被组列表替换。
- 新的实际NFO测试发现并修复无插件TV文件重新识别丢组、NFO错查普通季两处遗漏；无插件和真实SDK链路分别验证，NFO展示S01E01但库存仍S04E16，真实episode ID4016保留。
- 独占GUI实际选组并推荐模板，失败预览留稿、目的路径独立、成功预览不改历史；移动端点击立即整理真实完成两次硬链接/刮削，保持两个原记录ID。桌面单条历史复用也正确继承组，预览不改变执行结果。[浏览器证据](<../testdata/history-mp-alignment-20261115/episode-group-browser-evidence.json>)与[文件系统证据](<../testdata/history-mp-alignment-20261115/episode-group-filesystem-evidence.json>)区分合成输入和真实执行；页面捕获错误0。
- 组专项最终4.216s，历史/手动/推荐/元数据及真实SDK合并回归47.435s，全仓编译检查通过；前端635/635、类型检查、构建通过。[UI报告](<history-mp-episode-group-fallback-ui-20261115.md>)保留详细范围。
- 尚余跨标准季单文件、目录材料、原生移动/软链接及provider-native网页历史继承。整体目标保持进行中；本阶段未部署、未提交/推送，也未写入旧系统或生产NAS/下载器。

## 第八阶段：跨标准季单文件与蓝光目录

- 重新fetch后，HEAD/origin/main仍为`4a222f0fa0c06848e7924e6614654a38e999fb19`，继续原工作树。
- [跨季实现](<history-mp-cross-season-20261115.md>)通过可选episode_scopes保持一个物理文件一个资产，完整消费标准季集槽，不再因跨季直接阻塞。预览/执行的预约、排除、提交、删除、重整及订阅完成依据均贯通；旧单季JSON和哈希保持兼容。
- 实际多集文件映射S04E16+S00E02，命名/字幕/历史/SDK仍为S01E01-E02。历史重整保持稳定ID，目标删除清两槽、源仍保留。部分范围不会原位替换承载更多季集的旧物理目标。
- 从手动入口实际提交三种蓝光目录：明确跨季集范围、仅明确组季、季号留空；逐成员硬链接与原生NFO完成。未知集范围不推断整季齐全。季NFO改用原版seasonnumber、outline和日期字段。
- 合并历史/导入/目录/资产/质量/普通订阅/元数据及实际SDK回归通过92.259s；全仓编译和Linux/amd64应用测试交叉编译通过，Linux二进制已清理。本轮没有改前端或新增真实浏览器验收。
- [模式审计](<history-mp-native-modes-audit-20261115.md>)确认原生move/softlink仍缺实际执行闭环，已有历史move语义及模块扩展点不是完整发布能力。下一步继续模式与provider-native网页历史继承；目标未完成。

## 第九阶段：原生本地移动及实际历史操作

- [原生move](<history-mp-native-move-20261115.md>)已从能力缺口接成真实文件/字幕/蓝光发布与清源，复用持久收据、支持清源中断后的冷恢复；目录按MP保留空容器。
- 共用手动弹窗和目录规则新增实际可用的移动，历史方式显示中文；全局默认硬链接不变。真实浏览器直接立即整理两条历史，源消失、目标字节与SHA正确、稳定ID保留、库存仍是标准S04E16/S00E02而非展示编号。
- 实际单/多选重整暴露并修复了单历史自动携带未选字幕的问题；正式计划与预览均按选中物理记录独立执行。主/字幕资源来源和资产来源继承已测。
- 四种删除、空源容器、重整后旧库源已消失等场景贯通；新文件、重建源、错误收据与根变化仍保留，不扩大任意库文件删除范围。
- 独立SDK工人验证move主/字幕回调及SQL失败后同任务恢复；启用目录规则实际自动移动。合并回归103.120s，收尾移动/删除回归18.051s；前端635/635、类型检查、生产构建及全仓/Linux交叉编译通过。浏览器证据来自独占合成文件，不是生产/NAS验收。
- 尚余同盘快速移动/跨盘回退、自定义storage和接管transfer的移动适配、原生软链接、provider-native网页历史继承及最终联调。当前move仍复制发布后清源，不把行为终态验证等同于同盘rename实现一致；整体目标继续。

## 第十阶段：同盘快速移动与真实插件接管

- [同盘与插件实现](<history-mp-move-fast-plugin-20261115.md>)使原生同盘移动保留原inode，无第二份内容复制和全量SHA；空间估算同步覆盖字幕和目录成员。移动的最终暂存发布用排他rename，复制回退不再二次依赖硬链接；原持久恢复链路保留。
- 独占64 MiB HFS+映像实测真实跨设备：目标1,769,499字节与SHA相符，源消失；卷及映像已清理。另有实际64 MiB稀疏视频/蓝光和8 MiB字幕的手动入口验证，不把这些合成材料说成可播放影片或生产NAS验证。
- 普通视频/字幕的外部transfer真实move现在先冻结源SHA，核验目标与源消失后认领；SQL失败后同计划重试不重调插件、不碰同名新源。目录复制冒称move明确失败；storage.Source.move及目录真实接管仍未完成。
- 合并历史/库存/元数据/SDK回归119.211s，收尾移动/模块回归31.808s；全仓编译与Linux交叉编译通过。无本轮前端变更或新浏览器验收。
- 仍需核对跨盘复制的时间戳/目标默认权限，并继续存储对象、目录移动接管、软链接、provider-native网页历史和最后联调。整体目标继续，不宣称全部中间状态、文件属性或插件路径已1:1一致。

## 第十一阶段：存储对象、目录接管与文件属性

- [本轮实现和证据](<history-mp-move-storage-directory-20261115.md>)接通普通视频/字幕的真实storage.Source.move：调用前快照/SHA、已开始/封存结果、自有stage校验、排他发布及冷恢复；不是copy/link冒充移动。
- 外部transfer目录move已按调用前逐成员证明和明确返回清单收养，保留原根历史与空容器；实际变目标名称、换进程恢复、native fallback和错误内容验证通过。目录成员storage.Source.move是另一层，目前仍明确未接入。
- 原生copy及跨盘move以源时间戳、目标默认ACL/umask创建，不复制源执行/访问权限。APFS纳秒mtime与目标创建权限实际通过；HFS+不同设备再次验证字节、SHA、历史mtime及源消失。Linux为交叉编译验证，不宣称NAS/远端ACL实测。
- 完成目录move后的源缺失允许精确无删除对账，来源配置撤销不再误阻断；同名新目录仍受保护。
- 整合历史/导入/目录/库存/质量/元数据及SDK回归132.222s；跨设备/属性/缺源对账2.784s；全仓编译及Linux交叉编译通过。无本轮前端改动/新浏览器验收，独占卷/映像/重复worker已清理。
- 后续继续目录storage成员预冻结与恢复、原生软链接、provider-native网页历史及最终联调。整体目标继续。

## 第十二阶段：目录成员storage.move

- [本轮实现和验证](<history-mp-directory-storage-move-20261115.md>)接通目录完整成员清单预冻结、存储实例/key和原文件快照绑定、逐成员真实move及冷恢复；保持提供方FileItem/名称映射、原根单条历史和结果回调，保留源空容器。
- 已完成成员重试不再调用存储对象，也不读写新建同名源；未完成成员继续原顺序。封存结果已保存但staged尚未确认的真实stage，经过精确子收据/内容证明后可进入原恢复路径。
- 独立SDK四分支4.783s通过，包括换SDK进程恢复和自定义名称；冻结清单另验证完整覆盖、重复及文件/子目录前缀冲突。未新增竞态矩阵。
- 旧能力对照样本的native目标适配补入已实现的move，原版字典/源码摘要不变；SDK/HTTP能力契约10分支通过。另修正既有目录准备测试对跨秒父子领取顺序的假设，只显式安排测试任务时序，不改生产调度。
- 最终扩大整合回归242.366s、全仓编译、Linux/amd64交叉编译与diff检查通过；目录方式前端契约7/7通过，无本轮产品前端变更/新浏览器验收。
- 软链接低层探索未形成资产/历史/恢复完整闭环，已撤出产品代码和选择项，不把创建链接当作完成。接着继续软链接、provider-native网页历史及最终联调；整体目标仍进行中。

## 第十三阶段：原生普通视频/字幕软链接生命周期

- [原生文件软链接](<history-mp-native-softlink-files-20261115.md>)已用真实entry快照和独立referent证明接通排他发布、资产登记、冷恢复、主/字幕SDK回调；资产大小保持媒体字节，不把源快照当链接归属。
- 精确受管链接专用读取不改变通用nofollow契约；目录元数据列表保留已受管字幕和断链条目，不把它们误判为空目录或可播放视频。
- 仅目标删除保留实体；两端删除沿用原来源/下载器守卫；断链与同文本新inode替换均按真实entry处理。重整直接引用原普通实体并保留资源来源，原位更正复用原entry，待删唯一实体依赖在预检保留。
- 实际导入批次结束后运行真实刮削任务，NFO写在库内链接旁，未写到资源实体目录。完整文件生命周期/独立SDK测试6.956s；扩大整合252.950s、全仓编译、Linux交叉编译及diff检查通过。
- 本轮没有开放softlink选项或新增浏览器验收。目录软链接、storage/external接管、delete_file override及库存/订阅/洗版的受管链接消费者仍需继续；既有质量测试通过不等于这些新路径已实现。后续再接方式UI、provider-native网页历史和最终联调，整体目标继续。

## 第十四阶段：软链接库存与当前V3覆盖

- [本轮实现](<history-mp-softlink-consumers-v3-20261115.md>)接通普通完成和共享订阅快照的entry/referent核验；只用调用方q读取受管证明，可在单连接完成事务内复核，不重入Store.DB。库存展示仍沿用slots，不添加全量扫描。
- 当前V3覆盖读取先确认owner，策略大小/mtime使用普通媒体实体，物理snapshot仍是entry；旧entry隔离/恢复/备份删除不依赖referent存续，新目标确认仍检查实体。并行归属使用完整值语义，字幕原entry/proof可正确接管到新资产。
- 新库存测试实际保存订阅、实际导入softlink并在单连接事务中复核，缺referent及同文本新inode均失败。V3原生host测试验证真实larger/smaller策略、broken预检、缺referent时隔离/restore、旧backup删除与字幕接管，未伪造成功收据。
- 普通完成回归3.675s；V3/旧方式及并行回归7.665s；扩大整合254.883s；联合全部软链接/库存/V3用例8.888s；全仓编译、Linux交叉编译和diff检查通过。协议边界为实际原生host+合成素材，不扩称完整新增softlink下载协议或NAS实测。
- V1/V2旧计划仍走既有兼容路径，不为新模式退回旧槽位整组交换。目录、存储/外部接管、delete_file override、方式UI及provider-native网页历史继续；无本轮前端变更/新浏览器验收，整体目标进行中。

## 第十五阶段：原生蓝光目录软链接与生命周期

- [本轮实现](<history-mp-native-softlink-directory-20261115.md>)在真实目标目录中逐成员创建软链、冻结父目录与子发布收据，保持MP file-only递归、小附加文件、空源目录、单一根资产/历史及完整SDK列表/STREAM语义。真实首child发布后取消、重开重试复用原inode。
- 聚合proof区分真实目录/leaf entry与引用实体；库存检查完整目标及各ref，不因源后来新增无关NFO假失效。通用快照/文件打开/浏览器stat仍nofollow。
- 历史仅目标/断链/两端删除、新路径与原位重整已实测。原位从匹配真实旧收据继承映射而非伪造成功列表，目标清理不跟随引用实体，稳定原资源历史与归属保持。
- 目录刮削新写NFO为显式普通成员；已链接NFO默认保留，显式overwrite仅交换entry，源字节不变。实际元数据SQL失败后App冷恢复从原持久日志完成owner/receipt/proof更新，不把已知NFO认作未知文件；原取消和write lease仍有效。
- 文件V3覆盖继续挡住目录proof，未将目录混入普通文件unlink。外部transfer声明soft而实际copy的SDK反例明确失败，外部soft结果尚未认领，不冒称相关提供方已支持。
- 目录发布及旧方式9.405s；定向软链接与方法检查19.727s；NFO冷恢复1.05s；扩大整合264.364s；全仓编译、Linux交叉编译和diff检查通过。证据限独占合成材料/本地Go-SQLite-FS-独立SDK，不是生产NAS或完整新增softlink下载协议实测。
- 仍需已有目录直接合并/覆盖、存储/外部transfer和delete_file接管、文件管理owned链接/方式UI、provider-native普通网页历史和最后联调；无本轮前端变更或新浏览器验收，整体目标继续。

## 第十六阶段：普通本地文件外部transfer与source storage软链接

- [本轮实现](<history-mp-softlink-external-storage-files-20261115.md>)区分外部调用前许可与真实已完成entry。冻结普通源、目标原不存在及父目录inode，provider需显式softlink/local-file/精确目标，host核验真实链接和ref后认领；已封存结果冷恢复不重调。
- source actor按MP调用Source.softlink，FileItem完整冻结，Path=逻辑目标/NewName=nil/PublicationPath=授权stage；先持久调用身份，再封存结果和核验entry，随后复用现有排他rename。缺结果不重调或native回退，普通copy/false均不发布或登记。
- 真实SDK覆盖视频/字幕、外部SQL失败冷恢复、copy冒充、调用前已有link不调用provider；源actor返回且封存后、staged快照前取消，重开更换SDK PID后复用同一stage inode/每源一次调用，源字节保持。
- 外部及旧回归13.193s；source actor定向5.732s；扩大整合271.195s；全仓编译、Linux交叉编译和diff检查通过。重建复用原worker路径，未累积版本副本；证据不扩称云盘/跨存储/完整新下载协议或NAS实测。
- native目录生命周期保留，目录actor/外部transfer、delete_file、已有目录合并覆盖、文件管理owned链接与方式UI、provider-native网页身份及最终联调仍需继续。未开放软链接选项，无本轮前端/浏览器验收；整体目标进行中。

## 第十七阶段：目录外部与source actor软链接

- [本轮实现](<history-mp-softlink-external-storage-directory-20261115.md>)冻结完整原树/精确目标空缺/父目录身份，外部结果须有明确softlink/local-dir及完整双向bijection；实际root/parents真实dir、每leaf真实link/ref才认领，不认领根symlink、copy或未知额外成员，不伪造native child receipts。
- 已published只复核已拥有目标与各ref，source新增无关NFO后普通恢复/SQL冷恢复均不重调provider或callback。原提供方FileItem包括目录size=null完整保留；STREAM=12在receipt/history独立核对。
- 目录source actor按冻结名称映射逐成员softlink，首次Source instance/key绑定防pending成员悄悄换actor/native；guard与publisher共用封存结果+真实stage证明，staged确认前取消后App冷恢复复用同一inode、每源一次调用。
- SDK实测名称变换BluRay/Streams/feature.m2ts、tiny成员、空CERTIFICATE保留、完整FileItem/逻辑与stage参数、单资产/历史与源快照/字节保持；copy反例不认领。
- 外部目录与旧回归19.690s；source目录actor4.484s；扩大整合275.990s；全仓编译、Linux交叉编译和diff检查通过。证据仅独占合成材料/本地Go-SQLite-FS-SDK，不扩称NAS、任意云盘或完整新增下载协议实测。
- delete_file、已有目录直接合并覆盖、文件管理精确owned链接/方式UI、provider-native网页历史与最终联调仍需继续。无本轮前端变更/新浏览器验收，未开放软链接选项，整体目标进行中。

## 第十八阶段：softlink删除提供方和普通网页历史来源

- [本轮实现](<history-mp-softlink-delete-history-provider-20261115.md>)复用封存SourceOperation/browserOperation，对精确owned软链entry/root/member构建nofollow许可；沿原delete_file/FileItem调用。False保留、None/异常原链native fallback，true须有真实效果，返回先封存、未知结果不重复调用；native仅quarantine entry/目录，不跟随源。
- history/asset仅softlink分支接入provider/恢复；即使仅删可见历史，资产仍按精确执行取回原FileItem。真实SDK覆盖断链、两端、无效果true、False、fallback、已返回结果终态SQL失败冷恢复、未知结果、实际scrape普通NFO及隐藏历史后删资产。
- 普通from_history恢复provider Source/ID、媒体类型、季集，清除过期TMDB和不适用group。只有实际读取所选历史授予私有originalHistoryProvider，普通JSON不接收该标志，不借用内部retry；经原recognize_media exact-ID识别完整DTO后才走原planner/cleanup。
- 实际Cookie登录后普通/api/files/organize验证豆瓣电影701/Bangumi电视剧702，预览不变更、单选与后台多选prepare→file→import完成真实copy；故意TMDB999仍保持Source/ID和季集，稳定历史ID/最新实际提交/未选项和源数据语义保持。
- 删除13组回归30.377s；普通网页来源5.411s；最终扩大整合298.021s；全仓编译、Linux交叉编译及diff检查通过。初轮宽前缀误含缺专用binary的无关类注册测试，最终精确业务范围重跑；未将未配置项计成功。
- 已有目录直接merge/overwrite、文件管理精确owned链接和方式UI/浏览器、剩余全局来源控制与最终联调仍需继续；本轮无前端变更/新浏览器验收，未开放软链接选项，整体目标进行中。

## 第十九阶段：非受管既有目录合并与source copy/link收据

- [本轮实现](<history-mp-directory-merge-storage-publication-20261115.md>)按MP目录递归而非普通leaf V3：非受管真实既有root开放native copy/hardlink，同名entry精确隔离/发布，不搬根、不穿透旧硬链接写源；无never/size/latest/普通overwrite事件，source STREAM与本次完整file_list保持。
- SharedDirectory仅owned子集，observed根/无关NFO/其他版本/既有dirs不认领。history/asset逐leaf删除，shared-root不传delete_file；metadata只旧owned+实际writes原子rebase owner/receipt；reorg/库存/质量/presence都使用子集。
- 实际SDK/FS联测10业务case+1subset unit通过18.149s：copy/hard/copy-cold，删除4、实际Scraping=true→真实NFO→SQL失败冷rebase、history reorg、quality/ordinary inventory。最后新建空dir清理2个机制样本另复核1.227s，既有root/dirs与新非空dir保留。
- source actor copy/link同一child receipt增加Started/Returned/result绑定，完整FileItem/parent/copy摘要和实际entry；两方式互冒、unknown结果不重调不fallback；普通copy/目录hardlink cold复用inode/属性。23 SDK样本19.527s通过，move/soft旧路径保持。
- 扩大整合303.482s；全仓编译、Linux交叉编译及diff检查通过。证据为独占合成材料/真实Go-SQLite-FS-SDK，未扩称NAS或云盘。
- managed已有根跨资产成员转移、shared move/softlink及高层外部merge、文件管理/方式UI/浏览器、全局来源控制和最终联调仍需继续。无本轮前端变更，未开放软链接选项，整体目标继续。

## 第二十阶段：shared软链接/移动与真实文件管理入口

- [实现报告](<history-mp-shared-modes-file-browser-20261115.md>)：已有非受管plain root四种native方式已贯通；softlink占用叶不预删/不备份、不套leaf V3，保真实prefix；move复用原全成员冻结/返回stage收据/实际清源。shared根Softlink=nil，仅真实leaf拥有proof与媒体字节。
- metadata linked NFO原entry保留或显式交换entry，真实SQL故障冷rebase保源；Source.soft/move每leaf一次调用、冷启stage inode保留。7真实SDK样本+4机制单元12.939s，旧目录/shared49.002s，外部whole-root边界19.490s。
- 文件管理native/SDK列表仅exact owned link→file＋软链接/断引用标识，显示媒体字节；当前根/entry/ref/FD核验后下载或识别，通用browserStat/manifest仍nofollow。copy等方式从普通owned link继续整理绑定原resource/history；有效资产破坏操作仍走历史。
- 共用手动方式softlink已开放，自动默认与独立目标/开关保留，全局默认hardlink不变。真实Chrome/新构建/实际App-SQLite-SDK：history软链重整完成、文件管理22B链接标识、随后copy独立目标**不点预览**直接实际成功且video/字幕原历史ID保留。
- 扩大Go319.154s，全仓及Linux交叉编译/diff通过；前端636/636、新构建及格式通过。独占测试标签/服务/worker关闭、临时数据与Linux产物清理；用户Chrome其余页面与旧服务未操作。
- [结构化证据](<../testdata/history-mp-alignment-20261115/shared-modes-file-browser-evidence.json>)。managed已有root/跨资产归属、external shared-root认领、目录规则softlink入口/自动流、全局来源输入和最终整体联调仍需继续；不扩称NAS/云盘验证或全量目标完成。

## 第二十一阶段：识别来源设置、目录规则软链接与自动整理

- [实现报告](<history-mp-recognition-source-directory-softlink-20261115.md>)：新增可空 `recognition_source`（旧客户端/其它分区保存继续继承启动值，只有显式保存才落库），`effective_recognition_source` 与宿主插件设置读取统一生效值；识别、元数据、订阅刷新改用同一来源。
- 手动整理补齐 MP 的 `media_source`／`media_id`：非 TMDB 来源经原 `recognize_media`＋`obtain_images`＋provider DTO 投影，私有 grant 参与计划；历史选中优先，表单来源不覆盖。前端媒体识别分区新增来源选择，整理弹窗按来源换编号标签/搜索参数并隐藏 TMDB 剧集组。
- 目录规则校验与 `/api/directories/transfer-types` 本地方式增补「软链接」，与 MP 本地 transtype 一致，默认仍硬链接；目录监控/下载完成两条原自动入口复用原队列，普通视频＋字幕各自软链接、BluRay 盘根单候选逐叶发布，未完成/混合任务不排队，停用与 none 不产任务。
- 验证：目录自动 **5.079s**、目录回归 **20.315s**、方式/软链接 **7.020s**、来源 HTTP **8.305s** PASS；扩大整合 **355.922s** PASS（[日志](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round21-integration-final2.log>)），全仓/Linux 交叉编译与 diff 检查通过；前端 **641/641**、构建通过。
- 真实 Chrome：设置保存豆瓣来源并读回生效值；历史视频行重新整理显示「豆瓣编号」701，预览不写历史，立即整理后同一历史 ID `file_3U…` 保留并更新为最新目标/hardlink/douban 701；目录规则展开可选「软链接」并保存为 `mode=softlink`，回退监控后 `mode` 仍保留。
- [结构化证据](<../testdata/history-mp-alignment-20261115/recognition-source-directory-softlink-evidence.json>)。managed 既有根跨资产、外部 shared-root 认领、来源对自动订阅链的进一步联调与最终整体联调仍需继续；不扩称 NAS/云盘或生产验收。




## 第二十二阶段：受管目录追加发布与上游基线

- 上游 `origin/main` 更新为 `8307f90e`，已将其中 qBittorrent 5 刷流下载列表登录修复直接应用到工作树（未新建提交）。
- [本轮实现](<history-mp-managed-directory-append-20261115.md>)：按 MP「目标目录已存在即写入、旧条目保留、历史按源独立」的语义，开放同一已登记受管目录的追加发布。准入要求真实目录 inode 与登记快照一致、资产同路径同 Root、媒体键与季相同；追加复用原目录资产并合并集数/集范围，不产生第二个资产。
- 归属仍按本次实际发布：新收据 `SharedDirectory` 只登记新成员，旧成员属于原收据；删除追加历史只删本次成员。不同影视身份或跨资产同名成员仍以明确错误失败，不伪造转移。
- 验证：追加主样本 copy/hardlink 真实两轮目录导入并通过 **5.923s**（旧成员字节不变、新成员落地、单资产、两条历史、删除只删新成员），原生 softlink 追加机制样本真实链接；目录/共享/历史回归 **91.230s**；扩大整合 **343.561s**、全仓编译、Linux 交叉编译与 diff 检查通过。
- [结构化证据](<../testdata/history-mp-alignment-20261115/managed-directory-append-evidence.json>)。跨资产覆盖转移、外部整树 softlink 增量认领、来源对自动订阅链联调与最终全量联调仍需继续；不扩称 NAS/云盘或生产验收。

## 第二十三阶段：受管成员替换与自动目录来源

- [本轮实现](<history-mp-member-replacement-auto-source-20261115.md>)：目标叶属于同一追加资产且登记快照未变时，按 MP 目录覆盖语义先备份再发布新成员；其它资产或已漂移的叶仍明确失败，不伪造所有权转移。
- `directoryFileIdentity` 在全局来源非 TMDB 时改走来源识别链（recognize_media → obtain_images → provider DTO），以私有 `originalDirectoryProvider` 参与计划；电视剧沿用文件名季集，NFO 已给出的 TMDB 身份保持本地身份。
- 验证：真实两轮目录导入替换同资产成员、漂移/外部资产拒绝；保存豆瓣来源后自动目录识别得到 `douban:704`，实际执行历史记录 provider 身份且无 TMDB ID。专项 **11.517s**，自动来源与目录回归 **66.080s**；扩大整合 **351.646s**、全仓编译、Linux 交叉编译与 diff 检查通过。
- [结构化证据](<../testdata/history-mp-alignment-20261115/member-replacement-auto-source-evidence.json>)。跨资产成员转移、外部整树增量认领、订阅刷新来源联调与最终整体联调仍需继续；不扩称 NAS/云盘或生产验收。

## 第二十四阶段：跨资产同名成员接管

- [本轮实现](<history-mp-cross-asset-leaf-takeover-20261115.md>)：目录整理覆盖同名文件时不再因“叶属于其它资产”一律拒绝。实际内容与登记快照一致则备份发布新内容，同一事务内删除旧叶登记（逐字段比对）、旧资产置为 `superseded` 并释放槽；漂移或发布期间变化则拒绝。
- 旧资产其它文件与旁车记录不触碰，旧历史仍是独立成功记录；新叶归属本次目录收据子集，删除旧历史会因快照不符安全拒绝，与 MP“只写目标文件”的行为一致。
- 验证：真实单文件整理产生资产后再以目录整理覆盖同名叶，目标为新字节、旧资产作废、槽清零、目录资产唯一、两条历史成功；同资产替换、漂移拒绝、追加与原生软链追加保持通过。专项 **11.858s**，目录与历史回归 **70.752s**；扩大整合 **355.631s**、全仓编译、Linux 交叉编译与 diff 检查通过。
- [结构化证据](<../testdata/history-mp-alignment-20261115/cross-asset-leaf-takeover-evidence.json>)。外部整树 shared-root 增量认领、订阅刷新来源联调与最终整体联调仍需继续；不扩称 NAS/云盘或生产验收。

## 第二十五阶段：订阅刷新来源与自动目录剧集

- [本轮验证](<history-mp-refresh-source-verification-20261115.md>)：保存豆瓣来源后，订阅刷新使用的 `originalChatLatestRecognize` 经独立 SDK worker 返回豆瓣 DTO，`source=douban`、`douban_id=705`，无 TMDB 编号；自动目录电视剧（Bangumi）得到 `bangumi:706`、季 1 集 [1]，计划媒体为 Bangumi 身份且 ready。
- 刷新识别路径无需改动生产代码，本轮用真实模块回调确认既有 `effectiveRecognitionSource` 生效，而不是只读代码判断。
- 回归 **16.721s PASS**，整体回归 **351.766s** PASS；全仓编译、Linux 交叉编译与 diff 检查通过；本轮未改生产代码路径。外部整树 softlink 增量认领与最终整体联调继续。
- [结构化证据](<../testdata/history-mp-alignment-20261115/refresh-source-verification-evidence.json>)。

## 第二十六阶段：外部整树软链接增量认领

- [本轮实现](<history-mp-external-softlink-incremental-20261115.md>)：外部目录软链接冻结阶段允许已有目标（受管根先经追加准入），记录调用前条目/链接；模块调用后只把新增的、指向对应原文件的真实链接登记为成员，新建父目录登记为目录，既有条目要求身份/快照/链接完全不变，映射外新增拒绝。
- 收据保留 `Existing` 供校验，删除/重整只遍历本次成员；受管根复用 `AppendAssetID`；已发布收据重复发布不再重冻证明。
- 验证：`success`/`asset-sql-cold`/`counterfeit-copy` 保持，新增 `existing-target` 真实外部模块写入后仅 5 个新链接被认领、`foreign.txt` 保持且不认领、无伪造子收据、一次模块调用；专项 **3.681s**，外部软链/目录/历史回归 **113.324s**；扩大整合 **363.389s**、全仓编译、Linux 交叉编译与 diff 检查通过。
- [结构化证据](<../testdata/history-mp-alignment-20261115/external-softlink-incremental-evidence.json>)。最终整体联调与交付说明继续；不扩称 NAS/云盘或生产模块验收。

## 第二十七阶段：最终联调与交付

- [交付说明](<history-mp-final-delivery-20261115.md>)汇总全部能力、验证与边界：最终扩大回归 **357.709s**（含设置/FNOS 脱敏）、裸 `go test ./...` 通过、全仓编译、Linux amd64 交叉编译与 diff 检查通过；前端 **641/641**、构建成功。
- 真实 Chrome 最终联调：历史列表加载 2 条成功记录；单条字幕历史预览→立即整理→成功关闭并刷新为最新目标；多选显示「批量删除（2）」「批量重新整理（2）」；[界面证据](</Users/accidia/projects/light-media/work/history-mp-alignment-20261115/round27-history-final.png>)。
- 测试实例、监听与临时目录已清理；未提交、未推送、未部署。生产下载器/NAS/云盘写入与 Linux 运行仍为明确未验证边界。

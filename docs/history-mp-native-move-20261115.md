# 第九阶段：原生本地移动、历史重整与删除

本页保留第九阶段的实现与证据；同盘快速移动、真实跨设备验证及普通文件插件接管已在[第十阶段](<history-mp-move-fast-plugin-20261115.md>)继续完成，下面的同盘复制描述是当时状态。

## 本轮交付

远程 main 再次 fetch 后仍为 `4a222f0fa0c06848e7924e6614654a38e999fb19`。继续原工作树；未提交、推送或部署，不操作旧系统、生产 NAS 或下载器。

本地原生 `move` 已从字符串/历史样本变成实际操作：普通文件、关联字幕和蓝光成员均可完成目标发布与源文件移除。共用手动弹窗和目录规则现在提供「移动」，历史显示中文方式。全局默认仍是硬链接，未恢复全局整理设置，也未增加必选预览。

## 执行与 MP 对照

- [MP 四模式分派](</Users/accidia/projects/MoviePilot/app/modules/filemanager/transhandler.py#L690-L708>)调用对应存储操作。本轮的[原生移动](<../internal/app/import_move.go>)复用现有复制暂存/发布收据，校验目标后再从源同目录隔离并删除源。`pending → quarantined → completed` 保存在同一收据，没有新建清理任务平台。
- 当前本地移动**同盘也仍复制发布再清源**，不是 MP 的同盘 rename 快速路径；因此同盘空间估算、IO 和中间状态时序尚未完全对齐。下一阶段继续优化，不能据源最终消失就宣称这一层 1:1 完成。
- [目录移动](<../internal/app/import_move_directory.go>)先完成全部成员发布及完整源快照核对，再按子收据移除成员；恢复阶段不再拿已被自身改变的整目录摘要阻断后续清理。与 [MP 递归成员行为](</Users/accidia/projects/MoviePilot/app/modules/filemanager/transhandler.py#L948-L1003>)一致，移动不顺便删除源端空目录容器。
- 目标发布/清源失败会保留事实和目标；冷打开后继续原收据。已经完成的移动不再删除后来重建的同名源。
- 已经复制/硬链接过的同源文件，不会仅因匹配既有资产就被记成已完成移动；新移动仍走实际覆盖策略，不覆盖时明确失败并保留两端。

## 原版回调与历史

源在移动后消失，回调继续使用预先冻结的 FileItem，不再重新 stat 旧源。主文件和字幕分别绑定各自上下文；原版完成事件仍为 `move`，不是 `copy` 冒充。

实际历史重整发现并修正了原有单条历史的关联文件扫描问题：[计划入口](<../internal/app/import_plan.go>)对 `originalSingleHistory` 不再自动附带未选字幕，预览和正式执行一致。因此单选主文件只移动主文件，单选字幕只移动字幕，多选时每条物理历史只执行一次，保持稳定 ID 与原资源来源。

[删除入口](<../internal/app/history_delete.go>)保留四种选择及原有归属、下载器、在用任务和快照检查：

- 对精确匹配的已完成原生移动收据，旧源明确不存在时仅对账，不执行源删除；这也覆盖字幕重整后旧源位于媒体库的情况。
- 目录移动记录自身造成的空源容器快照，供之后用户明确选择「删除源文件」使用；新文件出现或快照变化仍保留源。
- 源仍存在且位于旧库路径时，必须有冻结的原受管资产/视频及当前根路径证明，不把整个库根变成可删源。

## 验证

1. [低层移动](<../internal/app/import_move_test.go>)：真实字节、SHA、延迟清源、取消/变化保留、隔离后 SQL 失败与冷恢复、同名新源保护。
2. [手动入口](<../internal/app/import_move_flow_test.go>)与[目录恢复](<../internal/app/import_move_directory_test.go>)：预览不移源；实际提交移动主文件、字幕及蓝光；目录清源中途停止后冷打开继续；源空容器保留。
3. [实际 SDK](<../internal/app/import_move_sdk_test.go>)：独立 transfer-events worker 收到主文件/字幕完成事件各一次；冻结源项、实际目标和 move 方式正确；资产 SQL 失败后同任务/计划恢复，不重复事件或触碰重建源。
4. [实际历史与自动任务](<../internal/app/import_move_history_test.go>)：主文件单选、字幕单选、两者多选、稳定资源继承、四种删除代表组合；启用目录规则经过轮询、稳定性、organize_file 和 import 实际移动。
5. [目录及重整后删除](<../internal/app/import_move_history_directory_test.go>)：空源删除、两端删除、新文件保留、错误收据、根变化和主/字幕旧源已消失等 9 个场景。
6. 历史/导入/目录/库存/质量/元数据/实际 SDK 合并回归通过 **103.120s**；收尾修改后的移动与删除回归通过 **18.051s**。日志：[合并回归](<../work/history-mp-alignment-20261115/round9-integration.log>)、[收尾回归](<../work/history-mp-alignment-20261115/round9-move-final.log>)。这不是全应用全部外部进程测试。
7. 前端 **635/635** 通过，类型检查及生产构建通过；全仓仅编译检查及 Linux/amd64 应用测试交叉编译通过。没有执行 Linux 二进制或生产文件系统实验。

## 真实浏览器证据

使用独占 loopback 实例、真实构建网页和真实队列，提供方数据及两个 25 字节文件均为合成样本，不是可播放影片或生产库。通过历史页勾选两条 → 共用弹窗选择移动/目的路径/剧集组/复用历史 → **直接立即整理**，没有先执行预览。

- [移动表单](<../testdata/history-mp-alignment-20261115/native-move-form.png>)展示原有控件布局与新方式；该截图采于随后启用「复用历史」之前。
- [刷新后的结果](<../testdata/history-mp-alignment-20261115/native-move-history-final.png>)为两条移动成功，自动关闭弹窗，仍为原稳定历史 ID。
- [历史 API 实录](<../testdata/history-mp-alignment-20261115/native-move-browser-history.json>)与[文件系统/SQLite 核验](<../testdata/history-mp-alignment-20261115/native-move-filesystem-evidence.json>)证实两源消失、目标字节和 SHA 相符、收据 completed；展示 S01E01/E02 对应标准库存 S04E16/S00E02。

验证结束已停止测试实例、独占 Chrome 和控制 daemon，清理测试数据库、合成媒体、浏览器 profile 和本轮测试二进制；证据在清理前采集。SDK 工人继续供未完成的整体目标复用。

## 尚余范围

- 同盘快速移动与跨盘回退、空间估算对齐；目前未验证真正跨文件系统部署。
- 自定义存储对象的 `storage.Source.move` 尚未接入，当前返回明确错误，不偷偷调用其 copy。直接接管 transfer 的插件真实移除源后，结果收养仍须适配，不等同于本轮原生+事件观察 SDK 的验证。
- 原生 softlink、媒体来源及 provider-native 网页历史继承、最终整体联调。

整体目标继续进行，尚不宣布所有模式或全部高级功能 1:1 完成。

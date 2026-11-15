# 第十二阶段：目录成员的真实 storage.move

继续原工作树，重新 fetch 后 HEAD/origin/main 均为 `4a222f0fa0c06848e7924e6614654a38e999fb19`。原版项目只读；无提交、推送、部署或现有媒体/下载器改写。

## 实际接通

上一阶段的普通视频/字幕 storage.move 现扩展至蓝光目录成员，复用原队列、父目录收据和每文件发布收据，不新增任务平台。

- [目录冻结与验证](<../internal/app/import_move_storage_directory.go>)在第一次破坏性调用前持久保存原完整树、存储实例/key、提供方的全部文件顺序、名称映射和 FileItem。用原 SourceSnapshot 校验树证明，列表覆盖所有物理文件；遗漏、重复及文件/子目录名称碰撞在移动前处理。
- [目录发布](<../internal/app/plugin_original_transfer_directory_publish.go>)随后逐成员调用真实 storage.move，而非调用 copy/link。每个尚未移动文件仍与原文件快照/SHA核对；父层只核对原根 inode，不再要求被本任务逐步清空的整树摘要保持不变。
- [存储发布器](<../internal/app/import_move_storage.go>)沿用封存返回结果、自有暂存内容验证、排他发布及 completed 收据。目录成员使用提供方冻结的完整 FileItem，包含与实际 basename 不同的自定义 Name。
- 已完成成员恢复只核验既有目标，不重新执行存储 move，也不读写后来同名新源；未完成成员继续冻结顺序。单一原根历史、结果回调及蓝光 STREAM 大小口径保持。
- 移动后仍保留源 BDMV/STREAM/CERTIFICATE 等空容器；有新文件时不把原根认作可删除的空树。原生 move 仍沿用原有先发布全部成员、再清源的路径；只有自定义 storage.move 的实际破坏性成员调用采用上述处理。

## 必要恢复边界

目录守卫原先只认 staged/published 子收据。当提供方已移动文件且返回结果已封存，但 staged 快照尚未提交时，真实 stage 会被误判为未知文件，阻断原有恢复入口。

本轮让父守卫仅在子收据 ID、原源/目标、原文件快照、存储实例/key、确定 stage 名称及封存返回结果均匹配时，复用相同的完整 SHA/缺源验证，允许精确 stage 进入原发布恢复。未封存结果的已开始调用仍不盲目重放；没有泛放所有隐藏临时文件。

## 验证与边界

[实际独立 SDK 测试](<../internal/app/import_move_storage_directory_sdk_test.go>)验证：全成员真实移动、首成员完成后取消并冷打开恢复、重建同名源保留、自定义目录/文件名映射；逐源 move 恰一次，没有 copy/link 冒充。另验证提供方返回已封存但 staged 尚未确认的取消恢复边界。

[清单单元测试](<../internal/app/import_move_storage_directory_test.go>)验证完整来源覆盖和目标名称冲突；没有扩大为竞态测试矩阵。

真实 SDK 四分支通过 **4.783s**，日志见[成员移动与冷恢复](<../work/history-mp-alignment-20261115/storage-directory-move-sdk.log>)。目录方式前端契约测试 **7/7** 通过，仍未开放未接通的软链接选项。

扩大回归时发现旧原版对照样本的 `native_adaptation.target` 仍停留于只支持 link/copy/auto，导致已实际接通的 move 声明出现三项期望失配。本轮仅同步该显式目标适配和对应 native/fallback 期望，保留原版字典、源码摘要及其余原版分派样本不动；[实际 SDK/HTTP 能力契约](<../internal/app/plugin_original_transfer_types_test.go>)全部 10 个分支复核通过，没有借样本调整开放软链接。

另在扩大回归中复现既有目录准备测试的跨秒假设：启动恢复把父任务 run_at 更新为当前秒，已到期子任务可先被工作器领取；原测试却要求父任务永远先领取。仅在该测试中显式把子任务预约至父重放完成后再执行，保留实际 claim/safeRun/文件验证，没有为测试改变生产调度顺序、加入 sleep 或新增竞态矩阵。

[最终扩大整合回归](<../work/history-mp-alignment-20261115/round12-integration-verified.log>)通过 **242.366s**：覆盖历史、自动/手动、导入、目录、库存、质量、元数据及整个 OriginalTransfer 前缀的 SDK/回调/存储/方式契约测试，而不仅本轮新增用例。全仓仅编译检查、Linux/amd64 测试交叉编译及 `git diff --check` 通过；未执行 Linux 二进制。原路径 SDK worker 已复用，无重复 worker 或本轮新增挂载卷，交叉编译二进制已删除。

[结构化证据](<../testdata/history-mp-alignment-20261115/directory-storage-move-evidence.json>)。初次扩大回归中的失配及修正均保留本轮日志；最终通过日志另存，不覆盖前次失败事实。

本阶段没有产品前端改动，也没有新增浏览器验收。软链接尚未完成链接条目/引用文件双重快照、资产、历史删除/重整、目录和刮削链路；探索过的低层原型未通过完整执行验收，已撤出产品代码及选择项，仅在工作区保留实验记录。不能把创建符号链接等同于产品软链接整理已实现。

整体目标继续：接着补齐软链接、provider-native 网页与历史身份继承，以及最终整体验证。

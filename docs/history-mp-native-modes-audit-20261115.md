# 整理模式与历史重整：MoviePilot / LM 源码核查

日期：2026-11-15。

这是第八阶段、移动实现前的只读审计。后续原生本地移动已在[第九阶段](<history-mp-native-move-20261115.md>)接通并实测；本报告保留当时发现的问题。自定义存储/模块接管移动和软链接的剩余项仍见后续报告。

## 证据口径与结论

本轮为**源码只读核查**：读取本地 MoviePilot 参考源码与 LM 工作树，未启动整理实机实验，未验证部署版本、浏览器实际选项、NAS 文件系统或插件真实移动行为。下列“已有”指当前源码存在对应分支，不等同于本轮运行验收。MoviePilot 参考项目未修改。

| 模式 | MoviePilot 本地到本地实现 | LM 原生发布链现状 |
| --- | --- | --- |
| copy | 调用存储 `copy` | 存在复制、暂存、内容校验和发布收据路径 |
| link / hardlink | 调用存储 `link` | 插件边界名称 `link`，Go 发布器使用 `hardlink` |
| move | 调用存储 `move` | 原生发布器未实现；历史成功 move 的源路径选择已处理 |
| softlink | 调用存储 `softlink` | 原生发布器未实现；普通文件快照及打开方式也未适配链接对象 |
| auto | 取决于上游选择及存储能力 | 属于方式解析/硬链接失败转复制路径，不是第三种物理发布操作 |

**核心结论：补下拉选项或透传字符串不足以交付 move / softlink。** 发布、受管归属、恢复、旁车、目录递归与历史重整需一起核对。本轮跨季库存改动不包含这两种模式的实现。

## 1. MoviePilot 的实际函数位置

文件整理实现位于 [transhandler.py](</Users/accidia/projects/MoviePilot/app/modules/filemanager/transhandler.py>)，不是 [chain/transfer.py](</Users/accidia/projects/MoviePilot/app/chain/transfer.py>)：

- [本地四模式分派：690–708](</Users/accidia/projects/MoviePilot/app/modules/filemanager/transhandler.py#L690-L708>)：`copy`、`move`、`link`、`softlink` 分别调用对应存储操作。
- [本地到远端 move：732–744](</Users/accidia/projects/MoviePilot/app/modules/filemanager/transhandler.py#L732-L744>)：上传成功后删除源文件，说明 move 的源生命周期与 copy 不同。
- [`__transfer_dir_files`：948–1003](</Users/accidia/projects/MoviePilot/app/modules/filemanager/transhandler.py#L948-L1003>)：递归列目录，对普通文件调用 `__transfer_command`，将原/新路径记入结果。
- [`__transfer_file`：1005 起](</Users/accidia/projects/MoviePilot/app/modules/filemanager/transhandler.py#L1005-L1059>)：单文件及关联文件入口，包含 `TransferIntercept` 与目标存在/覆盖判断。

这里仅记录源码分支，未据此推定所有远端存储都具备四种模式。

## 2. LM 入口与原生发布器

- [原生存储能力表](</Users/accidia/projects/light-media/internal/app/plugin_original_transfer_types.go#L10-L16>) 仅声明 `link`、`copy`、`auto`。
- [手动整理弹窗](</Users/accidia/projects/light-media/web/src/components/ReorganizeDialog.vue#L585-L596>) 的静态选项是自动、复制、硬链接。
- [`publishImport`](</Users/accidia/projects/light-media/internal/app/import_run.go#L1082-L1094>) 入口仅接受 `copy`、`hardlink`；其他值直接返回错误。
- [暂存到正式文件的发布](</Users/accidia/projects/light-media/internal/app/import_run.go#L1295-L1311>) 使用 `Linkat`，配合原有 planned → created → staged → published 收据和文件指纹。该流程没有“源已移动/已删除”的独立持久事实。

[插件方式名称转换](</Users/accidia/projects/light-media/internal/app/plugin_original_transfer_intercept.go>) 将 hardlink 转为 link；其他名称的字符串透传本身不构成物理实现。

## 3. 插件结果、目录和旁车仍有源保留假设

### 插件结果接管

[插件发布结果核验](</Users/accidia/projects/light-media/internal/app/plugin_original_transfer_module.go#L190-L250>) 在插件返回成功后仍读取源快照，并要求它与原快照一致；提交前再次验证源。真实 move 若已移除源，即使目标存在，也会被这些检查判定为源变化。因此，“插件声称支持 move”尚不足以贯通 LM 的归属登记。

### 目录发布

[目录发布入口](</Users/accidia/projects/light-media/internal/app/plugin_original_transfer_directory_publish.go#L137-L158>) 首先验证整个源目录冻结快照；[每个子文件的检查](</Users/accidia/projects/light-media/internal/app/plugin_original_transfer_directory_publish.go#L230-L249>) 再验证整个源目录，并调用同一个 `publishImport`；[结束阶段](</Users/accidia/projects/light-media/internal/app/plugin_original_transfer_directory_publish.go#L302-L317>) 仍要求整个源目录未改变。

若仅把子文件发布改成移动，第一个源文件消失就会改变目录摘要，后续子文件及冷恢复会遇到源变化。目录 move 需要可恢复的成员级迁移进度，而非简单沿用“整目录始终不变”。

### 关联文件

[`originalTransferSideFile` 与 `publishOriginalTransferSide`](</Users/accidia/projects/light-media/internal/app/plugin_original_transfer_auxiliary.go#L10-L25>) 从主文件派生旁车任务，继承方式并逐物理文件执行。因此字幕/音轨的源生命周期、失败重试及已成功旁车归属也要一起适配。

## 4. softlink 需要链接对象与目标内容的双重身份

[文件快照](</Users/accidia/projects/light-media/internal/app/import_fs.go#L20-L33>) 记录设备、inode、大小、修改时间，仅接受普通文件；[路径打开](</Users/accidia/projects/light-media/internal/app/import_fs.go#L74-L128>) 使用 `O_NOFOLLOW` 并再次检查普通文件类型。

要支持软链接，需分别记录并校验：

1. 链接条目本身及其链接文本；
2. 被引用内容的身份、允许路径与存在状态；
3. 删除时只移除受管链接条目，不把被引用的下载源当成受管目标删除；
4. 重新整理与旧目标清理时，防止新链接指向随后被清理的旧受管目标。

现有普通文件 `Snapshot` 不能直接同时代表链接条目和 referent。

## 5. 历史重整已有部分行为，但不是 move 发布证据

[`transferHistoryItem.organizeSource`](</Users/accidia/projects/light-media/internal/app/transfer_history.go#L157-L164>) 已实现：仅成功 move 且目标非空时从目标继续，其余使用源路径。此逻辑服务于历史入口，不表示原生 move 已实现。

两处容易误读的测试：

- [历史 move 样本](</Users/accidia/projects/light-media/internal/app/history_mp_alignment_test.go#L36-L51>) 人工设置 move 状态、写目标并删除源，用于验证历史源路径选择；没有执行 move 发布器。
- [名称含 Move 的手动重整测试](</Users/accidia/projects/light-media/internal/app/organize_manual_test.go#L151-L171>) 实际请求 `Mode: "copy"`，随后运行 `reorganize_cleanup`；验证的是复制新受管目标后清理旧目标，并保留原下载源。

[重整提交及清理](</Users/accidia/projects/light-media/internal/app/import_reorganize.go>) 依赖新受管文件已发布、旧资产归属和快照可核对。未来 softlink 模式还需防止清理打断新链接；未来 move 模式需区分下载源与旧发布目标的迁移授权和持久进度。

## 后续实现验证清单（本轮未执行）

- 单文件及关联文件的 copy / hardlink / move / softlink 行为与源保留结果。
- 同盘移动、跨盘移动、取消、中断、提交失败及冷恢复，避免重复删除源。
- 链接条目被替换、引用目标变化或消失、外部链接及清理范围。
- 目录成员部分完成后的恢复，以及旁车成功而主文件失败的归属。
- 历史单条/批量重整、稳定记录 ID、成功 move 的目标继承和删除选择。

本报告不把 HTTP 成功、测试名、能力表或历史样本当成部署功能通过。

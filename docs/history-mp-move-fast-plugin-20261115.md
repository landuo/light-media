# 第十阶段：同盘移动、跨文件系统与插件接管

本页保留第十阶段实现及当时边界；存储对象普通文件移动、目录 transfer 接管和复制属性已在[第十一阶段](<history-mp-move-storage-directory-20261115.md>)继续实现。目录 storage 成员仍待接入。

## 已交付

继续原工作树；再次 fetch 后 HEAD/origin/main 均为 `4a222f0fa0c06848e7924e6614654a38e999fb19`。未提交、推送或部署，没有操作现有媒体、下载器或生产卷。

### 原生移动不再同盘复制整部影片

[移动发布器](<../internal/app/import_move.go>)根据真实源/目标设备选择物理路径：

- 同盘：发布原 inode，再清理旧源名。不复制内容，不为同一个 inode 重复读完整 SHA；保留原有 inode、大小、修改时间及路径核对。
- 跨盘：复制内容、核对 SHA 后清源。源链接返回 EXDEV 或链接失败时也可回退复制，实际复制前仍检查空间。
- [最终发布](<../internal/app/import_run.go>)对移动收据改用排他 rename 消费自有暂存文件，避免复制回退的最后一步又依赖硬链接。普通 copy/hardlink 的既有发布方式不变。
- 本项目仍有“发布原 inode → 持久确认 → 清源”的阶段，**不是 MP 一次 `shutil.move` 的全部瞬时行为相同**。这样保留原有恢复收据及蓝光成员延迟清源，不增加另一套任务机制。
- 老移动收据没有 `publication` 字段时仍按原复制路径恢复；新字段不会改变普通 copy/hardlink 收据。

[空间估算](<../internal/app/import_preflight.go>)不再为同盘移动预留第二份视频/字幕内容。目录逐成员看设备号，包含嵌套挂载的情况；仍保留目录项、元数据等既有开销预算。复制回退继续在执行时核验真实剩余空间。

对照：[MP Local.move](</Users/accidia/projects/MoviePilot/app/modules/filemanager/storages/local.py#L320-L345>)、[复制与目标权限/时间戳](</Users/accidia/projects/MoviePilot/app/modules/filemanager/storages/local.py#L218-L260>)。同盘实现已消除上一阶段全量复制差异；跨盘仍沿用本项目原复制发布器，其时间戳和目标默认权限策略还需继续逐项核对，未据字节正确宣称所有文件属性完全一致。

### 普通文件的真实插件 move 收养

[插件移动收养](<../internal/app/plugin_original_transfer_move.go>)不再要求成功移动之后的旧源还存在：

1. 真正调用 transfer 提供方之前，核对源快照并把源 SHA 与调用开始状态一起持久保存。
2. 插件返回后检查声明方式确为 move、目标在允许根内、目标大小/完整内容符合原源，且旧源确实消失。
3. 保存 `publication=plugin / state=completed`，不冒用原生清源，不调用插件 copy 假装 move。
4. 发布后恢复只验证目标，不重新调用插件，不接触后来重建的同名源。

实际覆盖普通视频及字幕。目录 transfer 接管尚未完成：插件复制目录却声明 move 会明确报错，不认领资产、不记成功历史；提供方返回 None 的原生 fallback 和已完成的原生目录重放仍可用。

## 实测范围

- [原生快速/回退测试](<../internal/app/import_move_fast_test.go>)：真实 64 MiB 稀疏视频、8 MiB 字幕及蓝光；从手动入口执行，源消失，目标保留原视频 inode，媒体内容不计第二份空间；EXDEV/不支持链接回退实际复制字节；旧收据仍能完成。
- 排他 rename 发布后、SQL 确认前失败：自有暂存名已消失但目标/原源均在，冷打开从 staged 收据认回目标并继续清源，不重新复制。
- 使用本任务新建的 **64 MiB HFS+ 磁盘映像**验证实际跨设备移动，不是仅注入 EXDEV；源和目标设备号不同，1,769,499 字节目标与 SHA 相符，源消失。不是 NAS/NFS/SMB 或生产媒体测试。
- [真实独立 SDK](<../internal/app/plugin_original_transfer_move_test.go>)：主/字幕成功、资产 SQL 失败后的同计划恢复、同大小错误内容、源未移除，以及目录 copy 冒称 move。封存结果重试不会再次执行插件。
- 本轮没有前端变更，没有新增浏览器验收；沿用上一阶段已接通的移动选项。没有把协议返回或 HTTP 200 当作文件移动证明。

历史、导入、库存、目录、质量、元数据与实际 SDK 合并回归通过 **119.211s**；最终排他发布修改后的原生移动/模块接管回归通过 **31.808s**，包含真实不同设备测试。全仓仅编译检查及 Linux/amd64 应用测试交叉编译通过，未执行 Linux 二进制。

[结构化证据](<../testdata/history-mp-alignment-20261115/move-fast-plugin-evidence.json>)、[合并日志](<../work/history-mp-alignment-20261115/round10-integration.log>)、[最终移动/模块日志](<../work/history-mp-alignment-20261115/round10-move-final.log>)。新建卷已卸载，映像、挂载目录与交叉编译二进制已清理；SDK worker 原路径已更新，未留下重复版本。

## 仍需继续

- 自定义 `storage.Source.move`，以及目录级直接 transfer 接管，尚未接入完整执行/恢复；当前明确返回未支持，不误报成功。
- 跨盘复制的时间戳和目标默认权限等属性对齐。
- 原生软链接、媒体来源/provider-native 网页历史身份继承及最后整体验证。

整体目标继续；本轮不是所有模式、所有插件接管、所有文件系统已 1:1 完成的声明。

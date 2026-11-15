# 第十三阶段：原生普通视频、字幕软链接生命周期

本页保留第十三阶段实现和当时边界；普通订阅完成、共享库存及当前V3覆盖读者已在[第十四阶段](<history-mp-softlink-consumers-v3-20261115.md>)继续接通。目录与方式UI仍未开放。

重新 fetch 后继续原工作树：HEAD/origin/main 均为 `4a222f0fa0c06848e7924e6614654a38e999fb19`。原 MoviePilot 只读；未提交、推送、部署或改写现有媒体/下载器。

## 本轮实际实现

对照 [MP 本地软链接](</Users/accidia/projects/MoviePilot/app/modules/filemanager/storages/local.py#L358-L367>)，原生普通文件使用真实 symlink，保留源实体；不是复制或硬链接。

### 发布、登记与恢复

[软链接实现](<../internal/app/import_softlink.go>)将两个身份分开：

- `StageSnapshot` 和 `managedFile.Snapshot` 是实际链接条目的设备、inode、大小、mtime；不是源文件的快照。
- `Softlink` 证明记录规范引用路径和普通引用实体的冻结快照。只有精确收据/受管记录允许检查该链接；普通 importOpen、snapshotPath、browserStat 仍拒绝跟随任意链接。
- `Symlinkat` 创建确定 stage，保存其真实条目后，通过现有排他 rename 发布。staged 或 rename 后 SQL 确认失败可以从条目证明恢复；只有文本相同、没有持久 inode 证明的未知 stage 不被认领。
- [执行器](<../internal/app/import_run.go>)把证明写入主文件和字幕登记；资产 Size 仍是实际媒体字节。普通 copy/move 不因此获得隐式解引用能力。
- [原版结果投影](<../internal/app/plugin_original_transfer_module.go>)在资产提交前从真实发布收据生成目标 FileItem，目标路径/名称属于库内链接，大小与时间取核对后的普通实体；保留完整主文件/字幕历史和回调路径。

### 删除、重整与来源保护

[生命周期实现](<../internal/app/import_reorganize.go>)、[资产删除](<../internal/app/asset_manage.go>)和[历史目标删除](<../internal/app/history_auxiliary.go>)携带同一证明，隔离、复核、unlink 都操作链接条目：

- 仅媒体库目标删除保留源实体；断链也按真实 entry 清理。
- 两端删除继续先目标、后源；源端仍接受原来源、快照、归属和下载器检查，软链接没有额外放大源删除范围。
- 同文本的新 inode 不等于旧受管链接。新建替代条目保留并报明确原因。
- 从受管链接重新整理时直接引用已核对的普通 referent，不创建指向待清理旧链接的链中链；保留原资源来源。
- 若普通旧目标是唯一可用实体且随后必须清理，预检保留旧结果，不先生成悬空新链接，也不暗建 backing-file 平台。
- 原位软链接更正复用已核对的实际条目，不把旧普通文件或旧链接错误地当成另一种整理方式成功。

### 刮削

[刮削媒体检查](<../internal/app/asset_scrape.go>)和[旁车写入](<../internal/app/plugin_original_metadata_write.go>)仅接纳精确受管证明。NFO/图片仍以库内 `asset.Path` 计算，保持 nofollow 旁车发布，不能写回资源实体目录。

元数据存储列表另区分“条目存在”和“可刮削视频”：已受管字幕/音频和断链仍是目录中的真实条目，不因父视频删除或 referent 缺失被当作空目录。只有核对成功的视频 referent 进入媒体刮削；不放开普通文件管理的任意 symlink。

## 验证

[实际文件/SDK/刮削测试](<../internal/app/import_softlink_test.go>)与[生命周期测试](<../internal/app/import_softlink_lifecycle_test.go>)合跑通过 **6.956s**：

- 真实主文件、字幕链接，真实 entry 与实体快照分离，源字节保持。
- 发布后 SQL 确认失败、关闭应用后的冷恢复。
- 资产 SQL 提交失败后关闭应用、更换独立 SDK PID，按原计划恢复，主/字幕成功回调各一次。此处先等待在途观察者正常投递再优雅关闭，验证的是冷资产恢复，不宣称覆盖进程突然死亡时的事件投递保障。
- 实际完成导入任务、领取已生成的刮削任务，NFO 出现在库内链接旁，资源目录没有新 NFO。
- 目标删除、两端删除、断链清理、同文本新 inode 替换保护。
- 旧链接到新链接的实际 cleanup、原位更正、原来源优先与唯一实体保护。

[本轮软链接日志](<../work/history-mp-alignment-20261115/round13-softlink-final.log>)。既有历史删除与重整回归也通过（7.230s）；[扩大整合回归](<../work/history-mp-alignment-20261115/round13-integration.log>)通过 **252.950s**，包含历史、自动/手动、原有移动/复制/硬链接、目录、元数据、库存质量、真实SDK及本轮软链接测试。全仓仅编译检查、Linux/amd64测试交叉编译及diff检查通过，未执行Linux二进制；交叉编译产物已清理，无本轮新增服务器/浏览器或挂载卷。

[结构化证据](<../testdata/history-mp-alignment-20261115/native-softlink-files-evidence.json>)。原有库存质量测试通过不代表新软链接已接通所有库存/洗版读取路径，具体剩余项仍见下节。

这些是独占合成材料、实际 Go 服务/文件系统与独立 SDK 进程验证，不是生产 NAS/可播放媒体实测。

## 明确未完成

- 目录软链接、storage actor/external transfer 接管、外部 delete_file override 的软链接删除还未接通；相应路径保持明确失败，不静默跳过提供方或冒称支持。
- 库存/订阅完成/洗版等仍有普通文件快照读入口需接入受管链接证明，须在整个模式开放前完成核对。
- 本轮没有开放软链接选择项，没有产品前端改动或新增浏览器验收；不把原生普通文件后端闭环说成整个软链接模式已交付。
- 接着完成剩余软链接消费者、目录及网页选项，再继续 provider-native 网页历史身份继承和最终整体验证。整体目标继续。

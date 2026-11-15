# 第十六阶段：普通本地文件的外部 transfer 与源存储软链接

后续[第十七阶段](<history-mp-softlink-external-storage-directory-20261115.md>)已继续接通目录外部结果认领与source actor逐成员软链。本页保留第十六阶段当时边界；delete_file、已有目录覆盖和UI仍需继续。

重新 fetch 后继续原工作树，HEAD/origin/main 均为 `4a222f0fa0c06848e7924e6614654a38e999fb19`。原版只读；无提交、推送、部署或生产媒体、下载器操作。

## 原版分流依据

MP TransHandler 对 softlink 调用 **source storage** 的 `softlink(fileitem, target_file)`，Local 最终 `dest.symlink_to(src)`。原版云盘实现并非都支持该操作：Alist/Rclone/AliPan/115 空实现，SMB 不声明该能力。get_transfer_types 的标签或提供方返回 true，不是链接已产生的证据。本轮只实现能核验真实普通源文件与本地目标的分支，不引入隐藏 backing-copy，不冒充云盘/跨存储软链接。

## 外部 transfer 普通文件认领

[外部软链实现](<../internal/app/plugin_original_transfer_softlink.go>)在真实模块调用开始前冻结：

- 普通源的原计划快照；精确授权目标原不存在事实；真实父目录 inode。
- 调用许可 `OriginalSoftlink` 与已完成 entry 的 `Softlink` 证明分开，native 完成路径不被误要求外部 Adopted 标志。

结果必须显式声明 `transfer_type=softlink` 和 local/file 的 target_item，且目标与调用前冻结目标相同。host 验证真实链接 inode、精确绝对引用文本、引用实体快照和父目录身份，sync 后复核，再发布现有 receipt/managedFile 所需证明。

[模块分发](<../internal/app/plugin_original_transfer_module.go>)沿既有封存结果恢复，不重新调用已记录的 provider。普通 copy、硬链接、大小相同、仅声明成功均不构成软链。调用前已存在的 link 不借新任务获得认领权限。缺预调用证明的目标变更或目录外部结果仍明确失败，不静默认领。

## Source storage actor

[源存储发布](<../internal/app/import_softlink_storage.go>)沿原 source actor 分流：

1. 校验真实冻结普通源、目标/stage 空缺及已选本地 actor 的可调用 softlink 方法。
2. 持久 `StorageSoftlink` 调用身份 marker，再调用 source actor；保留完整冻结 FileItem。
3. `Path` 是逻辑完整目标，`NewName=nil`，`PublicationPath` 是 host 授权的确定 stage。
4. 封存真实结果；校验实际 stage 是引用该普通源的软链，源仍未改变；记录真实 staged inode，再复用现有排他 rename 发布。

缺已持久结果不会重调 actor，也不会偷偷改用 native。封存结果存在但 staged 快照尚未确认时，可从真实 entry 与 referent 恢复并继续原子发布；普通文件拷贝不能通过该核验。Target 侧已有 get_folder/get_item 流程保持，创建链接的操作仍属于 Source。

## 真实验证

[外部 SDK 测试](<../internal/app/plugin_original_transfer_softlink_test.go>)从实际 provider 调用验证视频＋字幕、资产 SQL 失败后的 App 冷恢复且不重调、copy 冒充、以及真实模块入口对调用前已有 link 的保护。配合旧完整性/native softlink/目录/move 回归，**13.193s** 通过，见[外部回归](<../work/history-mp-alignment-20261115/softlink-external-regression.log>)。

[源 actor SDK 测试](<../internal/app/import_softlink_storage_sdk_test.go>)通过 **5.732s**，见[存储软链测试](<../work/history-mp-alignment-20261115/round16-storage-softlink.log>)：

- 实际独立 SDK actor 为视频、字幕创建 stage，host 发布真实链接；完整 FileItem、逻辑/stage 参数、媒体字节和每源一次调用均核对。
- 在真实 actor 返回且封存结果写入后取消任务，此时物理 stage 已存在但尚无 staged 快照；关闭重开 App，更换 SDK PID，重试复用原 stage inode，不重调已完成 actor。
- actor 返回 false 或实际 copy 时没有 native 回退、没有目标发布或资产认领，源字节保持。

复用既有 worker 路径重建，没有积累版本副本。以上为独占合成材料＋真实本地 Go/SQLite/文件系统/独立 SDK，不是 NAS、生产或完整新增 softlink 下载协议实测。

[扩大整合](<../work/history-mp-alignment-20261115/round16-integration.log>)通过 **271.195s**，包含全部新增普通文件提供方/源 actor 测试及旧 native、目录、历史、V3、元数据等回归。全仓仅编译检查、Linux/amd64测试交叉编译及diff检查通过；未执行Linux二进制，交叉产物已清理。[结构化证据](<../testdata/history-mp-alignment-20261115/softlink-external-storage-files-evidence.json>)。

## 继续项

本轮只扩普通本地视频/字幕，目录 storage/外部 transfer、delete_file 接管、已有目录合并覆盖、文件管理精确 owned 链接及方式 UI/浏览器验证、provider-native 普通网页历史继承仍需继续。native 目录既有生命周期保留；未开放软链接选项，无前端变更。全局默认继续固定硬链接，整体目标仍进行中。

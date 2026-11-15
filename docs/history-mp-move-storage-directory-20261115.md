# 第十一阶段：存储对象与目录移动、复制文件属性

本页保留第十一阶段实现和当时边界；当时尚未接入的目录成员 storage.Source.move 已在[第十二阶段](<history-mp-directory-storage-move-20261115.md>)接通并验证。

继续原工作树，重新 fetch 后 HEAD/origin/main 仍为 `4a222f0fa0c06848e7924e6614654a38e999fb19`。本阶段没有提交、推送、部署或生产媒体操作。

## 已接通的实际执行

### 自定义存储对象：普通视频及字幕

[存储移动发布器](<../internal/app/import_move_storage.go>)实际调用所选对象的 `move(FileItem, targetDir, newName)`，实际写入位置仍为调用契约中的自有暂存路径，不以 copy/link 代替 move。

- 调用前保存原源快照、SHA 与存储实例/key；调用开始及返回结果持久保存，返回结果封存。
- 校验暂存文件全部内容和原源确实消失后写入 staged 收据，复用现有排他发布，最后记为 `publication=storage / completed`。
- staged/published 恢复不再访问已移动的原源，也不重调存储对象；新建的同名源保留。
- 对象返回失败、内容不符或源未移除均不登记成功资产。调用已开始但返回结果未持久保存时，不盲目重放破坏性调用。
- **本阶段只接通普通文件/字幕。目录成员的 storage.Source.move 仍明确返回未接入**；没有借原生 copy 伪装完整支持。

[实际 SDK 测试](<../internal/app/import_move_storage_sdk_test.go>)核验独立进程、主/字幕各一次 storage.move、完整 FileItem 语义、逻辑目的目录/名称、真实暂存文件、源消失、冷恢复及失败反例。原 copy/link/对象重载回归也通过。

### 外部 transfer 模块：目录真实 move

[目录移动收养](<../internal/app/plugin_original_transfer_move_directory.go>)在调用前冻结完整原目录成员快照与文件 SHA。源根仍使用原 SourceSnapshot，不把移走后的空目录冒充原身份。

- 外部返回必须明确声明 move，并提供逐成员来源/目标清单；按实际允许目标核验成员数量、大小和全部内容，不凭目录根路径自行推断成功。
- 源可已消失，或仍是同一根 inode 且只剩目录。保留空容器，记录实际 MoveSourceSnapshot，供之后用户显式删除源目录核对。
- 目录仍只形成原根的一条历史、一个资产和一次完整结果回调。
- 冷恢复只验证已认领目标，不再要求已移动源保持原树，也不碰重建源。提供方返回 None 的原生 fallback 仍沿用原有原生目录恢复。
- [目录实际 SDK 测试](<../internal/app/plugin_original_transfer_move_directory_test.go>)包含逐成员真实 move、小型附加文件、实际变更目标名称、资产提交失败/换进程恢复、原生 fallback 及错误内容；复制目录却报告 move 的旧反例仍失败。

这与上一节 storage.Source 是两个独立接管层；目录 transfer 已接通，不意味着目录 storage 已接通。

### 原生复制与跨盘移动属性

对照 [MP 复制实现](</Users/accidia/projects/MoviePilot/app/modules/filemanager/storages/local.py#L218-L260>)：

- [原生复制创建](<../internal/app/import_run.go>)采用普通新文件创建的 0666 请求，由目标目录默认 ACL 和进程 umask 决定最终权限，不复制源的私有/执行权限，也不在复制后 chmod 清除继承访问规则。
- [Darwin](<../internal/app/import_copy_times_darwin.go>)及[Linux](<../internal/app/import_copy_times_linux.go>)通过已打开的暂存文件描述符设置源修改/访问时间，不对可被替换的路径执行跟随式时间修改；不引入 Python/CGo 运行依赖。
- [属性测试](<../internal/app/import_copy_attributes_test.go>)实际验证复制及 move-copy 回退的内容、纳秒 mtime、非继承源执行位及与目标普通创建一致的权限。目标后续核验读取可能更新 atime，未宣称读取后 atime 不变。
- 独占 HFS+ 映像再次验证真实跨设备移动，目标字节/SHA与源一致，并保持在两种文件系统均可表示的历史 mtime。映像已卸载删除。不是生产 NAS 或全部远端 ACL 实测。

[历史删除](<../internal/app/history_delete.go>)补齐已完成目录移动的缺源对账：精确完成收据加源 ENOENT 可以无删除地完成对账，不因之后撤销旧来源配置而失败；同名新建目录不获得删除权限。

## 验证

- [整合回归](<../work/history-mp-alignment-20261115/round11-integration.log>)通过 **132.222s**：历史、自动/手动、导入/移动、目录、库存、质量、元数据及实际模块/存储 SDK。
- [真实跨设备与属性/缺源对账](<../work/history-mp-alignment-20261115/round11-cross-attributes.log>)通过 **2.784s**。设备号 `16777229 → 16777237`，1,769,499 字节，mtime `1700000000000000000ns`；完整 SHA 与前阶段同一合成字节样本一致。
- 目录 SDK 回归 **10.138s**；存储移动四分支 **5.272s**，旧存储回归 **6.657s**。
- 全仓仅编译检查及 Linux/amd64 测试交叉编译通过；未执行 Linux 二进制。`git diff --check` 通过。
- 独占卷/映像、交叉编译二进制及两个重复 worker 已清理；保留原路径 worker 供后续目标联调。

[结构化证据](<../testdata/history-mp-alignment-20261115/move-storage-directory-evidence.json>)。无本轮前端修改，也没有新增浏览器验收；保留上一阶段共用弹窗、队列、历史功能。

## 下一步

目录 storage.Source.move 仍需先冻结成员列表并适配逐成员已移动后的原目录校验/恢复；接着继续原生软链接、provider-native 网页/历史身份继承和最后整体验证。整体目标保持进行中。

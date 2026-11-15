# 第十四阶段：软链接订阅库存与当前 V3 覆盖

后续[第十五阶段](<history-mp-native-softlink-directory-20261115.md>)已接通原生蓝光目录逐成员软链接、历史/元数据生命周期；本页保留第十四阶段当时边界。存储/外部接管和UI仍需继续。

重新 fetch 后沿用原工作树，HEAD/origin/main 均为 `4a222f0fa0c06848e7924e6614654a38e999fb19`。原版只读；无提交、推送、部署或生产媒体/下载器操作。

## 普通完成与共享库存读者

[普通订阅完成](<../internal/app/subscription_completion_ordinary.go>)先从调用方传入的 `q` 读取精确受管视频，再核对实际链接条目及普通引用实体。完成依据中的 Snapshot 仍是 entry，Video 携带完整 Softlink 证明；不是把链接文本长度当作媒体大小，也不因链接存在就宣布媒体可用。

该路径会在完成事务里重建依据，因此没有调用内部再次查询 Store.DB 的帮助函数，避免单连接事务重入。外部未受管库存仍使用原普通文件/NFO/人工登记核对，不开放任意 symlink。

[共享订阅快照](<../internal/app/subscription_quality_snapshot.go>)在冻结和下载前复核时同样验证 entry＋referent；冻结的 managedFile 用完整值语义比较，包含 proof，而不是比较反序列化后的指针地址。此处既服务普通订阅下载，也服务洗版，不只 BestVersion 分支。

普通库存显示继续使用原 slots 投影；没有加入媒体库内容 UI、全量扫描或新的后台检查平台。物理核对仍放在原有完成/获取复核边界。

[库存测试](<../internal/app/import_softlink_inventory_test.go>)从真实导入的普通视频/字幕软链接资产和实际保存的订阅开始，验证单连接事务中的完成依据及共享快照复核；删除 referent、用同文本新 inode 替换链接均不再被接受为完成依据。替换测试保留旧 inode，不依赖时序竞争。

普通完成和既有回归通过 **3.675s**，日志见[完成回归](<../work/history-mp-alignment-20261115/round14-completion.log>)。

## 当前 V3 覆盖/洗版

本轮只接通当前主路径，不将新软链接强塞回旧 V1/V2 整组交换、槽位清理流程。当前新 qualityImportProof 固定 Version 3，并复用 directoryOverwrite。

[覆盖读取](<../internal/app/directory_overwrite.go>)读取旧受管归属后核对条目和 referent：

- 策略 FileItem 的大小与时间使用已核对的普通媒体实体。
- 持久目标 Snapshot 仍是实际 entry，Owner 封存完整旧 proof。
- TransferType 传递真实 softlink/copy/move/link，不用复制方式冒充。
- 旧目标隔离、恢复和备份删除只处理 entry；新目标确认仍要求 referent 可用。新引用若依赖待清理实体，沿原 source 保护预检保留，不创建悬空链接。

[覆盖执行与清理](<../internal/app/directory_overwrite_run.go>)、[并行归属](<../internal/app/import_parallel.go>)复用原收据、备份和事务登记，对新 proof 使用值语义。原下载源的普通文件检查保持；未受管 symlink 不因文本或指向相同文件而获得覆盖权限。

[实际 V3 原生 host 测试](<../internal/app/directory_overwrite_softlink_test.go>)验证完整的发布、登记、覆盖及备份清理：

- 旧媒体 3 字节、新媒体 16 字节应允许变大覆盖；旧媒体 8192 字节、新媒体 4096 字节应拒绝降级。两组分别排除误用链接文本长度造成的误拒和误放，且实际核对 referent mtime。
- 旧字幕维持原 entry/proof，并归属到真实新资产；旧主链接 backup 被 unlink，旧/新媒体实体及字幕实体均保留。
- 提供方真实旧 owner 下，referent 临时不可用仍可隔离、重复恢复隔离和 restore，恢复原链接 inode；没有伪造成功收据。
- 执行前旧 referent 缺失则保留旧 link/owner 和新源，不冒称覆盖完成。

[定向及旧方式/并行回归](<../work/history-mp-alignment-20261115/softlink-v3-overwrite-regression.log>)通过 **7.665s**。这是当前 V3 实际调用的原生 host 与合成材料验证，不等于新增 softlink 的完整下载器协议或 NAS 实测。

[扩大整合回归](<../work/history-mp-alignment-20261115/round14-integration.log>)通过 **254.883s**；[联合软链接/库存/V3用例](<../work/history-mp-alignment-20261115/round14-softlink-final.log>)通过 **8.888s**，包括所有本轮新增测试及之前的真实SDK、冷恢复、删除、重整和NFO测试。全仓仅编译检查、Linux/amd64测试交叉编译及diff检查通过，未执行Linux二进制；交叉编译产物已清理，无新服务器、浏览器、挂载卷或重复worker。

[结构化证据](<../testdata/history-mp-alignment-20261115/softlink-consumers-v3-evidence.json>)。本轮无前端变更/新浏览器验收。

## 尚未完成

目录软链接、存储/外部 transfer 接管、delete_file override 的软链接删除、方式选择 UI 与浏览器验证仍需继续；本轮未开放软链接选项。旧 V1/V2 无新 proof 收据保留原限制，未宣称支持先前不存在的软链接旧计划。

后续继续剩余软链接模式及 provider-native 网页历史身份继承，再做最终联调。整体目标保持进行中。

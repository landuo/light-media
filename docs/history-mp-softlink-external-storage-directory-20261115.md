# 第十七阶段：目录外部 transfer 与 source storage 软链接

后续[第十八阶段](<history-mp-softlink-delete-history-provider-20261115.md>)已接通softlink delete_file及普通网页provider-native历史身份继承。本页保留当时边界，已有目录覆盖、文件管理与方式UI仍需继续。

重新 fetch 后沿用同一原工作树，HEAD/origin/main 均为 `4a222f0fa0c06848e7924e6614654a38e999fb19`。原版只读，无提交、推送、部署或生产媒体/下载器操作。

## 外部目录 transfer

[目录认领](<../internal/app/plugin_original_transfer_softlink_directory.go>)复用既有根目录 Softlink proof，但把调用前许可单列为 OriginalSoftlinkDirectory：

- 调用前冻结无任意链接的完整原树、根目标原不存在事实和父目录 inode，保留 STREAM 字段。
- 封存结果必须明确 softlink/local dir/精确根目标，并为原文件提供完整、不重复、双向 file_list mappings；源空目录不成为目标成员。
- 实际目标根及必要父节点须是真实目录，每个叶项必须是真实链接，文本精确指向其映射的原普通文件；copy、hardlink、整根目录 symlink、遗漏或未知成员均不认领。
- 核验、sync、复验后生成聚合真实 entry/目录证明，不伪造 native child receipts；provider 原 FileItem 和名称映射保持。
- 已持久认领后的恢复复核已拥有目标和各原引用，不重复要求整个源目录永久静态，后来无关 source NFO 不使已完成目录失效。目录内已有显式 metadata rebase 的普通成员继续按其原证明核对。

## source storage 逐成员执行

[目录 actor 绑定](<../internal/app/import_softlink_storage_directory.go>)只持久化本次 Source actor 的 instance/key，继续使用原非破坏性源树锁和目录映射，不复制 move 的清源平台。

[目录发布器](<../internal/app/plugin_original_transfer_directory_publish.go>)按实际 provider 列表/名称对每个普通成员调用既有 Source.softlink，保留完整冻结 FileItem、逻辑目标与指定 stage。尚未完成的目录不在恢复时悄悄切换 Source actor 或回退 native。

[暂存核验](<../internal/app/import_softlink_storage.go>)供普通发布与目录进度检查共用：真实返回结果已封存、但 staged inode SQL 尚未确认时，只接纳精确授权 stage 的真实链接/ref，不重调 actor。false 或普通 copy 不会成为软链接资产。

原单一根资产、历史、完整映射、tiny 文件、空源目录保留、源数据字节和 STREAM 语义均沿用；不会把每个 m2ts 拆成独立历史。

## 验证

[外部目录 SDK 测试](<../internal/app/plugin_original_transfer_softlink_directory_test.go>)从实际 provider 执行验证成功、资产 SQL 失败冷恢复、copy 反例及调用前已有目标阻断；零伪造 native child receipts。成功及冷恢复都验证 source 新增无关 NFO 后 provider/callback 不重调，root/leaf 真实类型、单资产/历史、完整 mappings 与源保留。

外部提供方的 root FileItem.size=null 按原结果完整保留，不冒充 native STREAM 输出；STREAM=12 在 receipt/history 独立核对。与旧目录/普通文件完整性回归合跑 **19.690s**，见[外部目录回归](<../work/history-mp-alignment-20261115/softlink-external-directory-regression.log>)。

[source actor SDK 测试](<../internal/app/import_softlink_storage_directory_sdk_test.go>) **4.484s** 通过，见[目录存储软链日志](<../work/history-mp-alignment-20261115/round17-storage-directory-softlink.log>)：

- provider 目录/文件名称重映射保持（BluRay/Streams/feature.m2ts），全部 FileItem 与逻辑/stage 参数保持，成功每源一次 actor 调用。
- 第一个真实 actor 创建链接并返回、结果封存后取消任务；此时尚无 staged 快照。关闭重开 App、更换 SDK PID后，通过目录 guard 接纳原 stage，复用 inode，不重调已完成调用，再完成其余成员。
- copy 冒充失败且无根资产；原源树快照/字节与空 CERTIFICATE 保持；tiny 文件不丢，根历史仍单条。

证据限独占合成材料＋真实本地 Go/SQLite/文件系统与独立 SDK；不扩称生产 NAS、任意云盘或完整新增 softlink 下载协议实测。复用既有 worker 输出路径，没有新增版本副本。

[扩大整合](<../work/history-mp-alignment-20261115/round17-integration.log>)通过 **275.990s**，包含全部新增目录提供方/source actor测试及此前普通/原生目录软链、移动、历史、元数据与V3回归。全仓仅编译检查、Linux/amd64测试交叉编译及diff检查通过；未执行Linux二进制，交叉产物已清理。[结构化证据](<../testdata/history-mp-alignment-20261115/softlink-external-storage-directory-evidence.json>)。

## 继续项

外部 delete_file 的软链接删除、已有目录直接合并/覆盖、文件管理精确 owned 链接、方式 UI 与浏览器验收、provider-native 普通网页身份继承和最后联调仍需继续。本轮未开放软链接选项，无前端改动，整体目标进行中。

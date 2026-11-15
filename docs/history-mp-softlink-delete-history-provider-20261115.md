# 第十八阶段：软链接删除提供方与普通网页历史来源继承

后续[第十九阶段](<history-mp-directory-merge-storage-publication-20261115.md>)已推进非受管既有普通目录的native copy/hardlink成员级合并及消费者，并补齐copy/link source actor调用收据。本页保留当时边界；managed-root/其他mode合并和UI仍需继续。

重新 fetch 后继续同一原工作树，HEAD/origin/main 均为 `4a222f0fa0c06848e7924e6614654a38e999fb19`。原版只读，无提交、推送、部署或生产媒体/下载器操作。

## delete_file 软链接适配

[专用适配器](<../internal/app/plugin_original_history_softlink_delete.go>)扩展已有封存 SourceOperation/browserOperation，不新增另一套公共删除流程，不全局放开 browserStat/普通 manifest 对任意链接的跟随。

- 许可仅来自已冻结 owned entry/root/member 证明，manifest 为 nofollow 的真实 inode/mtime/mode；普通 metadata rebase NFO 成员同样按已知证明核对。
- 沿原 `delete_file` dispatcher 和原 FileItem 调用，资产仍可从精确执行记录取回原 FileItem，即使之前仅删除了可见历史。
- 原版 False 停止保留，None/异常按既有链进入 native fallback。native 只把精确受管 entry/目录 quarantine，后续目录使用已有逐成员 unlink；不碰链接指向的源。
- 真实返回先封存，再核对物理效果；true 但条目未移除是 review，不当作成功。未知已开始但未封存的结果不重复调用。恢复继续原封存返回和原 phase。
- 父目录 FD/当前身份、原 root 和实际剩余条目仍核验。普通文件旧删除链和公开四种删除选项保持。

[history hook](<../internal/app/history_auxiliary.go>)与[asset hook](<../internal/app/asset_manage.go>)仅在 owned softlink 分支接入调用/恢复，不再静默绕过已安装删除 provider。

[实际 SDK 测试](<../internal/app/plugin_original_history_softlink_delete_test.go>)覆盖普通 entry/断链/两端、None native delegate、真实物理删除、False 保留、true 无效果、异常回退、目录已返回结果的 SQL 冷恢复、未知结果不重调，以及实际刮削生成普通 NFO 后、先隐藏历史再删资产的 FileItem 保真。连旧删除/目录生命周期/外部认领等 **13 组回归 30.377s** 通过，见[删除回归](<../work/history-mp-alignment-20261115/softlink-delete-regression.log>)。MP 蓝光删除后的父清理可能对已缺失自身另调一次 delete_file；这与重放已完成 adapter 是两回事，测试分别核对。

## 普通网页 from_history 来源继承

只读 MP `app/api/endpoints/transfer.py:322–340`：from_history 不仅继承 tmdbid，还继承 doubanid/bangumiid/anilistid/media_source/media_id。此前内部聊天/插件 retry 已能使用 provider-native，普通网页仍只继承 TMDB，内部路径通过不等于普通网页验证。

[继承器](<../internal/app/organize_history_batch.go>)现在区分真实 source ID 与 TMDB ID：provider-native 记录恢复其 Source/ID/媒体类型并清除过期表单 TMDB 值和不适用的 TMDB group；TMDB 记录则清除私有 provider identity，仍走原路径。

[普通手动整理](<../internal/app/organize_manual.go>)仅在实际读取已选历史且 from_history=true 后授予私有 `originalHistoryProvider`，不新增可被 JSON 提交的信任字段，不冒用内部 originalRetry。按该来源的精确 ID 经过同一 recognize_media 链取得完整 DTO，验证类型/来源不变后才进入现有 planner；预览与提交都这样执行，目标清理仍在计划核验之后。

[provider import](<../internal/app/import_provider.go>)保留原下载与插件 retry 能力，普通历史分支独立授权。实际后台 prepare → organize_file → import 仍用原持久父子任务，Source/ID 来自每个所选历史，不靠未导出的临时字段跨 JSON 持久化。

[真实普通 HTTP 测试](<../internal/app/organize_history_provider_test.go>) **5.411s** 通过，见[网页来源继承日志](<../work/history-mp-alignment-20261115/round18-history-provider-web.log>)：

- 实际管理员网页登录 Cookie，经 `/api/files/organize` 普通 JSON，未携带任何私有 retry/provider 标志。
- 起始为真实已执行的 provider import：豆瓣电影701、Bangumi电视剧702，SDK按不同source:mediaid返回完整 DTO；不是手造 successful history。
- 可选 preview 不提交/不改原记录与文件；单选立即整理及后台多选真实 prepare/file/import 都完成物理 copy。
- 表单故意带过期 TMDB999，最后仍各自保持 Source/ID、类型和季集，源文件稳定 ID 不合并，选择项最新 attempt 更新、旧目标清理，未选择项保持，源字节不变。

中间测试修正仅是事实口径：普通 API 要 Cookie（插件 API key 不代替网页登录），公开历史 JSON 不含内部 ResourceSource，organize_file 正常交接同一任务到 import 新 generation，而非直接成功。测试 helper 现在带真实 trace context，按实际下一阶段继续执行，不用 SQL 伪造成功。

以上是普通 HTTP+真实 SDK/Go/SQLite/本地 FS，未新增浏览器验收，不扩称所有来源 UI、任意云盘或生产 NAS 实测。

[最终扩大整合](<../work/history-mp-alignment-20261115/round18-integration-final.log>) **298.021s** 通过，包含本轮来源继承/删除接管及之前native/actor/外部目录、历史、V3和元数据回归。全仓仅编译、Linux/amd64测试交叉编译和diff检查通过；未执行Linux二进制，交叉产物已清理。[结构化证据](<../testdata/history-mp-alignment-20261115/softlink-delete-history-provider-evidence.json>)。首轮新增宽前缀误包含无关的class-registry专用SDK样本，该样本缺OWNED_STANDARD_FIXTURE_BINARY（其余业务样本未报失败）；最终整合改为原业务范围加精确的OriginalHistorySoftlinkDelete，不把未配置的类注册样本计作通过。

## 继续项

已有目录直接合并/覆盖、文件管理精确 owned 链接和方式 UI/浏览器、全局来源选择剩余项、最终联调继续。本轮未开放软链接选择项，整体目标仍进行中。

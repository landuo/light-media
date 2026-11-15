# 第二十阶段：shared目录软链接/移动与文件管理真实入口

继续原工作树；重新fetch后HEAD/origin/main仍为 `4a222f0fa0c06848e7924e6614654a38e999fb19`。原MoviePilot及既有服务只读；本轮仅独占合成材料，没有提交、推送或部署。

## 已有普通目录的四种方式

继[第十九阶段copy/hardlink](<history-mp-directory-merge-storage-publication-20261115.md>)，本轮补齐同一**未受管、初始普通**既有根的softlink/move：

- shared根的Softlink仍nil；link entry/ref证明只在实际Members中。原source整树snapshot/成员顺序不被改写成目标owned子集。
- softlink缺失leaf正常调用原publisher/选定Source actor，占用leaf直接失败。已成功前缀保留，旧占用inode/字节不变；没有预删除、备份实物、普通overwrite.check或V3 never/size/latest路径。
- move沿原deferred move、directory Source actor原成员冻结与现有finish清源；实际成功后清两个源叶、保留目录容器，冷恢复不伪造源树仍完整。Shared target只增加成员级核验。
- link-aware guard只认原普通observations及精确child收据/真实returned stage，不把未知新链接纳为owned。聚合Size是媒体引用字节，digest仍保真实link entry身份。
- metadata新NFO为普通owned member；linked NFO默认保留，显式overwrite交换entry而非穿透写source，SQL故障后owner/receipt共同恢复。

[最终实际SDK/FS样本](<../work/history-mp-alignment-20261115/round20-shared-modes-final.log>) **12.939s**：7个真实业务case＋4个单独标识的admission机制单元。业务固定两leaf，覆盖soft成功/NFO/target-delete、第二leaf占用失败前缀、history reorg、Source.softlink returned-stage冷启、native move、Source.move returned-stage冷启、linked-NFO overwrite SQL失败冷恢复。持久actor journal每个leaf一次、stage inode不换、root intercept而非leaf覆盖事件。

旧目录/shared消费者回归 **49.002s**；[外部whole-root边界回归](<../work/history-mp-alignment-20261115/round20-shared-boundaries-r2.log>) **19.490s**。完整只读证据与文件责任见[后端交接](<../work/history-mp-alignment-20261115/round20-shared-modes-handoff.md>)。

### 外部provider边界

同一选定module既可能走native也可能自写文件，不按fixture方法名猜测来源。shared pre-call明确TargetAbsent=false；custom provider真实写出文件但没有native per-leaf receipts时，whole-root adoption以REVIEW_PLUGIN_SOFTLINK_PROOF终止，外部真实效果保留，不制造收据或认领整个旧根。旧“所有调用必须target_absent=true”测试已收窄为此契约。原独占根external成功/counterfeit/cold仍通过。managed既有根/跨资产转移与外部shared-root收养仍留后续。

## 文件管理owned链接入口

[文件管理适配](<../internal/app/file_manager_softlink.go>)仅扩展**真实link entry与持久归属精确吻合**的读入口：

- 普通owned视频/字幕及独占/shared目录中已证明的link member，列表投影为file，附softlink/broken；显示referent媒体字节与时间，不将link文本长度当视频大小。未匹配/被换inode的链接仍为受限symlink。
- native列表和实际StorageChain.list_files模块列表使用同一精确投影。已知断引用保留可见entry和cached媒体大小，但读取/识别/整理报告实际缺失或变化。
- 下载/详情在当前根授权内核entry/ref后，no-follow打开已冻结source并核FD；仍走原download_file选择链及返回路径边界。没有让通用browserStat、manifest或未知链接自动跟随。
- 识别已知链接复用当前资产身份；普通owned链接从文件管理继续复制/硬链接/移动时，走已有resolver绑定原resource/history，而非把软链接路径建成新资源。目录member可读/识别，重新整理所属目录仍走history，不拆成伪独立目录资产。
- 原交互重命名/删除对有效资产的引用限制保留，继续从历史处理；未为软链接绕开既有限制。普通plugin/metadata链的能力没有扩大为任意浏览权限。

实际Cookie HTTP/FS/SDK样本见[文件管理联测](<../work/history-mp-alignment-20261115/round20-browser-owned-link-api.log>)：真实导入→owned/unknown列表→下载真实字节→识别→文件管理copy实际作业→原resource及稳定history；broken和same-text新inode不读；真实目录SDK列表与内容读取。第一次失败是fixture重复创建已有管理员，改为真实login，不计作业务成功。

## 共用方式控件与真实浏览器

[共享弹窗](<../web/src/components/ReorganizeDialog.vue>)增加实际softlink选项，默认仍自动；全局默认仍固定hardlink。未增加全局整理设置、必选预览或媒体库内容页。文件管理标识链接与引用失效；历史转移方式增加中文label。

使用新构建web资源、真实App/SQLite/临时文件、独立SDK进程与真实Chrome CDP，在独占测试实例18081验证：

1. 已实际导入video/字幕，从history打开共用弹窗，选择softlink、自填目标、复用history，实际重整完成并关闭，原video历史ID保留。
2. 文件管理沿真实目录树浏览新目标，实际显示file行、22B、软链接和识别/刮削/整理操作。见[受管链接列表](<../work/history-mp-alignment-20261115/round20-owned-link-file-list.png>)及[方式选项](<../work/history-mp-alignment-20261115/round20-history-softlink-mode.png>)。
3. 该链接行重新整理：copy＋独立新目标，**未点预览**直接立即整理，真实作业成功后关闭；video/字幕仍为原两条resource/history，最新mode为copy、新目标正确。只读结果见[浏览器摘要](<../work/history-mp-alignment-20261115/round20-browser-result.json>)。

这是合成材料真实浏览器/原生SDK联调，不等同用户NAS/云盘/下载器实机验收；未操作已有媒体和种子。没有录制用户个人页面或保存Cookie/Token。

## 验证与下一项

- 扩大Go业务回归 **319.154s**；全仓仅编译、Linux/amd64交叉编译和diff检查通过，Linux二进制未执行且产物清理。
- 全部前端 **636/636** 通过，构建及相关Prettier检查通过。
- managed既有根跨资产成员转移/同根追加、外部shared-root收养、目录规则softlink方式入口及全局识别来源控件仍需继续；当前手动选择项已开放，不再沿用前几阶段“UI未开放”的历史边界。
- 目标继续，不宣称整理历史全量高级行为或MP全量1:1已经完成。

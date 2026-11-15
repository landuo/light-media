# 整理历史剩余选项核对（最终状态）

主流程已按阶段回归；以下是继续实现前确认的差异，不能据已有测试宣布原版全部选项1:1完成。原版相关四个整理模板相对参考HEAD无修改；其他参考工作树文件不动。

## 剧集组

- 原版 [手动弹窗](</Users/accidia/projects/MoviePilot-Frontend/src/components/dialog/ReorganizeDialog.vue#L1413-L1428>) 对TMDB电视剧显示真实剧集组、默认空项、加载状态及组季集数，并随影视ID变更重新获取；请求携带episode_group。
- 第七阶段已贯通真实选择、组识别、标准库存/展示编号分离、命名、字幕、NFO、历史和原版回调；单条/批量历史复用已验证。实际浏览器完成两条合成记录的硬链接与刮削，并保持原历史 ID，详见[实现与证据](<history-mp-episode-groups-20261115.md>)。
- 第八阶段已贯通单物理文件的多个标准季集槽，以及蓝光目录的展示投影、实际发布和刮削；仍保持一物理文件/目录一个资产。没有明确集范围的原盘不推断整季齐全。新增实际SDK、文件系统、历史及订阅库存验证，详见[跨季与目录报告](<history-mp-cross-season-20261115.md>)。

## 集数定位推荐

- 原版 [推荐流程](</Users/accidia/projects/MoviePilot-Frontend/src/components/dialog/ReorganizeDialog.vue#L740-L910>) 按实际源选择、已配置识别规则和API结果生成episode_format；不是任意猜测文件数字。
- 第六阶段已接通独立集数定位规则配置、原文件名规则匹配、原版附加样本兼容判断、共用弹窗推荐及仅回填定位格式；不是复用 CustomIdentifiers 作为规则表。详见[实现报告](<history-mp-episode-recommend-20261115.md>)。
- 第七阶段已补齐多数派/diff自动推导、原生解析交叉校验、体积门槛放宽、置信度及错误优先级，并用15组源推导oracle及实际文件验证。真实GUI中的全停用非空规则表已触发自动推荐；保留原版前端“空表点击提示而不POST”的差异。详见[自动推导报告](<history-mp-episode-auto-recommend-20261115.md>)及[UI报告](<history-mp-episode-group-fallback-ui-20261115.md>)。

## 整理方式

- 原版 [四种方式](</Users/accidia/projects/MoviePilot-Frontend/src/api/constants.ts#L139-L144>) 是copy/move/link/softlink，表单另加自动项。
- 第九阶段[原生本地move](<history-mp-native-move-20261115.md>)已实际接通文件/字幕/蓝光、自动目录、历史单选/多选、恢复及删除；共用弹窗与目录规则已提供移动，并完成真实浏览器两条记录的移动及文件系统核验。
- 第十阶段[同盘快速移动与普通插件接管](<history-mp-move-fast-plugin-20261115.md>)已接通：同盘保持原inode、无第二份内容空间和全量SHA；跨盘复制清源已在独占HFS+映像的真实不同设备验证。仍采用持久暂存/清源阶段，不宣称与MP单次rename的全部中间状态相同。
- 第十一阶段[存储对象与目录接管](<history-mp-move-storage-directory-20261115.md>)已实现普通视频/字幕的storage.Source.move，以及外部transfer模块的目录move。第十二阶段[目录存储move](<history-mp-directory-storage-move-20261115.md>)进一步接通完整成员列表预冻结、逐成员真实move及冷恢复，保持原FileItem和名称映射，不重调已完成员、不触同名新源。独立SDK和真实字节/源消失验证已通过；没有把底层copy/link冒充自定义move。
- 原生复制及跨盘移动已按MP采用源mtime/atime和目标默认创建策略，不复制源权限、不后置chmod清除继承ACL。实际验证APFS纳秒mtime、目标普通创建权限及HFS+真实跨设备历史mtime；Linux当前为编译验证，未据此宣称所有NAS/网络卷或远端ACL已实测。后续核验读取可能更新atime。
- 第十三阶段[原生普通视频/字幕软链接](<history-mp-native-softlink-files-20261115.md>)已接通entry/referent双身份、实际发布/资产登记、独立SDK回调和冷资产恢复、仅目标/两端删除、断链及新inode保护、重整cleanup/原位更正和库旁NFO。第十四阶段[订阅消费者与当前V3覆盖](<history-mp-softlink-consumers-v3-20261115.md>)已接入普通完成事务、共享库存快照、旧目标的媒体大小/mtime决策、entry隔离/恢复/删除及并行归属值比较。第十五阶段[原生蓝光目录软链接](<history-mp-native-softlink-directory-20261115.md>)已接通逐成员真实发布、冷恢复、完整SDK mappings/STREAM、历史删除和新路径/原位重整，以及NFO entry替换/目录proof原子重基准和SQL失败后冷恢复。第十六阶段[普通本地文件外部transfer/source actor软链](<history-mp-softlink-external-storage-files-20261115.md>)已接通：外部调用前源/目标不存在/父目录证明、真实结果认领和SQL冷恢复；source.softlink以完整FileItem/逻辑目标/授权stage调用，封存结果后中断冷恢复不重调，false/copy均不native回退。第十七阶段[目录外部/source actor软链](<history-mp-softlink-external-storage-directory-20261115.md>)已接通完整原树/目标不存在许可、封存双向bijection和实际逐leaf认领，不伪造native child；source actor保留名称映射并绑定首次选择，目录guard与stage共用真实返回结果证明，冷恢复复用inode不重调。第十八阶段[softlink删除提供方及普通网页来源历史](<history-mp-softlink-delete-history-provider-20261115.md>)已接通：delete_file复用封存browserOperation，原FileItem、真实效果和未知结果恢复；普通from_history按所选Source/ID经原链识别并走原planner，不冒用内部retry或把来源编号作TMDB。第十九阶段[已有普通目录成员合并](<history-mp-directory-merge-storage-publication-20261115.md>)已开放非受管既有root的native copy/hardlink：逐叶entry替换、shared-owned子集、metadata/delete/reorg/inventory消费者和cold恢复，保留无关NFO/dirs/其他版本，目录不套普通never/size/latest事件；source copy/link actor也补齐真实Started/Returned结果封存。第二十阶段[shared soft/move与文件管理](<history-mp-shared-modes-file-browser-20261115.md>)已接通非受管plain root全部四种native方式、exact owned link列表/读取/识别及从link继续整理；手动softlink选项已开放并用真实Chrome验证history重整与文件管理copy直执行。第二十一阶段[识别来源与目录软链接](<history-mp-recognition-source-directory-softlink-20261115.md>)已开放目录规则softlink（含两条自动入口与BluRay盘根）、持久识别来源设置及手动非TMDB来源编号，并用真实Chrome核验。第二十二阶段[受管目录追加](<history-mp-managed-directory-append-20261115.md>)已按MP“目标已存在即写入、旧条目保留”的语义开放同一受管目录追加并复用原资产，旧成员与独立历史保持。第二十三阶段[成员替换与自动来源](<history-mp-member-replacement-auto-source-20261115.md>)允许同一资产在快照一致时替换同名成员，并让自动目录识别跟随全局识别来源。第二十四阶段[跨资产接管](<history-mp-cross-asset-leaf-takeover-20261115.md>)在快照一致时允许目录整理替换其它资产的同名叶，并作废旧资产归属。第二十五阶段[订阅刷新来源](<history-mp-refresh-source-verification-20261115.md>)以真实 SDK 回调确认刷新识别按全局来源进行，并补齐自动目录电视剧季集验证。第二十六阶段[外部软链增量](<history-mp-external-softlink-incremental-20261115.md>)允许外部整树目录软链接向已有目标发布，只认领新增链接并保留既有条目。全局默认仍固定hardlink。最终全量联调与交付说明仍需继续。

## 媒体来源与历史继承

- 原版弹窗由全局RECOGNIZE_SOURCE决定显示TMDB编号还是豆瓣编号，并不是始终显示TMDB。媒体来源不应混作TMDB ID。
- 本项目既有provider-native识别、来源ID、插件/聊天历史重试及下载整理路径。第十八阶段已让普通网页[历史身份继承](<../internal/app/organize_history_batch.go>)回填ProviderSource/ProviderID，并由[每文件普通请求](<../internal/app/organize_manual.go>)在实际读取from_history记录后授权[原链exact-ID识别](<../internal/app/import_provider.go>)，与[MP来源字段继承](</Users/accidia/projects/MoviePilot/app/api/endpoints/transfer.py#L322-L340>)对齐，且不借内部originalRetry。真实Cookie普通HTTP覆盖预览、单选及后台多选：豆瓣电影与Bangumi电视剧不同Source/ID不混作TMDB。
- 第二十一阶段补齐[全局识别来源](<history-mp-recognition-source-directory-softlink-20261115.md>)：可空持久设置（未显式保存继续继承启动值）、生效值 API 与宿主 SDK 设置读取、设置页来源选择，以及共用弹窗按来源切换编号/搜索、`media_source`＋`media_id` 手动输入与隐藏 TMDB 剧集组。真实 Chrome 保存豆瓣后重整历史并保持原历史 ID。来源对自动订阅/下载链的进一步联调仍需继续，未声称所有来源路径均已改造。

## 冻结分类的原版回调投影

- 第五阶段已让普通TMDB下载继承经过下载上下文身份核验的分类；分类开关关闭仍保存事实，开启时目标使用已冻结分类，旧DTO不覆盖当前身份。
- 进一步代码核对发现 [preview上下文](<../internal/app/import_plan.go>)、[执行时上下文](<../internal/app/import_run.go>) 与 [intercept上下文](<../internal/app/plugin_original_transfer_intercept.go>) 调用 [originalTransferContextWithMedia](<../internal/app/plugin_original_transfer_context.go>) 时没有显式传plan.Category。源MediaInfo可能重新按当前策略得到不同category。
- 第六阶段已修复上述投影：主文件、字幕、执行时新增处理器均回填非空的冻结plan.Category；替代源intercept复用完整冻结MediaInfo而非重新识别。实际SDK测试覆盖命名字典、intercept、完成回调、字幕出版及历史。旧计划无分类时保留旧DTO，不追加分类HTTP请求。证据：[SDK测试](<../internal/app/history_category_sdk_test.go>)。

## 手动预计划失败的部分事实

第六阶段已让 [previewImport](<../internal/app/import_plan.go>) 在错误返回时携带截至失败实际确认的媒体、分类、方式/根等部分事实；[manualOrganize](<../internal/app/organize_manual.go>) 的早期失败统一保留这些事实或已有解析/请求季集，不把未验证的输入ID当确认身份、不补造目标，也不为历史追加网络识别。目录后台和下载后台合并已确认媒体/分类时保留自身完整源文件列表。[实际失败测试](<../internal/app/history_preparation_failure_test.go>)覆盖分类503与非法目的路径，并确认预览无执行历史。更早的自动识别→目录匹配路径仍只有request/解析信息，后续媒体来源接通时继续核对完整确认身份的传递。

## 明确保留的用户差异

- 同源稳定历史ID、最新实际提交、预览不进历史；不同源不按TMDB合并。
- 类型分类只表示二级分类，不添加电影/电视剧一级目录，不恢复两个相互含糊的开关。
- 独立目的存储/路径/方式；单页共用弹窗与底部可选预览；成功关闭、失败留表单。
- 不增加暂停、媒体库内容、数据库备份入口；不删除内部恢复收据来模拟MP的物理删除记录。

## 收尾状态

已识别的 MoviePilot 整理历史功能差异均已在第一至第二十六阶段实现并验证，最终回归、前端全量测试/构建与真实 Chrome 联调见[交付说明](<history-mp-final-delivery-20261115.md>)。剩余项不是待实现功能，而是环境边界：生产下载器/真实 RSS/NAS/云盘写入与 Linux 运行未验证，MoviePilot 内部数据结构未逐字段复制。全局默认整理方式仍固定硬链接，未增加自创必选步骤或额外确认矩阵。

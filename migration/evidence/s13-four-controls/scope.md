# S13 下一有限片：四个固定单次命令，不按标题扩大 reannounce

只读判断，2026-10-31 23:16 UTC。活动树 HEAD `f91359b612cefc068ff590d6c99622d329e6d474`；固定 core `8a83efeacd3e26b82be495e17d50146e31c96a60`。**建议下一片完整实现 qB `set_torrents_tag / set_torrent_category / stop_torrents` + TR `stop_torrents` 四个普通方法的 SDK→当前配置/授权→独立 native client→owned 协议闭环。** 不依赖 S04/S06 旧行决策，不恢复业务插件、不请求真实下载器；本页不改产品/账本、不执行源码/Go/网络。

## 精确存量：旧八项没有被 S03 自动完成

唯一 ledger `/features/63/semantic_scope_reconciliation` 限定 **qB 五项 + TR 三项**，当前仍 discovered：

| 原方法（固定 core 行） | 真实合同与本次裁定 |
|---|---|
| qB `set_torrents_tag` 336–346 | 调 `torrents_add_tags(tags, torrent_hashes)`；缺 client、正常、所捕获异常均 **None**，异常记日志。不是覆盖标签、不是 bool。选入首片。 |
| qB `set_torrent_category` 686–700 | 缺 client/falsey hash→False；category 假值→空字符串；固定 setCategory，非抛错→True。选入首片。 |
| qB `stop_torrents` 490–502 | 调 pause alias；不先拒绝假值 ids，非抛错→True。vendor 每次版本读取，API≥2.11→stop，否则pause。选入首片。 |
| TR `stop_torrents` 240–252 | 固定 torrent-stop；原 vendor require_ids，None/空列表是本地异常→wrapper False，不能转“所有任务”。选入首片。 |
| qB `delete_torrents_tag` 304–319 | **先 removeTags，再 deleteTags 全局定义**，两写及部分成功/未知副作用必须独立保留。旧 RemoveTag 只第一步，不能冒完整实现。留后一片，不触任何下载数据删除。 |
| qB `get_torrent_id_by_tag` 383–399 | 实际 `range(1,10)`=9轮，每轮先等3秒；取列表首项 hash，成功后调用上述两写删除。依赖完整 get_torrents/tag/status 及多阶段错误；旧 PollByTag 的单标签/规范 hash 限制不同，不能直接替代。 |
| TR `set_torrent_tag` 168–181 | falsey ids/tags→False；`list(set((org_tags or []) + tags))`，不是普通标签覆盖/任意有序去重；需保 Python set/异常与 vendor labels 的窄源基准，现 SetTorrentTags 的 exact-hash 路径非等价普通入口。 |
| TR `change_torrent` 388–423 | 仅 upload/download/ratio/seed-idle 四参数；None/0分支、bool/int/float/round 及 **try 外转换异常** 要保。vendor kwargs 原样合并，空参数真实 ValueError；不扩任意 torrent_set/路径/文件选择 RPC。 |

安装版 `tags/delete_tags/reannounce` 是 ledger 明列“另核”，不是旧八项；`remove_torrents_tag`、qB `change_torrent`、TR `get_torrent_tags` 也不能凭宽标题补入本轮。`shared_qbittorrent_completed.go` 的 reannounce→reannounce_in **字段映射不是重新通告命令**。

## 已有可继承，和真正缺口

- S03 已交有限八命令 Add/Start/Recheck/DeleteKeepFiles 的源码、SDK、真实 host、独立 native 与正常 CLI/owned 门继续有效；ledger S03 父项仍 implementing，不能改成全下载器完成。当前 `shared_downloader_commands.go` SDK、App `originalDownloaderCommandMethod`、leaf `QBC/TRC` 均只列旧四操作，**首片四方法的普通 wire/dispatch/leaf/native 接线是真缺**。
- 直接复用 S03 当前配置 exact-ID/enabled snapshot、cap/scope/identity/过期回调守卫、固定 factory、每跳 guard、auth/session、配置变化校验、Close 和 private mutation proof；固定128KiB普通调用即可，不需要 upload/material/store 新通路。不要新业务 registry/任意 RPC/worker 提供 endpoint 或凭据。
- 旧 `IYUUClient.SetCategory/RemoveTag/BrushFlowControl`、`Client.SetTorrentTags` 确实已有协议部件与历史信用；但它们有 exact hash、非空 selector、严格返回正文、版本缓存或多余 data-delete 分支，不能整方法复用冒原普通合同。应在 **同一个 S03 私有 client** 上添加固定 ports，复用内部准备/dispatch而不恢复旧业务壳。
- 已保存普通消费者：`clean-invalid-seed-mutation-phase/installed-plugin.py:660,667` 实际调用两种打标签；`iyuu-auto-seed-2.19/source/__init__.py:1059,1066` 实际调用 tag 查找及分类。它们独立外部 pin，不冒 core SHA；只证明平台消费需求，不启动这些插件。

## 最小源基准与定向门

先仅完整四原 wrapper AST + recording qB/TR端口/日志采集，覆盖缺 client、普通/nullable selector、返回 None/True/False、普通异常；标签与category空值由真实源产生 expected。沿现固定 vendor **只静读** qB `_list2string`、stop/pause别名、addTags 的 `version_introduced=2.3.0` 支持门、setCategory，以及 TR IDs/require_ids/torrent-stop；不导入 vendor/requests，不伪装 recording 为真实 HTTP。随后四固定 native 的 owned HTTP 门核精确 form/RPC、qB新旧版本、TR空ids零selected写；同 installed worker 验授权/无权/失败恢复，相关 guard/race 与两个普通host CLI增量，不重跑S03所有历史或真实daemon。

qB普通字符串/列表 selector 不能窄化成仅40hex；None省略与空字符串/空列表字段存在应沿S03既有准备保留。qB打标签 source None 返回需单独 wire 表达，不能统一 bool。边界拒绝/取消/已委派未知失败不冒 source False；继续已声明 **无应用级 selected replay**、无 GetBody 回放，但不声称 Go transport 永远单次尝试（参见既有 ZERO-WRITE clarification）。

TLS仅沿已明确批准的 **S03独立qB clone**（2026-10-31 21:55:10 Sentinel_6f870ab66a148191a118376aa072856d）关闭校验；不能扩旧IYUU/completed/TR/全局/CA/env/Git/browser。TR保现独立 trust_env=False/严格TLS；跨域凭据/代理守卫不变。所有 future peers 仅 owned，无真实 daemon/下载/端点探针。本判断不为后续新增操作授运行权限。

### 本次关键只读绑定

- core qB `app/modules/qbittorrent/qbittorrent.py` SHA `114b2f5e78a89bd3384c1bb0fe29a9e6b5144e575ef63fb852bfe148387b29b0`；TR SHA `6fc20e208b86e9e68ada074643f82b88270c2e51749e460ad881b10f744cf676`。
- 现有 `audits/s03-native-contract/input-bindings.json` 固定 qB2026.6.0/TR7.0.11；本次 `vendor-qb/torrents.py` SHA `d087e5ef72ee748a8af160fd7f4fdbf119d41df6b1fdf2fe1d50509a3a0c2e26`，`vendor-tr/client.py` SHA `5d34047c37bcadb00f3320fd8dbbe46aae6583acb5fd7877ed9a4ca0eea4bf34`。旧 acquisition incident/未知副作用记录保留，不重取/不执行来消除它。
- 两外部消费者 SHA：installed-plugin `fd398230ee132f6002dbaf892e28fb10b9b95567fda9ce513e1543329a8b7fda`；IYUU2.19 `a47d36432e87da046af025fc76281d99c714064a8fe05bdf8b11e83d2c683a3f`。本页静态引用不是新运行证据。

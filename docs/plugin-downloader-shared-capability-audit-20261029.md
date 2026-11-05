# 标准插件调用宿主下载器能力复查

2026-10-29，核对 `main@97476582910dc4b696f7ccd5010f28d823812d40` 与当前 NAS。用户本轮要求核对三项能力，本轮没有修改生产实现、挂载、下载器设置或种子任务。

结论：第 1、3 项缺少现行标准插件调用宿主的入口；第 2 项已有实例数据目录，但备份目录接入与端到端验证未完成。旧业务包存在实现，不等于从 GitHub 安装的标准插件可以调用它。本轮补充项在唯一对照表 S01–S03 维护，不以历史 46/46 覆盖这些缺口。

| 唯一对照表项 | 原版依据 | 现有目标实现 | 缺口与状态 |
|---|---|---|---|
| S01：枚举已完成任务 | 固定 B `app/modules/qbittorrent/qbittorrent.py:254`、`app/modules/transmission/transmission.py:136`；业务参照 `testdata/plugins/torrent-transfer-1.10.3/torrenttransfer.py:690` | `internal/plugins/downloader_tasks.go:12–56`、`internal/app/plugin_original_downloader_tasks.go:15–86`；旧内部 `internal/plugins/iyuuautoseed/native/transfer.go:30` | 现行 `downloader.tasks.read` 必须传准确 Hash；缺失/null 拒绝，空数组不请求下载器。无法从未知 Hash 开始枚举全部已完成任务。旧内部完成任务查询尚未接为标准资源。**待实现**。 |
| S02：备份、fastresume、临时文件 | 业务参照同文件 `:785,835–857`；固定 B `_PluginBase.get_data_path` 对应现行 Base | `internal/app/plugin_original_base.go:521–549`、`internal/plugins/base.go`；旧内部 `internal/plugins/torrenttransfer/native_host.go:201–342` | 已有各实例独立数据目录，可供插件用普通 Go 文件操作处理自己的文件。现行资源白名单没有备份文件读取/临时文件操作资源；当前 NAS 没有挂入下载器种子备份目录。备份选择、只读访问、重建材料与临时清理的标准插件实测尚未完成。不能把“没有专用 RPC”解释为 Go 插件无法直接操作已挂载文件，也不预设必须新增通用文件系统框架。整体 **待实现**。 |
| S03：添加、校验、启动、只删任务 | 固定 B qB wrapper `:401,477,503,608`，Transmission wrapper `:201,227,253,375`；业务参照同文件 `:638,661,891,899,976` | 旧内部 `internal/plugins/torrenttransfer/native_host.go:429–662`、`internal/plugins/iyuuautoseed/native/transfer.go:55`；宿主 UI 控制桥 `internal/app/plugin_original_downloader_control.go` | 底层协议和宿主界面操作已有；`originalStandardCapabilityAllowed` 未注册这些供标准 worker 反向调用的操作。旧 `torrent_transfer.*` 私有契约不受现行安装器支持。宿主调用插件的模块覆盖接口也不是插件调用宿主的资源接口。**待实现**。 |

固定平台源码版本为 MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60`；上表业务文件是仓库保留的 TorrentTransfer 1.10.3 参照，不能混称固定平台提交自带业务插件。安装契约依据 `internal/app/plugin_implementation.go:21–36` 与 `internal/app/plugin_original_resources.go:14–21,80–82`。

## 本轮实际验证

- 5 个宿主顶层测试通过，0 失败/跳过，8.525 秒：实际独立 SDK worker 经 App 安装/会话/HostCaller，验证 qB、Transmission、分身、准确 Hash、凭据不返回插件、空数组不请求下载器、越权操作拒绝、撤销/取消与协议错误。下载器为自建 loopback 合成协议服务，不能称 NAS 下载器实机操作。
- SDK 精度/选择和 Transmission 只删任务双协议测试共 2 个通过，0 失败/跳过，0.431 秒。后者证明底层传递 `delete_local_data=false`，不证明标准插件已经有删除入口。
- 实例数据目录边界测试 2 个通过，0 失败/跳过，3.018 秒：不同实例目录分离、创建、路径/符号链接与取消边界；没有把目录测试算作 backup/fastresume 完整链通过。
- 首轮宿主测试因 macOS 默认临时目录祖先不满足现有制品安全校验而失败（5 个顶层失败）。保留原日志；指定工作树内真实 `TMPDIR` 后重跑通过，没有关闭/修改产品路径检查。
- NAS 仅只读 `docker inspect` 与二进制 SHA 核对：light-media 为 healthy，SHA256 `f57f098b1c151d5dcbfe2ca54d22b7378f6a5e57a6ea88e193a27ca61a7d3acd`，与上轮部署一致。容器目标挂载为 `/vol2/1000/media`、`/media`、`/config`、`/app/light-media-server`、`/etc/ssl/certs`、`/etc/hosts`，没有下载器备份专用挂载。未读取备份内容，未枚举生产种子，未执行添加/校验/启动/删除。Memos 两组相关关键词检索未找到匹配记录，本轮结论以当前源码和 NAS 观测为准。

原始回归日志、命令、结果及摘要：`testdata/plugin-downloader-shared-capability-audit-20261029/manifest.json`。历史 final JSON 及其证据保持原样；唯一对照表当前结论必须纳入这三个新增缺口，不能再把历史封存结论当作它们已完成的证明。

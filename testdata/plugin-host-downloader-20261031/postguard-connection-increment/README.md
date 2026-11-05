# S01 / S03 / S13 下载器宿主证据

本目录是唯一对照表中 S01、S03、S13 的证据附件，状态由 `docs/plugin-module-parity-matrix.md` 维护，不另建验收总表。没有新增业务插件、Python 运行依赖、AI 或工作流扩展点。

`source-target-evidence.json` 绑定固定原版源码、目标函数与已执行证据。`manifest.json` 绑定交付文件及所有证据 SHA256；`independent-review.json` 记录父代理的源码审查和其独立 SDK 测试。旧轮次目录明确保留在各自版本，不能代替最终轮次。

## 固定依据

- MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60`，qB wrapper 文件 SHA256 `114b2f5e78a89bd3384c1bb0fe29a9e6b5144e575ef63fb852bfe148387b29b0`。TR 文件 SHA 和全部符号行号见 `source-oracle.json`。
- 已安装插件来源是只读交接 ZIP；每份 Python 文件 SHA 与原有 `installed-source-ast.json.gz` 核对，`installed-call-candidates.json` 与 `tracker-consumers.json` 保留真实调用及消费字段；没有照 Go 实现生成预期。
- qB API 库 `2026.6.0`，固定 requirements 的 wheel/模块 SHA 及 MIT 许可证见 vendor 文件。Cookie 解析采用固定 Docker Python `3.12.13` 的实际 `SimpleCookie`，保留 PSF 许可证及源 SHA。
- TR 依赖在固定 requirements 中为 `~=7.0.11`；仅只读检查旧容器实际安装版，确认 **7.0.11**。`transmission-rpc-7.0.11-source.json.gz` 只含 MIT 库 client/utils 源码及 SHA。未读取旧业务配置、修改或重启旧服务，进程状态见 `old-service-readonly-after-vendor.json`。

## 有限标准 SDK 合同

`plugins.Downloaders` 只接受当前插件调用的 `HostCaller`，不保存凭据，不提供任意 RPC。

| 方法 | capability | 固定行为与边界 |
| --- | --- | --- |
| `Completed` | `downloader.tasks.completed` | qB daemon `filter=completed` 后按源标签/完成谓词；TR 仅状态 5/6，状态过滤先于标签；保留错误顺序。 |
| `Downloading` | `downloader.tasks.downloading` | qB 完全使用 daemon `filter=downloading`，不本地猜状态；TR 仅 3/4。 |
| `List` | `downloader.tasks.list` | 原版显式 `get_torrents()` 全任务安全投影，保留顺序与数字字面量。旧准确 Hash 读取契约未改。 |
| `Add` | `downloader.tasks.add` | bytes / magnet / raw URL 三者互斥；支持 qB 多 URL、Cookie 同步版本/失败 fallback、TR Cookie、暂停/路径/标签/分类/限速。 |
| `Control` | `downloader.tasks.control` | 非空准确 Hash；start/recheck/pause/remove_task/reannounce/set_tags/remove_tags/delete_tags/set_category/set_limits，按实际 source/vendor 的适用 daemon 区分。`remove_task` 永远保留文件。 |
| `Tags` | `downloader.tags.read` | qB 全局定义或 TR 准确任务标签。TR set_tags 合并 org_tags 并去重；空 tags 返回 false、零 RPC。 |
| `FindByTag` | `downloader.tasks.find_by_tag` | 源 9 轮、每轮先睡 3 秒；匹配后清标签；清标签失败不抹去已找到 Hash。 |
| `DeleteTags` | `downloader.tags.delete` | 单独删除 qB 全局标签定义，区别于选定任务先 detach 后删定义。 |
| `Trackers` | `downloader.trackers.read` | 准确 Hash；status/tier/TR flags、源字符串比较事实、脱敏展示及稳定 opaque 比较键；不返回 private URI 或 errorString 凭据。 |
| `DeleteFiles` | `downloader.tasks.delete_files` | 与 remove_task 分开；精确非空 Hash、当前宿主认领、授权资源根、两次远端布局/本地快照、O_NOFOLLOW、硬链接/共享任务/受管资产保护后，一次 daemon 删除。 |
| `Connection` | `downloader.connection.read` | 显式 ID 的只读认证/session 探测，返回源 `is_inactive` 布尔，不能用 enabled 冒充连接。disabled/取消/撤销拒绝；实际认证失败、不可达为 inactive=true。 |

新增 public 资源全部遵守固定 `ServiceBaseHelper` 的 **enabled** 选择，包括显式 ID 的读取和写入；每次 HTTP 前重查 enablement/连接配置/当前 invocation。只有宿主内部共享文件保护可私下读取停用实例。旧已验的准确 Hash 只读契约保留原适配，不因本轮扩大其含义。`disabled-source-oracle.json` 是固定源码实际 AST 运行结果；真实 Linux 对全部 10 个新 public capability 的停用拒绝及 SDK 零外发/中途停用测试分别保留。

## 必要适配

1. Go typed DTO/error/receipt 代替 Python 对象、False/None/tuple；不建立 Python 兼容环境。准确 Hash 数组 `[]` 明确不外发，遗漏/null 的枚举语义仅属于 Completed/Downloading；真实原插件使用遗漏或非空准确 ID。
2. 普通元数据保持 128KiB；种子 bytes 使用已有 16MiB SDK envelope（base64 后实际 metainfo 上限约 12MiB）。拒绝未知字段及混合材料，不能借配置/HTTP 请求加入任意 RPC。
3. tracker 原始值只在宿主内存。HTTP 使用源 netloc 分组，非 HTTP 使用源完整字符串分组（含 UDP query）；稳定 HMAC 保持等价关系，原始消息先做精确/strip 比较再脱敏，不改变插件判断。
4. `delete_file=True` 不认领任意旧 MP/外部任务；复用现宿主认领、当前授权根和文件安全边界。未挂载、未知/缺失文件、symlink、hardlink、文件替换、共享路径或使用中的整理资产均拒绝。页面下载列表仍保持原有正在下载范围，插件桥可处理本宿主认领的完成/做种任务。
5. 删除回执分开 `daemon_applied` 与 `bookkeeping_completed`。daemon 确认后本地 SQLite 取消失败，仍明确报告已发生外部删除，不能视为安全重试；丢失 ACK 为 unknown。仅取消选中 Hash 的持久任务/预留，不影响其他下载器或 Hash。
6. **TR 空 URL 与 qB 不同**：实装 7.0.11 把空字符串作为 `filename=""` 发给 daemon，空 bytes 与 `file:` URI 在库内失败。最终 TR 增量保留这一顺序；实际 daemon 拒绝空 filename，Go 返回明确失败，未创建任务。qB 空材料仍返回 false、零 HTTP。早期错误的“TR 空 URL 零 RPC”预期及修正保留在增量日志，不作为通过证据。

## 实际执行分型

| 类型 | 证据 | 可证明内容 |
| --- | --- | --- |
| 固定原版 AST 执行 | source/cookie/tracker/disabled/transmission-vendor oracle JSON 及 capture 脚本 | 原标签顺序、状态、异常、wire 参数、Cookie/URL 版本分支、vendor 行为。依赖用受控对象，无生产调用。 |
| 合成协议 + 实际标准 SDK/安装宿主 | Go `TestHostDownloader*`，压缩 race/native 日志 | 插件安装/真实独立进程、当前 scope/配置/停用撤销、失败不重试、两种 TR dialect、clone/reload/cold、各保护分支。它们不是实际 daemon 证据。 |
| 实际 Linux + 两个真实 daemon | `final-enabled/real-*.json` 与 server 日志 | 独占目录、真实 qB 5.1.2/WebAPI2.11.4 与 TR4.0.6；添加/URL+Cookie获取/校验/启动/暂停/标签/分类/限速/重载/升级/分身/重启/卸载；只删任务文件 SHA 不变；删文件任务及文件均消失；失败保留自建文件；SQLite 故障后外部副作用可判定。全部是自建无外部 tracker 的 private 测试种子，另有仅 loopback 的 synthetic tracker 错误测试。 |
| 实际浏览器 + App/SDK + **合成** qB peer | `browser/result.json`、截图与 AX/DOM、peer-calls | 页面配置保存/一次执行/完成枚举/add/control失败恢复/start/recheck/只删任务→空列表、分身独立配置数据和 PID。此处不声称浏览器直接连接 NAS daemon；SSH 转发被 NAS 策略拒绝，未修改 SSH 配置。 |

`final-enabled/build-binding.json.gz` 是最后 TR 增量 server 的 Go 实际解析的全部源文件/标准库/模块/EmbedFiles 闭包，构建前后逐文件 SHA 相等。各结果内含实际 server/worker SHA；不同阶段的 race/UI 采用各自绑定，不能冒称同一 binary 全量运行。源代码/生成数据/编译输入改变时，必须重新判定受影响证据。

## 后置 guard 与连接状态的独立增量

`postguard-connection-increment/manifest.json` 覆盖基线后增加的有限行为，基线 manifest/四轮 Linux/UI 保留原 binary，不重写成新 binary 的全量结果。

`DownloaderMutationV1.failure_code` 是可选有限错误码，正常成功不发送此字段。`CALL_CHANGED_AFTER_MUTATION` 表示已发送或已确认外部动作后，配置/调用引用被撤销；保留真实 `accepted/outcome/hash/added_ids`，**立即停止使用旧引用执行后续调用，核对当前实例及真实结果，不自动重试**。`PARTIAL_MUTATION` 表示批量 category 的前项已确认、后项失败，`accepted=false/outcome=unknown`；不能把整个批次误记为未发送。若 readonly preflight 后就撤销而没有任何写，则仍是原 guard 错误、零写，没有伪造外部副作用。

实际标准 SDK 进程 + owned HTTP peer 的 8 个因果场景确认回执能穿过外层 HostCaller 交付：cookie 全局写后撤销、add 确认后撤销并保留 IDs、丢失 ACK、control/删全局标签确认后撤销、category 前项成功后后项 guard 拒绝/远端失败、只读 preflight 撤销零写。这是合成协议证据，不冒充对真实 daemon 的故障注入。

连接源依据为固定 qB173 / TR88 `is_inactive`、constructor 的 login 返回值及 ServiceBase116/293 的启用配置实例持有关系。`connection-source-oracle.json` 执行 14 个构造/谓词场景、6 个实际 SeedRescue 消费场景，并绑定其他 8 个直接下载器调用。源有效 host/port 且没有 qbc/trc 时为 true；有 client 为 false；显式无 host/port 返回 false，而缺 host 构造后可能触发属性缺失异常。目标现有配置先验证可用 URL/实例，不能伪造这种无效结构为 connected。Python 持有 client 状态可能过时，Go 按请求以既有 `TransferConnected` 执行固定认证/session 只读检查，这是明确的必要生命周期适配；不复制原 login 后的全局未完成文件后缀设置。

`postguard-connection-increment/real-connection-result.json` 使用真实 Linux server、单独安装的 SDK worker、真实且开启认证的 qB/TR，覆盖成功、错凭据、不可达与 disabled。凭据只在内存随机生成；在探测前独立证明无认证请求为 401/403；SDK 只返回身份/kind/inactive 四字段，无原始错误、URL 或凭据；任务列表保持为空，保存一次执行与卸载链同时通过。最初 linuxserver 镜像启动覆盖手写 TR auth 设置导致负例前提不成立，失败被保留；修复测试环境以 stdin env-file 传入随机认证，未修改产品来强迫测试通过。

运行方式（直接使用共享工作树和默认 Go 缓存，TMPDIR 必须在项目 owned work 下）：

```sh
python3 testdata/plugin-host-downloader-20261031/capture_source.py
python3 testdata/plugin-host-downloader-20261031/capture_disabled_source.py
python3 testdata/plugin-host-downloader-20261031/capture_transmission_vendor.py
go build -race -o work/plugin-host-downloader-20261031/plugin-host-downloader-fixture-race ./cmd/plugin-host-downloader-fixture
PLUGIN_HOST_DOWNLOADER_FIXTURE_BINARY="$PWD/work/plugin-host-downloader-20261031/plugin-host-downloader-fixture-race" TMPDIR="$PWD/work/plugin-host-downloader-20261031/tmp" go test -race ./internal/app -run '^TestHostDownloader' -count=1 -v
```

`run_nas_{qb,tr,delete_files}.py` 是测试驱动，不能用于生产端点。NAS 两个 daemon 固定仅绑定 loopback 39282/39292，App 39281、owned 仓库/材料源 39283；容器只挂独占目录，DHT/PEX/LSD/UPnP 关闭。完整镜像 digest、挂载与清理证明分别见 `daemon-environment.json`、`cleanup-nas.json`。本轮未提交、推送或部署生产服务。

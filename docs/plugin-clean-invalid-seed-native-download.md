# CleanInvalidSeed 原生下载器协议依赖

状态：正式 `internal/download` 已增加原生读取、tracker 与标签协议方法；这是插件宿主可使用的内部能力，不代表 CleanInvalidSeed 已注册、已调度、已部署或已在真实下载器运行。没有修改已有七个下载器 Go 文件，没有修改清理纯包、App 注册、公开 HTTP API 或主下载管理范围。

## 接口

```go
func (c *Client) NativeTorrents(ctx context.Context) (json.RawMessage, error)
func (c *Client) NativeTrackers(ctx context.Context, hash string) (json.RawMessage, error)
func (c *Client) SetTorrentTags(ctx context.Context, hash string, tags []string) error
```

三个方法复用已配置 Client 的凭据、地址、认证、同源 Referer、禁止重定向、16 MiB 响应上限、错误脱敏、互斥与 Transmission 会话/协议状态；不提供任意 endpoint/method 或跨实例 RPC。tracker 与标签选择器只接受一个真实 hash 或属于该 Client 的复合标识，不接受 all、数值 ID、拼接列表或其他实例。等待 Client 锁与在途 HTTP 请求均响应 context 取消。

`NativeTorrents` 与 `Task` 投影、`ListDownloading` 分离。qB 获取完整 `/torrents/info` 数组，结构校验后返回独立原始字节副本；不强加 Task 必填字段、去重或一万行截断。JSON 使用 `UseNumber` 校验，未知字段、大整数、空值与字段存在性保留。

TR 使用安装版 `_trarg` 全字段，保持原列表顺序及两组重复的 rate 字段。旧协议发送原 camelCase 字段，4.1 JSON-RPC 发送规范 snake_case；已知 torrent 字段与 `trackerStats` 字段恢复为 transmission-rpc 7.0.11 原字段名称，供原生对象构造器消费。未知嵌套对象不递归改名，不补缺省字段；冲突的新旧同义字段明确失败。完整字段不是 `download.Task` 的替代品，也不扩展下载管理展示范围。

`NativeTrackers` 仅支持 qB，每次都真实请求 `/torrents/trackers`，不缓存。原插件 `hasattr`、真值检查、赋值会读取三次，两轮筛选共六次 GET；测试经过真正的 Go 原生对象 getter 核对。TR tracker 数据在完整快照中。

标签按原 daemon 操作：qB `addTags` 追加，TR `torrent_set` labels 替换。不 trim、去重或套用 Add 的 32 标签限制，保留已有 TR 标签、Unicode 和输入顺序；qB 的逗号仍由其原协议拆分，TR 的逗号保留在单个 JSON 标签内。字串必须是有效 UTF-8，发出的标签数据受大小限制。原 TR wrapper 在调用前负责合并旧标签与新标签；不能在 Client 中再擅自变成追加语义。

HTTP 成功不等于已观察到标签或确认 hash 仍存在。qB 官方标签接口空响应视为协议确认，非空异常/false 响应不会变成成功；TR 检查 RPC envelope、ID 与业务结果。429、重定向、业务 false 和传输不确定不自动重试变更，也不更换方言重试；仅沿用原认证拒绝与 HTTP409 会话拒绝的执行前重试。

## 固定七操作

| 原生调用 | 内部方法 |
| --- | --- |
| `qb.torrents_info` | `NativeTorrents` |
| `qb.torrents_trackers(torrent_hash)` | `NativeTrackers` |
| `qb.torrents_add_tags(torrent_hashes,tags)` | `SetTorrentTags`，追加 |
| `qb.torrents_delete(...,delete_files=false)` | 既有 `Control(...,"remove_keep_data")` |
| `tr.torrent_get(arguments=_trarg)` | `NativeTorrents` |
| `tr.change_torrent(ids,labels)` | `SetTorrentTags`，替换 |
| `tr.remove_torrent(...,delete_data=false)` | 既有 `Control(...,"remove_keep_data")` |

表中的七操作绑定目前在测试中实现，尚未接正式 App 宿主。它不是新增通用 worker RPC。正式宿主须固定下载器实例和配置 revision、核验变更授权并持久记录真实/未知结果；不能由 worker 选择地址、实例或删除数据。原 wrapper 的 qB 标签错误仍返回 None，TR 返回 false，插件原计数可能照常累加；宿主不能把这些返回、日志或计数当作真实效果收据，也不能据此盲目重试。

删除复用原 `Control`，其存在性/Task 数值校验保持不变。完整 native 列表能够读取某对象，不等于其一定满足原有删除条件。这里没有放宽删除身份或文件安全策略。

## TR 私有读取 helper

既有 `trRPC` 为 Task 消费者递归执行 `normalizeLegacy`，会连未知扩展对象中的 `hashString` 等字段也改名，无法直接用于完整原生 JSON。新增 helper 只固定读取 `torrent_get` 与固定字段列表，不接受任意方法参数；调用原 `trConnect`、`request`，共用会话、方言与 request ID，保留同样的409/envelope校验，跳过递归 Task normalizer。标签变更仍直接用既有 `trRPC`。原七个文件没有为此修改。

## 来源与验证口径

`testdata/plugins/clean-invalid-seed-native/transmission-trarg.json` 来自本地旧模块 AST 的 `_trarg` literal；读取前核对完整模块 SHA256 与此前只读取得的安装版一致：`6fc20e208b86e9e68ada074643f82b88270c2e51749e460ad881b10f744cf676`。每个现代字段名均核对固定的官方 4.1.0 RPC 表，不从 Go 映射自行生成 expected。`source-manifest.json` 记录安装版 wrapper、原库版本、隔离候选清单和协议来源。

原安装 qB wrapper SHA 为 `114b2f5e78a89bd3384c1bb0fe29a9e6b5144e575ef63fb852bfe148387b29b0`，库版本 qBittorrent API 2026.6.0 / transmission-rpc 7.0.11。完整安装 AST、属性、wrapper 返回与 SDK 参考继续由清理纯包的既有来源资料证明；本文件增加协议证据，不把 loopback 当作实机。

主来源：[Transmission 4.1.0 规范](https://github.com/transmission/transmission/blob/4.1.0/docs/rpc-spec.md)、[4.0.6 旧协议](https://github.com/transmission/transmission/blob/4.0.6/docs/rpc-spec.md)、[qBittorrent 5.1.2 controller](https://github.com/qbittorrent/qBittorrent/blob/release-5.1.2/src/webui/api/torrentscontroller.cpp)。源 controller 仅用于核对协议事实，没有把 C++ 实现复制进 Go。

所有新协议测试均使用自建 loopback `httptest` 服务和合成凭据/字段：

- qB 认证、路径、cookie、Referer，原数组字节、Unicode、超大整数、未知嵌套字段、独立返回切片和 10,001 行快照。
- TR 旧/4.1 方言、真实源字段表、trackerStats、空值 presence、未知嵌套原键、同 request ID 的409会话刷新及标签替换。
- 真实冻结 `NativeDownloader` 到 HTTP 的七操作闭环、两轮六次 qB tracker、qB None/TR false 源返回、保留数据删除；测试绑定拒绝任意 RPC 和 delete-data=true。
- false/无效 JSON、错误 RPC ID、缺失/无效数组、别名冲突、会话头错误、409上限、302拒绝、429无重试、锁等待与在途 GET/POST 取消、并发 ID 不重复。
- 使用实际日志 Transport，合成密码、cookie/session、tracker passkey/URL、hash 和影视名称不出现在日志或错误中。

没有连接真实下载器、修改既有种子或文件、发送通知、修改旧服务或运行正式 App/full。插件注册、宿主授权/持久收据、调度、迁移和实机验证仍须后续接线，不由本包测试代替。

正式包本轮验证终态：`go test ./internal/download -count=1` 通过（1.367s），`go test -race ./internal/download -count=1` 通过（2.315s），`go vet ./internal/download` 退出0。迁入前后原七个 Go 文件 SHA 均未变化；新 runtime 源与冻结候选逐字节一致。未执行 App/full 或提交，源码、参考、本文档已冻结待统一集成。

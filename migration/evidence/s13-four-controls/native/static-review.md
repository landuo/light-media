# S13 native candidate01 独立静审 READY（未运行）

作者 tls_ab_independent，本审查 c_userdata_owner（Astra ultra）；仅两个新 native 文件及其必要原 vendor/旧 S03 调用闭包，未写主树/执行任何源、vendor、Go或网络。

**未发现新增实际静态阻断，可进入冻结后的最小3根 owned HTTP 编译/执行门。** 这是有限 native 候选准入，不是 vendor源码执行、真实daemon、installed worker或完整S13等价证明。作者以前对 source 的审查未作为作者自审 native 证据。

## 实际合同核对

- 新方法挂在旧 factory 返回的 `*sharedQBCommands/*sharedTRCommands`；签名精确满足新 optional AddTags/SetCategory/Stop ports。旧QBC/TRC/factory/构造器无需改，七个已封 native 文件逐SHA同。
- qB AddTags：原 vendor先准备 hashes/tags `_list2string`，再 `_request_manager`→版本支持门；候选同序、保null省略/空列表空字段/列表顺序与重复/Python scalar text。原构造器未传 RAISE_NOTIMPLEMENTEDERROR，默认False；<2.3返回nil且无选定POST，与源码一致。>=2.3仅addTags，不误调用replace-tags。版本经原 `Application.web_api_version` property调用真实getter，不把ClientCache误作响应cache。
- qB Stop：原pause别名指向stop，先fresh版本读取、再准备selector；>=2.11 stop，否则pause。候选同序；没有把全局/空ids硬套旧hash限制。Category只准备hash/category再setCategory，无额外版本/存在性查询；falsey hash及category-or-empty仍由已审leaf处理。
- TR Stop仅复用已交 `control(ctx,"torrent-stop",ids)`；require_ids/原类型有限域检查在RPC前，nil/空/负整数/非法字符串本地SourceException，不转全任务；保0、bool、lower40hash、recently-active。readonly session协商与selected请求分开，固定legacy envelope/未知effect处理不变。
- 四方法均沿旧 `owner.run` guard/锁/monotonic proof及selected helper；native nil返回不自判bool/None业务成功。authority/cancel/未知失败不伪装原普通异常；无应用层selected重放。沿已封 `TRANSPORT-ZERO-WRITE-CLARIFICATION.md` 理解“单次dispatch/POST”：**不声称Go底层永远一个transport attempt**，零写已证明失败的底层复用连接重试边界不变。
- 无TLS、CA、env/proxy、redirect、跨域凭据改动：同S03独立qB已授权verifyFalse，旧IYUU/completed/TR均未扩大。结构化category、任意Python iterable/ordered map、未支持标量域仍明确Boundary/pending；不叫sourceFalse。原vendor重试/广义catch与现unknown保护的已声明差异继续保留。

## 三个待执行根

1. `TestSharedDownloaderFourQBWireAndVersions`：同ownedpeer12请求固定序列，7次port调用；2.3低版不POST、null/empty、scalar join、category、2.11pause/stop，ignored正文。Bearer仅dummy。
2. `TestSharedDownloaderFourTRStopSelectors`：5类非法selector零HTTP；3成功调用实际owned session-get409/200+3 stop，保ids，无postquery。
3. `TestSharedDownloaderFourGuardAndUnknown`：四port分别pre-guard/pre-cancel零wire，固定500 selected在各自peer仅观察一次、错误仍unknown且不降SourceException。

这三根没有hidden compiler/process、env清洗、真实端点、forward/CONNECT或全局transport修改；锁与atomic用于owned观测。它们未运行，本审查不把手写期望视为原vendor oracle。源17例只证明原wrapper→recording端口，真实target协议门和后续worker/CLI各自计证。

详见 `native01-static-readback.json`：candidate01/current/旧native/源及vendor/leaf绑定逐项核同。待协调者精确复制后实际门；新实际FAIL须保留，不预判绿。

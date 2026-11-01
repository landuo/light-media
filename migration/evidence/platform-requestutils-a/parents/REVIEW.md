# A 消费父项对账（只读提案）

**不是机械 +7。** 本轮作者不自批自己的 A 实现；A App 消费门引用 meta 的独立验收，普通 CLI 尚待实际终态。精确采用已测字节、CLI 独审通过后，建议 **11 / 13 / 14 / 20** 以旧完整操作信用＋本次共享 A 修复闭合；**08 / 10 / 12** 保留与09同一个已证源 metadata >2MiB 读取差异，不得当 TLS 通过后一并消失。最终分类/计数只由唯一 ledger 记录。

## 可继承，不重复开发/全量复测

- 08 搜索/作者标签仓库交集、20→25、同数量刷新重置/空结果/分身隔离：旧真实 Linux/UI 与原23卡比较保留；新 TLS 没改 Vue/卡片字段。
- 10 force/cache/失败不覆盖/并发join/后台metadata任务：历史实际根保留，refresh 字节和旧Git相同。新 A 补取得材料，不改 cache 状态机。
- 11/13 安装/校验/兼容/镜像/配置/冷恢复：install两调用、market一调用机械替换；旧已证 mirror503 规则仍有效。另旧 `received502→origin` 错误期望必须局部退休，新A4实际正确根取代，不能抹旧失败或把旧错误PASS继续引用。
- 12 history/Release 分离、作者顺序、32/30min缓存、指定版本后台接口已验；原UI没有历史版本选择器，不新造。当前history后续SourceDisplay差分由market-pipeline独审继承。
- 14 本次真实同版本force不冒新的1→2/clone证据；旧真实升级、clone9.7、exact-ID属性和JS/CSS派生仍有效，核心派生/installed字节与旧Git同。
- 20 旧 `process-startup-wait-linux.log` 真实3.67s已证明先启动、阻塞仓库后修复/重初始化、缺失/坏SHA失败、合法恢复和safe mode；早先502失败原件仍留。sync/startup算法逐字与旧Git同。本次冷现有包不冒重新执行缺失修复，但不因此否认旧真实证据。

## 预算归属不能泛化，也不能漏掉

`DiscoverGitHubSourcePackage(... MaxIndexBytes)` 的源显示元数据 >2MiB差异仍实际存在，沿市场显示/刷新与history读取影响08/09/10/12。没有移除/批准此界，整父不作完整等价接受。新A修复没有改body预算。

旧报告写“08/10/12/20 not globally closed”只是有限证明边界，不等于四项各有同一已证故障。具体原大padding反例属于Python display metadata；目标 `python_metadata` 明置UnavailableReason，`syncOriginalPlugins` 在版本判断/修复前跳过。不能从这一个反例推断native20缺失修复坏了，更不能要求重新开发全DAO/任意Python。原Go验证制品模式/资源上限单列，不宣称源无限域等价。

07Wiki独立授权待核、B实际timeout及23查询/23–24 body边界完全独立，A不替它们PASS。普通CLI/精确落地仍是当前待闭手续，不增加无关UI/旧升级矩阵。

`historical-readback.json` 保存选定旧压缩证据的原/解压SHA与具体PASS根；`current-code-impact.json` 保存当前/旧Git/候选前后像。它们不是新的运行或全旧closure当前同一声明。

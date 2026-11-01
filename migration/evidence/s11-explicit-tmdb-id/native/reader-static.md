# native05 reader delta：READY（静态 preexec）

**当前可由唯一协调者执行05的两个既定catalog根。** 04不再作为执行候选。仅静态审核，未运行Go/source/网络，未改target或ledger，不声称运行PASS。

## 冻结核验

`candidate05.json` SHA256 `ecac8aa2a9410920348c2586e3055d443980be005e4e2855c14c5a1a42fec072`。全部12候选文件、3旧目标前像、全部bindings均逐项byte-count/SHA匹配。新client副本SHA `2607180bcfefa8ecebe7df9a89a57077e7053b98949d5d39ea4c569b70cc3e93`。

## 范围内delta

1. **生产factory不再丢失可证EOF分类：** 04 `config.Transport(sourceRecognitionTLSClone(nil))` 唯一相关改动为05 `networkproxy.Apply(sourceRecognitionTLSClone(nil), config.HTTPS)`。阅读`networkproxy.Apply`确认仍Clone传入base，并使用同一已Resolve的HTTPS代理字符串；没有切direct、备用route或fallback。空代理仍与原空HTTPS配置同义。新reader只构造固定HTTPS GET且拒绝redirect，所以单选HTTPS配置没有遗漏可达HTTP分支。
2. **旧安全语义不动：** 旧`config.Transport(nil)`仍为普通c.http构造transport；旧routedTransport/get没有改动。仅新的recognitionfactory取得未经旧genericwrapper抹平的error。logging.Transport保留error本体但日志不输出原error文本；snapshot最终只返回分类Error，不把带凭据URL的原error直接公开。
3. **classifier/guard/TLS不扩：** source_recognition_session.go、source_recognition_snapshot.go与04完全同hash；predicate未新增EPERM/unknown，ctx取消不进入EOF重试。最多一次同path显式rebuild，前后guard/ctx仍在，独立TLSClone/CheckRedirect/body/request bounds不变。scope TLS授权仍为F独立识别client，不影响旧c.http或全局。
4. **附加测试安全：** 新ownedTLS根中的productionroute断言只构造factory及调用`routed.Proxy(logical)`选择地址，不调用Do/RoundTrip，不DNS/CONNECT/拨号127.0.0.1:1或:2。另直接检查EPERM、unknown、canceled+EOF分类，均无socket。原自有TLS实际网络仍仅同一httptest localhost端点，测试映射guard及清理不变。
5. **已修旧finding保持：** cache实现与测试同hash未变；namespace/immutableclient/复合guard/ResponseObject片段在05native与04native的实际diff中无改动（native唯一差异为组key负零canonical，由其它owner审核）。因此04对ABA绑定和空[]静默的窄修复结论继续成立。

## 可执行根与证据限制

* `TestSourceRecognitionSnapshotErrorsAndGuard`
* `TestSourceRecognitionOwnedRetryAndPrivateTLS`

READY仅是当前固定候选的安全preexec审查。实际编译、断言、race及worker/CLI由协调者各自取得真实证据；不得将owned TLS正常/故障注入测试冒production upstream访问。完整requests内部重试栈、cachetools真实TTL/key等原未闭范围不因05而自动完成。本报告不审category/model/group，不自签自己source collector为独立PASS。

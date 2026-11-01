# S13 numeric：普通已安装宿主实际终审

**ACCEPT。** 非作者只读核对了实际普通 server + 已安装 worker 的 CLI02；不是重跑、不是仅编译或 recording HostCaller。实际 session `85269`，`0f2b5a → 9c46de`，exit 0；原 result `2fb5b908…` 与协调 receipt 相符。

- 5 个业务请求：原 underscores 成功 → 原相邻 underscores ValueError → 同 worker Unicode 恢复 → exponent 成功 → 同私有 DB 冷启 Unicode 成功。4 个 true、1 个源异常；5 次逻辑 HostCaller 调用均正常返回信封。异常不是 transport success 冒业务成功，也没有 result 字段。
- 原 raw `1235d588…` 前四案直接给出期望；比较实际 RPC 的全部值与类型，仅 ids 明示替换为 owned 固定 hash。`125.0` 仍是 float，Unicode `-٠` 对应原非空字符串 truth，idle mode 保持 1。非法转换窗口 peer 序号不变，零 HTTP；后续同 worker 成功。
- 固定 owned peer 实际 8 次 TR RPC（4 对 session-get/torrent-set），另 2 次 artifact GET。没有外部 daemon、CONNECT、上游、转发、浏览器或环境清空。12 份非登录原响应逐 SHA 复核；登录材料未保存/发布。
- 普通 host `25 → 53`、已安装 worker `41 → 68` 的 PID/父 PID/starttime/executable SHA/owned listener inode 与实际记录对应。首 worker 调用序号 1–4；冷启 worker init/invocation 回到 1，同私有 DB，无重复下载 artifact。两个 host 均自然 exit 0，driver 的等待/回收断言通过，forced_cleanup/peer_errors 均空。此次只读不追加 /proc 活体探针。
- 正常二进制分别为 worker `2f0a8c20…`、server `461a4c90…`；前序构建三阶段输入/生成源码与准入 seal 已核。本次 execution-bindings `2718241f…` 的 6406 件现字节全部匹配，Meta25 保护内容未变。环境前后 equality 是已绑定 driver 的真实比较，不导出含秘密的环境映射；18 个资源阶段采样符合原阈值，不称连续监控。
- trusted command 日志投影确为 `[]`，与固定自有 app 日志过滤结果相符；该原日志仅本地核对，不纳发布。RPC 断言是 decoded typed arguments 与实体 byte count，不冒完整 HTTP wire 字节/顺序等价。

保留 native normal01 的真实 App FAIL：新夹具误禁任何 Authorization，静态确定当前 TR 空 user/password 会构造 `Basic Og==`；原失败没有捕获 header，不冒实际抓包证据。唯一 successor02 修正该断言，产品/源/worker不变。fixture01 PASS 继承，App02 normal/race 两根实际 PASS；CLI01/race01 未运行，不改称通过。初次独审漏掉该夹具矛盾也保留。

此终审覆盖新增普通数字字符串的完整已安装宿主链。整项精确八方法的历史继承与发布资格另见 `audits/s13-parent-closure-final01`；没有新增真实 daemon、任意 Python 对象/无限整数或 remote/CI 绿声明。

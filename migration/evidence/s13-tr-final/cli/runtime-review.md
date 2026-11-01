# S13 Transmission 最后两方法：普通实际链最终独审

**结论：ACCEPT，限本片普通 `set_torrent_tag` / `change_torrent` 合同。** 未发现阻止该有限普通 slice 发布的实际未实现或必需未验缺口。不是全 S13、任意 Python 输入、完整 vendor kwargs、真实 Transmission daemon、UI 或远端交付结论。审查者不是本片产品或 CLI driver 作者；本轮仅只读源码/既有日志与哈希核回，没有重跑任何门。

## 当前候选与实际证据

- 原 8a83 两个完整方法的 23-case recording 原始输出、正常/竞态各 leaf2 + SDK2 + native6（新3/受影响旧3）+ App2 已在 `normal-race01-final-seal.json` 独立封存。本轮沿用该精确层级，不把 recording 依赖称为真实 vendor/daemon。
- CLI 执行回执是 00:50:12.238082–00:50:14.295861、exit0；协调者原工具标识为 session65003 / af9ab3→995f5f。磁盘 actual-execution-receipt 保存 argv/时点/exit，不含工具 ID；工具 ID 按协调消息引用而非捏造原 toolstore。
- `execution-bindings.json` 的 **3999 项**独立当前重哈无漂移；server3986、worker1089 编译前后闭包全等且包含于该绑定。另 fixture manifest/route 单根实际1933输入与三阶段、生成 testmain 副本、原 PASS 日志核回。三程序实际 SHA 均匹配，server/worker 是普通非 race 构建。
- 本轮集成11路径仍等冻结候选，两个实际 worker 源文件另绑定；新命令目录不冒充先前四包测试的依赖。正常 CLI driver SHA `5a5adddeeae0c21149d4627ab80d31500cb7199918cad008f8f76496c4d35c8e`。

## 实际业务，不以 HTTP200 代替结果

逐字节校验 **14 份非登录原始响应**，JSON 类型保持地对回7个业务结果；登录内容没有保留或读取。

1. 标签合并成功；实际发出 RPC15 下仍发送的 labels 操作并持久 WARN。
2. change 的原值真值→转换→round 顺序得到 `uploadLimited:true/uploadLimit:0`、`downloadLimited:true/downloadLimit:-4`、ratio `2.67`（浮点）、idle mode2/limit0；布尔、整数、浮点在实际 peer receipt 保持区分。
3. outside-try 非法转换为 typed `ValueError`，不是源 False；该次 **零 HTTP**。
4. all-None 到固定 native noargs 规则，真实回源 False、持久 ERROR；该次 **零 HTTP**。recording 端口的 nonthrowing all-None True 不是 vendor 的实际行为，未拿它伪造成功 RPC。
5. 唯一 selected HTTP500 导致 `host_operation_failed`，没有降格成 False/成功；同 invocation 内调用者明确给出的第二操作恢复，**不是自动重试**。下一调用仍同 worker 成功。
6. 两个普通 host **25→58 均自然 Wait0**；warm worker42 的 invocation1..6 连续、init1，cold worker72/init1/invocation1。沿同私有数据目录冷启动，不重新 bootstrap/install；index/worker 各仅 GET1，无冷重取。

实际总数：7业务 / **8个逻辑 SDK 调用与8回调**（7个 HostCaller 正常返回、1个错误；正常返回包含 ValueError envelope 与源 False）/ **14个 peer HTTP**（2 artifact +6 session-get +6 torrent-set）。原 raw 与显式区间证实无额外 selected mutation。实际 RPC 收据保留已解码 typed arguments、实体字节计数；未保存原 RPC 请求实体，不宣称整段 RPC JSON 字节顺序等同。

## 标签、日志与安全边界

- CLI 两次实际 labels 顺序均为 `[owned-old, owned-retained, owned-new]`，未排序修改收据。原 Python set 的实际排列不同；只在 **string-only 精确成员 + 唯一基数**层比较，无重复/缺失/额外/大小写或空白归一。不称 wire array 同序，不借此声称真实 daemon 顺序语义已测。
- 已独读固定 owned 持久 `app.jsonl` 并逐对象对回公开安全 projection：**WARN2 + noargs ERROR1**；相同日志 ID/call 也在 host 原日志，三条 call 归属与对应请求时间区间一致。私有 DB、凭据与其余私有数据没有读取或发布。
- 六组固定控制 method/path 仅在已验证 PID/starttime/executable/socket inode 的 `127.0.0.1:39123`；peer 仅固定 artifact GET 与合成 TR RPC，无 CONNECT、转发、任意 URL、真实 daemon。来源、凭据/身份检查、严格 TLS 和 unknown-effect 保护未放宽。
- driver 执行内前后完整环境比较同，协调启动仅添已批准 marker；不清 proxy/CA/guard，不推断跨 exec 环境全同。20个资源采样及终态保留线均满足，不称连续资源监控。实际没有 forced cleanup、peer error 或输入漂移。

## 收口

本片有限普通输入的 source→SDK→leaf→native→App→已安装普通 worker→正常 server HTTP→owned 协议及失败恢复/冷启动已闭合，可按该边界纳入发布白名单。源全23 recording 观测及包 race 是不同证据层；CLI 的 server 和 worker **不是 race binary**。不追加真实 daemon、全平台、浏览器、任意对象/kwargs、真实远端推送等未执行声明。历史采集事故的未知副作用结论亦未因这些哈希/通过而改写。

机器绑定见 `cli01-actual-readback.json`；跨层最终绑定见 `final-ordinary-seal.json`。原证据、失败记录和前期静审文件不覆盖。

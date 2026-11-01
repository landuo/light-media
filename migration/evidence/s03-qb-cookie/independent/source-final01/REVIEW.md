# S03 qB Cookie 原有限基准实际终核

**ACCEPT：固定 9 个完整原节点、真实纯 Version/SimpleCookie/urlparse、recording qB 协议/store 层的 17 案 / 18 次完整 add 观察。** 可作为该有限层的原差分基准；不是目标通过、真实 daemon/HTTP/完整 S03 等价。

## 真实终态和输入

唯一实际 tool `3d9730`，exit 0；launch TERMINAL，`07:05:03.609538` → `07:05:03.874954 UTC`，命令为已准入的固定 source interpreter `-I -S -B` + collector。exit 文件 0，stderr 0 字节；raw 152283 字节，SHA256 `758df1e970dad525d564dc6732e2153f545dc8a0314fbbfd0b3e846156f02b6a`。工具 receipt、launch 的 raw/preexec SHA 都匹配；receipt 按实际结果转录，不冒充独立原始工具回放。

119 输入当前逐路径 SHA/size 与冻结 manifest 一致，drift `[]`，包含 53 stdlib source/40 readable pycache、固定 executable、两 pure packaging、原节点/原文件/collector/案例/历史绑定。原 collector 成功终态说明其执行前后 bound-input 校验均通过；此处独立当前读回也通过，不虚构源采集没有保存的三阶段输入图。

raw 中 9 个 selected source-node 元数据与冻结完整节点列表精确一致；继承 preexec 的两源文件对固定 Git、完整 AST/含 decorator 原文审查，无输入变化。没有导入完整原模块或执行原 constructor/login。

## 模块闭包与 audit

file-backed module inventory 从 57 项到 59 项：之前恰是已绑定 stdlib module 名称加 collector `__main__`；之后仅新增 `packaging` 与 `packaging.version`，无删除/替换。每项 path/SHA 均能落到固定输入，当前再核也一致。packaging 26.3 两源与旧 S05 raw 散列相同，实际 Version 并非比较桩；SimpleCookie/urlparse 为绑定 stdlib 真模块。无 app/qB/TR vendor/requests/urllib3/socket/ssl/network-client 模块。

实际 audit 为 open 185、compile 19、exec 5、object.__setattr__ 4、builtins.id 112757，rejections `[]`。这些计数与已审闭包的必要行为吻合：open 对应两 packaging、两个输入 JSON、两原文件、后置 manifest+119 输入和 59 module 读回；compile/exec 包含原 class AST、pure packaging 及绑定 typing/NamedTuple 的纯代码生成。不能把 aggregate counts 说成逐 code-identity audit trace 或通用 syscall 沙箱。没有 socket/process/write 类事件或被清除的拒绝，stderr 也无拒绝。

## 原实际观察与顺序

所有 18 次调用都返回原 tuple；success 字段 16 次 True、2 次 False，ids 都是空 list，**18 个 tuple 的整体 Python truthiness 均为 True**。false success 不可被归一为整个返回 False。

原 trace 实际共 52 次外层 protocol calls：17 version、9 cookie read、8 setter、18 add；48 returns、4 owned RuntimeError、7 owned-store commits、5 WARN/ERROR。独立逐事件验证 call/return-or-exception 配对、setter commit 次序、每步 store before/after 与最终 store 连续性；这不是物理 HTTP 次数。

- threshold 和 local release 的同步走 version→read→set→add；setter 返回 None 仍同步成功，随后 per-add Cookie 为 None。低于阈值/rc/坏版本/None 原样 fallback，不读写 store。
- 无 hostname/空 morsels/非法 Cookie 都在 version 检查之后处理；非法 `owned@bad` 触发真实原 ERROR `Illegal key 'owned@bad'`，随后带原 Cookie add，不将输入异常预造为 collector expected。
- ordered merge 保留原插入顺序/后值替换；domain 的前导点、其它 path 保持不同身份。只留五个指定字段，空 value 被丢弃但 expirationDate=0 保留；新站点条目覆盖同键原整条记录。源普通 JSON getter/normalization/merge 真执行，不替换为目标算法。
- version/read/set 普通错误按原 WARN/ERROR 后继续 add，add Cookie 为原始字符串；magnet 只有 add，完全绕过版本/同步 API。
- setter 成功后，后续 add 返回 False 或抛 owned RuntimeError，原返回 `(False, [])`；此前 owned Cookie store 仍保留，无回滚。
- 最后一案同一原实例连续两次 add，第二次 before 等于第一次 after；最终顺序为 sid/keep/last，sid 更新为 owned-two、keep 保留、last 追加。collector 没有在第二次重新构造实例或清空 store。

## 必须保留的边界

`cookie-store-set-error` 是 recording port 明确定义的 **pre-effect RuntimeError**：trace 没有 commit、store 不变、原 catch 后 fallback。它只证明此普通异常输入下原控制流，**不证明真实 daemon setter 失败原子性、不证明 HTTP 已委派操作无副作用，也不授权 target 将 unknown/platform/authority/cancel 错误降为普通源异常继续 add 或重放写操作**。已委派未知聚合仍须保持保护。

真实 daemon Cookie 持久化、vendor 自有版本读取/HTTP gate、认证/form 编码、请求/重试次数、下载副作用、SDK/App/worker/full AddTorrent 都未运行。旧 source34/S05 只按原范围继承；本次不扩至任意 Python getter/vendor model 或全 Cookie/PEP440 畸形域。

本审查没有重采/执行 source、packaging、Go 或网络，只读 raw/代码/输入并写本独审目录；产品、collector、ledger 和旧证据未改。

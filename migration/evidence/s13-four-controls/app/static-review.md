# S13 App candidate01：独立静审 READY，尚未编译/接线

2026-10-31；作者 s01_release_owner，审查 c_userdata_owner（Astra ultra）。审查范围仅 `s13-four-app-preparation` handler/test01，不自审本 reviewer 编写的 source collector。源四叶实际执行由协调者报告 tool5131f7/exit0；本审查只以 SHA `de761347f6ba64f9d74eafaf0309c99b5a0fbab9aa6d2ad17ccdaa6ea2920c7c` raw 与原四方法为输入，不另作该 collector 独立验收。

**结论：未发现本 App component 的实际静态阻断，可随新 SDK/leaf/native 冻结后进入最小编译/两根组件门。不是运行 PASS，也不是 installed worker/真实 HTTP 验收。** 当前只外部候选，registry allow/dispatch 两个旧 hook 尚待协调者精确接入；不能把未接线候选当成产品已可调用。

- 精确四 capability→kind/op；invoke-only、128KiB、精确字段 presence/重复/Unicode/trailing 借原 S03 decoder，保持显式 null 与 UseNumber。拒 upload/任意字段/endpoint/新 reannounce；没有触旧 QBC/TRC 或 upload/session。
- 先 current ctx/scope，再 App admission/wg；exact enabled downloaderID/kind 经旧 select，无 default fallback。当前配置 deep-clone 后 guard 再验证，production 固定 `newOriginalDownloaderCommandNative`，私有参数 seam 只供 component recording ports。selector/extra 均新 JSON decode 局部值，不重新引用 caller map。
- 工厂前/后、leaf 后、序列化前后 guard；唯一 owned close defer，错误亦关闭；不在 registry锁持有下 dispatch。复用原 guard 的 current config/identity 比较，取消和 revocation 不被包装成源成功。App关闭 admission/Wait-vs-Drained旧机制未更改。
- qB tag→显式 `{value:null}`，nil error只表示原 None 路径，不是label变更成功；category/两 stop→leaf bool。leaf `SourceException` 的原catch/log与 boundary/unknown保留区别；guard错误或未知effect不重试/不降为False/None。App源码只有一次固定leaf调用。
- 测试真实 newTestApp/正常SQLite/禁统计fixture，但 native 是明确 recording端口；有限核四wire、tagNone/bool、大整数/空白/null、缺字段/重复/错误kind/超界零factory、stale/cancel、配置变化、Close及unknown无应用重放。TR None在该录制端口故意非抛错→True；这是原wrapper层，不冒真实TR require_ids路径。实际native门须另核。

根：`TestOriginalDownloaderControlHostWire`、`TestOriginalDownloaderControlHostScopeAndNoReplay`；无需 fixture ENV/子进程/网络，也没有 hidden build。Source broad捕获普通异常与平台已声明的 guard/unknown差异不扩大。测试当前依赖旧 `ownedDownloaderCommandApp`/ports 以及尚外部冻结的新四SDK常量/optional leaf ports，尚未实际编译，后续实际错误原件应保留而非预记PASS。

机器读回见 `app01-static-readback.json`：candidate01三文件、四旧输入逐 SHA 同；附当前 SDK/leaf、原四方法及实际raw绑定。没有改主树/产品/ledger、没有执行 Go/source/vendor/服务/网络。

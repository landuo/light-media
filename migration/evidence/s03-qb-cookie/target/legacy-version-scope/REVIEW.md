# 旧 iyuuVersionAtLeast：四个 Unicode 预折叠的有限影响核查

**结论：这是旧 helper 一个真实、局部的版本校验差异；建议只重开这四字符子域，保留其余旧交付信用。** 本次只读/静态分析，未运行源、Go、网络或 daemon，未修改产品/ledger。新 Cookie helper06 已修同类问题，不等于它调用的旧 Add layout gate 已修。

## 1. 当前直接调用点

ACTIVE `internal/plugins/iyuuautoseed/native/iyuu_version.go` SHA `4539b47b44bb0cc7927754c12e92434afae964b73fcb4a847c745b5c60d51c71`，函数 L18 起；L21–30 将 İ/ı→i、ſ→s、K→k，之后才匹配 ASCII 版本正则。

| 文件:行 | 当前函数/作用 | 阈值 |
|---|---|---|
| iyuu_version.go:79 | IYUUClient.webVersionLocked：首次版本响应验证，成功后缓存 | 0.0 |
| iyuu.go:588 | IYUUClient.control 的 start 分支：resume/start | 2.11 |
| brush_flow.go:61 | BrushFlowControl 的 pause 分支：pause/stop | 2.11 |
| shared_downloader_commands.go:228 | sharedQBCommands.version：普通 shared 调用每次版本响应验证 | 0.0 |
| shared_downloader_commands.go:249 | sharedQBCommands.control 的 start：resume/start | 2.11 |
| shared_downloader_commands.go:373 | sharedQBCommands.Add：无条件版本读取后 layout/root-folder 转换门 | 2.7 |
| shared_downloader_four_commands.go:25 | AddTags：endpoint 支持门 | 2.3 |
| shared_downloader_four_commands.go:74 | Stop：pause/stop | 2.11 |
| shared_qb_tag_commands.go:137 | Info 的 paused/stopped/resumed/running 特殊 filter 别名 | 2.11 |
| shared_qb_tag_commands.go:242 | write：RemoveTags/DeleteTags 支持门 | 2.3 |

共 **10 个生产调用**。另 `iyuu_projection_test.go:139` 为唯一直接测试调用。机器逐行记录含当前函数签名见 `call-sites.json`。Cookie overlay 把原 Add callback 等字节抽成 addInside，不消除表中的无条件校验与 layout 比较；本表仍准确标为 ACTIVE 原 Add，非假称 overlay 已集成。

`webVersionLocked` 的 7 个实际调用也单列机器清单：addPaused、control(start)、BrushFlowControl(pause)、BrushFlowAddDownloading、SeedRescueAddDownloading、SeedRescueConnected、TransferConnected。它们是校验可达性，不是新增业务重验清单；不能把这次版本缺陷扩成这些完整功能全部失效。shared version 的实际消费者则为 start/add/AddTags/Stop/Info 特殊 filter/tag writes。无版本读取的 Recheck/DeleteKeepFiles/SetCategory 和 TR 方法不因这四字符改变。

## 2. 原版本合同与旧 oracle 的准确证明力

本机固定 packaging **26.3** `version.py` SHA `558b2fb50fd198c660a4c082f96b8919c565911b6b4b6e07f34b6b60c074be97`：L192–225 的 `(?a:...)` 显式覆盖 release/pre/post/dev 和 local；L396 外层 IGNORECASE 不取消局部 ASCII。静态因此拒绝下面四个字符串；原源仍可允许其普通 Unicode 外围空白，不能顺手改 Strip/全域 ASCII 拒绝。

已交 `native-projection-oracle.json` SHA `de6cdbe4e03a9c6d9d9828285a7c4973d264254ae40d96039dc381bc6099fdb4` 有 **23 个 versions**：20 个接受的 ASCII 输入、3 个 InvalidVersion。唯一非 ASCII 输入是 `٢.١١`，原记录为 InvalidVersion。**没有 ſ、K、İ、ı 的 accepted 或 rejected 观测**，所以旧 PASS 既不支持当前预折叠，也不需要清零。

该原 raw 的 `sources` 仅列 qb.py/tr.py/tr_torrent.py/tr_types.py 四个 SHA，`method` 声明 installed wrapper/consumed-library AST；它自身没有 packaging 版本/hash。已交 S03 source-provenance 真实绑定这份 raw。独立的 S05 source-manifest 记录 packaging==26.3，source-finite-json-oracle 又绑定同一 version.py SHA；这可确认当前固定依赖来源，**不能倒补声称旧 23 原执行已绑定或测试了四 Unicode 字符**。原 raw、原历史记录不改。

## 3. 最窄修复与四个静态派生反例

仅移除旧 helper 的四字符特殊 folding switch；保 ASCII A–Z 转小写、legacyanime.Strip、正则、整数比较/阈值、错误分类、请求次数和所有 callers 原样。

| 派生输入 | 当前旧代码静态路径 | 固定 packaging26.3 静态结果 |
|---|---|---|
| `2.11.3poſt1` | 折成 post1 后接受 | InvalidVersion |
| `2.11.3+K` | 折成 +k 后接受 | InvalidVersion |
| `2.11.3+İ` | 折成 +i 后接受 | InvalidVersion |
| `2.11.3+ı` | 折成 +i 后接受 | InvalidVersion |

这些是**静态源派生反例，不是新增实际原 oracle**。四者在公共验证门即应拒绝，故无需给 0.0/2.3/2.7/2.11 各做笛卡尔积，更不扩全 PEP440。

## 4. 最小必要增量门建议

1. 新纯 root（建议 `TestIYUUVersionRejectsNonASCIIIdentifierFolds`）四子例，要求错误 `invalid_webapi_version`，不修改任何旧原 expected。若协调希望实原四输入，另行最小原调用即可，不重采旧23。
2. 原 `TestIYUUInstalledNativeProjectionOracle` 一根相关正常回归，保其完整 23 version 与同根原投影；无需把它拆造成新期待。
3. 当前 Cookie 已计划的 `TestSharedQBCookieScopeWireAndVersionGuards` 原根内加 **一个** `2.11.3+K`：Cookie probe 依新 helper 回 False；fallback add 的旧 layout/version 校验拒绝，**零 selected setCookies/add 写**。按现 ordinary source/平台分类核实际返回，不把单元 helper 错误冒最终 SDK 类型。此是消除新链已知依赖差异的必需接缝，不新开更多网络场景。
4. 复用旧 `TestSharedDownloaderCommandQBAdd` 的正常 add/layout 相关根（可与第3项同一编译程序），其余旧全 qB/TR、TLS、proxy、真实 daemon 均不自动重跑。现 `TestSharedDownloaderFourQBWireAndVersions` / `TestSharedQBTagScopeWireAndVersionGates` 是已有可选相关根，若协调按影响闭包追加只继承它们现例，不扩矩阵。

修复预期只改变上述曾错误接受的版本输入，拒绝发生在 selected mutation 前；已经进入多写 scope 的错误仍须现 monotonic proof 分类，不借版本修复改变 unknown/no-replay。旧 AddTorrent/S13/插件主体接受记录保留，仅该 version 子操作 reopened。此文是范围建议，未给实际 PASS 或修改授权。

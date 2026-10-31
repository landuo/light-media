# S13 App 两根实际有限接受

协调执行 `evidence/s13-four-app01`：session26151，initial ed1466 / terminal565438，exit0（外层工具事实由协调者消息提供，不是本 reviewer 重执行）。实际 result 时间 2026-10-31 23:31:55.266974–23:32:30.073046 UTC；go list / compile / tests 原 exit 均0、stderr均空。

独立读回 test2json：**恰2根 run/pass，无skip/fail，package pass**：
- `TestOriginalDownloaderControlHostWire`
- `TestOriginalDownloaderControlHostScopeAndNoReplay`

6224项 before/after-compile/after-test 清单完全相同，逐文件当前重hash **0 drift**；实际程序 SHA `dbc2cbbe07ef91c4c31d7c9d1f2d56fa6ba41b92b0446036aed04298982c28c4` 核同；1份实际 generated testmain 原路径/保全副本均核同。handler/test主树字节与独审 candidate01 两文件相同。

resource 实际变化精确两处：现resources合同 allow union增加固定四cap predicate；在原scope/ctx/identity检查及logcontext之后增加对应handler dispatch。机器逆去这两个唯一插入段后 SHA 精确回到原 `8cc333982f2856ce441b19ca2a6e17e4fa9a861c512a7efedb8c79700ef92f35`，没有改变其它旧路由/decoder/合同。两根直接调用私有component，所以**两hook这里只获得静态接线审查，不冒实际外层SDK身份/标准worker验收**。

接受层：真实App配置/生命周期/有界解码/leaf调用/错误日志，与明确recording native端口的两根组件事实。TRNone端口非抛错→True仅wrapper分层；tagNone非远端成功收据；配置改变/旧scope/cancel和unknown不重放按实际断言通过。不是真实nativeHTTP/daemon/安装worker/普通CLI/race或全S13完成；native新ports及后续宿主门仍待。环境继承仅为协调result陈述，本次未另执行driver或改任何env。

机器核回：`app01-runtime-readback.json` SHA `d29b6300aea3447ca1594af7851a2de4490d305778cc3e409ffedae59f628e92`。本 reviewer 只读取源码/原证据并写本audit，无Go/source/vendor执行，无产品/ledger写入。

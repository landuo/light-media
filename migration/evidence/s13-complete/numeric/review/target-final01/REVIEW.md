# S13 numeric：有限 pure leaf + SDK normal/race 实际 ACCEPT

非作者只读终审通过；未重新执行任何Go/原collector/原源码。接受candidate01精确六件在这两个纯包中的实际结果，不接受完整S13或真实下载器修改。

| 实际门 | 根 | 三时点/当前输入 | program SHA256前缀 |
|---|---:|---:|---|
| leaf normal | 4 | 1993 / 1993 | 63795b9a829983c6 |
| SDK normal | 3 | 2123 / 2123 | 01e4d8dc7c639c97 |
| leaf race | 4 | 2030 / 2030 | 8ebf0622debf143f |
| SDK race | 3 | 2160 / 2160 | 86755df3096a7522 |

完整SHA与逐artifact绑定在normal-readback.json/race-readback.json。

## 真实终态

Normal session88257，4251b2→fa79bb，exit0；leaf 10:25:27.409834→28.829875 UTC，SDK 28.877110→32.221061。
Race session2920，4860ed→b194e9，exit0；leaf 10:26:02.698430→05.479569，SDK 05.520129→10.544103。
四门实际串行顺序符合准入。每门go-list/compile/tests exit均0、stderr均0；coordinator-tool-receipt.result SHA与真实result一致。不是仅计划命令或只看日志“PASS”一行。

## 输入与执行程序

逐组inputs-before == after-compile == after-test；所有1993/2123/2030/2160记录输入当前仍存在且hash/长度全同，无cache缺失恢复冒当前成功。四个runtime program实际重读SHA同result。每组完整重解go-list原输出，逐条复算声明→virtual overlay backing，等于保存的virtual-compile-inputs；所有声明文件均在快照中。实际声明数1824/2253/1861/2290（可重复包/文件声明，非unique input数），package158/175/161/178。

每组一个真实generated testmain恢复副本hash与快照/当前cache同，且包含全部实际选中根；新helper/leaf test、SDK test及单一source-tr-numeric raw确进入相应编译声明。原candidate/preexec四binding和六backing均在实际三时点绑定内。没有把265静态本地预绑定冒成完整实际依赖闭包。

Race两组go-list与compile真实带-race，normal不带；均为test2json运行对应已绑定test binary。四组无skip/fail事件，root pass精确4/3，不混入其他根。每种mode的leaf新根与SDK新根均逐一通过原14个同名子案。

## 证据分层

Leaf同时保原23观察根/旧ownership与平台边界根，加新raw14完整有序结果、call kwargs/Python类型、错误消息、日志/无调用及输入保持比较；新增int64/finite守卫根通过。只按前次静审退休旧"１２" Boundary成员，其余旧断言未改。

新14源结果仍为3个True和11个try外ValueError，leaf只3次recording调用；SDK对同14输入则14次recording HostCaller dispatch，检查原字符串/null透传与已有ValueError分类。两层不能合并冒14次真实daemon调用。旧unknown/wrapped/context取消/no-replay边界仍通过。SDK生产没有更改。

-vet=off，**无vet PASS声明**。没有HTTP/native/App/worker/普通CLI或真实Transmission daemon验证；这些受影响参数链的后续真实门仍待协调。不是全Unicode逐点实测、一般并发压力验收、S13整项交付或push许可。原8操作其余历史信用继续继承，未重跑无关操作。

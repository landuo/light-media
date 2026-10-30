# 当前联合候选：独立 backend 运行审查

结论：**当前冻结候选的有限 backend 回归门通过；官方当前 UI 门仍未由此审查验收。** 不代表 SDK126 全包、完整 S09、完整 trigger/clone 或全平台等价。生产网络、旧被拒探针和浏览器均未执行；无 Go/编译命令；只写本独占审计目录。

## 固定输入

- HEAD/base：`51719d2f37fa4c37bf008c2ac3deae9ff702783b`。
- candidate SHA256：`bd30bc25e3fb5b98a9c670ce121c46d3b6b7770152fc9dcfb1a2423a16279a87`。
- owner runtime-result SHA256：`95a852bbe7aca6cc26ccf7b0c0fe48cc94ef4c288f1b45bb534e110fce3c2de2`。
- 当前 normal server：`65eb917a297d81e96e3e160f47ae6a025ba03501fd01178ef0c2493f3bdde61b`；race server：`62d72336fb10be7c8ed6e36b165afa37b7d655a328ac182fd47b13eeaada8c6d`。
- S09 已交付 35+README 无变化；候选82、完整358 embedded dist 绑定保持。未重标旧 dc80/current06 binary 或 UI。

## 1. owner 100×2 不是只读 summary

重新逐根解析每组真实 `.log` 和 `-test.list` 输出，对照独审100根清单：normal与race各100唯一顶层 RUN/PASS，0skip、0FAIL、0race-warning；命令正则及必需fixture环境匹配。历史 before/after/runtime 三份 manifest 逐字节相同；各次 test-inputs before/after 相同且当前输入仍匹配。

保存的 go-list JSON 独立解码：normal438 package/6288输入引用，race440 package/6297引用，全部被相应6448/6457 manifest覆盖。所有现存非cache文件匹配。缓存清理后最初3个旧生成cache文件缺失，本次完整复核时2个仍缺失，另一个被并行授权作业再生且hash等于旧值；不重造历史证据。本审查不清、不重建cache。

**限定：6448/6457只是已记录 go-list Go/C/汇编/embed +显式文件闭包，不是所有本机工具链/OS输入。** CGO=1，原manifest未hash cgo executable、实际外部C编译器/linker、loader/libc，故不能称全部本机构建输入在历史before已冻结。现存26个binary逐hash核回，且直接按固定Go buildinfo格式从ELF解析版本/flags：均go1.26.0，race13个均`-race=true`，normal13个不含race；`-s -w`及版本/denied fixture的`-X`记录一致。未执行`go version`。

owner S09原helper日志明确normal/race自行编译真实worker/server，无overlay。原15根均通过；其持久普通生产服务记录逐项核为`starts=2,normal_stops=2,anonymous_status=401,removed_file_cold_absent=true,source_missing_delete=true`。这仍只证明所列普通路径，不触碰已隔离取消/generation根。

## 2. 本审查直接复用冻结binary独立执行

全新独占TMPDIR/HOME；PATH仅`/usr/bin:/bin`；显式`MP_SERVER_HOST=''`，无proxy环境。各mode使用对应当前编译server/worker。仅启service instances/async，未继承其他CLIfixture/browser-state。直接调用`app.test`/`plugins.test`，没有任何Go/编译调用或S09 self-build。

每mode精确8根：

- `TestOriginalServiceWireSource66`
- `TestOriginalServiceWireQueryDecoding`（38纯解码例）
- `TestOriginalPluginCenterQueryParamsSource38`（另一组38纯解码例）
- `TestOriginalPluginCenterQuerySlashSource116`
- `TestPluginServicesProductionCLI`（实际当前server、普通clone/冷compat、CLI108）
- `TestCallbackErrorNegotiatedActualProcesses`
- `TestCallbackErrorFrozenOldBothDirections`
- `TestCallbackUnnegotiatedDetailRetiresSession`

结果：normal app5根4.297s/SDK3根0.130s；race app5根28.886s/SDK3根4.137s；全部exit0、0skip、0race-warning。469个输入before/after无漂移。冻结旧wire仅用于旧新兼容断言，不替代当前SDK。

## 3. 独立复比原始 observations

不只信Go断言，使用独立Python后处理比对原版固定recordings和 owner/本审查 normal/race四组输出：

- 每组source wire66全status/header/body（精确JSON整数/Decimal）一致。
- 每组CLI108 = **56原版源边界输入 + 52普通鉴权/native扩展观察**，不是108个source子测试。前56源差分一致；后52精确分类：18个401、22个307、8个403、3个native200和1个native400，redirect保留原path/query并忽略不可信forwarded origin。
- 每组usage116全scope原始path/query、peer wire、响应status/header/body一致；每组52 peer calls、稳定UID。
- owner whole-source6两组的status/success/enable/value/error与安全投影一致，9007199254740993精确保持；原source空串error与成功null未折叠。
- 仅有限归一化：已知合成fixture ID/provider名称；合法格式的观察时间；自有loopback origin；CLI附加的自有token。未删除其他行为字段。精确文件hash及18组比较在`observation-comparison.json`。

**保留后处理harness错误：**第一版把CLI108误当108源子测试/源oracle长度而assert失败；第二版漏把既有合成fixture ID映射用于路径，产生四次各10个path-only差异。原脚本及差异均保存为attempt01/02，纠正后18组0差异。未修改产品、源oracle、binary、原日志，也未为了变绿重跑产品测试。

## 4. 当前native补充（不倒填历史）

`native-dependency-supplement.json`和`native-environment-current.json`只记本external exec namespace当前状态。26个ELF逐一检查：有动态NEEDED的文件仅列`libc.so.6`，另有10个无NEEDED（具体文件见JSON）；server interpreter为`/lib64/ld-linux-x86-64.so.2`。本namespace的loader/libc候选、cgo/cc/gcc/ld/as当前路径/hash、允许列出的环境和loader cache存在性已保存。历史实际CC/CXX选择、其完整工具chain、managed preview实际加载映射、将来dlopen/NSS均不能据此推测。

给协调者的最小managed-launcher核对清单：同namespace检查当前server和worker的ELF interpreter/NEEDED；只读其自己启动server PID的`/proc/<pid>/maps`提取文件型ELF共享库并hash；另记`/etc/ld.so.cache`、`/etc/ld.so.preload`存在性及白名单LD_PRELOAD/LD_LIBRARY_PATH/TZ/LANG。不要复制完整environ或账号runtime。不在maps出现的晚加载依赖列未绑定，外部exec路径/hash不能冒充managed namespace。

## 5. UI与范围后门

本次独审后台门不包含当前官方浏览器观察。parent报告的preview/静态peer连通问题及其修复，需由当前官方UI原始记录证明；本审查未将进行中UI、旧UI或环境失败当作产品通过。后续只可绑定本次65eb/其真实依赖、当前dist及真实UI步骤，不能从本报告推出UI完成。

existing UTF8/scalar安全边界、Go错误诊断/无Python traceback等运行时差异仍须按既有差异项表述，不因测试通过而变成用户批准或全量原版一致。完整S09保持open；未合入trigger/clone候选不算本轮验收或失败。

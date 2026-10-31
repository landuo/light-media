# S12 parser join：源→旧目标失败→一行修复→有限回归

2026-10-31 UTC。**接受此一行 parser 修复及精确依赖复验；不代表 S12.material 完整链已完成。** 本次仅读原始结果、代码、输入和程序哈希；没有重复运行源、Go、网络或修改产品。

## 实际原始证据

1. `evidence/s12-parser-source01`：17:56:24 UTC 独立 Python 进程 exit0。原 21 输入当前哈希同值，实际导入的 6 个 torrentool 文件核回；原 staticmethod 的 AST/208–237 行片段 SHA 与准入记录一致。owned114B：parser=`Root/sub/x`，原 helper folder=`Root`、files=`["sub/x"]`。stdout SHA `8c86d061ef6c1ef5e3a20a85afdaa6edccd20915c8d2f3239872e638755ac246`，stderr空。仅 parser+原静态方法，不是 whole helper、HTTP、下载或DAO。
2. `s12-parser-before01`：17:59:39 UTC，编译成功，唯一新根真实 FAIL，原日志明确 got=`["x"]`、want=`["sub/x"]`。旧程序 SHA `19da8c7dfabac61dd16906c58921de7746904d3f7c0ad240539eca526ad8f5fb` 当前核回。3437 个输入仅 before/after-compile 两阶段相同；失败使 driver 没有生成 after-test，不伪造第三阶段。
3. 原 `torrent_helper.go` SHA `de943a74…` 的精确副本保留。当前 SHA `837f10b59390aeb07c3f8106fd41e069533ddb7be11e7b5a68e3a553885ee86d`，独立逐字节确认唯一更改是空串或末斜杠分支 `joined = s` → `joined += s`；绝对 component reset 与正常加分隔符路径不变。
4. `s12-parser-after01`：18:01:47 UTC exit0；2 roots PASS/no skip：新同114B一例，以及旧 oracle 的 **25 个文件投影**全部通过。没有执行旧18下载/缓存/HTTP场景。3437 输入 before/after-compile/after-test完全同值，当前这些已声明路径再次逐SHA相同。新程序 `120e839317203cae84a5404c17333b464339d1f58e86c6d1b7c651b3404cd74a` 核回。
5. `s12-s06-parser-dependent01`：18:03:01 UTC exit0；唯一 `TestSharedDownloadHistoryProducerSourceProjection` 的4 case通过，无skip。6184实际输入三阶段及当前已声明路径相同，程序 `6af307a4887ae93584240625840fe658baa27e01137c41899bb6c5c87045fd1b` 核回。

各门 generated-source 实际小副本均与各自编译清单哈希一致。实际父级工具终态分别为 source `fbf6fa/0`、before `4664/c3ff55/1`、after `79791/36ec58/0`、dependent `39060/e79ab8/0`；这里以父级原工具回执配合本轮原文件独读，不冒新的系统级进程追踪。

## 数据绑定与范围

- 新 Go test内嵌 base64 解码后与源114B逐字节相同，expected来自实际源raw，不由Go生成。新test SHA `2240795a306790bc50ef5814226ce7259adbaa99d13003dcfb93558bdb83b709` 未变。
- 旧 `torrent-helper-oracle.json` SHA `0af5deb44232b264a2624cee6a46e93e295a5fca68fb412326723bccf34c406e` **不在3437 go-list编译输入内**。它由 test01/fix01 运行前冻结、原preimage、协调者 after `runtime-oracle-readback.json`及本轮当前核回单独绑定；测试在读取时又先断言同SHA和25条。既非缺失，也不假称 Go 自动纳入此 runtime数据。
- S06 `producer-source.json` 是原raw的精简键投影，SHA `1c440aff…`，不是整份raw SHA `c7d72484…`。独立比较所有保留顶层键及完整4 cases均与原raw相同；3份torrent实际bytes与源case哈希同值，JSON/bytes确实在6184个 go:embed输入中。测试使用真实 TorrentFiles 后比较29kwargs；media identity/date仍是显式原观测输入，因此不是新identity resolver、两个实时clock、SQL insertion或真实download链等价。
- baseline→after整个编译闭包**并非只一行变动**：另有平行 SDK `shared_torrent_material.go`及其test的A2变更，以及不同immutable runner路径。它们不进入本纯 TorrentFiles 测试调用；没有把baseline全闭包宣称当前零漂移。明确只parser生产文件的一行diff及同测试、同输入修复证据。
- 当前新增 `iyuuautoseed/torrent_material_url.go` 不在旧门编译清单；另外若干未纳入的App文件是已有非Linux build-tag分支，不把它们误报新漂移。旧门仅覆盖当时已绑定版本，不冒新URL/HTTP/chain桥或当前全包已验。
- runner继承环境，只见已定 GOMAXPROCS/CGO编译差分；没有proxy清洗，也不声称各次exec环境值全相同。无新增race结论。

原source-ready、baseline FAIL、fix preimage/patch及所有raw保留。最初合同报告的路径反例现已由真实源、旧目标失败和修后对照闭合；此前16MiB预算和普通material接线待办不因此消失。

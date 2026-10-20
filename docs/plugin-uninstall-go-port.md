# PluginUnInstall 2.2 Go 实现与交付审计

2026-10-20，R3 本轮在 Git `bf769d2148878802d99994961267f61425ddb3a9` 的独立副本补齐实际宿主的停止异常、顺序故障、缓存与恢复语义。原系统、正式新服务及真实插件均未在本轮被修改；正式部署、导入和迁移计数由后续生产收据决定。

## 安装版来源

| 来源 | SHA-256 |
|---|---|
| `/app/app/plugins/pluginuninstall/__init__.py` | `2210e9dea47eeccdc46208d515e45ce1222dcadf50dbfc63cbeb41f94e470487` |
| `/app/app/api/endpoints/plugin.py` | `16e96285764587b266c07af21b6df140836c00f916d6626b87584a492a4031ce` |
| `/app/app/core/plugin.py` | `b0d26b661624ca4dd643af20553dd23c70ab3e38be4573844e053dffa3c3e4b7` |
| `/app/app/scheduler.py` | `35c5508ddec38e6f45c1e86e95abfbc79c2207e6d6d6092cf5d40e639f40d74d` |
| `/app/app/db/systemconfig_oper.py` | `87bff2e7c63d2ba98c72d92d6aed2018548730215de017330019bb54478634a2` |
| `/app/app/db/__init__.py` | `6da490f32d0de67839249b4e318fecbf4c877ec5acde4eef08634738c4cfafc9` |
| `/app/app/db/models/systemconfig.py` | `56c948af28f482b39e0d599b6889fd803c375aff50ecdde2d5b4583dca581164` |
| `/app/app/plugins/__init__.py` | `e6ac4a8621e078bf729d34c01188a5ff664ee48ad5e09cb4cf66fc44336a647f` |

只读提取完整插件和所需依赖方法，未导入原应用或执行原插件。入库参考剔除所有插件类元数据及私有图标 URL。旧 `uninstall-reference` 保留 31 个执行、5 个完整原表单案例；新增 `uninstall-source-failure` 保存真实停止、调度器及配置提交方法的 AST 和 15 个故障参考。参考执行仅使用自己创建的临时包目录与内存替身；配置模型提交替身按实际 `db_update` 的 commit/rollback/rethrow 边界注入错误，不冒称运行了原 SQLAlchemy 应用。

## 原表单与正常业务

- 表单保持原两个清理开关、已安装插件多选及原说明；选项按实际 `plugin_order` 稳定排序，包含自身，只读本地安装元数据。
- 未选择目标立即返回；不读取安装表、不清空选择、不清理配置/KV、不通知。
- 按独立安装列表的当前进程值逐个移除调度、停止并移除类、检查/删除私有代码包，然后请求清配置和数据。选择顺序和重复选择不改变该顺序。
- 全部目标处理后，先保存剩余安装列表，再单独保存自身 `plugin_ids=[]` 和原两个开关值；未知字段在成功重置时移除，缺失开关保存为 null。
- `get_state` 恒为 false，没有周期任务、命令、插件 API 或详情页。非空未知选择仍执行末尾重置。
- 保留源 JSON 真值、字符串子串/字典键成员判断及错误边界；普通表单使用多选数组。

实际安装版的 `remove_plugin` 先从 Manager 删除类；之后默认 `force=false` 的 `delete_plugin_config/data` 因类不存在返回 false。所以两个开关表示发出原请求，**不会在这一原安装组合中删除保留配置或 KV**。保留该原行为，不将“请求清理”报告为“已清理”。原插件也不删除 `/config/plugins/<ID>` 数据目录。

## 故障顺序与实际宿主

旧宿主将整批 SQL、自己重置和最终收据放进一个事务，尾部失败会恢复全部目标；停止异常也会中止。这与安装版不一致。`before-source-failure-summary.json` 对应实际 App/SQLite 的两条失败证据。

新宿主用 `plugin_uninstall_source.go` 实现以下边界：

1. 固定当前 run、版本、实例、generation、revision 和原始输入，读取宿主真实配置与安装快照。worker 的 RPC 参数必须为空对象，不能提供删除目标、路径或 force。
2. 按源顺序处理单个目标。只在轮到该目标后预留其清理 owner，等待时释放 lifecycle；任一时刻只持一个目标 owner，事务前重核代际和配置。后一个 owner 失败不提前阻止前一个目标。
3. 源调度器捕获 backend 错误并写系统消息；该消息自身失败则在当前类移除前中止。普通停止回调异常记录后继续移除，符合 Manager 的 catch。取消、失效授权、原生 owner 未知结果等宿主边界不会被伪装成普通回调异常。
4. 每个逻辑包移除与该目标的 owner 交接原子提交。此前已完成的目标不会因下一个目标、安装列表提交或自身配置提交失败而恢复。
5. 原版安装列表的缓存/持久值分开记录；列表提交失败时，暖缓存已变而持久值未变。逻辑包注册是原私有 Python 包目录的对应物；内置 Go handler 共用可执行文件，绝不删除共享程序。
6. 自身配置的进程缓存先变、随后独立提交。提交失败后，当前实例/代际/revision 的表单读到空选择，原持久选择保持；真正冷重开丢弃该内存覆盖，读取持久值。该覆盖不影响其他插件或新代际。
7. 加密分步证据与每个小事务一起提交。失去 COMMIT 回复时只按精确新/旧密文核实结果；无法核实则保持待核对，不自动重放。完整源结果独立于旧批次收据表，旧收据仍可读。
8. 自卸载保留当前已授权调用的收尾能力，允许原最后一次 update_config。结果可以恢复旧 run，但不能再作用于后来重装的实例。已开始、证据缺失或损坏的执行保留待核对；其他 App HTTP 与插件可继续启动。

所有动作在真实 SDK peer 完成握手、返回终态并退出后由可信宿主执行。进程授权、固定版本、执行代际、加密数据和不确定结果禁止自动重试是原生插件平台边界，不把这些保护冒称原 Python 内部实现。无 Python 运行时依赖，不提供任意脚本/目录卸载。

## 安装、重装与原全局数据边界

安装版 `api/endpoints/plugin.py:install` 只将当前 installed-list 中没有的 ID 追加到末尾；已安装升级不追加。卸载后重装必须排到末尾，不能复用旧 SQLite rowid。R1 末次复审发现该遗漏，保留实际 HTTP 失败证据后，在独立 R2 修正。

`plugin_install_sequence` 用明确的本地序号记录新装/重装，写入与安装同事务；源列表仍含已移除 ID 的失败场景中，重装保留其原位置。修改配置、启停、Reset、已安装升级不改变它；真正冷重开保持序号。第一次增加该表时，仅以现有本地行顺序补初值，不在每次启动时重建缺失行；已存在顺序表中缺失记录时，卸载拒绝而其他 HTTP 继续可用。它和另外两个加密分步表均保持 `plugin_storage_schema=1` 的 additive 迁移。

**本地序列不等于已经迁移原版 UserInstalledPlugins 顺序。** 分批导入 Category→PluginUnInstall→其他插件会与原列表不同；原安装列表、文件夹布局是单独待迁移的公共数据。本次工具只导入该插件的空选择配置/KV0/off，不自动重排任何实例。将来的公共数据迁移必须尊重新服务用户已进行的布局或重装操作，不能用 rowid/导入先后冒充原顺序，也不能从未记录的旧历史猜测重装顺序。

新增五例 `install-order-oracle.json` 直接执行安装版 install 方法 AST，安装/注册/重载均为自有替身。实际 HTTP 卸载→重装→自卸载先于剩余目标、Reset/配置/升级不改序、冷重开、本地旧表一次回填和缺失行不重建由 `plugin_uninstall_install_order_test.go` 检验。

## 跨独立执行的全局安装列表（R3）

R2 的分步列表只有每个 run 的证据，下一次仍从 `installed=1` 重建，无法表达“包已移除、源持久列表尚未成功保存”。Root 复审后保留实际 HTTP→worker→SQLite→冷重开→新保存 run 的 FAIL，再在新物理 R3 修正。

`plugin_source_installed` 是一个独立加密的全局列表及 CAS revision。第一次增加该表时只回填新服务已有本地安装顺序；既有表缺失行不在冷启动时重建。普通安装仅追加当前源列表没有的 ID，普通卸载移除该成员；PU 的逐包逻辑移除不改它，直到源最后独立 `SaveInstalled`。该提交与本 run 的加密列表证据同事务；其前的内存缓存先更新，提交失败保留旧 durable。与源 `SystemConfigOper.set` 一样，持久记录存在且新值等于旧暖缓存时只更新缓存、跳过数据库写入，不擅自修复旧 durable；空列表的删除、缺失记录创建空列表也分别保留存在状态。后续**新的显式保存执行**在暖进程读取缓存，冷进程读取 durable，不能把两者都替换为已加载类的集合。

源列表中的已移除类按 Manager.stop 的原 no-op 遍历：仍移除其服务并执行原非强制清理请求，保留配置/KV，不再增加 native generation，不重建或调用旧 cleanup owner。每次新执行都重新绑定当前实例身份；后来重装的实例由新显式操作决定，旧 failed/uncertain run 仍禁止 replay。安装列表的暖/冷差异也决定重装是否追加：暖列表已移除此 ID 时追加，冷持久列表仍含它时保持原位置。

新增两次连续 init 的真实源 AST 参考（合成提交和内存冷缓存替身，并非原应用完整启动），以及实际 App 的暖/冷 HTTP 新 run、原始列表提交失败、末尾故障、中途故障、重装位置/新代际、空选择不读取损坏列表的验证。全局原 UserInstalledPlugins 与文件夹的实际迁移仍是前述独立边界，不能把这次本地列表回填当成已完成原公共数据迁移。

## 本轮可执行证据

`plugin_uninstall_source_failure_test.go` 使用实际 App、SQLite 故障 trigger、HTTP 保存、真实 Go worker、SDK 和真正关闭/重开数据目录，验证停止异常、调度器 catch/消息失败、后目标 owner 失败、独立安装列表/自身配置提交、暖缓存/冷持久值、自卸载/重装以及未知/缺失证据不重放且 `/healthz` 可用。旧批量 cleanup 用例已改为验证后续配置失败保留已提交 owner，原全回滚证据留在独立交接目录。

最终完整回归、race、vet、纯 Go Linux 编译、同一最终二进制的实际网页证据及冻结 SHA 以本轮 `VERIFICATION.json`、`SOURCE-FROZEN.json`、`HANDOFF-FINAL.txt` 为准。2026-10-16 的 NAS 合成验证属于前版历史证据，不能替代此次新顺序实现的验证；本轮未运行新 NAS fixture。

## 原配置迁移

只读复核的配置为 60 B，SHA-256 `defba4aee5f41f0cf4b1e50a812f2117ab04de0ff3ec69ca43bd045bab514bf1`，只有两个 boolean 开关和空 array 选择；KV 数量 0，运行状态 false。两个已知开关原样保留，不把任何实际安装插件 ID 填入选择。

一次性导入应以源原始 RawMessage 内存传递、目标独占锁和加密 Import 完成，默认 dry-run；导入 `desired_enabled=false`，不得排 init/reload，不产生 run/notice/卸载。后续显式打开此空选择也只执行无副作用返回。迁移 runner、正式目标身份/路径复核、生产 apply 及读回收据由 Root 独立负责；零目标配置安全迁移本身不替代上述业务闭环。

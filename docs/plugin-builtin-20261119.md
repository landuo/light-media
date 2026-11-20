# 插件内置化（in-process）：架构变更与交付（2026-11-19）

## 决策与结果

运营决策：**取消插件市场，全部插件编译进光影本体；插件更新随光影版本发布。**

设备实测（FN EVO4-R，light-media 0.6.2）：

| 指标 | 外置进程模式（0.5.19） | 内置模式（0.6.2） |
|---|---|---|
| 插件进程数 | 20 | **0** |
| 容器 cgroup（工作中） | 249–293 MiB | **117–127 MiB** |
| 容器 cgroup（静置启动后） | ~200 MiB | **67–76 MiB** |
| 主进程 RSS | 89–102 MB | **105–110 MB**（含全部 22 个插件） |
| 二进制体积 | 62 MB | 72 MB |
| 加载失败 | — | 无 |

最终版 0.6.5 实测：插件进程 **0**，cgroup **72–76 MiB**（重启后静置，CPU 5–191% 说明调度在跑），UI 200，空闲收割探针跟踪对象 0（内置类不参与进程收割）。

净收益：容器内存降 **约 60–65%**。降幅来自三处：20 份 Go runtime、20 份私有插件代码页、进程栈与 mprotect 记账整体消失。

## 架构

### 1. 会话抽象（`internal/plugins/session.go`）

宿主原本对着 `*plugins.Client` 具体类型编程。新增 `Session` 接口（18 个方法：Call 系列、stop 车道、cleanup 续延、能力查询、停止），两种实现都满足：

- `*Client`：原有 wire 客户端（外部/市场插件路径，保留）
- `*inProcessSession`：新增内置实现

app 层仅 9 处具体类型引用 + 4 个测试文件，全部改为接口；其余 5.7 万行插件框架代码零改动。

### 2. 进程内会话（`internal/plugins/inprocess.go`）

替代 exec/pipe/handshake 全套。保真度按现有语义逐项对齐：

- **身份来自构造**：`SessionBinding` 绑定 PluginID/InstanceID/Version，构造时校验 manifest 一致。没有握手，因此没有握手漂移问题（昨晚 MergeMessageNotify 那类故障在结构上消失）。
- **panic 隔离**：每次调用 `recover`，失败终止该会话并作为调用错误返回，**不扩散到宿主**。
- **stop 车道语义**：`Stop` 先执行 manifest 的 `stop` 方法再单次 join；`CallCleanup`/`CallPrivateSchedulerStop`/`CallPublicServiceStop` 走保留车道，绕过 drain fence（与 wire 客户端一致）。
- **清理续延**：`CleanupContext` 只对处于 draining 的会话开放；续延继承根上下文寿命，取消只撤销该调用。
- **能力末端**：`SupportsConcurrent/ParallelHostCalls/TransportClose/PrivateSchedulerStop/PublicServiceStop/CleanupContinuations` 由 manifest 与 serve 模式推导，与 wire 协商结果等价。

### 3. 编译期注册表（`internal/app/plugin_builtin.go`）

22 个类硬链接进宿主。关键实现细节：

- **SDK 零改动**：插件 SDK 位于插件仓库的 `internal/` 包，宿主不能导入。用**函数字面量桥接**解决——`sdkBridge(host)` 把宿主的具名 `HostCaller` 适配成未命名函数类型，再赋给 SDK 的具名类型（Go 允许一个具名与一个未命名同形函数类型互赋）。宿主从头到尾不出现 SDK 类型名。
- **manifest 转换**：SDK manifest 经 JSON 往返转为宿主 schema（等价于原市场索引的转换，现在发生在链接期），转换失败在启动即报错而不是运行时。
- **serve 模式保真**：`CategoryEditor` 保持 serial（`concurrent:false`），其余 21 类 concurrent，与各 `cmd/<class>/main.go` 的 `Serve`/`ServeConcurrent` 选择一致。
- `MergeMessageNotify` 的 SMTP 适配器在宿主侧内联重建（原 `cmd` 里的 `sendSMTP`），不丢声明的渠道。

### 4. 版本发布列车（`adoptBuiltinVersions`）

启动时（任何对象加载前）把持久化安装行对齐到编译期 catalog：版本不同即 `PluginStore.Install`（抬 generation、换 manifest、保留 enabled 偏好），并记 `builtin_adopted` 日志。这就是"插件升级=光影升级"的数据面。

`resolveBuiltinImplementation` 只在 catalog 命中**且版本一致**时启用内置；版本漂移时回退（由启动采用收敛）。克隆实例暂不内置，仍走磁盘路径。

### 5. 产品面收敛：市场关闭

内置部署下，逐插件获取面被显式关闭（`pluginAcquisitionClosed`）：

- `POST /api/plugins/install`、卸载路由 → `PLUGIN_MANAGED_BY_BUILD`：「插件已随光影版本内置，无需单独安装或卸载；升级光影即可更新插件」
- `POST /api/plugins/markets` → `PLUGIN_MANAGED_BY_BUILD`：「插件市场已停用，插件随光影版本发布」
- 读接口（已安装列表、市场源读取）保留，UI 仍能呈现"这个版本内置了什么"
- 回滚开关同时重开获取面

前端同一切口：插件页只保留「我的插件」，市场标签页移除（`tab` 只可能为 `installed`）。市场分支的模板与脚本作为不可达死代码保留，未做物理删除。

代码本体（`artifacts` 包、wire 客户端、安装/收据/身份校验）**保留未删**。这是有意的：删除会同时失去 (a) 回滚能力、(b) 21 个 wire 框架测试与 40+ 个制品测试的意义。物理删除留待观察期后决定。

### 6. 回滚开关

`LIGHT_MEDIA_PLUGIN_BUILTIN=off`（或 `App.pluginBuiltinDisabled`）整体关闭内置解析，回到外部 worker + 磁盘包路径——即内置化之前的完整行为。设置后需要磁盘上仍有各插件的安装包。

## 测试

- `plugin_builtin_inproc_test.go`：会话生命周期（init/get_state/非法方法/非法载荷/零超时拒绝/stop join）、panic 隔离、22 类全目录生命周期（每类真实 init + get_state + stop）、内置插件不进入空闲收割。
- `plugin_builtin_app_test.go`：App 级解析——catalog 命中解析为 builtin 且无 worker 命令、版本漂移不解析为 builtin。
- 全部通过 `-race`。
- **既有框架测试保持外部路径**：`newTestApp` 与 CategoryEditor 标准测试套件显式设置 `pluginBuiltinDisabled`，因为它们验证的是 wire 框架能力（市场安装、收据、克隆、冷恢复），且 CategoryEditor 是它们的编译样本。这条路径对未来的市场插件仍然必要，因此继续被测试保护。

## 未迁移的部分（保留）

- `internal/plugins/artifacts`（市场索引、安装、收据、源码构建）与 wire 客户端：内置模式下不参与解析，但代码与测试保留。原因：(a) 回滚开关需要；(b) 未来若重新开放外部插件，这是唯一路径。
- `internal/app/plugin_original_*` 的进程管理分支（`Runner.Start`、空闲收割、市场探测缓存）：内置类不进入这些分支，外部类仍需要。

## 已删除：多调用 bundle（0.6.5）

内置化上线后 bundle 彻底失去意义（内置优先 + 市场关闭 + 外部插件用原单件包更简单），已物理删除：
宿主侧 `internal/app/plugin_bundle.go` 与其测试、`pluginBundleVerified` 字段、实现层的选路分支；插件仓库 `cmd/light-media-bundle/`。设备上 `plugin-packages/light-media-bundle*` 两个文件同步删除。

## 已识别的后续清理（未做）

如果确认永久不再需要市场，可删除：市场 HTTP 路由与前端入口、`artifacts` 包、收据/摘要/身份校验、`plugin-packages` 目录管理、空闲收割整套、bundle 相关文件。估计可删 2.3 万行，但会放弃回滚能力，建议观察一段时间后再定。

## 回归归因（完整 fixture 套件，310 分片）

| 项 | 结果 |
|---|---|
| 通过分片 | 181 |
| 失败分片 | 39（全部 `internal/app`） |
| 本次新增测试 | 20 个，**全部通过** |
| 失败测试总数 | 364（昨日基线 381） |
| 与昨日差集 | 新增 15 / 修复 32 |

新增的 15 个逐一在**干净 HEAD + 同一 fixture 环境**下复跑：**15/15 同样失败**。这是 `internal/app` 已知的顺序依赖型存量问题（测试在分片内依赖前序状态铺垫，重排后失败），与本次改动无关。没有引入任何新失败。

## 观测口径

- 容器真实计费看 docker stats（cgroup）。`monitor proc-list` 现在只有 1 个进程，数字与 cgroup 的差异来自 file-backed 代码页。
- `/healthz` 保留 `plugin_load_failure`（最近一次加载失败文本，≤600 字节）与 `plugin_idle`（收割机探针；内置模式下跟踪对象恒为 0）。

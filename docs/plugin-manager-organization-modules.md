# 插件管理入口与模块覆盖桥（2026-10-25）

固定参考为 MoviePilot `8a83efeacd3e26b82be495e17d50146e31c96a60`，本批没有操作原系统或迁移业务插件。

## G10：页面实际入口

`Plugins.vue` 同时读取应用 API `/plugins` 和 `/plugins/organization`。此前原版兼容的 `/api/v1/plugin/folders` 与 `/api/v1/user/config/PluginOrder` 已存在，但页面使用的 organization 路由未注册，真实 Handler 返回 404，导致页面初次加载失败。

新增 organization 应用响应封装，仍使用同一原版文件夹与排序存储：只提交 folders 时保留 order，只提交 order 时保留 folders，完整页面保存可同时更新两者。旧 revision 字段接受但不作为原版没有的写入前提。未知文件夹属性和浮点排序值不被重写。原版兼容接口继续可见同一结果。这是前端传输适配，不声称 URL 或响应封装与 MoviePilot 完全相同。

`GET /plugins/installed` 从独立加密的持久安装顺序返回 ID，停用或停止不等于卸载；分身使用各自 ID。新增安装、卸载后的重装和重新创建的分身追加到末尾；已有安装的更新、制品替换不改变位置，也不受页面排序影响。安装标记与清单在同一事务更新；清单写入失败不会提前停止插件。旧数据库首次使用按创建时间、行插入顺序回填，此前没有保存的历史重装次序无法反向恢复。未再通过正在运行的对象列表推断安装状态。参考 `app/api/endpoints/plugin.py:402`。

## G13：启动修复与版本同步

`plugin_original_sync.go` 对应原源 `app/core/plugin.py:686` 的 `sync()`、`:1495` 的 `is_plugin_exists()` 和 `app/startup/plugins_initializer.py` 的同步后重载流程。首次加载之后后台读取配置中的远程、本地仓库，复用版本去重和兼容性筛选；只处理持久清单中已安装的源实例。缺少真实制品、未加载对象或运行版本低于候选时获取并验证制品；本地版本更高不会被降级，分身保留各自复制的代码。

原源的最多五个安装工作项通过有界 worker 实现；既有 Go 制品发布锁仍包含获取阶段，因此实际下载和发布仍受现有安全串行约束，未声称网络并行时序完全相同。一个仓库失败、平台不兼容或制品哈希不符不会被记为成功，也不会移除已安装成员；其他成功项仍保留。存在成功项后使用共同 manager 初始化入口重新加载所有已安装对象并注册 API、公共任务和命令；无变化不重复初始化。配置、插件数据和独立安装顺序保持。

真实 `App.Start` 已接入后台同步。安装、卸载、分身及配置/reload/reset 写操作共用操作锁和关闭入口检查；`Close` 先封闭入口并取消后台请求，再等待正在执行的修改和 worker 结束。关闭后排队请求返回 503，不能再次加载插件。测试包含请求卡在 loopback 仓库读取时关闭应用，以及真实 Handler 在关闭入口封闭后拒绝各类修改。

Go 依赖包含在已编译、已验证的 executable 中，不运行原版 Python/pip 依赖安装器。原版远端安装上报属于 G27，仍未冒充已经接入；具体 23 个业务插件的恢复及实机验证也未计入本批。

## G15：注册与执行的共同桥

`internal/app/plugin_original_modules.go` 对应原源 `app/core/plugin.py:933` 的 `get_plugin_modules`，以及 `app/chain/__init__.py:225,298` 的空值判断和模块调用顺序。只读取当前加载对象的插入顺序快照，先确认 `get_module` 声明再读状态，停用对象不读取模块；状态或声明失败只影响该插件。空声明仍保留空组，源实例和分身保留独立身份及自定义名称。

标准 Go SDK 用 `get_module` 返回的 `{"核心方法":"实际制品声明的方法名"}` 代替 Python 绑定 callable。调用保留实例绑定，验证目标方法实际存在于制品 manifest，经持久 worker 执行，输入为 `{"args":[],"kwargs":{}}`。没有新增公开的任意方法执行 API。

普通文件项可用 `plugins.NewModuleFileItem(fields)` 构造，保留固定版本 FileItem 的默认值、显式空值、整数/时间转换和未知字段忽略行为。该函数只构造数据，不授予文件权限；宿主另外核对真实本地路径、快照和已封存的操作。`plugins.ModuleTransferInfoResult(fields)` 构造整理返回模型；普通 `{"success":true}` 字典仍不等于 TransferInfo。非有限浮点数无法经标准 JSON 传输，Go 构造器对此报错，差异已记录。

文件列表的普通 JSON 数组参与后续列表合并；`plugins.ModuleResultTuple(items...)` 是终止元组。原版整理的兄弟文件采集可以迭代元组，文件浏览接口随后调用排序方法，因此拒绝元组。插件须按实际调用方选择返回类型，不能把两种列表调用统一理解为相同契约。关联字幕/音轨保留其 FileItem，同时继承主文件的完整识别元数据。

可表示的 JSON 结果严格区分：`null` 继续后续插件，列表仅合并后续列表，标量停止后续调用；`false`、`0`、空字符串和空对象也属于停止值。已有列表遇到标量或 null 保留原列表并继续。普通执行失败保留当前结果并继续；`kwargs.raise_exception` 为真时返回该错误并停止。数值内容保留 RawMessage，未经过 float64 聚合。

G15 仍未整体关闭。当前桥尚未接入原版全部系统核心方法；没有用虚构 provider 或空实现替代缺失核心。当前 SDK 不能表达 Python tuple、任意模型对象与 callable 类型；尤其 `[null,null]` 是列表，不能冒充原版“全部 None 的 tuple”。不支持区分原版 RateLimitExceededException。worker 仅提供错误码，尚未补齐原版异常文本、traceback、MessageHelper 通知与 system.error 的完整诊断链。JSON 范围通过不等于这些类型边界通过。

## 验证

所有进程测试使用测试应用、独占临时数据库、loopback 制品仓库、真实哈希校验和实际安装接口，未访问生产或外部媒体。没有跳过，也没有把协议回放称作实机业务验证。

| 范围 | 普通 | App 与 worker 均带 race | 内容 |
| --- | --- | --- | --- |
| G10 首批入口门 `TestPluginOrganization(UI\|Compat\|HTTP\|Source)` | 6 根，2.922 秒 | 6 根，15.614 秒 | 实际页面 GET/PUT、原接口互通、独立写入、登录/来源校验、事务失败回滚、真实源插件与两个分身的配置和数据保持、停止仍已安装、卸载移除 |
| G15 `TestOriginalModules` | 4 根、9 子例，5.417 秒 | 4 根、9 子例，31.920 秒 | 实际制品及缺省声明变体、顺序、分身自定义名、停用、声明/状态失败、停止、参数原值、null/list/scalar 聚合、错误隔离、raise_exception、未声明方法拒绝 |

后续增加安装持久顺序和 G13 后，最终联合门为 G10 7 根、G15 4 根、G13/关闭 4 根，共 15 根及 29 子例；普通 9.201 秒、App 与 worker 双端 race 91.511 秒均通过，0 skip、0 fail。该门同时包括旧库顺序回填、源插件重装、分身重建、失败事务、实际升级/修复以及关闭同步；最终原始结果见 `work/plugin-module-20261025/sync/{normal-final,race-final}.jsonl`。所有 worker 都从当前源重新编译，v2 通过 `-ldflags '-X main.version=2.0'` 生成，缺省声明变体通过 `-ldflags '-X main.declareModules=false'` 生成。

`go vet ./internal/app ./cmd/owned-module-override` 通过。当前结论为限定组件门，未运行整个应用全量套件或生产验收。测试文件要求 `OWNED_STANDARD_FIXTURE_BINARY`、`OWNED_MODULE_OVERRIDE_BINARY`、`OWNED_MODULE_NO_DECLARATION_BINARY`、`OWNED_MODULE_OVERRIDE_V2_BINARY` 指向上述真实制品；race 测试必须使用对应 race 编译制品。

最终原始日志在 `work/plugin-module-20261025/organization/{normal-final,race-final}.jsonl` 和 `work/plugin-module-20261025/modules/{normal-final,race-final}.jsonl`。macOS 测试 TMPDIR 使用真实项目路径，避免 `/var` 符号链接祖先触发既有制品安全检查；未降低生产检查。首轮失败记录保留用于区分测试配置及测试假设修正，未计入通过结果。
